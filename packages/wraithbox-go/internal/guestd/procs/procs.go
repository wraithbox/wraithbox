// Package procs is wb-guestd's kill loop and stop loop
// (S13-01-process-hardening, "When the link drops", "When a project's
// last session ends", "Per-user launchd services"; SEC08-proj-isolation).
// The kill loop sends SIGKILL to every process of one project user and
// boots out its launchd domain, round after round, until a check finds
// none. The stop loop sends SIGSTOP to every process with a UID in the
// project range, until none of them runs, and records which ones were
// stopped already.
//
// Both loops are bounded in rounds and time (SEC13-bounded-resources),
// and a loop that ends with processes left logs its rule, the count
// and at most MaxNames names, escaped and cut (SEC10-audit). The
// process table is guest input: a process picks its own name.
package procs

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// The bounds of both loops (S13-01-process-hardening, "Per-user launchd
// services").
const (
	// MaxRounds is the most rounds of signals and checks.
	MaxRounds = 10
	// MaxTime is the longest a loop runs. A round that starts before it
	// runs to its check.
	MaxTime = 30 * time.Second
	// MaxLeft caps the count of processes left that a kill reports. It
	// equals internal/guestlink's MaxProcessesLeft: the host shows it as
	// "more than 1000" (S06-10-leftover-processes, "Kill IDs and
	// records").
	MaxLeft = 1001
	// MaxNames is the most process names a failed loop logs.
	MaxNames = 5
	// maxNameShown cuts a process name in a log record. The kernel keeps
	// 16 bytes, so it only matters for a fake or a later OS.
	maxNameShown = 64
)

// The rules of a loop that ends with processes left.
const (
	// RuleKillIncomplete: the kill loop of a project user ended at a
	// bound with processes of the user left, or without a check that
	// could read the process table.
	RuleKillIncomplete = "project-user-kill-incomplete"
	// RuleStopIncomplete: the stop loop ended at a bound with processes
	// in the project range still running.
	RuleStopIncomplete = "project-users-stop-incomplete"
)

// Clock is the time the loops read and wait on. Tests pass a fake.
type Clock interface {
	Now() time.Time
	// Sleep waits d, or until ctx is done.
	Sleep(ctx context.Context, d time.Duration)
}

// SystemClock is the wall clock.
type SystemClock struct{}

// Now returns time.Now().
func (SystemClock) Now() time.Time { return time.Now() }

// Sleep waits d, or until ctx is done.
func (SystemClock) Sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// settle is how long a round waits between its signals and its check,
// so the kernel can tear down what it killed. It grows by round.
func settle(round int) time.Duration {
	return min(time.Duration(round)*50*time.Millisecond, 500*time.Millisecond)
}

// Loops runs the kill and stop loops. Its loops run one at a time.
type Loops struct {
	table gp.Table
	clock Clock
	log   *slog.Logger

	mu sync.Mutex
	// ours holds each process the stop loop stopped itself and still
	// finds stopped, so a resumed session sends SIGCONT only to those
	// (S13-01-process-hardening, "When the link drops").
	ours map[gp.Key]bool
}

// New returns Loops on table.
func New(table gp.Table, clock Clock, log *slog.Logger) *Loops {
	return &Loops{table: table, clock: clock, log: log, ours: map[gp.Key]bool{}}
}

// KillResult is what a kill loop found at its end.
type KillResult struct {
	// Left is the count of processes left, capped at MaxLeft. 0 is clean.
	Left uint32
	// Rounds is how many rounds ran.
	Rounds int
}

// Clean reports whether no process was left.
func (r KillResult) Clean() bool { return r.Left == 0 }

