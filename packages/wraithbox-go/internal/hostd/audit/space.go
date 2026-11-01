package audit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/volspace"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// ReserveName is the reserve file in <logs>: space the writer frees
// when the volume is full, so the records of a failed write and the
// stops it causes have room (S09-11-audit, "Free space and failed
// writes").
const ReserveName = "audit-reserve"

// ReserveSize is the reserve file's size, allocated, not sparse.
const ReserveSize = 64 << 20

// Volume is the space of the volumes the writer checks: volspace on the
// host, a fake in tests.
type Volume interface {
	Stat(path string) (volspace.Space, error)
	Allocated(path string) (uint64, error)
	Allocate(path string, size int64) error
}

type hostVolume struct{}

func (hostVolume) Stat(path string) (volspace.Space, error) { return volspace.Stat(path) }

func (hostVolume) Allocated(path string) (uint64, error) { return volspace.Allocated(path) }

func (hostVolume) Allocate(path string, size int64) error { return volspace.Allocate(path, size) }

// Refusal is the writer's refusal of a VM start or a session open. It
// is the error CanStart, CanOpenSession and Write return.
type Refusal struct {
	// Rule is RuleFreeSpace or RuleWriteFailed.
	Rule string
	// Message says what is refused, why, and what ends it.
	Message string
	// Until is when the refusal ends, zero for a rule that ends when its
	// condition does. No rule of this package ends at a set time.
	Until time.Time
	// Sessions is whether new sessions are refused as well as VM starts.
	// A missing reserve refuses VM starts only.
	Sessions bool
}

func (r *Refusal) Error() string { return r.Message }

// CanStart checks that slot's VM may start (S09-11-audit, "Free space
// and failed writes"). It refuses with RuleWriteFailed while writes are
// failing, and with RuleFreeSpace while the volume is below the floor,
// while the reserve is missing, or when the volume's free space is less
// than the floor plus what the VM's data disk can still grow when the
// disk is on the same volume. The guest decides what the disk holds, so
// the check counts the disk at its cap.
func (w *Writer) CanStart(slot string) error {
	if r := w.refusal(true); r != nil {
		return r
	}
	logs, err := w.cfg.Volume.Stat(w.cfg.Dir)
	if err != nil {
		return w.spaceRefusal(fmt.Sprintf("the free space of the audit log's volume can't be read (%v)", err))
	}
	need := w.floor
	grow, err := w.diskGrowth(slot, logs.Volume)
	if err != nil {
		return w.spaceRefusal(fmt.Sprintf("the space of the %s VM's data disk can't be read (%v)", slot, err))
	}
	need += grow
	if logs.Free < need {
		return w.spaceRefusal(fmt.Sprintf(
			"the volume of %s has %d MiB free, and starting the %s VM needs %d MiB: the floor of %d MiB, and %d MiB its data disk can still grow",
			w.cfg.Dir, logs.Free>>20, slot, need>>20, w.floor>>20, grow>>20))
	}
	return nil
}

// CanOpenSession checks that a new session may open: the check of
// CanStart, without a data disk.
func (w *Writer) CanOpenSession() error {
	if r := w.refusal(false); r != nil {
		return r
	}
	logs, err := w.cfg.Volume.Stat(w.cfg.Dir)
	if err != nil {
		return w.spaceRefusal(fmt.Sprintf("the free space of the audit log's volume can't be read (%v)", err))
	}
	if logs.Free < w.floor {
		return w.spaceRefusal(fmt.Sprintf("the volume of %s has %d MiB free, below the floor of %d MiB",
			w.cfg.Dir, logs.Free>>20, w.floor>>20))
	}
	return nil
}

// Refusal returns the writer's standing refusal of VM starts, for `wb
// vm status`, or nil when it has none. It doesn't read the volume: the
// per-start check is CanStart's.
func (w *Writer) Refusal() *Refusal {
	return w.refusal(true)
}

// refusal is the standing refusal: a failed write, or the floor, or a
// missing reserve, which refuses VM starts only.
func (w *Writer) refusal(vmStart bool) *Refusal {
	w.mu.Lock()
	failure, low, noRes := w.failure, w.low, w.noRes
	w.mu.Unlock()
	switch {
	case failure != nil:
		r := *failure
		return &r
	case low:
		return w.spaceRefusal(fmt.Sprintf("the volume of %s was below the floor of %d MiB at the last check",
			w.cfg.Dir, w.floor>>20))
	case noRes && vmStart:
		return &Refusal{
			Rule: RuleFreeSpace,
			Message: fmt.Sprintf("the audit log's reserve file %s is missing: VM starts are refused until it is made again, once the volume has %d MiB free",
				w.reservePath(), (w.floor+ReserveSize)>>20),
		}
	}
	return nil
}

func (w *Writer) spaceRefusal(why string) *Refusal {
	return &Refusal{
		Rule:     RuleFreeSpace,
		Message:  why + ": VM starts and new sessions are refused until there is space; free space on that volume",
		Sessions: true,
	}
}

