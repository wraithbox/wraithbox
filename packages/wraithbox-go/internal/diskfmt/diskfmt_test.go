package diskfmt_test

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
)

// The data disk steps of C11-host-daemons, with a fake diskutil and an
// in-memory file system (S06-13-data-disk-format, "Formatting a data disk").

type harness struct {
	t     *testing.T
	files *fakeFiles
	tool  *fakeTool
	f     *diskfmt.Formatter
	logs  *bytes.Buffer
	// leftAttached is set by a test whose device stays attached.
	leftAttached bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	files := newFakeFiles()
	logs := &bytes.Buffer{}
	h := &harness{t: t, files: files, tool: newFakeTool(files), logs: logs}
	h.f = &diskfmt.Formatter{
		Bundle: filepath.Join("/", "data", "vms", "macos-work.noindex"),
		Files:  files, Tool: h.tool,
		Log: slog.New(slog.NewJSONHandler(logs, nil)),
	}
	t.Cleanup(func() {
		// SEC15-no-guest-disk-mount: the final name is never attached.
		for _, p := range h.tool.paths {
			if p != h.f.TempPath() {
				t.Errorf("the fake diskutil was asked to attach %s", p)
			}
		}
		if len(h.tool.attached) != 0 && !h.leftAttached {
			t.Errorf("devices left attached: %v", h.tool.attached)
		}
	})
	return h
}

func (h *harness) refused(r *diskfmt.Refusal, rule diskfmt.Rule) {
	h.t.Helper()
	if r == nil {
		h.t.Fatalf("no refusal, want %s", rule)
	}
	if r.Rule != rule {
		h.t.Fatalf("rule = %s (%s), want %s", r.Rule, r.Detail, rule)
	}
	if !strings.Contains(h.logs.String(), `"rule":"`+string(rule)+`"`) {
		h.t.Errorf("the log doesn't name %s:\n%s", rule, h.logs.String())
	}
}

func (h *harness) erased() bool { return slices.Contains(h.tool.calls, "erase") }

