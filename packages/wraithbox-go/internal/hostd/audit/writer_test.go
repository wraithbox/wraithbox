package audit

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDayFiles: records go to the file of their UTC day, in the order
// the writer accepted them, and the file changes at midnight UTC. Each
// file has mode 0600.
func TestDayFiles(t *testing.T) {
	h := newHarness(t, nil)
	h.mu.Lock()
	h.now = time.Date(2026, 11, 1, 23, 59, 59, 0, time.UTC)
	h.mu.Unlock()
	h.w.Record(VMRunning("work", 1, "vm-start"))
	h.w.Record(VMStopping("work", 1, "vm-stop", "requested"))
	h.advance(time.Second)
	h.w.Record(VMStopped("work", 1, "vm-stop", "requested", false))
	h.flush()

	first, second := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	if got := h.messages(first); !slices.Equal(got, []string{"vm running", "vm stop"}) {
		t.Errorf("2026-11-01 = %q", got)
	}
	if got := h.messages(second); !slices.Equal(got, []string{"vm stopped"}) {
		t.Errorf("2026-11-02 = %q", got)
	}
	r := h.records(second)[0]
	if r.Time != second.UnixMilli() || r.Metadata.Version != OCSFVersion || r.Metadata.Product.Version != "test" {
		t.Errorf("record = %+v", r)
	}
}

// TestSyncs: the writer syncs on the tick, and before each VM stop
// record.
func TestSyncs(t *testing.T) {
	h := newHarness(t, nil)
	h.w.Record(VMRunning("work", 1, "vm-start"))
	h.flush()
	if n := h.faults.syncCount(); n != 0 {
		t.Fatalf("synced %d times before a tick or a stop record", n)
	}
	h.w.Record(VMStopped("work", 1, "vm-stop", "requested", false))
	h.flush()
	if n := h.faults.syncCount(); n != 1 {
		t.Errorf("synced %d times before the stop record, want 1", n)
	}
	h.advance(time.Second)
	if n := h.faults.syncCount(); n != 2 {
		t.Errorf("synced %d times after the tick, want 2", n)
	}
}

// TestRetention: a day's file is deleted once its day ended more than
// audit_retention_days ago, at start and each hour, and never to make
// room. Other files are kept (S09-11-audit, "Retention").
func TestRetention(t *testing.T) {
	var dir string
	old := []string{"audit-2026-08-01.jsonl", "audit-2026-08-02.jsonl"}
	kept := []string{"audit-2026-08-03.jsonl", "audit-2026-10-31.jsonl", "audit-2026-8-1.jsonl", "wb-hostd.log", "audit-reserve"}
	h := newHarness(t, func(c *Config) {
		dir = c.Dir
		makeFiles(t, dir, slices.Concat(old, kept))
	})
	exists := func(name string) bool {
		_, err := os.Lstat(filepath.Join(dir, name))
		return err == nil
	}
	for _, name := range old {
		if exists(name) {
			t.Errorf("%s was kept at start", name)
		}
	}
	for _, name := range append(kept, "audit-2026-01-01.jsonl") {
		if !exists(name) {
			t.Errorf("%s was deleted", name)
		}
	}
	if !strings.Contains(h.logs.String(), "rule=audit-retention decision=delete file=audit-2026-08-01.jsonl") {
		t.Errorf("log has no deletion line:\n%s", h.logs)
	}

	// Below the floor nothing more is deleted.
	h.vol.set(func(v *fakeVolume) { v.free = 0 })
	h.advance(time.Hour)
	if !exists("audit-2026-08-03.jsonl") {
		t.Error("a file inside retention was deleted below the floor")
	}
	// A day later, the file of 2026-08-03 is past retention too.
	h.advance(24 * time.Hour)
	if exists("audit-2026-08-03.jsonl") || !exists("audit-2026-10-31.jsonl") {
		t.Error("the hourly run didn't delete by age")
	}
}

