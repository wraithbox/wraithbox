package guestprocs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// Launchctl is the program Bootout runs, at its fixed path, never from
// PATH.
const Launchctl = "/bin/launchctl"

// launchctlEnv is launchctl's whole environment.
var launchctlEnv = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C"}

// The bounds of a launchctl run.
const (
	launchctlTimeout = 10 * time.Second
	maxOutput        = 4 << 10
	outputWait       = 2 * time.Second
	maxShown         = 256
)

// The p_stat values of XNU's bsd/sys/proc.h that a loop tells apart.
const (
	sStop = 4
	sZomb = 5
)

// System reads the process table of the guest it runs in.
type System struct {
	// Log gets each launchctl run. nil takes slog.Default().
	Log *slog.Logger
}

func (s *System) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

// ByUID reads sysctl kern.proc.uid and kern.proc.ruid, and joins the
// two lists by PID.
func (*System) ByUID(uid uint32) ([]Proc, error) {
	byEUID, err := unix.SysctlKinfoProcSlice("kern.proc.uid", int(uid))
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.proc.uid %d: %w", uid, err)
	}
	byRUID, err := unix.SysctlKinfoProcSlice("kern.proc.ruid", int(uid))
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.proc.ruid %d: %w", uid, err)
	}
	seen := make(map[int32]bool, len(byEUID)+len(byRUID))
	out := make([]Proc, 0, len(byEUID)+len(byRUID))
	for _, list := range [][]unix.KinfoProc{byEUID, byRUID} {
		for i := range list {
			p := procOf(&list[i])
			if !seen[p.PID] {
				seen[p.PID] = true
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// InRange reads sysctl kern.proc.all and keeps the processes with an
// effective or real UID in the range. x/sys reads the table again when
// it grew between the size query and the read.
func (*System) InRange(first, last uint32) ([]Proc, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.proc.all: %w", err)
	}
	var out []Proc
	for i := range all {
		p := procOf(&all[i])
		if (p.UID >= first && p.UID <= last) || (p.RUID >= first && p.RUID <= last) {
			out = append(out, p)
		}
	}
	return out, nil
}

// procOf reads what a loop needs from one kinfo_proc.
func procOf(k *unix.KinfoProc) Proc {
	name := k.Proc.P_comm[:]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	state := Running
	switch k.Proc.P_stat {
	case sStop:
		state = Stopped
	case sZomb:
		state = Zombie
	}
	return Proc{
		PID:   k.Proc.P_pid,
		Start: k.Proc.P_starttime.Sec*1_000_000 + int64(k.Proc.P_starttime.Usec),
		UID:   k.Eproc.Ucred.Uid,
		RUID:  k.Eproc.Pcred.P_ruid,
		Name:  string(name),
		State: state,
	}
}

// Signal reads kern.proc.pid for p's PID right before kill(2), and
// sends nothing when that process isn't p: another start time. It
// doesn't compare the UIDs, so a process that runs a setuid program
// between the listing and the signal still gets it. A PID is reused only after
// the kernel has handed out every other free one, so the gap between
// the read and the kill is far too short for a reuse to slip in.
func (*System) Signal(p Proc, sig Signal) error {
	if p.PID < 2 {
		return ErrPID
	}
	var num unix.Signal
	switch sig {
	case Kill:
		num = unix.SIGKILL
	case Stop:
		num = unix.SIGSTOP
	default:
		return fmt.Errorf("guestprocs: unknown signal %d", int(sig))
	}
	k, err := unix.SysctlKinfoProc("kern.proc.pid", int(p.PID))
	switch {
	case errors.Is(err, unix.EIO), errors.Is(err, unix.ESRCH):
		// kern.proc.pid of a PID with no process reads nothing, which
		// x/sys reports as EIO: the process is gone.
		return nil
	case err != nil:
		return fmt.Errorf("sysctl kern.proc.pid %d: %w", p.PID, err)
	}
	now := procOf(k)
	if now.PID != p.PID || now.Start != p.Start {
		return nil
	}
	if err := unix.Kill(int(p.PID), num); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("kill %d %s: %w", p.PID, sig, err)
	}
	return nil
}

// Bootout runs `launchctl bootout user/<uid>` with a fixed environment,
// within launchctlTimeout. A domain that doesn't exist makes launchctl
// fail, and the error says so. The caller decides by what the process
// table shows afterwards.
func (s *System) Bootout(ctx context.Context, uid uint32) error {
	ctx, cancel := context.WithTimeout(ctx, launchctlTimeout)
	defer cancel()
	target := "user/" + strconv.FormatUint(uint64(uid), 10)
	cmd := exec.CommandContext(ctx, Launchctl, "bootout", target)
	cmd.Env = launchctlEnv
	cmd.WaitDelay = outputWait
	out := &bounded{max: maxOutput}
	cmd.Stdout, cmd.Stderr = out, out
	start := time.Now()
	err := cmd.Run()
	attrs := []slog.Attr{
		slog.String("program", Launchctl), slog.String("args", "bootout "+target),
		slog.Duration("took", time.Since(start)), slog.String("output", shown(out.Bytes())),
	}
	if err != nil {
		s.log().LogAttrs(ctx, slog.LevelDebug, "launchctl bootout failed", append(attrs, slog.String("error", err.Error()))...)
		return fmt.Errorf("launchctl bootout %s: %w: %s", target, err, shown(out.Bytes()))
	}
	s.log().LogAttrs(ctx, slog.LevelInfo, "launchctl bootout ran", attrs...)
	return nil
}

// shown is launchctl's output for a log record: cut and quoted with
// every byte outside printable ASCII escaped.
func shown(b []byte) string {
	if len(b) > maxShown {
		b = b[:maxShown]
	}
	return strconv.QuoteToASCII(string(b))
}

// bounded keeps the first max bytes written to it, and drops the rest,
// so launchctl never blocks on a full pipe.
type bounded struct {
	bytes.Buffer

	max int
}

func (b *bounded) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		_, _ = b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
