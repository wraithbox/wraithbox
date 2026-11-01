package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/volspace"
)

const mib = 1 << 20

// fakeVolume is a volume with set free space. Paths under other are on
// volume 2, the rest on volume 1. Allocated reports the set size of a
// path, or the size of the file there.
type fakeVolume struct {
	mu       sync.Mutex
	free     uint64
	other    string
	used     map[string]uint64
	allocErr error
}

func (v *fakeVolume) Stat(path string) (volspace.Space, error) {
	if _, err := os.Lstat(path); err != nil {
		return volspace.Space{}, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	vol := uint64(1)
	if v.other != "" && strings.HasPrefix(path, v.other) {
		vol = 2
	}
	return volspace.Space{Free: v.free, Volume: vol}, nil
}

func (v *fakeVolume) Allocated(path string) (uint64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if n, ok := v.used[path]; ok {
		return n, nil
	}
	return uint64(info.Size()), nil
}

func (v *fakeVolume) Allocate(path string, size int64) error {
	v.mu.Lock()
	err := v.allocErr
	v.mu.Unlock()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (v *fakeVolume) set(change func(v *fakeVolume)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	change(v)
}

// faults are the failures the next writes and syncs of the day file
// give. A write fault writes half the line first.
type faults struct {
	mu      sync.Mutex
	write   []error
	short   int
	sync    []error
	writes  int
	syncs   int
	openErr error
}

func (fl *faults) next(list *[]error) error {
	if len(*list) == 0 {
		return nil
	}
	err := (*list)[0]
	*list = (*list)[1:]
	return err
}

func (fl *faults) syncCount() int {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	return fl.syncs
}

type faultFile struct {
	File
	fl *faults
}

func (f faultFile) Write(p []byte) (int, error) {
	f.fl.mu.Lock()
	f.fl.writes++
	err := f.fl.next(&f.fl.write)
	short := f.fl.short > 0
	if short {
		f.fl.short--
	}
	f.fl.mu.Unlock()
	if err != nil || short {
		n, _ := f.File.Write(p[:len(p)/2])
		return n, err
	}
	return f.File.Write(p)
}

func (f faultFile) Sync() error {
	f.fl.mu.Lock()
	f.fl.syncs++
	err := f.fl.next(&f.fl.sync)
	f.fl.mu.Unlock()
	if err != nil {
		return err
	}
	return f.File.Sync()
}

// stopper is the VM manager's part: it records each forced stop, and
// queues the VM's stop record as the manager does.
type stopper struct {
	mu    sync.Mutex
	w     *Writer
	stops []string
}

func (s *stopper) ForceStop(slot, cause string) {
	s.mu.Lock()
	s.stops = append(s.stops, slot+":"+cause)
	s.mu.Unlock()
	s.w.Record(VMStopped(slot, 1, cause, cause, true))
}

func (s *stopper) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.stops...)
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type harness struct {
	t       *testing.T
	w       *Writer
	dir     string
	data    string
	vol     *fakeVolume
	faults  *faults
	stopper *stopper
	logs    *lockedBuffer
	tick    chan time.Time

	mu  sync.Mutex
	now time.Time
}

var start = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// newHarness starts a writer on a fake clock and volume, with 10 GiB
// free, a floor of 1 GiB and data disks of 8 GiB.
func newHarness(t *testing.T, change func(*Config)) *harness {
	t.Helper()
	root := t.TempDir()
	h := &harness{
		t: t, dir: filepath.Join(root, "logs"), data: filepath.Join(root, "data"),
		vol:    &fakeVolume{free: 10 << 30, used: map[string]uint64{}},
		faults: &faults{}, logs: &lockedBuffer{}, tick: make(chan time.Time), now: start,
	}
	for _, d := range []string{h.dir, h.data} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{
		Dir: h.dir, RetentionDays: 90, FloorMiB: 1024, DataDir: h.data, DataDiskGiB: 8, Version: "test",
		Logger: slog.New(slog.NewTextHandler(h.logs, nil)), Now: h.clock, Tick: h.tick, Volume: h.vol,
		Open: func(path string) (File, int64, error) {
			h.faults.mu.Lock()
			err := h.faults.openErr
			h.faults.mu.Unlock()
			if err != nil {
				return nil, 0, err
			}
			f, size, err := openFile(path)
			if err != nil {
				return nil, 0, err
			}
			return faultFile{File: f, fl: h.faults}, size, nil
		},
	}
	if change != nil {
		change(&cfg)
	}
	w, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.w = w
	h.stopper = &stopper{w: w}
	w.Attach(h.stopper)
	t.Cleanup(func() { _ = w.Close() })
	return h
}

// checkStops checks the forced stops so far, as slot:cause.
func (h *harness) checkStops(want ...string) {
	h.t.Helper()
	if got := h.stopper.got(); !slices.Equal(got, want) {
		h.t.Errorf("stops = %q, want %q", got, want)
	}
}

func (h *harness) hasReserve() bool {
	_, err := os.Stat(filepath.Join(h.dir, ReserveName))
	return err == nil
}

// fault sets the faults of the next writes.
func (h *harness) fault(set func(*faults)) {
	h.faults.mu.Lock()
	defer h.faults.mu.Unlock()
	set(h.faults)
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

// flush has the loop write what is queued, and waits until it has.
func (h *harness) flush() {
	done := make(chan struct{})
	h.w.calls <- func() {
		h.w.drain(false)
		close(done)
	}
	<-done
}

// advance moves the clock by d, ticks, and waits for the tick's work.
func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	now := h.now
	h.mu.Unlock()
	h.tick <- now
	h.flush()
}

// written is what a test reads back of a record.
type written struct {
	Time     int64    `json:"time"`
	Message  string   `json:"message"`
	Metadata Metadata `json:"metadata"`
	Unmapped Fields   `json:"unmapped"`
}

// records reads the day file of day.
func (h *harness) records(day time.Time) []written {
	h.t.Helper()
	f, err := os.Open(filepath.Join(h.dir, dayName(day)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []written
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r written
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			h.t.Fatalf("line %q isn't JSON: %v", sc.Text(), err)
		}
		out = append(out, r)
	}
	return out
}

// messages are the messages of the records of day's file, in order.
func (h *harness) messages(day time.Time) []string {
	var out []string
	for _, r := range h.records(day) {
		out = append(out, r.Message)
	}
	return out
}

func (h *harness) makeDisk(slot string, used uint64) string {
	h.t.Helper()
	path := (&Writer{cfg: Config{DataDir: h.data}}).diskPath(slot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.vol.set(func(v *fakeVolume) { v.used[path] = used })
	return path
}

func rule(err error) string {
	if r, ok := errors.AsType[*Refusal](err); ok {
		return r.Rule
	}
	return ""
}
