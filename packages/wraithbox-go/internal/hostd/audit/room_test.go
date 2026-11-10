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