// diskGrowth is what slot's data disk can still grow to its cap, when
// it is on the volume logs. A disk that isn't made yet is counted at its
// cap, on the volume of the nearest directory above it that exists.
func (w *Writer) diskGrowth(slot string, logs uint64) (uint64, error) {
	path := w.diskPath(slot)
	vol, err := w.nearestVolume(path)
	if err != nil {
		return 0, err
	}
	if vol != logs {
		return 0, nil
	}
	used, err := w.cfg.Volume.Allocated(path)
	if errors.Is(err, fs.ErrNotExist) {
		used = 0
	} else if err != nil {
		return 0, err
	}
	if used >= w.diskCap {
		return 0, nil
	}
	return w.diskCap - used, nil
}

func (w *Writer) diskPath(slot string) string {
	return filepath.Join(vmbundle.Path(w.cfg.DataDir, slot), vmbundle.DataDiskName)
}

// nearestVolume is the volume of path, or of the nearest directory above
// it that exists.
func (w *Writer) nearestVolume(path string) (uint64, error) {
	for {
		sp, err := w.cfg.Volume.Stat(path)
		if err == nil {
			return sp.Volume, nil
		}
		parent := filepath.Dir(path)
		if !errors.Is(err, fs.ErrNotExist) || parent == path {
			return 0, err
		}
		path = parent
	}
}

// checkSpace runs the check of each minute (S09-11-audit, "Free space
// and failed writes"). Below the floor it stops each VM whose data disk
// on the log's volume grew since the last check, and refuses VM and
// session starts until the volume is above the floor again. It makes
// the reserve again once there is room for it above the floor.
func (w *Writer) checkSpace(now time.Time) {
	w.lastFree = now
	logs, err := w.cfg.Volume.Stat(w.cfg.Dir)
	low := err != nil || logs.Free < w.floor
	grew := w.disksGrown(logs.Volume, err == nil)
	w.setLow(low, logs.Free, err)
	if err == nil {
		w.checkReserve(logs.Free)
	}
	if !low {
		return
	}
	w.mu.Lock()
	stopper := w.stopper
	w.mu.Unlock()
	for _, slot := range grew {
		w.log.Warn("audit free space", "rule", RuleFreeSpace, "decision", "stop-vm", "vm", slot,
			"reason", "the data disk grew while the audit log's volume is below the floor",
			"free_mib", logs.Free>>20, "floor_mib", w.floor>>20)
		if stopper != nil {
			stopper.ForceStop(slot, RuleFreeSpace)
		}
	}
}

// disksGrown measures each VM's data disk on the log's volume, and
// returns the VMs whose disk grew since the last measure.
func (w *Writer) disksGrown(logs uint64, known bool) []string {
	var grew []string
	for _, slot := range vmbundle.Slots() {
		path := w.diskPath(slot)
		sp, err := w.cfg.Volume.Stat(path)
		if err != nil || !known || sp.Volume != logs {
			delete(w.disks, slot)
			continue
		}
		used, err := w.cfg.Volume.Allocated(path)
		if err != nil {
			delete(w.disks, slot)
			continue
		}
		if last, ok := w.disks[slot]; ok && used > last {
			grew = append(grew, slot)
		}
		w.disks[slot] = used
	}
	return grew
}

func (w *Writer) setLow(low bool, free uint64, err error) {
	w.mu.Lock()
	was := w.low
	w.low = low
	w.mu.Unlock()
	switch {
	case low && !was:
		w.log.Warn("audit free space", "rule", RuleFreeSpace, "decision", "refuse",
			"reason", "the audit log's volume is below the floor", "dir", w.cfg.Dir,
			"free_mib", free>>20, "floor_mib", w.floor>>20, "error", err)
	case !low && was:
		w.log.Info("audit free space", "rule", RuleFreeSpace, "decision", "accept",
			"reason", "the audit log's volume is above the floor again", "free_mib", free>>20, "floor_mib", w.floor>>20)
	}
}

func (w *Writer) reservePath() string { return filepath.Join(w.cfg.Dir, ReserveName) }

// checkReserve makes the reserve file when it is missing or short, once
// the volume has room for it above the floor. A missing reserve refuses
// VM starts.
func (w *Writer) checkReserve(free uint64) {
	path := w.reservePath()
	used, err := w.cfg.Volume.Allocated(path)
	if err == nil && used >= ReserveSize {
		w.setReserve(true, nil)
		return
	}
	if free < w.floor+ReserveSize {
		w.setReserve(false, fmt.Errorf("%d MiB free is below the floor and the reserve", free>>20))
		return
	}
	if err == nil {
		_ = os.Remove(path)
	}
	err = w.cfg.Volume.Allocate(path, ReserveSize)
	w.setReserve(err == nil, err)
}

// freeReserve deletes the reserve file, on a write that failed because
// the volume is full.
func (w *Writer) freeReserve() {
	err := os.Remove(w.reservePath())
	w.log.Warn("audit reserve freed", "rule", RuleWriteFailed, "decision", "free-reserve",
		"file", w.reservePath(), "error", err)
	w.setReserve(false, nil)
}

func (w *Writer) setReserve(present bool, why error) {
	w.mu.Lock()
	was := !w.noRes
	w.noRes = !present
	w.mu.Unlock()
	switch {
	case present && !was:
		w.log.Info("audit reserve made", "rule", RuleFreeSpace, "decision", "accept", "file", w.reservePath())
	case !present && was:
		w.log.Warn("audit reserve missing", "rule", RuleFreeSpace, "decision", "refuse",
			"reason", "VM starts are refused until the reserve file is made again", "file", w.reservePath(), "error", why)
	}
}
