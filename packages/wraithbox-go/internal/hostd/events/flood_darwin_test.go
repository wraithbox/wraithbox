package events

// The flood tests run on macOS only: fdchan.Socketpair is unsupported
// elsewhere, and hold_test.go checks the holds on every OS.

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// gatedFile is a day file whose writes wait until gate is closed.
type gatedFile struct {
	*os.File
	gate chan struct{}
}

func (f gatedFile) Write(p []byte) (int, error) {
	<-f.gate
	return f.File.Write(p)
}

// blocked reports whether a send failed because the receiver doesn't
// read: the sender's socket is full.
func blocked(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.ENOBUFS)
}

// floodUntilBlocked sends well-formed records from *seq on, as a sender
// that ignores every notice, until 20 sends in a row over 100ms fail
// because the socket is full.
func floodUntilBlocked(t *testing.T, raw *os.File, seq *uint64) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for fails := 0; fails < 20; {
		if time.Now().After(deadline) {
			t.Fatalf("the channel never blocked, %d records sent", *seq)
		}
		err := syscall.Sendmsg(int(raw.Fd()), marshal(t, eventRecord(*seq+1, work, validEvent())), nil, nil, 0)
		switch {
		case err == nil:
			*seq++
			fails = 0
		case blocked(err):
			fails++
			time.Sleep(5 * time.Millisecond)
		default:
			t.Fatal(err)
		}
	}
}

// watchQueue samples the writer's queue until stop is closed, and
// sends the largest length it saw on the channel it returns.
func watchQueue(w *audit.Writer, stop <-chan struct{}) <-chan int {
	most := make(chan int, 1)
	go func() {
		n := 0
		for {
			n = max(n, w.Queued())
			select {
			case <-stop:
				most <- n
				return
			case <-time.After(50 * time.Microsecond):
			}
		}
	}()
	return most
}

// floodSetup is a real audit writer whose file takes no writes until
// gate is closed, and a receiver on a real socketpair. raw is the
// sender's end, set not to block.
type floodSetup struct {
	w    *audit.Writer
	dir  string
	gate chan struct{}
	raw  *os.File
	r    *Receiver
	end  *ender
	logs *bytes.Buffer
	mu   *sync.Mutex
}

func newFlood(t *testing.T, queueCap int) *floodSetup {
	t.Helper()
	f := &floodSetup{dir: t.TempDir(), gate: make(chan struct{}), end: &ender{}, logs: &bytes.Buffer{}, mu: &sync.Mutex{}}
	now := time.Date(2026, 11, 1, 12, 0, 30, 0, time.UTC)
	w, err := audit.New(audit.Config{
		Dir: f.dir, DataDir: t.TempDir(), RetentionDays: 90, FloorMiB: 1, Version: "test", QueueCap: queueCap,
		Logger: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }, Tick: make(chan time.Time),
		Open: func(path string) (audit.File, int64, error) {
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
			return gatedFile{file, f.gate}, 0, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.w = w
	hub := NewHub(w, slog.New(slog.NewTextHandler(lockedWriter{f.mu, f.logs}, nil)))
	hub.recheck = 10 * time.Millisecond
	hub.Begin(work)
	fa, fb, err := fdchan.Socketpair(eventchan.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fa.Close() })
	if err := syscall.SetNonblock(int(fa.Fd()), true); err != nil {
		t.Fatal(err)
	}
	ch, err := fdchan.New(fb, eventchan.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	f.raw = fa
	f.r = hub.Attach(ch, Netd(work), f.end)
	return f
}

// TestFloodHeldBack: a sender that ignores every notice floods a real
// socketpair with well-formed records (S04-08-records-and-bounds, "Host
// work the guest can cause"; SEC13-bounded-resources). While the audit
// file takes no writes, the writer's queue stays at its cap and the
// sender's writes fail. Once it takes them, the receiver reads on until
// the VM is past its budget by the margin, and holds again. Once the
// sender is gone, every record it got into its socket is written.
func TestFloodHeldBack(t *testing.T) {
	const queueCap = 64
	f := newFlood(t, queueCap)
	stop := make(chan struct{})
	most := watchQueue(f.w, stop)

	var seq uint64
	floodUntilBlocked(t, f.raw, &seq)
	if q := f.w.Queued(); q != queueCap {
		t.Fatalf("queue of %d with the file shut, want the cap of %d", q, queueCap)
	}
	close(f.gate)
	limit := f.w.Budget("work").EventsPerMinute + eventsMargin
	for f.w.Budget("work").EventsThisMinute < limit {
		floodUntilBlocked(t, f.raw, &seq)
	}
	time.Sleep(50 * time.Millisecond)
	if n := f.w.Budget("work").EventsThisMinute; n != limit {
		t.Fatalf("%d records read, want the receiver held at %d", n, limit)
	}
	close(stop)
	if m := <-most; m > queueCap {
		t.Fatalf("the queue reached %d, over its cap of %d", m, queueCap)
	}

	_ = f.raw.Close()
	f.r.SenderGone()
	waitDone(t, f.r)
	if err := f.w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, "audit-2026-11-01.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := uint64(bytes.Count(data, []byte("\n"))); n != seq || len(f.end.got()) != 0 {
		t.Fatalf("%d records written of %d sent, ends %v", n, seq, f.end.got())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rule := range []string{RuleQueueFull, RuleBudgetIgnored} {
		if !strings.Contains(f.logs.String(), "rule="+rule) {
			t.Errorf("log has no %s:\n%s", rule, f.logs)
		}
	}
}

// TestFloodStopWhileQueueFull: a VM stop while the audit file takes no
// writes, as while writes fail, doesn't wait for the queue: once the
// sender is gone, the receiver reads the rest of its socket and is
// Done. The records wait in the queue and are written once the file
// takes them.
func TestFloodStopWhileQueueFull(t *testing.T) {
	const queueCap = 16
	f := newFlood(t, queueCap)
	var seq uint64
	floodUntilBlocked(t, f.raw, &seq)
	_ = f.raw.Close()
	f.r.SenderGone()
	waitDone(t, f.r)
	if q := uint64(f.w.Queued()); q != seq {
		t.Fatalf("queue of %d, want the %d records sent", q, seq)
	}
	close(f.gate)
	if err := f.w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, "audit-2026-11-01.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := uint64(bytes.Count(data, []byte("\n"))); n != seq {
		t.Fatalf("%d records written of %d sent", n, seq)
	}
}