// makeFiles makes a file of each name in dir, and a directory with the
// name of a day file.
func makeFiles(t *testing.T, dir string, names []string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "audit-2026-01-01.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// TestRetentionMinimum: the writer refuses a retention below 30 days.
func TestRetentionMinimum(t *testing.T) {
	_, err := New(Config{
		Dir: t.TempDir(), DataDir: t.TempDir(), RetentionDays: 29, FloorMiB: 1024,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err == nil || !strings.Contains(err.Error(), "audit-retention-min") {
		t.Errorf("New with 29 days = %v, want a refusal naming audit-retention-min", err)
	}
}

// TestFailedWrite: a failed write stops the record's VM first, frees the
// reserve when the volume was full, refuses starts with
// audit-write-failed, and then writes the failed record, the record of
// the failure, and the stop record, in that order (S09-11-audit, "Free
// space and failed writes"). The other VM keeps running.
func TestFailedWrite(t *testing.T) {
	tests := []struct {
		name    string
		fail    func(h *harness)
		freed   bool
		records []string
	}{
		{"no space", func(h *harness) {
			h.fault(func(f *faults) { f.write = []error{syscall.ENOSPC} })
			h.w.Record(VMRunning("work", 1, "vm-start"))
			h.flush()
		}, true, []string{"vm running", "audit write failed", "vm stopped"}},
		{"short write", func(h *harness) {
			h.fault(func(f *faults) { f.short = 1 })
			h.w.Record(VMRunning("work", 1, "vm-start"))
			h.flush()
		}, false, []string{"vm running", "audit write failed", "vm stopped"}},
		{"failed sync", func(h *harness) {
			h.w.Record(VMStarting("work", 1, "vm-start", Resources{}))
			h.flush()
			h.fault(func(f *faults) { f.sync = []error{syscall.EIO} })
			h.w.Record(VMRunning("work", 1, "vm-start"))
			h.advance(time.Second)
		}, false, []string{"vm start", "vm running", "audit write failed", "vm stopped"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, nil)
			if !h.hasReserve() {
				t.Fatal("no reserve at start")
			}
			tt.fail(h)
			h.checkStops("work:audit-write-failed")
			if h.hasReserve() == tt.freed {
				t.Errorf("reserve present = %v, want freed = %v", h.hasReserve(), tt.freed)
			}
			checkRefused(t, h, RuleWriteFailed, RuleWriteFailed)

			h.advance(time.Second)
			if got := h.messages(start); !slices.Equal(got, tt.records) {
				t.Errorf("records = %q, want %q", got, tt.records)
			}
			// A freed reserve refuses VM starts until it is made again.
			checkRefused(t, h, map[bool]string{true: RuleFreeSpace}[tt.freed], "")
			h.advance(time.Minute)
			checkRefused(t, h, "", "")
			if !strings.Contains(h.logs.String(), `msg="audit write failed" rule=audit-write-failed decision=stop-vm`) {
				t.Errorf("log has no failure line:\n%s", h.logs)
			}
		})
	}
}

// checkRefused checks the rule CanStart and CanOpenSession refuse with,
// "" for none.
func checkRefused(t *testing.T, h *harness, start, session string) {
	t.Helper()
	if err := h.w.CanStart("isolated"); rule(err) != start {
		t.Errorf("CanStart = %v, want rule %q", err, start)
	}
	if err := h.w.CanOpenSession(); rule(err) != session {
		t.Errorf("CanOpenSession = %v, want rule %q", err, session)
	}
	if r := h.w.Refusal(); (r == nil && start != "") || (r != nil && r.Rule != start) {
		t.Errorf("Refusal() = %v, want rule %q", r, start)
	}
}

// TestFailedWriteOfNoVM: a failed write of a record that belongs to no
// VM stops every VM.
func TestFailedWriteOfNoVM(t *testing.T) {
	h := newHarness(t, nil)
	h.faults.mu.Lock()
	h.faults.write = []error{syscall.EIO}
	h.faults.mu.Unlock()
	h.w.Record(GitChecked("accept", "host-git-version", Git{Binary: "/usr/bin/git"}))
	h.flush()
	h.checkStops("work:audit-write-failed", "isolated:audit-write-failed")
	h.advance(time.Second)
	want := []string{"host git accept", "audit write failed", "vm stopped", "vm stopped"}
	if got := h.messages(start); !slices.Equal(got, want) {
		t.Errorf("records = %q, want %q", got, want)
	}
}

// TestFailedOpen: a day file that can't be opened fails the write.
func TestFailedOpen(t *testing.T) {
	h := newHarness(t, nil)
	h.faults.mu.Lock()
	h.faults.openErr = syscall.EACCES
	h.faults.mu.Unlock()
	h.w.Record(VMRunning("work", 1, "vm-start"))
	h.flush()
	if err := h.w.CanStart("work"); rule(err) != RuleWriteFailed {
		t.Errorf("CanStart = %v", err)
	}
	h.faults.mu.Lock()
	h.faults.openErr = nil
	h.faults.mu.Unlock()
	h.advance(time.Second)
	if got := h.messages(start); len(got) != 3 || got[0] != "vm running" {
		t.Errorf("records = %q", got)
	}
}

// TestWriteWaits: Write returns once the record is written, and a shell
// opens only then. A failed write refuses the caller, drops its record
// and refuses later Writes at once until a write succeeds.
func TestWriteWaits(t *testing.T) {
	h := newHarness(t, nil)
	open := ShellOpened("work", 1, "shell-open", Session{ID: "s1"}, nil)
	if err := h.w.Write(t.Context(), open); err != nil {
		t.Fatal(err)
	}
	if got := h.messages(start); !slices.Equal(got, []string{"shell open"}) {
		t.Fatalf("the record isn't in the file when Write returns: %q", got)
	}

	h.faults.mu.Lock()
	h.faults.write = []error{syscall.EIO}
	h.faults.mu.Unlock()
	err := h.w.Write(t.Context(), ShellOpened("work", 1, "shell-open", Session{ID: "s2"}, nil))
	if rule(err) != RuleWriteFailed {
		t.Fatalf("Write of a failed record = %v", err)
	}
	if err := h.w.Write(t.Context(), ShellOpened("work", 1, "shell-open", Session{ID: "s3"}, nil)); rule(err) != RuleWriteFailed {
		t.Errorf("Write while failing = %v", err)
	}
	h.advance(time.Second)
	want := []string{"shell open", "audit write failed", "vm stopped"}
	if got := h.messages(start); !slices.Equal(got, want) {
		t.Errorf("records = %q, want %q: the refused opens aren't recorded", got, want)
	}
	if err := h.w.Write(t.Context(), ShellOpened("work", 2, "shell-open", Session{ID: "s4"}, nil)); err != nil {
		t.Errorf("Write after a write succeeded = %v", err)
	}
}

// TestFloor: below the floor, the minute check stops the VM whose data
// disk grew and leaves the other running, and starts and session opens
// are refused with audit-free-space until the volume is above it again.
func TestFloor(t *testing.T) {
	h := newHarness(t, nil)
	work := h.makeDisk("work", 1<<30)
	h.makeDisk("isolated", 1<<30)
	h.advance(time.Minute)

	h.vol.set(func(v *fakeVolume) {
		v.free = 512 * mib
		v.used[work] = 2 << 30
	})
	h.advance(time.Minute)
	h.checkStops("work:audit-free-space")
	for name, err := range map[string]error{
		"CanStart work": h.w.CanStart("work"), "CanStart isolated": h.w.CanStart("isolated"),
		"CanOpenSession": h.w.CanOpenSession(),
	} {
		if rule(err) != RuleFreeSpace {
			t.Errorf("%s = %v", name, err)
		}
	}
	if r := h.w.Refusal(); r == nil || r.Rule != RuleFreeSpace {
		t.Errorf("Refusal() = %v", r)
	}
	if !strings.Contains(h.logs.String(), "rule=audit-free-space decision=stop-vm vm=work") {
		t.Errorf("log has no stop line:\n%s", h.logs)
	}

	h.vol.set(func(v *fakeVolume) { v.free = 20 << 30 })
	h.advance(time.Minute)
	if err := h.w.CanStart("work"); err != nil {
		t.Errorf("CanStart above the floor = %v", err)
	}
	if h.w.Refusal() != nil {
		t.Errorf("Refusal() above the floor = %v", h.w.Refusal())
	}
}

// TestReserve: a missing reserve refuses VM starts, not session opens,
// and is made again once the volume has room for it above the floor.
func TestReserve(t *testing.T) {
	h := newHarness(t, nil)
	reserve := filepath.Join(h.dir, ReserveName)
	if err := os.Remove(reserve); err != nil {
		t.Fatal(err)
	}
	h.vol.set(func(v *fakeVolume) { v.free = 1024*mib + 32*mib })
	h.advance(time.Minute)
	if err := h.w.CanStart("work"); rule(err) != RuleFreeSpace {
		t.Errorf("CanStart without a reserve = %v", err)
	}
	if err := h.w.CanOpenSession(); err != nil {
		t.Errorf("CanOpenSession without a reserve = %v", err)
	}
	h.vol.set(func(v *fakeVolume) { v.free = 10 << 30 })
	h.advance(time.Minute)
	if _, err := os.Stat(reserve); err != nil {
		t.Errorf("reserve not made again: %v", err)
	}
	if err := h.w.CanStart("work"); err != nil {
		t.Errorf("CanStart with the reserve = %v", err)
	}
}

// TestReserveAllocateFails: a reserve that can't be made refuses VM
// starts from the writer's start.
func TestReserveAllocateFails(t *testing.T) {
	vol := &fakeVolume{free: 10 << 30, used: map[string]uint64{}, allocErr: syscall.ENOTSUP}
	h := newHarness(t, func(c *Config) { c.Volume = vol })
	if err := h.w.CanStart("work"); rule(err) != RuleFreeSpace {
		t.Errorf("CanStart = %v", err)
	}
	if !strings.Contains(h.logs.String(), "audit reserve missing") {
		t.Errorf("log has no reserve line:\n%s", h.logs)
	}
}

// TestCanStartDisk: a VM start needs the floor plus what its data disk
// can still grow to its cap, when the disk is on the log's volume.
func TestCanStartDisk(t *testing.T) {
	const floor, diskCap = 1 << 30, 8 << 30
	tests := []struct {
		name  string
		free  uint64
		disk  bool
		used  uint64
		other bool
		want  string
	}{
		{"disk short of its cap", floor + 7<<30, true, 512 * mib, false, RuleFreeSpace},
		{"disk near its cap", floor + 7<<30, true, 1536 * mib, false, ""},
		{"disk at its cap", floor, true, diskCap, false, ""},
		{"disk past its cap", floor, true, diskCap + 1, false, ""},
		{"no disk yet", floor + 7<<30, false, 0, false, RuleFreeSpace},
		{"no disk yet, room", floor + diskCap, false, 0, false, ""},
		{"disk on another volume", floor, true, 0, true, ""},
		{"below the floor", floor - 1, true, diskCap, false, RuleFreeSpace},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, nil)
			if tt.disk {
				h.makeDisk("work", tt.used)
			}
			h.vol.set(func(v *fakeVolume) {
				v.free = tt.free
				if tt.other {
					v.other = h.data
				}
			})
			if err := h.w.CanStart("work"); rule(err) != tt.want {
				t.Errorf("CanStart = %v, want rule %q", err, tt.want)
			}
		})
	}
}

