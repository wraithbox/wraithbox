package guesttools

import (
	"errors"
	"fmt"
	"os"
)

// Linux guests are later (S12-platforms), and where their tools disk and
// installed programs go isn't decided yet. Until it is, every call
// returns an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("guesttools: the guest tools disk on linux: %w", errors.ErrUnsupported)

// MountPoint and InstallDir aren't decided for Linux guests.
const (
	MountPoint = ""
	InstallDir = ""
)

// OpenDisk opens the tools disk mounted at path and checks it.
func OpenDisk(string) (*os.Root, error) { return nil, errUnsupported }

// PrepareDir makes and checks the install directory.
func PrepareDir(string) error { return errUnsupported }

// Exec runs the program at path in this process's place.
func Exec(string) error { return errUnsupported }
