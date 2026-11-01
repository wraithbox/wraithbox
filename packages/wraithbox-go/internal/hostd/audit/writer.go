// Package audit is wb-hostd's audit writer (S09-11-audit): OCSF records,
// one JSON line each, in one file per UTC day,
// <logs>/audit-YYYY-MM-DD.jsonl. One goroutine writes them, in the
// order the writer accepted them.
//
// The writer enforces SEC10-audit's floor on the host: a VM start and a
// shell open are refused while the log can't take records, a failed
// write stops the VM of every record that waits, and a file is deleted
// by age only, never to make room (S09-11-audit, "Retention" and "Free
// space and failed writes").
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
)

// The writer's rules, each in the log line of its decision and in the
// refusals it gives (S09-11-audit, "Free space and failed writes").
const (
	// RuleFreeSpace: the log's volume is below the free space floor, a
	// VM start would leave it below once the data disk grows to its cap,
	// or the reserve file is missing.
	RuleFreeSpace = "audit-free-space"
	// RuleWriteFailed: a record couldn't be written, and no write has
	// succeeded since.
	RuleWriteFailed = "audit-write-failed"
	// RuleRetention: a day's file is older than audit_retention_days.
	RuleRetention = "audit-retention"
)

// syncEvery is the most often the writer syncs the file, besides before
// each VM stop record.
const syncEvery = time.Second

// spaceEvery is how often the writer checks free space.
const spaceEvery = time.Minute

// retentionEvery is how often the writer deletes old files.
const retentionEvery = time.Hour

var errClosed = errors.New("the audit writer is closed")

// Stopper stops a VM at once, for a failed write and for a data disk
// that grew below the floor. vm.Manager implements it. ForceStop must
// not wait for the writer: the writer calls it from its own goroutine.
type Stopper interface {
	ForceStop(slot, cause string)
}

// Config configures a Writer.
type Config struct {
	// Dir is <logs>, which holds the day files and the reserve file.
	Dir string
	// RetentionDays is audit_retention_days, at least
	// config.MinAuditRetentionDays.
	RetentionDays uint32
	// FloorMiB is audit_free_space_floor in MiB.
	FloorMiB uint64
	// DataDir is <data>, which holds the VM bundles and their data disks.
	DataDir string
	// DataDiskGiB is vm_data_disk_gib, the cap a data disk grows to.
	DataDiskGiB uint32
	// Version is wb-hostd's build version, in each record's metadata.
	Version string
	Logger  *slog.Logger
	// Now is the clock. nil means time.Now.
	Now func() time.Time
	// Tick drives the writer's timed work: sync, retries, the free space
	// check and retention. nil means a ticker of one second.
	Tick <-chan time.Time
	// Volume is the file system's space. nil means the host's.
	Volume Volume
	// Open opens a day file for appending. nil means the host's.
	Open func(path string) (File, int64, error)
}

// Writer is the audit writer. Its methods are safe for concurrent use.
type Writer struct {
	cfg      Config
	log      *slog.Logger
	floor    uint64
	diskCap  uint64
	metadata Metadata

	mu      sync.Mutex
	queue   []*item
	failure *Refusal
	low     bool
	noRes   bool
	stopper Stopper
	closed  bool

	wake  chan struct{}
	calls chan func()
	quit  chan struct{}
	done  chan struct{}

	// The loop's own state.
	file     dayFile
	unsynced []*item
	lastSync time.Time
	lastFree time.Time
	lastAge  time.Time
	disks    map[string]uint64
	// stopped is the VMs the last failure stopped, nil once a write
	// succeeds.
	stopped []string
}

// item is an accepted record. done is nil for a record no caller waits
// for.
type item struct {
	rec  Record
	at   time.Time
	line []byte
	done chan error
	// taken: the loop has begun to write it, so a Write whose context
	// ended can no longer take it back. Guarded by Writer.mu.
	taken bool
}

// New checks cfg, deletes the files past retention, checks free space
// and the reserve file, and starts the writer.
func New(cfg Config) (*Writer, error) {
	if cfg.Dir == "" || cfg.DataDir == "" || cfg.Logger == nil {
		return nil, errors.New("audit: Dir, DataDir and Logger are required")
	}
	if cfg.RetentionDays < config.MinAuditRetentionDays {
		return nil, fmt.Errorf("audit: a retention of %d days is below the minimum of %d (%s)",
			cfg.RetentionDays, config.MinAuditRetentionDays, config.RuleRetentionMin)
	}
	if cfg.FloorMiB == 0 || cfg.FloorMiB > config.MaxAuditFreeSpaceFloorMiB {
		return nil, fmt.Errorf("audit: a free space floor of %d MiB is out of range", cfg.FloorMiB)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Volume == nil {
		cfg.Volume = hostVolume{}
	}
	if cfg.Open == nil {
		cfg.Open = openFile
	}
	w := &Writer{
		cfg: cfg, log: cfg.Logger, floor: cfg.FloorMiB << 20, diskCap: uint64(cfg.DataDiskGiB) << 30,
		metadata: Metadata{
			Product: Product{Name: productName, VendorName: productName, Version: cfg.Version},
			Version: OCSFVersion, LogName: "audit",
		},
		wake: make(chan struct{}, 1), calls: make(chan func()), quit: make(chan struct{}), done: make(chan struct{}),
		disks: map[string]uint64{},
	}
	now := cfg.Now()
	w.prune(now)
	w.checkSpace(now)
	tick, stop := cfg.Tick, func() {}
	if tick == nil {
		t := time.NewTicker(syncEvery)
		tick, stop = t.C, t.Stop
	}
	go w.loop(tick, stop)
	return w, nil
}

// Attach sets the stopper of failed writes and of growing data disks.
// wb-hostd attaches the VM manager once it is built, because the manager
// takes the writer.
func (w *Writer) Attach(s Stopper) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopper = s
}