func TestFormatAndRename(t *testing.T) {
	h := newHarness(t)
	uuid, r := h.f.Format(t.Context(), diskfmt.DefaultSizeGiB)
	if r != nil {
		t.Fatal(r)
	}
	if uuid != h.tool.uuid {
		t.Errorf("uuid = %s, want %s", uuid, h.tool.uuid)
	}
	want := []string{"attach", "backing", "backing", "erase", "uuid", "detach"}
	if !slices.Equal(h.tool.calls, want) {
		t.Errorf("calls = %v, want %v", h.tool.calls, want)
	}
	if r := h.f.Rename(t.Context()); r != nil {
		t.Fatal(r)
	}
	if h.files.has(h.f.TempPath()) || !h.files.has(h.f.FinalPath()) {
		t.Errorf("after the rename: temporary %t, final %t", h.files.has(h.f.TempPath()), h.files.has(h.f.FinalPath()))
	}
	if err := h.f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestAttachReportsNoBacking: diskutil on macOS 27 names no backing
// path in the attach output, and the registry read decides.
func TestAttachReportsNoBacking(t *testing.T) {
	h := newHarness(t)
	h.tool.attachNoBacking = true
	if _, r := h.f.Format(t.Context(), diskfmt.DefaultSizeGiB); r != nil {
		t.Fatal(r)
	}
}

// TestRegistryRefusesAtAttach: when the attach output names no backing
// path and the I/O Registry names another, the check at the attach
// refuses, before the identity check and the format.
func TestRegistryRefusesAtAttach(t *testing.T) {
	h := newHarness(t)
	h.tool.attachNoBacking = true
	h.tool.backing = h.f.FinalPath()
	_, r := h.f.Format(t.Context(), diskfmt.DefaultSizeGiB)
	h.refused(r, diskfmt.RuleBackingPath)
	if want := []string{"attach", "backing", "detach"}; !slices.Equal(h.tool.calls, want) {
		t.Errorf("calls = %v, want %v", h.tool.calls, want)
	}
}

// TestLeftoverDeleted: a leftover temporary file is unlinked, and the
// file attached is the new one.
func TestLeftoverDeleted(t *testing.T) {
	h := newHarness(t)
	h.files.put(h.f.TempPath(), true)
	leftover := h.files.ino(h.f.TempPath())
	h.tool.beforeAttach = func() {
		if h.files.ino(h.f.TempPath()) == leftover {
			t.Error("the leftover is about to be attached")
		}
	}
	if _, r := h.f.Format(t.Context(), diskfmt.MinSizeGiB); r != nil {
		t.Fatal(r)
	}
	if !strings.Contains(h.logs.String(), "leftover temporary data disk deleted") {
		t.Errorf("the deletion isn't logged:\n%s", h.logs.String())
	}
}

func TestLeftoverDirectoryRefused(t *testing.T) {
	h := newHarness(t)
	h.files.put(h.f.TempPath(), false)
	_, r := h.f.Format(t.Context(), diskfmt.DefaultSizeGiB)
	h.refused(r, diskfmt.RuleTempDelete)
	if len(h.tool.calls) != 0 {
		t.Errorf("diskutil ran: %v", h.tool.calls)
	}
}

func TestSizeRefused(t *testing.T) {
	for _, gib := range []uint32{0, diskfmt.MinSizeGiB - 1, diskfmt.MaxSizeGiB + 1, 1 << 31} {
		h := newHarness(t)
		_, r := h.f.Format(t.Context(), gib)
		h.refused(r, diskfmt.RuleSizeInvalid)
		if h.files.has(h.f.TempPath()) || len(h.tool.calls) != 0 {
			t.Errorf("%d GiB: a file was created or diskutil ran (%v)", gib, h.tool.calls)
		}
	}
	for _, gib := range []uint32{diskfmt.MinSizeGiB, diskfmt.DefaultSizeGiB, diskfmt.MaxSizeGiB} {
		if r := diskfmt.CheckSize(gib); r != nil {
			t.Errorf("CheckSize(%d) = %v", gib, r)
		}
	}
}

// TestRefusals: each failed check fails with its rule. A failure after
// the attach detaches that device, and one before it detaches nothing.
// The file at the final name, when there is one, is kept.
func TestRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		setup    func(h *harness)
		rule     diskfmt.Rule
		attached bool
	}{
		{"another backing path at the attach", func(h *harness) {
			h.tool.attachBacking = filepath.Join("/", "data", "vms", "macos-isolated.noindex", "data.wbdisk.tmp")
		}, diskfmt.RuleBackingPath, true},
		{"an ambiguous backing path at the attach", func(h *harness) {
			h.tool.attachAmbiguous = true
		}, diskfmt.RuleBackingPath, true},
		{"another backing path before the format", func(h *harness) {
			h.tool.backing = h.f.FinalPath()
			h.tool.backingFrom = 1
		}, diskfmt.RuleBackingPath, true},
		{"replaced before the attach", func(h *harness) {
			h.tool.beforeAttach = func() { h.files.put(h.f.TempPath(), true) }
		}, diskfmt.RuleIdentity, true},
		{"hard link before the attach", func(h *harness) {
			h.tool.beforeAttach = func() { h.files.link(h.f.TempPath(), filepath.Join(h.f.Bundle, "other")) }
		}, diskfmt.RuleIdentity, true},
		{"replaced before the format", func(h *harness) {
			h.afterAttach(func() { h.files.put(h.f.TempPath(), true) })
		}, diskfmt.RuleIdentity, true},
		{"hard link before the format", func(h *harness) {
			h.afterAttach(func() { h.files.link(h.f.TempPath(), h.f.FinalPath()) })
		}, diskfmt.RuleIdentity, true},
		{"attach fails", func(h *harness) {
			h.tool.attachErr = errors.New("hdiutil: attach failed")
		}, diskfmt.RuleDiskutil, false},
		{"erase fails", func(h *harness) {
			h.tool.eraseErr = errors.New("erase failed")
		}, diskfmt.RuleDiskutil, true},
		{"volume UUID not canonical", func(h *harness) {
			h.tool.uuid = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"
		}, diskfmt.RuleVolumeUUID, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			finalBefore := h.files.ino(h.f.FinalPath())
			tc.setup(h)
			_, r := h.f.Format(t.Context(), diskfmt.DefaultSizeGiB)
			h.refused(r, tc.rule)
			if h.erased() && tc.rule != diskfmt.RuleDiskutil && tc.rule != diskfmt.RuleVolumeUUID {
				t.Error("the device was formatted after a failed check")
			}
			if got := len(h.tool.detached); (got == 1) != tc.attached || got > 1 {
				t.Errorf("detached %v, want one detach: %t", h.tool.detached, tc.attached)
			}
			if finalBefore == 0 && h.files.ino(h.f.FinalPath()) != 0 && !strings.Contains(tc.name, "hard link") {
				t.Error("a file appeared at the final name")
			}
		})
	}
}