// TestClose: Close writes and syncs what was accepted, and a record
// after Close is dropped with a log line.
func TestClose(t *testing.T) {
	h := newHarness(t, nil)
	h.w.Record(VMStopped("work", 1, "vm-stop", "shutdown", false))
	if err := h.w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := h.messages(start); !slices.Equal(got, []string{"vm stopped"}) {
		t.Errorf("records = %q", got)
	}
	h.w.Record(VMRunning("work", 2, "vm-start"))
	if err := h.w.Write(t.Context(), VMRunning("work", 2, "vm-start")); !errors.Is(err, errClosed) {
		t.Errorf("Write after Close = %v", err)
	}
	if !strings.Contains(h.logs.String(), "audit record dropped") {
		t.Errorf("log has no dropped line:\n%s", h.logs)
	}
}

// TestDirtyLine: when a failed write's bytes can't be truncated, the
// next record starts on a new line, so every whole record stays a line
// of its own.
func TestDirtyLine(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		open := c.Open
		c.Open = func(path string) (File, int64, error) {
			f, n, err := open(path)
			if err != nil {
				return nil, 0, err
			}
			return noTruncate{f}, n, nil
		}
	})
	h.faults.mu.Lock()
	h.faults.short = 1
	h.faults.mu.Unlock()
	h.w.Record(VMRunning("work", 1, "vm-start"))
	h.flush()
	h.advance(time.Second)
	data, err := os.ReadFile(filepath.Join(h.dir, dayName(start)))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 4 || !strings.Contains(lines[1], `"vm running"`) {
		t.Errorf("lines = %q, want the cut line, then three whole records", lines)
	}
}

type noTruncate struct{ File }

func (noTruncate) Truncate(int64) error { return io.ErrClosedPipe }
