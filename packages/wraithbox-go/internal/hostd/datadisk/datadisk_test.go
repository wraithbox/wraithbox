package datadisk_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	diskfmtv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/diskfmt/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk/datadisktest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// The wb-hostd cases of C11-host-daemons, "Data disk steps": a fake
// wb-diskfmt and a temporary state.db.

const (
	uuid1 = "0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0"
	uuid2 = "11111111-2222-3333-4444-555555555555"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// steps wraps state.db, and records each data disk write with what is
// in the bundle at that moment.
type steps struct {
	*state.DB

	bundle   string
	mu       sync.Mutex
	log      []string
	failMark error
}

func (s *steps) note(step string) {
	_, tmpErr := os.Lstat(filepath.Join(s.bundle, vmbundle.DataDiskTempName))
	_, finalErr := os.Lstat(filepath.Join(s.bundle, vmbundle.DataDiskName))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, step+" temp="+present(tmpErr)+" final="+present(finalErr))
}

func present(err error) string {
	if err == nil {
		return "yes"
	}
	return "no"
}

func (s *steps) RecordDataDiskFormat(ctx context.Context, rec state.DataDisk) error {
	s.note("step7")
	return s.DB.RecordDataDiskFormat(ctx, rec)
}

func (s *steps) MarkDataDiskFormatted(ctx context.Context, slot, uuid string) error {
	s.note("step9")
	if s.failMark != nil {
		return s.failMark
	}
	return s.DB.MarkDataDiskFormatted(ctx, slot, uuid)
}