// Kill runs the kill loop for uid: each round sends SIGKILL to every
// process of the user, runs `launchctl bootout user/<uid>`, and checks
// again, so a KeepAlive agent launchd would start again goes with its
// domain. At least one round runs, even when the first check finds
// nothing, because a domain can hold a job between two starts. It stops
// at the first clean check, after MaxRounds, or past MaxTime, and then
// logs the processes left with RuleKillIncomplete. A check that
// couldn't read the process table counts as MaxLeft processes left.
func (l *Loops) Kill(ctx context.Context, uid uint32) KillResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := l.clock.Now()
	procs, err := l.byUID(uid)
	round := 0
	for round < MaxRounds && ctx.Err() == nil {
		round++
		for _, p := range procs {
			l.signal(ctx, p, gp.Kill)
		}
		if err := l.table.Bootout(ctx, uid); err != nil {
			// launchctl fails for a domain that doesn't exist. What counts
			// is the check below.
			l.log.LogAttrs(ctx, slog.LevelDebug, "bootout failed",
				slog.Uint64("uid", uint64(uid)), slog.Int("round", round), slog.String("error", err.Error()))
		}
		l.clock.Sleep(ctx, settle(round))
		procs, err = l.byUID(uid)
		if err == nil && len(procs) == 0 {
			l.log.LogAttrs(ctx, slog.LevelInfo, "project user killed",
				slog.Uint64("uid", uint64(uid)), slog.String("decision", "clean"), slog.Int("rounds", round))
			return KillResult{Rounds: round}
		}
		if l.clock.Now().Sub(start) >= MaxTime {
			break
		}
	}
	left := uint32(min(len(procs), MaxLeft))
	attrs := []slog.Attr{
		slog.Uint64("uid", uint64(uid)), slog.String("rule", RuleKillIncomplete),
		slog.String("decision", "processes-left"), slog.Int("rounds", round),
		slog.Duration("took", l.clock.Now().Sub(start)),
	}
	if err != nil {
		left = MaxLeft
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	l.log.LogAttrs(ctx, slog.LevelError, "processes of a project user left after the kill loop",
		append(attrs, slog.Int("count", len(procs)), slog.Any("names", Names(procs)))...)
	return KillResult{Left: left, Rounds: round}
}

// byUID lists uid's processes that run: a zombie has exited.
func (l *Loops) byUID(uid uint32) ([]gp.Proc, error) {
	procs, err := l.table.ByUID(uid)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(procs, func(p gp.Proc) bool { return p.State == gp.Zombie }), nil
}

// signal sends sig to p, and logs a failure: the next check finds p
// again when it still runs.
func (l *Loops) signal(ctx context.Context, p gp.Proc, sig gp.Signal) {
	if err := l.table.Signal(p, sig); err != nil {
		l.log.LogAttrs(ctx, slog.LevelWarn, "signal failed",
			slog.Int("pid", int(p.PID)), slog.String("signal", sig.String()), slog.String("error", err.Error()))
	}
}

// StopResult is what a stop loop found at its end.
type StopResult struct {
	// Running is the count of processes in the project range still
	// running. 0 is a full stop.
	Running int
	// Stopped is how many processes stop loops stopped themselves and
	// the last check found stopped: StoppedByUs.
	Stopped int
	// AlreadyStopped is how many processes this loop found stopped that
	// no stop loop had sent SIGSTOP.
	AlreadyStopped int
	Rounds         int
}

// StopAll runs the stop loop: each round sends SIGSTOP to every running
// process whose UID is in the project range, projectuser.FirstID to
// projectuser.LastID, and checks again. It uses the range rather than
// the known users, because a new wb-guestd doesn't know them yet. The
// range leaves out root, the system accounts and nobody. It never boots
// out a domain, so a resumed session keeps its services. reason names
// the trigger in the log: the link drop or the start.
//
// A process it finds stopped that no stop loop sent SIGSTOP was
// stopped by someone else, such as job control. It counts in
// AlreadyStopped, and StoppedByUs leaves it out.
func (l *Loops) StopAll(ctx context.Context, reason string) StopResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	run := &stopRun{l: l, start: l.clock.Now(), already: map[gp.Key]bool{}}
	run.check()
	for run.left() && run.res.Rounds < MaxRounds && ctx.Err() == nil &&
		l.clock.Now().Sub(run.start) < MaxTime {
		run.res.Rounds++
		for _, p := range run.running {
			l.ours[p.Key()] = true
			l.signal(ctx, p, gp.Stop)
		}
		l.clock.Sleep(ctx, settle(run.res.Rounds))
		run.check()
	}
	return run.finish(ctx, reason)
}

