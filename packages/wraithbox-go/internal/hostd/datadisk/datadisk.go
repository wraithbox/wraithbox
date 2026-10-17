// Package datadisk is wb-hostd's side of each VM's data disk: the
// records in state.db, the checks before a VM starts, the run of
// wb-diskfmt that makes a new disk, and the reset that replaces one
// (S06-13-data-disk-format, "Formatting a data disk", "After a crash";
// S05-03-management-commands, "`wb vm reset-data`").
//
// SEC15-no-guest-disk-mount: wb-hostd never attaches, opens or reads a
// data disk. It reads only a name's existence with lstat, and deletes
// the file at the final name only in Reset. A VM starts only with a
// disk that state.db records as formatted.
//
// Every refusal is a *Refusal with its rule, logged with that rule and
// a message that names the fix (NFR06-explained-refusals).
package datadisk

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// Rule names the rule behind a refusal.
type Rule string

// The rules of this package. A refusal of wb-diskfmt keeps its own
// diskfmt- rule.
const (
	// RuleUnrecorded: a file has the final name, and state.db doesn't
	// record it as formatted, as after a crash between the rename and
	// step 9. The file is kept.
	RuleUnrecorded Rule = "data-disk-unrecorded"
	// RuleMissing: state.db records a formatted disk, and no file has
	// the final name. wb-hostd never makes a new disk on its own
	// (S06-05-disks, "After a crash").
	RuleMissing Rule = "data-disk-missing"
	// RuleBusy: a format or a reset of the slot's disk is running.
	RuleBusy Rule = "data-disk-busy"
	// RuleRecordFailed: state.db couldn't read or write the record.
	RuleRecordFailed Rule = "data-disk-record-failed"
	// RuleFormatFailed: wb-diskfmt didn't start, ended without an
	// answer, or refused with a rule that isn't one of its own.
	RuleFormatFailed Rule = "data-disk-format-failed"
	// RuleShimReply: wb-diskfmt's answer isn't valid: a volume UUID not
	// in canonical form, or a name other than the fixed one.
	RuleShimReply Rule = "data-disk-shim-reply"
	// RuleResetFailed: the reset couldn't delete the old file.
	RuleResetFailed Rule = "data-disk-reset-failed"
	// RuleSlotUnknown: a slot other than work and isolated.
	RuleSlotUnknown Rule = "data-disk-slot-unknown"
)

// Refusal is the error of every refusal in this package.
type Refusal struct {
	Rule Rule
	Slot string
	// Message says what was refused, why, and how to go on. It can hold
	// a refusal's text from wb-diskfmt, cut to diskfmt.MaxDetail.
	Message string

	err error
}

func (r *Refusal) Error() string { return r.Message }

// Unwrap returns the cause.
func (r *Refusal) Unwrap() error { return r.err }

// Records is the data disk part of state.db. *state.DB implements it.
type Records interface {
	DataDiskRecord(ctx context.Context, slot string) (state.DataDisk, bool, error)
	RecordDataDiskFormat(ctx context.Context, rec state.DataDisk) error
	MarkDataDiskFormatted(ctx context.Context, slot, volumeUUID string) error
	MarkDataDiskStarted(ctx context.Context, slot string, at time.Time) error
	DropDataDisk(ctx context.Context, slot string) error
}

// Config is what the manager runs with.
type Config struct {
	// DataDir is <data>.
	DataDir string
	Records Records
	Shim    Shim
	// SizeGiB is the size of a new disk, from config.toml. 0 takes
	// diskfmt.DefaultSizeGiB.
	SizeGiB uint32
	// Logger gets the decisions. nil takes slog.Default().
	Logger *slog.Logger
	// Now is the clock. nil takes time.Now.
	Now func() time.Time
}

// Manager runs the data disk steps of both slots. Its methods are safe
// for concurrent use, and run at most one format or reset per slot.
type Manager struct {
	cfg Config
	log *slog.Logger

	mu   sync.Mutex
	busy map[string]bool
}

