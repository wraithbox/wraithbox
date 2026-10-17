package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// SlotEnv names the slot (S04-04-vm-processes, "`wb-diskfmt`").
const SlotEnv = diskfmt.SlotEnv

// The start rules: each refuses before confinement, and is logged
// (S04-04-vm-processes, "`wb-diskfmt`"). The format's own rules are in
// internal/diskfmt.
const (
	// ruleArguments: wb-diskfmt takes no argument.
	ruleArguments = "diskfmt-arguments"
	// ruleSlotMissing: WB_DISKFMT_SLOT is missing or empty.
	ruleSlotMissing = "diskfmt-slot-missing"
	// ruleSlotUnknown: WB_DISKFMT_SLOT is neither work nor isolated.
	ruleSlotUnknown = "diskfmt-slot-unknown"
	// ruleBundleMissing: the slot's bundle doesn't exist or isn't a
	// directory.
	ruleBundleMissing = "diskfmt-bundle-missing"
	// ruleSlotBundleMismatch: the bundle's real path isn't that slot's
	// bundle under the real path of <data>, as when the bundle is a
	// symbolic link to the other slot's (SEC08-proj-isolation).
	ruleSlotBundleMismatch = "diskfmt-slot-bundle-mismatch"
	// rulePaths: <data> can't be found from the user database.
	rulePaths = "diskfmt-paths"
	// ruleDescriptor: descriptor 3 isn't a connected Unix datagram
	// socket.
	ruleDescriptor = "diskfmt-descriptor-invalid"
	// ruleUnsupported: this OS has no data disk format.
	ruleUnsupported = "diskfmt-unsupported"
)

// startRefusal is a refusal before confinement.
type startRefusal struct {
	Rule   string
	Detail string
	attrs  []slog.Attr
}

func logStartRefusal(log *slog.Logger, r *startRefusal) {
	attrs := append([]slog.Attr{
		slog.String("rule", r.Rule), slog.String("decision", "refuse"),
		slog.String("reason", r.Detail),
	}, r.attrs...)
	log.LogAttrs(context.Background(), slog.LevelError, "wb-diskfmt start refused", attrs...)
}

// checkStart checks the start interface and returns the real path of
// the slot's bundle, the profile's BUNDLE parameter. The slot comes from
// the entry's environment, never from an argument or from wb-hostd, so
// the slot and the bundle come from the same launcher entry.
func checkStart(log *slog.Logger, args []string, getenv func(string) string) (string, *startRefusal) {
	if len(args) > 0 {
		return "", &startRefusal{Rule: ruleArguments, Detail: fmt.Sprintf("wb-diskfmt takes no argument, and got %d", len(args))}
	}
	slot := getenv(SlotEnv)
	if slot == "" {
		return "", &startRefusal{Rule: ruleSlotMissing, Detail: SlotEnv + " is missing"}
	}
	if !vmbundle.ValidSlot(slot) {
		return "", &startRefusal{Rule: ruleSlotUnknown, Detail: fmt.Sprintf("%s is %s, not %s or %s", SlotEnv, strconv.Quote(cut(slot)), vmbundle.SlotWork, vmbundle.SlotIsolated)}
	}
	data, err := dataDir(log)
	if err != nil {
		return "", &startRefusal{Rule: rulePaths, Detail: "<data> can't be found: " + err.Error()}
	}
	return realBundle(slot, data)
}

// realBundle resolves the slot's bundle and <data> through every
// symbolic link, and requires that the one is the slot's bundle under
// the other.
func realBundle(slot, data string) (string, *startRefusal) {
	bundle := vmbundle.Path(data, slot)
	attrs := []slog.Attr{slog.String("slot", slot), slog.String("bundle", bundle)}
	isDir, err := statDir(bundle)
	if err != nil || !isDir {
		detail := fmt.Sprintf("the %s VM's bundle %s isn't a directory", slot, bundle)
		if err != nil {
			detail = fmt.Sprintf("the %s VM's bundle %s can't be read: %v", slot, bundle, err)
		}
		return "", &startRefusal{Rule: ruleBundleMissing, Detail: detail, attrs: attrs}
	}
	realData, err := filepath.EvalSymlinks(data)
	if err != nil {
		return "", &startRefusal{Rule: rulePaths, Detail: "<data> can't be resolved: " + err.Error(), attrs: attrs}
	}
	realPath, err := filepath.EvalSymlinks(bundle)
	if err != nil {
		return "", &startRefusal{Rule: ruleBundleMissing, Detail: "the bundle can't be resolved: " + err.Error(), attrs: attrs}
	}
	want := vmbundle.Path(realData, slot)
	if realPath != want {
		return "", &startRefusal{
			Rule:   ruleSlotBundleMismatch,
			Detail: fmt.Sprintf("the %s VM's bundle resolves to %s, not %s", slot, realPath, want),
			attrs:  append(attrs, slog.String("real_path", realPath)),
		}
	}
	return realPath, nil
}

// statDir reports whether path, followed through any symbolic link, is
// a directory. The real path check that follows catches a link.
func statDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// maxReported bounds a value from the environment that a refusal
// repeats.
const maxReported = 64

func cut(s string) string {
	if len(s) <= maxReported {
		return s
	}
	return s[:maxReported] + "[cut]"
}
