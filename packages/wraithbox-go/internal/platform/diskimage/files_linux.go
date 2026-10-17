package diskimage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
)

// Diskutil is the program Tool runs on macOS. There is none on linux.
const Diskutil = ""

// Files isn't built on linux, whose hosts are later (S12-platforms).
type Files struct{}

// NewFiles returns an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func NewFiles() (Files, error) { return Files{}, errUnsupported }

// Tool isn't built on linux.
type Tool struct{ Log *slog.Logger }

// NewTool returns an error that wraps errors.ErrUnsupported.
func NewTool(*slog.Logger) (*Tool, error) { return nil, errUnsupported }

// errUnsupported is the error of every call here.
var errUnsupported = fmt.Errorf("diskimage: formatting a data disk on linux: %w", errors.ErrUnsupported)

// Create returns an error that wraps errors.ErrUnsupported.
func (Files) Create(string, int64) (diskfmt.Handle, error) { return nil, errUnsupported }

// Lstat returns an error that wraps errors.ErrUnsupported.
func (Files) Lstat(string) (diskfmt.Identity, error) { return diskfmt.Identity{}, errUnsupported }

// Unlink returns an error that wraps errors.ErrUnsupported.
func (Files) Unlink(string) error { return errUnsupported }

// RenameExcl returns an error that wraps errors.ErrUnsupported.
func (Files) RenameExcl(string, string) error { return errUnsupported }

// Attach returns an error that wraps errors.ErrUnsupported.
func (*Tool) Attach(context.Context, string) (diskfmt.Attachment, error) {
	return diskfmt.Attachment{}, errUnsupported
}

// BackingPath returns an error that wraps errors.ErrUnsupported.
func (*Tool) BackingPath(context.Context, string) (string, error) { return "", errUnsupported }

// Erase returns an error that wraps errors.ErrUnsupported.
func (*Tool) Erase(context.Context, string) error { return errUnsupported }

// VolumeUUID returns an error that wraps errors.ErrUnsupported.
func (*Tool) VolumeUUID(context.Context, string) (string, error) { return "", errUnsupported }

// Detach returns an error that wraps errors.ErrUnsupported.
func (*Tool) Detach(context.Context, string) error { return errUnsupported }

// Allocated returns an error that wraps errors.ErrUnsupported.
func Allocated(string) (int64, error) { return 0, errUnsupported }
