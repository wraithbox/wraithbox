package guestusers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Find is the program EmptyHome runs, at its fixed path.
const Find = "/usr/bin/find"

// childPath is the PATH of the child EmptyHome starts.
const childPath = "PATH=/usr/bin:/bin:/usr/sbin:/sbin"

// EmptyHome removes every entry of the home at home, as the project
// user uid with only its own group gid, never as root
// (S06-08-projects-and-sessions, "Removing a home";
// SEC08-proj-isolation). It runs `find -x <home> -mindepth 1 -delete`:
// find never follows a link and never crosses into another mount, and
// as the user it can only remove what the user could. The child gets a
// fixed HOME and PATH, RLIMIT_CORE 0, and is killed at ctx's deadline.
// A nonzero exit is an error that wraps *exec.ExitError: entries the
// user couldn't remove stay, and the caller's unlinkat of the home then
// fails and is logged.
func (s *System) EmptyHome(ctx context.Context, uid, gid uint32, home string) error {
	if !strings.HasPrefix(home, "/") || strings.ContainsAny(home, "\x00\n") || strings.Contains(home, "/../") {
		return fmt.Errorf("guestusers: %q isn't a home path", cut(home))
	}
	// The child inherits the limit. Setting it on wb-guestd itself does
	// no harm: wb-guestd writes no core file either (SEC13-bounded-resources).
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}); err != nil {
		return fmt.Errorf("guestusers: RLIMIT_CORE 0: %w", err)
	}
	cmd := exec.CommandContext(ctx, Find, "-x", home, "-mindepth", "1", "-delete")
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
		slog.String("program", Find), slog.Uint64("uid", uint64(uid)), slog.String("home", cut(home)),
		slog.Duration("took", time.Since(start)), slog.String("stderr", cut(stderr.String())),
	}
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && ctx.Err() == nil {
			attrs = append(attrs, slog.Int("exit", exit.ExitCode()))
		}
		s.log().LogAttrs(ctx, slog.LevelWarn, "emptying a home failed", append(attrs, slog.String("error", err.Error()))...)
		return fmt.Errorf("find as %d: %w: %s", uid, err, cut(stderr.String()))
	}
	s.log().LogAttrs(ctx, slog.LevelInfo, "home emptied", attrs...)
	return nil
}
