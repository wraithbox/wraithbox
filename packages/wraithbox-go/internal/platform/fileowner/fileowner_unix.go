//go:build darwin || linux

package fileowner

import (
	"fmt"
	"io/fs"
	"syscall"
)

func uid(info fs.FileInfo) (uint32, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("fileowner: the owner of %q can't be read", info.Name())
	}
	return st.Uid, nil
}
