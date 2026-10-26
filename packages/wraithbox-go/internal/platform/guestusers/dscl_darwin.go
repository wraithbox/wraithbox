package guestusers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Dscl is the program System runs, at its fixed path, never from PATH.
// wb-guestd never runs sysadminctl -addUser, which failed with
// eDSPermissionError (X17-image-build), or createhomedir
// (S06-05-disks, "Owners and modes").
const Dscl = "/usr/bin/dscl"

// dsclEnv is dscl's whole environment.
var dsclEnv = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C"}

// The bounds of a dscl run.
const (
	dsclTimeout  = 15 * time.Second
	maxErrOutput = 4 << 10
	// outputWait is how long a run waits for dscl's output after it
	// exits or its deadline passes.
	outputWait = 5 * time.Second
)

// notFound is in dscl's error output for a record that doesn't exist.
const notFound = "eDSRecordNotFound"

// The forms System passes to dscl. The record name, the attribute and
// the value are each one argument, and dscl reads an argument that
// starts with a hyphen as an option, so none may.
var (
	recordName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,31}$`)
	attrName   = regexp.MustCompile(`^[A-Za-z]{1,64}$`)
)

// System reads and writes the records of the guest it runs in, with
// dscl on the local node.
type System struct {
	// Log gets each dscl run. nil takes slog.Default().
	Log *slog.Logger
}

func (s *System) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (k Kind) path() (string, error) {
	switch k {
	case Users:
		return "/Users", nil
	case Groups:
		return "/Groups", nil
	}
	return "", fmt.Errorf("guestusers: unknown record kind %d", int(k))
}

// List runs `dscl -plist . -readall /Users|/Groups <attrs>`.
func (s *System) List(ctx context.Context, kind Kind, attrs []string) ([]Record, error) {
	dir, err := kind.path()
	if err != nil {
		return nil, err
	}
	args := []string{"-plist", ".", "-readall", dir}
	for _, a := range attrs {
		if !attrName.MatchString(a) {
			return nil, fmt.Errorf("guestusers: %q isn't an attribute name", cut(a))
		}
		args = append(args, a)
	}
	out, err := s.run(ctx, MaxListOutput, slog.LevelDebug, args...)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}
	return ParseRecords(out)
}

// Read runs `dscl -plist . -read /Users|/Groups/<name>`. found is false
// only when dscl says eDSRecordNotFound.
func (s *System) Read(ctx context.Context, kind Kind, name string) (Record, bool, error) {
	path, err := s.recordPath(kind, name)
	if err != nil {
		return nil, false, err
	}
	out, err := s.run(ctx, MaxReadOutput, slog.LevelDebug, "-plist", ".", "-read", path)
	if _, ok := errors.AsType[*notFoundError](err); ok {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	recs, err := ParseRecords(out)
	if err != nil {
		return nil, false, err
	}
	if len(recs) != 1 {
		return nil, false, fmt.Errorf("guestusers: dscl read %d records for %s", len(recs), path)
	}
	return recs[0], true, nil
}

// Create runs `dscl . -create /Users|/Groups/<name> <attr> <value>`,
// which makes the record when it is missing and sets the attribute to
// the one value.
func (s *System) Create(ctx context.Context, kind Kind, name, attr, value string) error {
	path, err := s.recordPath(kind, name)
	if err != nil {
		return err
	}
	if !attrName.MatchString(attr) {
		return fmt.Errorf("guestusers: %q isn't an attribute name", cut(attr))
	}
	if value == "" || strings.HasPrefix(value, "-") || len(value) > MaxValueLen || strings.ContainsAny(value, "\x00\n") {
		return fmt.Errorf("guestusers: %q isn't a value dscl takes as one argument", cut(value))
	}
	_, err = s.run(ctx, MaxReadOutput, slog.LevelInfo, ".", "-create", path, attr, value)
	return err
}

func (*System) recordPath(kind Kind, name string) (string, error) {
	dir, err := kind.path()
	if err != nil {
		return "", err
	}
	if !recordName.MatchString(name) {
		return "", fmt.Errorf("guestusers: %q isn't a record name wb-guestd uses", cut(name))
	}
	return dir + "/" + name, nil
}

// notFoundError is dscl's answer for a record that doesn't exist.
type notFoundError struct{ args string }

func (e *notFoundError) Error() string { return "dscl " + e.args + ": " + notFound }

// run runs dscl with args and the fixed environment, within
// dsclTimeout, and returns its standard output, at most limit bytes:
// more is an error. It logs the run at level, and a failure as an error.
func (s *System) run(ctx context.Context, limit int, level slog.Level, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, dsclTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, Dscl, args...)
	cmd.Env = dsclEnv
	cmd.WaitDelay = outputWait
	stdout := &boundedBuffer{max: limit}
	stderr := &boundedBuffer{max: maxErrOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	start := time.Now()
	err := cmd.Run()
	joined := strings.Join(args, " ")
	attrs := []slog.Attr{
		slog.String("program", Dscl), slog.String("args", joined),
		slog.Duration("took", time.Since(start)), slog.String("stderr", cut(stderr.String())),
	}
	if err == nil && stdout.over {
		err = fmt.Errorf("guestusers: dscl wrote more than %d bytes", limit)
	}
	if err != nil && ctx.Err() == nil && strings.Contains(stderr.String(), notFound) {
		s.log().LogAttrs(ctx, slog.LevelDebug, "dscl found no record", attrs...)
		return nil, &notFoundError{args: joined}
	}
	if err != nil {
		s.log().LogAttrs(ctx, slog.LevelError, "dscl failed", append(attrs, slog.String("error", err.Error()))...)
		return nil, fmt.Errorf("dscl %s: %w: %s", joined, err, cut(stderr.String()))
	}
	s.log().LogAttrs(ctx, level, "dscl ran", attrs...)
	return stdout.Bytes(), nil
}

// boundedBuffer keeps the first max bytes written to it, and drops the
// rest, so dscl never blocks on a full pipe.
type boundedBuffer struct {
	bytes.Buffer

	max  int
	over bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := b.max - b.Len()
	if len(p) > room {
		b.over = true
		if room > 0 {
			_, _ = b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}
