package logpipe

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// The rotation of S04-08-records-and-bounds, "Logs and health".
const (
	// RotateSize is the size at which a file is rotated.
	RotateSize = 10 << 20
	// KeepFiles is how many files of a process are kept: the current one
	// and the 4 rotated before it.
	KeepFiles = 5
)

// File is a process's log file, <logs>/<process>.log, rotated at
// RotateSize. The rotated files are <process>.log.1 (the newest) to
// <process>.log.4. Each Write is one record, and a record is never split
// across two files. File is safe for concurrent use. The files are mode
// 600.
type File struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	keep    int
	f       *os.File
	size    int64
	closed  bool
}

// OpenFile opens or creates the log file of process in dir, appending.
func OpenFile(dir, process string) (*File, error) {
	if err := checkProcess(process); err != nil {
		return nil, err
	}
	lf := &File{path: filepath.Join(dir, process+".log"), maxSize: RotateSize, keep: KeepFiles}
	if err := lf.open(); err != nil {
		return nil, err
	}
	return lf, nil
}

func (lf *File) open() error {
	f, err := os.OpenFile(lf.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	lf.f, lf.size = f, info.Size()
	return nil
}

// Write appends p, rotating first when p would take the file past its
// size. A failed rotation fails that Write, and the next one tries
// again.
func (lf *File) Write(p []byte) (int, error) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	if lf.closed {
		return 0, os.ErrClosed
	}
	if lf.f == nil {
		if err := lf.open(); err != nil {
			return 0, err
		}
	}
	if lf.size > 0 && lf.size+int64(len(p)) > lf.maxSize {
		if err := lf.rotate(); err != nil {
			return 0, fmt.Errorf("logpipe: rotating %s: %w", lf.path, err)
		}
	}
	n, err := lf.f.Write(p)
	lf.size += int64(n)
	return n, err
}

// rotate closes the file, shifts <process>.log.N to .N+1, which drops
// the oldest, and starts an empty file.
func (lf *File) rotate() error {
	err := lf.f.Close()
	lf.f = nil
	if err != nil {
		return err
	}
	for i := lf.keep - 1; i >= 1; i-- {
		err := os.Rename(lf.name(i-1), lf.name(i))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return lf.open()
}

func (lf *File) name(i int) string {
	if i == 0 {
		return lf.path
	}
	return fmt.Sprintf("%s.%d", lf.path, i)
}

// Close closes the file.
func (lf *File) Close() error {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.closed = true
	if lf.f == nil {
		return nil
	}
	err := lf.f.Close()
	lf.f = nil
	return err
}
