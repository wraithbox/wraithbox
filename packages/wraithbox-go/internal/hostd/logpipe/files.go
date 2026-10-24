package logpipe

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
)

// RuleOpenFailed: a process's log file couldn't be opened, so its lines
// are dropped. The pipe is still read, so the process never blocks on
// its log.
const RuleOpenFailed = "daemon-log-open-failed"

// Files hands out the log file of each process in one directory. A
// file is opened on its first use and kept open until Close, so each
// process has one File, whichever run of it writes. Files is safe for
// concurrent use.
type Files struct {
	dir string
	log *slog.Logger

	mu     sync.Mutex
	files  map[string]*File
	closed bool
}

// NewFiles returns the files of dir, <logs>. log gets a file that
// couldn't be opened. nil takes slog.Default().
func NewFiles(dir string, log *slog.Logger) *Files {
	if log == nil {
		log = slog.Default()
	}
	return &Files{dir: dir, log: log, files: make(map[string]*File)}
}

// Writer returns the log file of process, for Copy. When the file can't
// be opened, the failure is logged with RuleOpenFailed and the writer
// drops what it gets. After Close it drops everything.
func (fs *Files) Writer(process string) io.Writer {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.closed {
		return io.Discard
	}
	if f, ok := fs.files[process]; ok {
		return f
	}
	f, err := OpenFile(fs.dir, process)
	if err != nil {
		fs.log.LogAttrs(context.Background(), slog.LevelError, "daemon log not opened",
			slog.String("decision", "drop"), slog.String("rule", RuleOpenFailed),
			slog.String("process", process), slog.String("error", err.Error()))
		return io.Discard
	}
	fs.files[process] = f
	return f
}

// Close closes every file. A Copy that still writes gets os.ErrClosed,
// and drops the line.
func (fs *Files) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.closed = true
	var errs []error
	for _, f := range fs.files {
		if err := f.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