// stopRun is one run of the stop loop.
type stopRun struct {
	l     *Loops
	start time.Time
	res   StopResult
	// already holds each process found stopped that no stop loop had
	// signaled.
	already map[gp.Key]bool
	// running and stopped are what the last check found, and err is its
	// error.
	running []gp.Proc
	stopped map[gp.Key]bool
	err     error
}

// left reports whether the last check found a process running, or
// couldn't read the table.
func (r *stopRun) left() bool { return len(r.running) > 0 || r.err != nil }

// check lists the processes in the project range.
func (r *stopRun) check() {
	procs, err := r.l.table.InRange(projectuser.FirstID, projectuser.LastID)
	r.running, r.err = nil, err
	if err != nil {
		return
	}
	r.stopped = map[gp.Key]bool{}
	for _, p := range procs {
		k := p.Key()
		switch p.State {
		case gp.Stopped:
			r.stopped[k] = true
			if !r.l.ours[k] {
				r.already[k] = true
			}
		case gp.Zombie:
		case gp.Running:
			r.running = append(r.running, p)
		default:
			// Running, or a state the table doesn't name: it counts as
			// running, so the loop fails closed.
			r.running = append(r.running, p)
		}
	}
}

// finish keeps in ours only the processes the last check found stopped,
// then logs the result, with RuleStopIncomplete when processes still
// run.
func (r *stopRun) finish(ctx context.Context, reason string) StopResult {
	if r.err == nil {
		for k := range r.l.ours {
			if !r.stopped[k] {
				delete(r.l.ours, k)
			}
		}
	}
	r.res.Running = len(r.running)
	r.res.Stopped = len(r.l.ours)
	r.res.AlreadyStopped = len(r.already)
	attrs := []slog.Attr{
		slog.String("reason", reason), slog.Int("rounds", r.res.Rounds),
		slog.Int("stopped", r.res.Stopped), slog.Int("already_stopped", r.res.AlreadyStopped),
		slog.Duration("took", r.l.clock.Now().Sub(r.start)),
	}
	if !r.left() {
		r.l.log.LogAttrs(ctx, slog.LevelInfo, "project users stopped", append(attrs, slog.String("decision", "stopped"))...)
		return r.res
	}
	if r.err != nil {
		r.res.Running = max(r.res.Running, 1)
		attrs = append(attrs, slog.String("error", r.err.Error()))
	}
	r.l.log.LogAttrs(ctx, slog.LevelError, "processes of project users still run after the stop loop",
		append(attrs, slog.String("rule", RuleStopIncomplete), slog.String("decision", "processes-left"),
			slog.Int("count", len(r.running)), slog.Any("names", Names(r.running)))...)
	return r.res
}

// StoppedByUs returns the processes a stop loop stopped itself, as of
// its last run, sorted by PID. A resumed session sends SIGCONT only to
// these.
func (l *Loops) StoppedByUs() []gp.Key {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]gp.Key, 0, len(l.ours))
	for k := range l.ours {
		out = append(out, k)
	}
	slices.SortFunc(out, func(a, b gp.Key) int { return int(a.PID) - int(b.PID) })
	return out
}

// Names returns at most MaxNames process names of procs for a log
// record, each cut and quoted with every byte outside printable ASCII
// escaped, as S04-08-records-and-bounds ("The guest's version is a
// label") does with guest strings.
func Names(procs []gp.Proc) []string {
	out := make([]string, 0, min(len(procs), MaxNames))
	for _, p := range procs[:min(len(procs), MaxNames)] {
		out = append(out, Label(p.Name))
	}
	return out
}

// Label is a guest string in a form safe for a log record: cut to 64
// bytes, then quoted with every byte outside printable ASCII escaped.
func Label(s string) string {
	if len(s) > maxNameShown {
		s = s[:maxNameShown]
	}
	return strconv.QuoteToASCII(s)
}
