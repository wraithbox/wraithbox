package sessionproc

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// errUnsupported is the error of every call here: windows guests are
// later (S12-platforms).
var errUnsupported = fmt.Errorf("sessionproc: sessions in a windows guest: %w", errors.ErrUnsupported)

// ReplicaMode is the mode a PTY replica gets on darwin and linux.
const ReplicaMode = 0o600

// OpenPTY returns an error that wraps errors.ErrUnsupported.
func OpenPTY(Cred, uint16, uint16) (ctl, replica *os.File, err error) {
	return nil, nil, errUnsupported
}

// SetSize returns an error that wraps errors.ErrUnsupported.
func SetSize(*os.File, uint16, uint16) error { return errUnsupported }

// Size returns an error that wraps errors.ErrUnsupported.
func Size(*os.File) (rows, cols uint16, err error) { return 0, 0, errUnsupported }

// ForegroundGroup returns an error that wraps errors.ErrUnsupported.
func ForegroundGroup(*os.File) (int, error) { return 0, errUnsupported }

// StartHelper returns an error that wraps errors.ErrUnsupported.
func StartHelper(HelperSpec) (*os.Process, error) { return nil, errUnsupported }

// StartProgram returns an error that wraps errors.ErrUnsupported.
func StartProgram(ProgramSpec) (int, error) { return 0, errUnsupported }

// Wait returns an error that wraps errors.ErrUnsupported.
func Wait(int) (Status, error) { return Status{}, errUnsupported }

// SignalGroup returns an error that wraps errors.ErrUnsupported.
func SignalGroup(int, Signal) error { return errUnsupported }

// NoCore returns an error that wraps errors.ErrUnsupported.
func NoCore() error { return errUnsupported }

// CheckPipe returns an error that wraps errors.ErrUnsupported.
func CheckPipe(int) error { return errUnsupported }

// CloseOnExec does nothing.
func CloseOnExec(int) {}

// IsRoot reports false: no session helper runs here.
func IsRoot() bool { return false }

// Getsid returns an error that wraps errors.ErrUnsupported.
func Getsid(int) (int, error) { return 0, errUnsupported }

// Getpgid returns an error that wraps errors.ErrUnsupported.
func Getpgid(int) (int, error) { return 0, errUnsupported }

// UserTempDir returns an error that wraps errors.ErrUnsupported.
func UserTempDir(context.Context) (string, error) { return "", errUnsupported }

// System isn't built on windows.
type System struct{}

// ByUID returns an error that wraps errors.ErrUnsupported.
func (System) ByUID(uint32) ([]Proc, error) { return nil, errUnsupported }

// Kill returns an error that wraps errors.ErrUnsupported.
func (System) Kill(Proc) error { return errUnsupported }
