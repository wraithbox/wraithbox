package audit

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// File is an open day file, opened for appending.
type File interface {
	Write(p []byte) (int, error)
	Sync() error
	Truncate(size int64) error
	Close() error
}

// dayFile is the day file the loop writes. off is where the last whole
// record ends, and synced where the last sync left it. dirty is set when
// a failed write left part of a line that truncation couldn't remove:
// the next record starts on a new line.
type dayFile struct {
	f      File
	day    string
	off    int64
	synced int64
	dirty  bool
}

func (d *dayFile) close() {
	if d.f != nil {
		_ = d.f.Close()
	}
	*d = dayFile{}
}

const dayLayout = "2006-01-02"

// dayName is the file name of t's UTC day.
func dayName(t time.Time) string {
	return "audit-" + t.UTC().Format(dayLayout) + ".jsonl"
}

// openFile opens path for writing at its end, made with mode 0600, and
// refuses one that isn't a regular file (S09-11-audit, "Where"). It
// seeks to the end instead of opening with O_APPEND: Windows refuses
// the Truncate of a file opened to append, and the writer truncates a
// partial record. The writer is the file's one writer.
func openFile(path string) (File, int64, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s isn't a regular file", path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s isn't a regular file", path)
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekEnd)
	}
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

// write writes it, after syncing what is unsynced when it is a VM's stop
// record or the day changed. It reports whether the record was written.
func (w *Writer) write(it *item) bool {
	name := dayName(it.at)
	if w.file.f != nil && w.file.day != name && !w.syncAll() {
		return false
	}
	if w.file.f == nil || w.file.day != name {
		dirty := w.file.dirty && w.file.day == name
		w.file.close()
		f, size, err := w.cfg.Open(filepath.Join(w.cfg.Dir, name))
		if err != nil {
			w.file = dayFile{day: name, dirty: dirty}
			w.failWrite(it, err)
			return false
		}
		w.file = dayFile{f: f, day: name, off: size, synced: size, dirty: dirty}
	}
	if it.rec.Stop && !w.syncAll() {
		return false
	}
	line := it.line
	if w.file.dirty {
		line = append([]byte{'\n'}, line...)
	}
	n, err := w.file.f.Write(line)
	if err == nil && n < len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.failWrite(it, err)
		return false
	}
	w.file.off += int64(n)
	w.file.dirty = false
	w.unsynced = append(w.unsynced, it)
	if it.done != nil {
		it.done <- nil
		it.done = nil
	}
	w.recovered()
	return true
}

// syncAll syncs the file when a record is unsynced, and reports whether
// it succeeded.
func (w *Writer) syncAll() bool {
	w.lastSync = w.cfg.Now()
	if w.file.f == nil || len(w.unsynced) == 0 {
		return true
	}
	if err := w.file.f.Sync(); err != nil {
		w.failSync(err)
		return false
	}
	w.file.synced = w.file.off
	clear(w.unsynced)
	w.unsynced = w.unsynced[:0]
	return true
}

// failWrite handles a failed write of it: the bytes it left are cut, and
// the file is closed, to be opened again for the retry.
func (w *Writer) failWrite(it *item, err error) {
	w.cutTo(w.file.off)
	w.failed(err, []*item{it})
}

// failSync handles a failed sync, which is a failed write of every
// record since the last one (S09-11-audit, "Free space and failed
// writes"). Their lines are cut, and they go back to the head of the
// queue, to be written again in order. The file is closed and opened
// again, because a sync that failed once can report success on the same
// descriptor without the data reaching the disk.
func (w *Writer) failSync(err error) {
	items := slices.Clone(w.unsynced)
	clear(w.unsynced)
	w.unsynced = w.unsynced[:0]
	w.cutTo(w.file.synced)
	w.mu.Lock()
	w.queue = append(items, w.queue...)
	w.mu.Unlock()
	w.failed(err, items)
}

// cutTo truncates the file to off and closes it. When the truncation
// fails, the next record starts on a new line.
func (w *Writer) cutTo(off int64) {
	if w.file.f == nil {
		return
	}
	if err := w.file.f.Truncate(off); err != nil {
		w.log.Error("audit file not truncated", "rule", RuleWriteFailed, "file", w.file.day, "offset", off, "error", err)
		_ = w.file.f.Close()
		w.file = dayFile{day: w.file.day, dirty: true}
		return
	}
	w.file.close()
}