// New returns a manager.
func New(cfg Config) (*Manager, error) {
	if !filepath.IsAbs(cfg.DataDir) {
		return nil, fmt.Errorf("datadisk: the data directory %q isn't an absolute path", cfg.DataDir)
	}
	if cfg.Records == nil || cfg.Shim == nil {
		return nil, errors.New("datadisk: records and a shim are required")
	}
	if cfg.SizeGiB == 0 {
		cfg.SizeGiB = diskfmt.DefaultSizeGiB
	}
	if r := diskfmt.CheckSize(cfg.SizeGiB); r != nil {
		return nil, fmt.Errorf("datadisk: %w", r)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	m := &Manager{cfg: cfg, log: cfg.Logger, busy: map[string]bool{}}
	if m.log == nil {
		m.log = slog.Default()
	}
	return m, nil
}

// fix is the sentence each refusal ends with (NFR06-explained-refusals).
func fix(slot string) string {
	return fmt.Sprintf("Stop the VM with wb vm stop %s, then run wb vm reset-data %s, which deletes every project's home on that disk", slot, slot)
}

func (m *Manager) refuse(ctx context.Context, op string, r *Refusal) *Refusal {
	attrs := []slog.Attr{
		slog.String("rule", string(r.Rule)), slog.String("decision", "refuse"),
		slog.String("op", op), slog.String("slot", r.Slot), slog.String("reason", r.Message),
	}
	if r.err != nil {
		attrs = append(attrs, slog.String("cause", bound(r.err.Error())))
	}
	m.log.LogAttrs(ctx, slog.LevelWarn, "data disk refused", attrs...)
	return r
}

// lock marks slot busy, or refuses with RuleBusy, so at most one
// wb-diskfmt runs per slot.
func (m *Manager) lock(slot string) *Refusal {
	if !vmbundle.ValidSlot(slot) {
		return &Refusal{Rule: RuleSlotUnknown, Slot: slot, Message: fmt.Sprintf("there is no %q VM", slot)}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy[slot] {
		return &Refusal{Rule: RuleBusy, Slot: slot, Message: fmt.Sprintf("the %s VM's data disk is being made or reset: try again once that ends", slot)}
	}
	m.busy[slot] = true
	return nil
}

func (m *Manager) unlock(slot string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.busy, slot)
}

// Prepare runs the data disk checks before slot's VM starts
// (S06-13-data-disk-format, "The temporary and final names"):
//
//   - a formatted disk recorded and a file at the final name: the VM
//     starts;
//   - a formatted disk recorded and no file: refused with
//     data-disk-missing;
//   - a file at the final name not recorded as formatted: refused with
//     data-disk-unrecorded, and the file kept;
//   - otherwise, a leftover temporary file included: wb-diskfmt makes a
//     new disk, and deletes the leftover first.
//
// The bundle directory must exist.
func (m *Manager) Prepare(ctx context.Context, slot string) error {
	if r := m.lock(slot); r != nil {
		return m.refuse(ctx, "prepare", r)
	}
	defer m.unlock(slot)
	rec, found, err := m.cfg.Records.DataDiskRecord(ctx, vmbundle.Name(slot))
	if err != nil {
		return m.refuse(ctx, "prepare", recordFailed(slot, err))
	}
	final := filepath.Join(vmbundle.Path(m.cfg.DataDir, slot), vmbundle.DataDiskName)
	exists, err := nameExists(final)
	if err != nil {
		return m.refuse(ctx, "prepare", &Refusal{
			Rule: RuleRecordFailed, Slot: slot, err: err,
			Message: fmt.Sprintf("the %s VM can't start: %s can't be read: %v", slot, final, err),
		})
	}
	formatted := found && rec.Formatted
	switch {
	case formatted && exists:
		m.log.LogAttrs(ctx, slog.LevelInfo, "data disk ready", slog.String("decision", "accept"),
			slog.String("slot", slot), slog.String("volume_uuid", rec.VolumeUUID))
		return nil
	case formatted:
		return m.refuse(ctx, "prepare", &Refusal{
			Rule: RuleMissing, Slot: slot,
			Message: fmt.Sprintf("the %s VM can't start: its data disk %s is missing, so the project homes on it are gone. %s", slot, final, fix(slot)),
		})
	case exists:
		return m.refuse(ctx, "prepare", &Refusal{
			Rule: RuleUnrecorded, Slot: slot,
			Message: fmt.Sprintf("the %s VM can't start: %s isn't recorded as a formatted data disk, so it is kept and not used. %s", slot, final, fix(slot)),
		})
	}
	_, r := m.format(ctx, slot)
	if r != nil {
		return m.refuse(ctx, "prepare", r)
	}
	return nil
}

// Started records that slot's VM started with its disk, the end of the
// SEC15-no-guest-disk-mount exception (S06-13-data-disk-format, "The one
// exception"). The first start's time is kept.
func (m *Manager) Started(ctx context.Context, slot string) error {
	if err := m.cfg.Records.MarkDataDiskStarted(ctx, vmbundle.Name(slot), m.cfg.Now()); err != nil {
		return m.refuse(ctx, "started", recordFailed(slot, err))
	}
	return nil
}

// ResetResult is what a reset changed.
type ResetResult struct {
	// OldVolumeUUID is the volume UUID of the disk deleted, "" when no
	// record named one.
	OldVolumeUUID string
	NewVolumeUUID string
}

// Reset replaces slot's data disk (S05-03-management-commands, "`wb vm
// reset-data`"). The caller holds the VM's start lock and has checked
// that the VM is stopped. It deletes the file at the final name and any
// temporary file, without following a symbolic link, drops the record,
// and has wb-diskfmt make a new disk, which it records. It logs the
// reset as one line with the fields of a 5019 event.
func (m *Manager) Reset(ctx context.Context, slot string) (ResetResult, error) {
	if r := m.lock(slot); r != nil {
		return ResetResult{}, m.refuse(ctx, "reset", r)
	}
	defer m.unlock(slot)
	key := vmbundle.Name(slot)
	rec, _, err := m.cfg.Records.DataDiskRecord(ctx, key)
	if err != nil {
		return ResetResult{}, m.refuse(ctx, "reset", recordFailed(slot, err))
	}
	bundle := vmbundle.Path(m.cfg.DataDir, slot)
	// The one place wb-hostd deletes a file at the final name
	// (S06-05-disks, "After a crash"). os.Remove unlinks the name: it
	// never opens the file, and never follows a link
	// (SEC15-no-guest-disk-mount).
	for _, name := range []string{vmbundle.DataDiskName, vmbundle.DataDiskTempName} {
		if err := removeName(filepath.Join(bundle, name)); err != nil {
			return ResetResult{}, m.refuse(ctx, "reset", &Refusal{
				Rule: RuleResetFailed, Slot: slot, err: err,
				Message: fmt.Sprintf("the %s VM's data disk can't be deleted: %v. Move %s away, then run wb vm reset-data %s again", slot, err, name, slot),
			})
		}
	}
	if err := m.cfg.Records.DropDataDisk(ctx, key); err != nil {
		return ResetResult{}, m.refuse(ctx, "reset", recordFailed(slot, err))
	}
	m.log.LogAttrs(ctx, slog.LevelInfo, "data disk deleted", slog.String("decision", "accept"),
		slog.String("slot", slot), slog.String("volume_uuid", rec.VolumeUUID))
	uuid, r := m.format(ctx, slot)
	if r != nil {
		return ResetResult{OldVolumeUUID: rec.VolumeUUID}, m.refuse(ctx, "reset", r)
	}
	// The fields of a Device Config State Change (5019) event. M3.10's
	// audit writer turns this line into the event (S09-11-audit).
	m.log.LogAttrs(ctx, slog.LevelInfo, "data disk reset",
		slog.String("event", "5019"), slog.String("decision", "accept"),
		slog.String("command", "wb vm reset-data"), slog.String("vm", slot),
		slog.String("old_volume_uuid", rec.VolumeUUID), slog.String("new_volume_uuid", uuid))
	return ResetResult{OldVolumeUUID: rec.VolumeUUID, NewVolumeUUID: uuid}, nil
}

// format runs wb-diskfmt for slot and writes the records in the order
// of S06-13-data-disk-format: the volume UUID and the temporary name after the
// detach (step 7), before the rename, and the formatted flag after it
// (step 9). A crash between the rename and step 9 leaves a file at the
// final name not recorded as formatted, which Prepare refuses.
func (m *Manager) format(ctx context.Context, slot string) (string, *Refusal) {
	run, err := m.cfg.Shim.Start(ctx, slot)
	if err != nil {
		return "", shimFailed(slot, "start", err)
	}
	defer func() {
		if err := run.Close(); err != nil {
			m.log.LogAttrs(ctx, slog.LevelWarn, "wb-diskfmt ended with an error",
				slog.String("slot", slot), slog.String("error", bound(err.Error())))
		}
	}()
	res, err := run.Format(ctx, m.cfg.SizeGiB)
	if err != nil {
		return "", shimFailed(slot, "format", err)
	}
	uuid := res.GetVolumeUuid()
	if !diskfmt.CanonicalUUID(uuid) || res.GetTemporaryName() != vmbundle.DataDiskTempName {
		return "", shimReply(slot, fmt.Sprintf("the volume UUID %q and the name %q", bound(uuid), bound(res.GetTemporaryName())))
	}
	key := vmbundle.Name(slot)
	rec := state.DataDisk{Slot: key, VolumeUUID: uuid, TempName: vmbundle.DataDiskTempName, SizeGiB: m.cfg.SizeGiB}
	if err := m.cfg.Records.RecordDataDiskFormat(ctx, rec); err != nil {
		return "", recordFailed(slot, err)
	}
	renamed, err := run.Rename(ctx)
	if err != nil {
		return "", shimFailed(slot, "rename", err)
	}
	if renamed.GetFinalName() != vmbundle.DataDiskName {
		return "", shimReply(slot, fmt.Sprintf("the final name %q", bound(renamed.GetFinalName())))
	}
	if err := m.cfg.Records.MarkDataDiskFormatted(ctx, key, uuid); err != nil {
		return "", recordFailed(slot, err)
	}
	m.log.LogAttrs(ctx, slog.LevelInfo, "data disk made", slog.String("decision", "accept"),
		slog.String("slot", slot), slog.String("volume_uuid", uuid), slog.Uint64("size_gib", uint64(m.cfg.SizeGiB)))
	return uuid, nil
}

func recordFailed(slot string, err error) *Refusal {
	return &Refusal{
		Rule: RuleRecordFailed, Slot: slot, err: err,
		Message: fmt.Sprintf("the %s VM's data disk record in the state database failed: %v", slot, err),
	}
}

func shimReply(slot, what string) *Refusal {
	return &Refusal{
		Rule: RuleShimReply, Slot: slot,
		Message: fmt.Sprintf("wb-diskfmt answered with %s, which isn't valid, so the %s VM's new data disk isn't used", what, slot),
	}
}

// shimFailed turns wb-diskfmt's error into a refusal. A *ShimRefusal
// keeps its rule when that is a diskfmt- rule name. Any other error, or
// rule, is data-disk-format-failed.
func shimFailed(slot, step string, err error) *Refusal {
	sr, ok := errors.AsType[*ShimRefusal](err)
	if !ok {
		return &Refusal{
			Rule: RuleFormatFailed, Slot: slot, err: err,
			Message: fmt.Sprintf("the %s VM's new data disk failed at the %s: %s", slot, step, bound(err.Error())),
		}
	}
	rule := RuleFormatFailed
	if isRuleName(sr.Rule) && strings.HasPrefix(sr.Rule, "diskfmt-") {
		rule = Rule(sr.Rule)
	}
	return &Refusal{
		Rule: rule, Slot: slot, err: err,
		Message: fmt.Sprintf("wb-diskfmt refused the %s VM's new data disk at the %s (%q): %s", slot, step, bound(sr.Rule), bound(sr.Detail)),
	}
}

// nameExists reports whether anything has the name path, read with
// lstat: a link isn't followed, and no file is opened.
func nameExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// removeName deletes the name path, a symbolic link and not its
// target. A missing name is no error.
func removeName(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// isRuleName reports whether s has the form of a rule: 1 to 64 bytes of
// lowercase ASCII letters, digits and hyphens.
func isRuleName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	return strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

// bound cuts s to diskfmt.MaxDetail bytes. wb-diskfmt is confined and
// its text is still treated as untrusted (S04-04-vm-processes,
// "Reaching an instance").
func bound(s string) string {
	if len(s) <= diskfmt.MaxDetail {
		return s
	}
	return strings.ToValidUTF8(s[:diskfmt.MaxDetail], "") + " [cut]"
}