type harness struct {
	t       *testing.T
	data    string
	db      *state.DB
	records *steps
	shim    *datadisktest.Shim
	logs    *syncBuffer
	m       *datadisk.Manager
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	data := t.TempDir()
	for _, s := range vmbundle.Slots() {
		if err := os.MkdirAll(vmbundle.Path(data, s), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	db, err := state.Open(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &harness{
		t: t, data: data, db: db, logs: &syncBuffer{},
		records: &steps{DB: db, bundle: vmbundle.Path(data, "work")},
		shim:    &datadisktest.Shim{DataDir: data, UUIDs: []string{uuid1, uuid2}},
	}
	h.m, err = datadisk.New(datadisk.Config{
		DataDir: data, Records: h.records, Shim: h.shim, SizeGiB: 64,
		Logger: slog.New(slog.NewJSONHandler(h.logs, nil)),
		Now:    func() time.Time { return time.Unix(1000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) path(name string) string {
	return filepath.Join(vmbundle.Path(h.data, "work"), name)
}

func (h *harness) write(name, content string) {
	h.t.Helper()
	if err := os.WriteFile(h.path(name), []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) finalContent() string {
	h.t.Helper()
	b, err := os.ReadFile(h.path(vmbundle.DataDiskName))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func (h *harness) record() (state.DataDisk, bool) {
	h.t.Helper()
	rec, ok, err := h.db.DataDiskRecord(h.t.Context(), "macos-work")
	if err != nil {
		h.t.Fatal(err)
	}
	return rec, ok
}

func refused(t *testing.T, err error, rule datadisk.Rule) *datadisk.Refusal {
	t.Helper()
	r, ok := errors.AsType[*datadisk.Refusal](err)
	if !ok {
		t.Fatalf("err = %v, want a refusal with %s", err, rule)
	}
	if r.Rule != rule {
		t.Fatalf("rule = %s (%s), want %s", r.Rule, r.Message, rule)
	}
	return r
}

// TestNewDisk: no record and no file make a new disk, with the records
// written in the order of steps 7 and 9.
func TestNewDisk(t *testing.T) {
	h := newHarness(t)
	if err := h.m.Prepare(t.Context(), "work"); err != nil {
		t.Fatal(err)
	}
	rec, ok := h.record()
	if !ok || !rec.Formatted || rec.VolumeUUID != uuid1 || rec.TempName != vmbundle.DataDiskTempName || rec.SizeGiB != 64 {
		t.Fatalf("record %+v (%t)", rec, ok)
	}
	want := []string{"step7 temp=yes final=no", "step9 temp=no final=yes"}
	if !slices.Equal(h.records.log, want) {
		t.Errorf("steps %v, want %v", h.records.log, want)
	}
	if h.shim.Open() != 0 {
		t.Error("the run wasn't closed")
	}
	// A second start uses the disk, and runs no wb-diskfmt.
	if err := h.m.Prepare(t.Context(), "work"); err != nil {
		t.Fatal(err)
	}
	if got := h.shim.Starts(); len(got) != 1 {
		t.Errorf("wb-diskfmt runs %v, want one", got)
	}
	if err := h.m.Started(t.Context(), "work"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := h.record(); !rec.FirstStarted.Equal(time.Unix(1000, 0)) {
		t.Errorf("first start %v", rec.FirstStarted)
	}
}

// The setups of TestStartChecks.

func fileNotRecorded(_ *testing.T, h *harness) { h.write(vmbundle.DataDiskName, "kept") }

func fileRecordedNotFormatted(t *testing.T, h *harness) {
	h.write(vmbundle.DataDiskName, "kept")
	rec := state.DataDisk{Slot: "macos-work", VolumeUUID: uuid2, TempName: vmbundle.DataDiskTempName, SizeGiB: 8}
	if err := h.db.RecordDataDiskFormat(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
}

func linkAtFinalName(t *testing.T, h *harness) {
	if err := os.Symlink(filepath.Join(h.data, "elsewhere"), h.path(vmbundle.DataDiskName)); err != nil {
		t.Fatal(err)
	}
}

func formattedDiskMissing(t *testing.T, h *harness) {
	if err := h.m.Prepare(t.Context(), "work"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path(vmbundle.DataDiskName)); err != nil {
		t.Fatal(err)
	}
}

// TestStartChecks: each case of a start that is refused keeps the file
// at the final name, and runs no wb-diskfmt.
func TestStartChecks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *harness)
		rule  datadisk.Rule
	}{
		{"a file not recorded", fileNotRecorded, datadisk.RuleUnrecorded},
		{"a file recorded but not formatted", fileRecordedNotFormatted, datadisk.RuleUnrecorded},
		{"a link at the final name", linkAtFinalName, datadisk.RuleUnrecorded},
		{"a formatted disk missing", formattedDiskMissing, datadisk.RuleMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.setup(t, h)
			before := h.finalContent()
			runs := len(h.shim.Starts())
			r := refused(t, h.m.Prepare(t.Context(), "work"), tc.rule)
			if !strings.Contains(r.Message, "wb vm stop work") || !strings.Contains(r.Message, "wb vm reset-data work") {
				t.Errorf("the refusal doesn't name the fix: %s", r.Message)
			}
			if got := h.finalContent(); got != before {
				t.Errorf("the final name holds %q, was %q", got, before)
			}
			if len(h.shim.Starts()) != runs {
				t.Error("wb-diskfmt ran")
			}
			if !strings.Contains(h.logs.String(), `"rule":"`+string(tc.rule)+`"`) {
				t.Errorf("the log doesn't name %s", tc.rule)
			}
		})
	}
}

// TestLeftoverFormattedAgain: a leftover temporary file is left to
// wb-diskfmt, which deletes it and formats again.
func TestLeftoverFormattedAgain(t *testing.T) {
	h := newHarness(t)
	h.write(vmbundle.DataDiskTempName, "leftover")
	if err := h.m.Prepare(t.Context(), "work"); err != nil {
		t.Fatal(err)
	}
	if got := h.finalContent(); got != "formatted 64 GiB" {
		t.Errorf("the final name holds %q", got)
	}
}

// TestCrashBeforeStep9: a rename whose step 9 failed leaves a file at
// the final name not recorded as formatted, and the next start is
// refused with data-disk-unrecorded.
func TestCrashBeforeStep9(t *testing.T) {
	h := newHarness(t)
	h.records.failMark = errors.New("disk I/O error")
	refused(t, h.m.Prepare(t.Context(), "work"), datadisk.RuleRecordFailed)
	h.records.failMark = nil
	refused(t, h.m.Prepare(t.Context(), "work"), datadisk.RuleUnrecorded)
	if got := h.finalContent(); got != "formatted 64 GiB" {
		t.Errorf("the final name holds %q", got)
	}
}

// TestShimAnswers: wb-diskfmt's refusal keeps its own rule, and any
// other answer is checked as untrusted. Nothing is recorded as
// formatted after a failure.
func TestShimAnswers(t *testing.T) {
	long := strings.Repeat("x", 5000)
	for _, tc := range []struct {
		name     string
		onFormat func(string, uint32) (*diskfmtv1.FormatResult, error)
		onRename func(string) (*diskfmtv1.RenameResult, error)
		rule     datadisk.Rule
	}{
		{name: "backing path refused", onFormat: func(string, uint32) (*diskfmtv1.FormatResult, error) {
			return nil, &datadisk.ShimRefusal{Rule: "diskfmt-backing-path", Detail: "disk7 is backed by another image"}
		}, rule: "diskfmt-backing-path"},
		{name: "a rule of another component", onFormat: func(string, uint32) (*diskfmtv1.FormatResult, error) {
			return nil, &datadisk.ShimRefusal{Rule: "data-disk-missing", Detail: long}
		}, rule: datadisk.RuleFormatFailed},
		{name: "not a rule", onFormat: func(string, uint32) (*diskfmtv1.FormatResult, error) {
			return nil, &datadisk.ShimRefusal{Rule: "diskfmt-\x1b[2J", Detail: "x"}
		}, rule: datadisk.RuleFormatFailed},
		{name: "the shim ended", onFormat: func(string, uint32) (*diskfmtv1.FormatResult, error) {
			return nil, errors.New("no answer from wb-diskfmt: EOF")
		}, rule: datadisk.RuleFormatFailed},
		{name: "lowercase uuid", onFormat: func(string, uint32) (*diskfmtv1.FormatResult, error) {
			return &diskfmtv1.FormatResult{VolumeUuid: strings.ToLower(uuid1), TemporaryName: vmbundle.DataDiskTempName}, nil
		}, rule: datadisk.RuleShimReply},
		{name: "another temporary name", onFormat: func(string, uint32) (*diskfmtv1.FormatResult, error) {
			return &diskfmtv1.FormatResult{VolumeUuid: uuid1, TemporaryName: "../../x"}, nil
		}, rule: datadisk.RuleShimReply},
		{name: "another final name", onRename: func(string) (*diskfmtv1.RenameResult, error) {
			return &diskfmtv1.RenameResult{FinalName: "data.img"}, nil
		}, rule: datadisk.RuleShimReply},
		{name: "final exists", onRename: func(string) (*diskfmtv1.RenameResult, error) {
			return nil, &datadisk.ShimRefusal{Rule: "diskfmt-final-exists", Detail: "kept"}
		}, rule: "diskfmt-final-exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.shim.OnFormat, h.shim.OnRename = tc.onFormat, tc.onRename
			r := refused(t, h.m.Prepare(t.Context(), "work"), tc.rule)
			if len(r.Message) > 2*1024+200 {
				t.Errorf("the message is %d bytes, not cut", len(r.Message))
			}
			if rec, ok := h.record(); ok && rec.Formatted {
				t.Errorf("recorded as formatted: %+v", rec)
			}
			if h.shim.Open() != 0 {
				t.Error("the run wasn't closed")
			}
		})
	}
}

// TestReset: the old file and a leftover are deleted, the record
// dropped, and a new disk recorded with a new UUID. The log line holds
// both UUIDs.
func TestReset(t *testing.T) {
	h := newHarness(t)
	if err := h.m.Prepare(t.Context(), "work"); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Started(t.Context(), "work"); err != nil {
		t.Fatal(err)
	}
	h.write(vmbundle.DataDiskName, "guest-written")
	h.write(vmbundle.DataDiskTempName, "leftover")
	res, err := h.m.Reset(t.Context(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if res.OldVolumeUUID != uuid1 || res.NewVolumeUUID != uuid2 {
		t.Errorf("result %+v", res)
	}
	rec, ok := h.record()
	if !ok || !rec.Formatted || rec.VolumeUUID != uuid2 || !rec.FirstStarted.IsZero() {
		t.Errorf("record %+v (%t)", rec, ok)
	}
	if got := h.finalContent(); got != "formatted 64 GiB" {
		t.Errorf("the final name holds %q", got)
	}
	logs := h.logs.String()
	for _, want := range []string{`"msg":"data disk reset"`, `"old_volume_uuid":"` + uuid1 + `"`, `"new_volume_uuid":"` + uuid2 + `"`, `"command":"wb vm reset-data"`, `"vm":"work"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log misses %s:\n%s", want, logs)
		}
	}
}

// TestResetWithoutDisk: a reset of a slot that never had a disk makes
// one, with no old UUID.
func TestResetWithoutDisk(t *testing.T) {
	h := newHarness(t)
	res, err := h.m.Reset(t.Context(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if res.OldVolumeUUID != "" || res.NewVolumeUUID != uuid1 {
		t.Errorf("result %+v", res)
	}
}

// TestResetRefusedDirectory: a final name that can't be deleted refuses
// the reset, and the record stays.
func TestResetRefusedDirectory(t *testing.T) {
	h := newHarness(t)
	if err := h.m.Prepare(t.Context(), "work"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path(vmbundle.DataDiskName)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.path(vmbundle.DataDiskName), "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := h.m.Reset(t.Context(), "work"); return err }(), datadisk.RuleResetFailed)
	if rec, ok := h.record(); !ok || rec.VolumeUUID != uuid1 {
		t.Errorf("record %+v (%t), want it kept", rec, ok)
	}
}

// TestOneRunPerSlot: a second format or reset of a slot while one runs
// is refused with data-disk-busy, and the other slot isn't held.
func TestOneRunPerSlot(t *testing.T) {
	h := newHarness(t)
	h.shim.Block = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- h.m.Prepare(context.WithoutCancel(t.Context()), "work") }()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.shim.Starts()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	refused(t, h.m.Prepare(t.Context(), "work"), datadisk.RuleBusy)
	refused(t, func() error { _, err := h.m.Reset(t.Context(), "work"); return err }(), datadisk.RuleBusy)
	close(h.shim.Block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := h.m.Prepare(t.Context(), "isolated"); err != nil {
		t.Fatal(err)
	}
	refused(t, h.m.Prepare(t.Context(), "build"), datadisk.RuleSlotUnknown)
}

func TestNewRefusesSize(t *testing.T) {
	for _, gib := range []uint32{4, 2048} {
		if _, err := datadisk.New(datadisk.Config{DataDir: t.TempDir(), Records: &steps{}, Shim: &datadisktest.Shim{}, SizeGiB: gib}); err == nil {
			t.Errorf("%d GiB accepted", gib)
		}
	}
}