// afterAttach runs fn once the attach has attached the file, before the
// checks that come before the format.
func (h *harness) afterAttach(fn func()) {
	h.tool.afterAttach = fn
}

// TestReplacedBeforeRename: a name replaced after the checks before the
// format is found by the check before the rename, and nothing gets the
// final name.
func TestReplacedBeforeRename(t *testing.T) {
	h := newHarness(t)
	h.tool.beforeErase = func() { h.files.put(h.f.TempPath(), true) }
	if _, r := h.f.Format(t.Context(), diskfmt.DefaultSizeGiB); r != nil {
		t.Fatal(r)
	}
	h.refused(h.f.Rename(t.Context()), diskfmt.RuleIdentity)
	if h.files.has(h.f.FinalPath()) {
		t.Error("the replaced file was renamed to the final name")
	}
}

// TestFinalExists: a file put at the final name before the rename is
// kept, and the rename fails with the rule.
func TestFinalExists(t *testing.T) {
	h := newHarness(t)
	if _, r := h.f.Format(t.Context(), diskfmt.DefaultSizeGiB); r != nil {
		t.Fatal(r)
	}
	h.files.put(h.f.FinalPath(), true)
	planted := h.files.ino(h.f.FinalPath())
	h.refused(h.f.Rename(t.Context()), diskfmt.RuleFinalExists)
	if h.files.ino(h.f.FinalPath()) != planted {
		t.Error("the file at the final name was replaced")
	}
	if len(h.files.renames) != 0 {
		t.Errorf("renames: %v", h.files.renames)
	}
}

// TestDetachRetried: a detach that fails once after the format is tried
// again. One that fails twice refuses with the device named.
func TestDetachRetried(t *testing.T) {
	h := newHarness(t)
	h.tool.detachFails = 1
	if _, r := h.f.Format(t.Context(), diskfmt.DefaultSizeGiB); r != nil {
		t.Fatal(r)
	}
	h = newHarness(t)
	h.leftAttached = true
	h.tool.detachFails = 2
	_, r := h.f.Format(t.Context(), diskfmt.DefaultSizeGiB)
	h.refused(r, diskfmt.RuleDiskutil)
	if !strings.Contains(r.Detail, "disk9") {
		t.Errorf("the detail doesn't name the device: %s", r.Detail)
	}
}

func TestRenameWithoutFormat(t *testing.T) {
	h := newHarness(t)
	h.refused(h.f.Rename(t.Context()), diskfmt.RuleRename)
}

func TestCanonicalUUID(t *testing.T) {
	for s, want := range map[string]bool{
		"0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0":  true,
		"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0":  false,
		"0F1E2D3C4B5A69788796A5B4C3D2E1F0":      false,
		"0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F":   false,
		"0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F00": false,
		"0F1E2D3C-4B5A-6978-8796_A5B4C3D2E1F0":  false,
		"0G1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0":  false,
		"":                                      false,
	} {
		if got := diskfmt.CanonicalUUID(s); got != want {
			t.Errorf("CanonicalUUID(%q) = %t, want %t", s, got, want)
		}
	}
}