// Record accepts r and returns at once. The writer writes it in the
// order it accepted it.
func (w *Writer) Record(r Record) {
	w.enqueue(r, nil)
}

// Write accepts r and waits until it is written: write(2) returned its
// whole length. It returns the writer's refusal, without accepting r,
// while writes are failing. A caller that only acts once its record is
// written, such as a shell open, refuses the action on an error
// (S09-11-audit, "Free space and failed writes").
func (w *Writer) Write(ctx context.Context, r Record) error {
	w.mu.Lock()
	failure := w.failure
	w.mu.Unlock()
	if failure != nil {
		return failure
	}
	done := make(chan error, 1)
	it := w.enqueue(r, done)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	// A caller that gave up refuses its action, so its record must not be
	// written later as if the action went ahead. One the loop is writing
	// already gets its answer soon.
	if w.withdraw(it) {
		return ctx.Err()
	}
	return <-done
}

// Close writes and syncs the records the writer has accepted, closes
// the file and stops the writer. A record accepted after Close is
// dropped, with a log line. Call it once every VM has stopped, so the
// stop records are among the last writes: a write that fails during
// Close stops no VM.
func (w *Writer) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()
	close(w.quit)
	<-w.done
	return nil
}

func (w *Writer) enqueue(r Record, done chan error) *item {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.log.Error("audit record dropped", "rule", RuleWriteFailed, "decision", "drop",
			"reason", errClosed.Error(), "vm", r.Slot, "message", r.Event.head().Message)
		if done != nil {
			done <- errClosed
		}
		return nil
	}
	at := w.cfg.Now()
	h := r.Event.head()
	h.Time = at.UnixMilli()
	h.Metadata = w.metadata
	line, err := json.Marshal(r.Event)
	if err != nil {
		w.mu.Unlock()
		w.log.Error("audit record dropped", "rule", RuleWriteFailed, "decision", "drop", "error", err)
		if done != nil {
			done <- err
		}
		return nil
	}
	it := &item{rec: r, at: at, line: append(line, '\n'), done: done}
	w.queue = append(w.queue, it)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return it
}

// withdraw takes it out of the queue unless the loop has begun to write
// it or already answered it. It reports whether it did.
func (w *Writer) withdraw(it *item) bool {
	if it == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	i := slices.Index(w.queue, it)
	if i < 0 || it.taken {
		return false
	}
	w.queue = slices.Delete(w.queue, i, i+1)
	return true
}

func (w *Writer) loop(tick <-chan time.Time, stop func()) {
	defer close(w.done)
	defer stop()
	for {
		select {
		case <-w.wake:
			w.drain(false)
		case <-tick:
			w.onTick(w.cfg.Now())
		case f := <-w.calls:
			f()
		case <-w.quit:
			w.drain(true)
			w.syncAll()
			w.file.close()
			w.dropWaiters(errClosed)
			w.logUnwritten()
			return
		}
	}
}

// onTick syncs, retries a failed write, and runs the free space check
// and retention when they are due.
func (w *Writer) onTick(now time.Time) {
	retry := w.failing()
	if now.Sub(w.lastSync) >= syncEvery {
		w.syncAll()
	}
	w.drain(retry)
	if now.Sub(w.lastFree) >= spaceEvery {
		w.checkSpace(now)
	}
	if now.Sub(w.lastAge) >= retentionEvery {
		w.prune(now)
	}
}

// drain writes the queue in order. While writes are failing it writes
// only on a retry, once a tick, not on each new record.
func (w *Writer) drain(retry bool) {
	if w.failing() && !retry {
		return
	}
	for {
		it := w.head()
		if it == nil || !w.write(it) {
			return
		}
		w.pop()
	}
}

func (w *Writer) failing() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failure != nil
}

func (w *Writer) head() *item {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.queue) == 0 {
		return nil
	}
	w.queue[0].taken = true
	return w.queue[0]
}

func (w *Writer) pop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.queue[0] = nil
	w.queue = w.queue[1:]
}

// logUnwritten logs the records a failing writer still held when it
// closed, each with its VM and message, so the gap is in wb-hostd's log.
func (w *Writer) logUnwritten() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, it := range w.queue {
		w.log.Error("audit record dropped", "rule", RuleWriteFailed, "decision", "drop",
			"reason", errClosed.Error(), "vm", it.rec.Slot, "message", it.rec.Event.head().Message,
			"time", it.at.UTC().Format(time.RFC3339Nano))
	}
	w.queue = nil
}

// dropWaiters tells each caller still waiting on a record err, and drops
// its record: the caller refuses what the record was for.
func (w *Writer) dropWaiters(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.queue[:0]
	for _, it := range w.queue {
		if it.done != nil {
			it.done <- err
			continue
		}
		kept = append(kept, it)
	}
	clear(w.queue[len(kept):])
	w.queue = kept
}
