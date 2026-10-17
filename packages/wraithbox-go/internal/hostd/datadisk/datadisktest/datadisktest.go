// Package datadisktest has a fake wb-diskfmt for tests of wb-hostd's
// data disk steps and of what is built on them. It acts on the real
// bundle in a temporary <data> as the shim does, with plain files: no
// image is attached or formatted.
package datadisktest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	diskfmtv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/diskfmt/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// Shim is a fake wb-diskfmt. Format deletes a leftover temporary file,
// writes a new one, and answers with the next UUID of UUIDs. Rename
// renames it to the final name, and refuses with diskfmt-final-exists
// when a file has that name, as RENAME_EXCL does.
//
// Set the fields before the first call.
type Shim struct {
	DataDir string
	// UUIDs are the volume UUIDs Format answers with, in order. When
	// they run out, Format answers with the last one again.
	UUIDs []string
	// OnFormat and OnRename, when set, run first, and a non-nil
	// message is the answer instead. A *datadisk.ShimRefusal error is a
	// refusal.
	OnFormat func(slot string, sizeGiB uint32) (*diskfmtv1.FormatResult, error)
	OnRename func(slot string) (*diskfmtv1.RenameResult, error)
	// Block, when set, holds Format until it is closed.
	Block chan struct{}

	mu      sync.Mutex
	starts  []string
	formats int
	open    int
}

// Starts returns each slot a run was started for, in order.
func (s *Shim) Starts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.starts...)
}

// Open returns the number of runs not closed.
func (s *Shim) Open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

// Start implements datadisk.Shim.
func (s *Shim) Start(_ context.Context, slot string) (datadisk.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, slot)
	s.open++
	return &session{s: s, slot: slot}, nil
}

func (s *Shim) nextUUID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.UUIDs) == 0 {
		return "0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0"
	}
	i := min(s.formats, len(s.UUIDs)-1)
	s.formats++
	return s.UUIDs[i]
}

type session struct {
	s         *Shim
	slot      string
	formatted bool
	closed    bool
}

func (x *session) bundle() string { return vmbundle.Path(x.s.DataDir, x.slot) }

func (x *session) Format(ctx context.Context, sizeGiB uint32) (*diskfmtv1.FormatResult, error) {
	if x.s.Block != nil {
		select {
		case <-x.s.Block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if x.s.OnFormat != nil {
		if res, err := x.s.OnFormat(x.slot, sizeGiB); res != nil || err != nil {
			return res, err
		}
	}
	tmp := filepath.Join(x.bundle(), vmbundle.DataDiskTempName)
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, &datadisk.ShimRefusal{Rule: "diskfmt-temp-delete", Detail: err.Error()}
	}
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, &datadisk.ShimRefusal{Rule: "diskfmt-create", Detail: err.Error()}
	}
	if _, err := fmt.Fprintf(f, "formatted %d GiB", sizeGiB); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	x.formatted = true
	return &diskfmtv1.FormatResult{VolumeUuid: x.s.nextUUID(), TemporaryName: vmbundle.DataDiskTempName}, nil
}

func (x *session) Rename(context.Context) (*diskfmtv1.RenameResult, error) {
	if x.s.OnRename != nil {
		if res, err := x.s.OnRename(x.slot); res != nil || err != nil {
			return res, err
		}
	}
	if !x.formatted {
		return nil, &datadisk.ShimRefusal{Rule: "diskfmt-rename", Detail: "no image was formatted"}
	}
	final := filepath.Join(x.bundle(), vmbundle.DataDiskName)
	if _, err := os.Lstat(final); err == nil {
		return nil, &datadisk.ShimRefusal{Rule: "diskfmt-final-exists", Detail: final + " exists, and is kept"}
	}
	if err := os.Rename(filepath.Join(x.bundle(), vmbundle.DataDiskTempName), final); err != nil {
		return nil, &datadisk.ShimRefusal{Rule: "diskfmt-rename", Detail: err.Error()}
	}
	return &diskfmtv1.RenameResult{FinalName: vmbundle.DataDiskName}, nil
}

func (x *session) Close() error {
	if x.closed {
		return nil
	}
	x.closed = true
	x.s.mu.Lock()
	x.s.open--
	x.s.mu.Unlock()
	return nil
}