// failed enters the failed state, or stays in it (S09-11-audit, "Free
// space and failed writes"). In order: it stops the VM of each failed
// record and of every record queued behind it, or every VM when one
// belongs to none, frees the reserve when the cause was a full
// volume, and queues a record of the failure. Each caller waiting on a
// record is refused, and the record it waited on is dropped, because
// the caller refuses its action. The failed records stay queued, and
// the writer writes them again, then the stop records, once a tick.
// Under a lasting failure it logs and stops again only when the VMs to
// stop change. Once Close began it stops none: every VM stopped first.
func (w *Writer) failed(err error, items []*item) {
	now := w.cfg.Now()
	w.mu.Lock()
	first := w.failure == nil
	if first {
		w.failure = &Refusal{
			Rule: RuleWriteFailed,
			Message: fmt.Sprintf("the audit log in %s can't be written since %s (%v): VM starts and new sessions are refused until a record is written",
				w.cfg.Dir, now.UTC().Format(time.RFC3339), err),
			Sessions: true,
		}
	}
	failure, stopper, closing := w.failure, w.stopper, w.closed
	w.mu.Unlock()
	w.dropWaiters(failure)
	slots := slotsOf(append(slices.Clone(items), w.queued()...))
	if first || !slices.Equal(slots, w.stopped) {
		w.stopped = slots
		w.log.Error("audit write failed", "rule", RuleWriteFailed, "decision", "stop-vm",
			"vms", slots, "records", len(items), "error", err)
		if stopper != nil && !closing {
			for _, slot := range slots {
				stopper.ForceStop(slot, RuleWriteFailed)
			}
		}
	}
	if errors.Is(err, syscall.ENOSPC) {
		w.freeReserve()
	}
	if first {
		w.Record(WriteFailed(now, err.Error()))
	}
}

// queued is a copy of the queue.
func (w *Writer) queued() []*item {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.queue)
}

// recovered leaves the failed state after a successful write.
func (w *Writer) recovered() {
	w.mu.Lock()
	was := w.failure
	w.failure = nil
	w.mu.Unlock()
	w.stopped = nil
	if was != nil {
		w.log.Info("audit write succeeded", "rule", RuleWriteFailed, "decision", "accept",
			"reason", "a record was written, and VM starts and new sessions are allowed again")
	}
}

// slotsOf is the VMs to stop for items: each record's VM, or every VM
// when one belongs to none.
func slotsOf(items []*item) []string {
	var slots []string
	for _, it := range items {
		if it.rec.Slot == "" {
			slots = vmbundle.Slots()
			break
		}
		if !slices.Contains(slots, it.rec.Slot) {
			slots = append(slots, it.rec.Slot)
		}
	}
	slices.Sort(slots)
	return slots
}

// prune deletes each day file whose day ended more than RetentionDays
// ago (S09-11-audit, "Retention"). Every record in a day's file is from
// that UTC day, so its day's end bounds the age of its newest record.
// It deletes by age only, never to make room.
func (w *Writer) prune(now time.Time) {
	w.lastAge = now
	entries, err := os.ReadDir(w.cfg.Dir)
	if err != nil {
		w.log.Error("audit retention failed", "rule", RuleRetention, "error", err)
		return
	}
	cutoff := now.AddDate(0, 0, -int(w.cfg.RetentionDays))
	for _, e := range entries {
		day, ok := parseDayName(e.Name())
		if !ok || e.Name() == w.file.day || !e.Type().IsRegular() || day.AddDate(0, 0, 1).After(cutoff) {
			continue
		}
		err := os.Remove(filepath.Join(w.cfg.Dir, e.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		w.log.Info("audit file deleted", "rule", RuleRetention, "decision", "delete",
			"file", e.Name(), "retention_days", w.cfg.RetentionDays, "error", err)
	}
}

// parseDayName returns the day of a day file's name.
func parseDayName(name string) (time.Time, bool) {
	const prefix, suffix = "audit-", ".jsonl"
	if len(name) != len(prefix)+len(dayLayout)+len(suffix) || name[:len(prefix)] != prefix ||
		name[len(name)-len(suffix):] != suffix {
		return time.Time{}, false
	}
	day, err := time.Parse(dayLayout, name[len(prefix):len(name)-len(suffix)])
	return day, err == nil
}
