package audit

import (
	"slices"
	"syscall"
	"testing"
	"time"
)

// closedNow reports whether c is closed.
func closedNow(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// TestQueueCapRoom: Room makes its callers wait while the queue is at
// its cap, Record still accepts every record past it, and Room is
// closed once the queue drains below the cap (S04-08-records-and-bounds,
// "Host work the guest can cause").
func TestQueueCapRoom(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.QueueCap = 2 })
	if !closedNow(h.w.Room()) {
		t.Fatal("an empty queue has no room")
	}
	h.fault(func(f *faults) { f.write = []error{syscall.EIO} })
	for range 3 {
		h.w.Record(VMRunning("work", 1, "vm-start"))
	}
	h.flush()
	room := h.w.Room()
	if closedNow(room) || h.w.Queued() < 3 {
		t.Fatalf("room with %d records queued and a cap of 2", h.w.Queued())
	}
	h.advance(time.Second)
	if !closedNow(room) || h.w.Queued() != 0 || !closedNow(h.w.Room()) {
		t.Fatalf("no room once the queue drained, %d queued", h.w.Queued())
	}
	running := 0
	for _, r := range h.records(start) {
		if r.Message == "vm running" {
			running++
		}
	}
	if running != 3 {
		t.Fatalf("%d of the 3 records past the cap written", running)
	}
}

// TestRoomCountsUnsynced: a record written but not yet synced counts
// against the cap, because a failed sync puts it back in the queue. The
// writer syncs once the unsynced records reach the cap, so Room's
// callers don't wait for the tick.
func TestRoomCountsUnsynced(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.QueueCap = 4 })
	h.w.Record(VMRunning("work", 1, "vm-start"))
	h.flush()
	h.fault(func(f *faults) { f.write = []error{syscall.EIO} })
	h.w.Record(VMRunning("work", 1, "vm-start"))
	h.flush()
	// Queued: the record that failed, and the failure's own records.
	if closedNow(h.w.Room()) || h.w.Queued() != 3 {
		t.Fatalf("room with %d queued and one unsynced, under a cap of 4", h.w.Queued())
	}
	h.advance(time.Second)
	if !closedNow(h.w.Room()) {
		t.Fatalf("no room once the tick synced, %d queued", h.w.Queued())
	}
	h.fault(func(f *faults) { f.syncs = 0 })
	for range 4 {
		h.w.Record(VMRunning("work", 1, "vm-start"))
	}
	h.flush()
	h.fault(func(f *faults) {
		if f.syncs != 1 {
			t.Errorf("%d syncs of 4 records under a cap of 4, want 1", f.syncs)
		}
	})
	if !closedNow(h.w.Room()) {
		t.Fatal("no room once the writer synced at the cap")
	}
}

// TestRoomAfterFailedSyncAtCap: a sync at the cap that fails puts the
// records back in the queue, so Room stays closed, and once a retry
// writes and syncs them Room is open again with every record written.
func TestRoomAfterFailedSyncAtCap(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.QueueCap = 2 })
	h.fault(func(f *faults) { f.sync = []error{syscall.EIO} })
	h.w.Record(VMRunning("work", 1, "vm-start"))
	h.w.Record(VMRunning("work", 1, "vm-start"))
	h.flush()
	if closedNow(h.w.Room()) || h.w.Queued() < 2 {
		t.Fatalf("room after a failed sync at the cap, %d queued", h.w.Queued())
	}
	h.advance(time.Second)
	if !closedNow(h.w.Room()) {
		t.Fatalf("no room once the retry synced, %d queued", h.w.Queued())
	}
	running := 0
	for _, r := range h.records(start) {
		if r.Message == "vm running" {
			running++
		}
	}
	if running != 2 {
		t.Fatalf("%d of the 2 records written once the retry synced", running)
	}
}

// TestRoomOnClose: a closed writer releases Room's callers, so a
// receiver never waits on a writer that is gone.
func TestRoomOnClose(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.QueueCap = 1 })
	h.fault(func(f *faults) { f.write = slices.Repeat([]error{syscall.EIO}, 10) })
	h.w.Record(VMRunning("work", 1, "vm-start"))
	h.flush()
	room := h.w.Room()
	if closedNow(room) {
		t.Fatal("room at the cap")
	}
	if err := h.w.Close(); err != nil {
		t.Fatal(err)
	}
	if !closedNow(room) || !closedNow(h.w.Room()) {
		t.Fatal("Room still waits after Close")
	}
}
