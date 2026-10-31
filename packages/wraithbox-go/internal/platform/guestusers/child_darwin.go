package guestusers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Find is the program EmptyHome runs, at its fixed path.
const Find = "/usr/bin/find"

// Chmod is the program the first pass of EmptyHome runs from find.
const Chmod = "/bin/chmod"

// childPath is the PATH of the children EmptyHome starts.
const childPath = "PATH=/usr/bin:/bin:/usr/sbin:/sbin"

// EmptyHome removes every entry of the home at home, as the project
// user uid with only its own group gid, never as root
// (S06-08-projects-and-sessions, "Removing a home";
// SEC08-proj-isolation). It runs find twice, with -x, so it never
// follows a link and never crosses into another mount, and as the user
// it can only change what the user could:
//
//  1. `find -x <home> -type d ! -perm -u+rwx -exec chmod u+rwx {} ;`
//     gives the user every right on each of its directories that lacks
//     one, so a directory it set to mode 000 doesn't keep its entries
//     from the second pass. With `;` find runs chmod on a directory
//     before it reads it. The filter keeps the pass to a few chmod runs
//     in a normal home, so a home with tens of thousands of directories
//     still fits the deadline;
//  2. `find -x <home> -mindepth 1 -delete` removes the entries.
//
// Each child gets a fixed HOME and PATH, RLIMIT_CORE 0, and is killed
// at ctx's deadline, which covers both passes. A nonzero exit of the
// first pass is logged, and the second runs. A nonzero exit is an error
// that wraps *exec.ExitError: entries the user couldn't remove stay,
// and the caller's unlinkat of the home then fails and is logged.
func (s *System) EmptyHome(ctx context.Context, uid, gid uint32, home string) error {
	if !strings.HasPrefix(home, "/") || strings.ContainsAny(home, "\x00\n") || strings.Contains(home, "/../") {
		return fmt.Errorf("guestusers: %s isn't a home path", quoted(home))
	}
	// The child inherits the limit. Setting it on wb-guestd itself does
	// no harm: wb-guestd writes no core file either (SEC13-bounded-resources).
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}); err != nil {
		return fmt.Errorf("guestusers: RLIMIT_CORE 0: %w", err)
	}
	opened := s.runFind(ctx, uid, gid, home, openArgs(home)...)
	if _, exited := errors.AsType[*exec.ExitError](opened); opened != nil && (!exited || ctx.Err() != nil) {
		return opened
	}
	if err := s.runFind(ctx, uid, gid, home, deleteArgs(home)...); err != nil {
		return err
	}
	return opened
}

// openArgs are find's arguments for EmptyHome's first pass: chmod
// u+rwx on each directory of home that lacks one of those rights, and
// on no other.
func openArgs(home string) []string {
	return []string{"-x", home, "-type", "d", "!", "-perm", "-u+rwx", "-exec", Chmod, "u+rwx", "{}", ";"}
}

// deleteArgs are find's arguments for EmptyHome's second pass.
func deleteArgs(home string) []string {
	return []string{"-x", home, "-mindepth", "1", "-delete"}
}

// runFind runs find with args as uid, and logs the run with its
// stderr, quoted: the names in it are the user's.
func (s *System) runFind(ctx context.Context, uid, gid uint32, home string, args ...string) error {
	cmd := exec.CommandContext(ctx, Find, args...)
	cmd.Env = []string{"HOME=" + home, childPath, "LANG=C"}
	cmd.Dir = "/"
	cmd.WaitDelay = outputWait
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{gid}},
		Setsid:     true,
	}
	stderr := &boundedBuffer{max: maxErrOutput}
	cmd.Stdout, cmd.Stderr = nil, stderr
	start := time.Now()
	err := cmd.Run()
	attrs := []slog.Attr{
		slog.String("program", Find), slog.String("args", quoted(strings.Join(args, " "))),
		slog.Uint64("uid", uint64(uid)), slog.Duration("took", time.Since(start)),
		slog.String("stderr", quoted(stderr.String())),
	}
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && ctx.Err() == nil {
			attrs = append(attrs, slog.Int("exit", exit.ExitCode()))
		}
		s.log().LogAttrs(ctx, slog.LevelWarn, "find as a project user failed", append(attrs, slog.String("error", err.Error()))...)
		return fmt.Errorf("find as %d: %w: %s", uid, err, quoted(stderr.String()))
	}
	s.log().LogAttrs(ctx, slog.LevelInfo, "find as a project user ran", attrs...)
	return nil
}

// quoted cuts s and quotes it with every byte outside printable ASCII
// escaped, for a log record or an error (SEC10-audit): a home's file names
// are the user's.
func quoted(s string) string { return strconv.QuoteToASCII(cut(s)) }
