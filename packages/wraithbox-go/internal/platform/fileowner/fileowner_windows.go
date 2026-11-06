package fileowner

import (
	"errors"
	"fmt"
	"io/fs"
)

// Windows guests are later (S12-platforms). Until they are built, UID
// returns an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("fileowner: file owners are not supported on windows yet: %w", errors.ErrUnsupported)

func uid(fs.FileInfo) (uint32, error) { return 0, errUnsupported }
