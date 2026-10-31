package shells

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// The relay's waits.
const (
	// HangupDelay is how long wb-guestd gives a shell to end after a
	// hangup before it kills the shell's processes
	// (S06-09-session-lifecycle, "Closing the terminal ends the
	// session").
	HangupDelay = 10 * time.Second
	// HangupWait is how long the relay waits for the guest stream to end
	// after a hangup, a failure or an exit: HangupDelay and a 5 s margin.
	// Past it, the relay cancels the stream.
	HangupWait = HangupDelay + 5*time.Second
	// LostWait is how long the relay waits for the link to show down
	// after the guest stream failed in transport, before it takes the
	// failure for the stream's own.
	LostWait = 2 * time.Second
)

// The rules of the relay's log lines that aren't failures.
const (
	// RuleExit: the shell ended with an exit status in range.
	RuleExit = "shell-exit"
	// RuleHangup: wb's stream ended, or wb sent a hangup, before the
	// exit, so wb-guestd was told to hang up the shell.
	RuleHangup = "shell-hangup"
	// RuleClientFrame: wb sent a message that isn't a frame after the
	// open. The relay takes it as a hangup.
	RuleClientFrame = "shell-client-frame"
)

// Client is wb's side of a shell: ShellService.Shell's server stream
// after the opened message.
type Client interface {
	Context() context.Context
	Send(*hostdv1.ShellResponse) error
	Recv() (*hostdv1.ShellRequest, error)
}

// Guest is wb-guestd's side of a shell: GuestService.Shell's client
// stream after the start.
type Guest interface {
	Send(*guestv1.ShellRequest) error
	Recv() (*guestv1.ShellResponse, error)
}

// Relay joins one shell's wb stream and guest stream. There is no queue:
// each direction has one frame in flight, and gRPC flow control holds
// back the rest, so a slow or stopped wb slows its own program and no
// other shell on the guest connection (SEC13-bounded-resources).
type Relay struct {
	Shell  *Shell
	Client Client
	Guest  Guest
	// Cancel ends the guest stream. The relay calls it past HangupWait,
	// and when it returns.
	Cancel context.CancelFunc
	// Down is closed when the link the guest stream runs on drops.
	Down    <-chan struct{}
	Checker *Checker
	Log     *slog.Logger
	// After is time.After; tests set a fake.
	After func(time.Duration) <-chan time.Time
}

// Outcome is how a shell ended.
type Outcome struct {
	// Exit is the exit status wb got, or nil.
	Exit *terminalv1.Exit
	// Rule is the failure's rule when Exit is nil.
	Rule string
	// Lost: the link dropped under the shell.
	Lost bool
}

type guestRecv struct {
	resp *guestv1.ShellResponse
	err  error
}

type relay struct {
	*Relay
	attrs []slog.Attr

	sendMu     sync.Mutex
	hangupOnce sync.Once
	hungUp     chan struct{}
	done       chan struct{}
	// last is set once the shell's end is known: an exit, or a failure.
	last atomic.Bool
	// Only the loop's goroutine uses these.
	clientGone bool
	got        bool
}

// Run relays until the shell ended and the guest stream is closed, or
// HangupWait after the hangup, a failure or the exit. It returns how the
// shell ended. The caller then ends the Shell with Outcome.Lost.
func (r *Relay) Run() Outcome {
	if r.After == nil {
		r.After = time.After
	}
	rl := &relay{
		Relay: r, hungUp: make(chan struct{}), done: make(chan struct{}),
		attrs: []slog.Attr{
			slog.String("shell_id", r.Shell.ID), slog.String("project", r.Shell.ProjectID),
			slog.String("slot", string(r.Shell.Slot)), slog.Uint64("generation", r.Shell.Generation()),
		},
	}
	defer r.Cancel()
	defer close(rl.done)
	frames := rl.readGuest()
	go rl.readClient()
	return rl.loop(frames)
}

// loop reads the guest's frames. Once the shell's end is known it reads
// on until the guest stream closes, HangupWait at most.
func (rl *relay) loop(frames <-chan guestRecv) Outcome {
	var st loopState
	hungUp := rl.hungUp
	for {
		select {
		case res := <-frames:
			if res.err != nil {
				return rl.streamEnd(res.err, st.out)
			}
			rl.received(res.resp, &st)
		case <-hungUp:
			hungUp = nil
			st.arm(rl.After)
		case <-st.deadline:
			return rl.timeout(st.out)
		}
	}
}

// loopState is the loop's own: the shell's outcome once known, and the
// end of the wait for the guest stream to close.
type loopState struct {
	out      *Outcome
	deadline <-chan time.Time
}

// arm starts the HangupWait, once.
func (st *loopState) arm(after func(time.Duration) <-chan time.Time) {
	if st.deadline == nil {
		st.deadline = after(HangupWait)
	}
}

// received handles one guest frame: checked and passed to wb before the
// shell's end, logged and dropped after it.
func (rl *relay) received(resp *guestv1.ShellResponse, st *loopState) {
	rl.got = true
	if st.out != nil {
		rl.afterLast(resp)
		return
	}
	if st.out = rl.frame(resp); st.out != nil {
		st.arm(rl.After)
	}
}

// frame checks one guest frame and passes it to wb. It returns the
// outcome once the frame ends the shell.
func (rl *relay) frame(resp *guestv1.ShellResponse) *Outcome {
	out, fail := rl.Checker.Check(resp.GetFrame())
	switch {
	case fail != nil:
		rl.fail(fail)
		return &Outcome{Rule: fail.Rule}
	case out == nil:
		rl.Log.LogAttrs(context.Background(), slog.LevelWarn, "guest stopped frame dropped", rl.with(
			slog.String("rule", RuleStoppedRepeat), slog.String("decision", "drop"), slog.Int("count", rl.Checker.Repeats()))...)
		return nil
	}
	rl.toClient(out)
	e := out.GetExit()
	if e == nil {
		return nil
	}
	rl.last.Store(true)
	rl.Log.LogAttrs(context.Background(), slog.LevelInfo, "shell ended", rl.with(
		slog.String("rule", RuleExit), slog.String("decision", "end"),
		slog.Int("code", int(e.GetCode())), slog.Int("signal", int(e.GetSignal())))...)
	return &Outcome{Exit: e}
}

// fail ends the shell as a Wraith Box failure: the failure frame to wb,
// a hangup to wb-guestd, and the rule in the log.
func (rl *relay) fail(f *Failure) {
	rl.toClient(f.Frame())
	rl.Log.LogAttrs(context.Background(), slog.LevelWarn, "shell failed", rl.with(
		slog.String("rule", f.Rule), slog.String("decision", "fail"), slog.String("detail", f.Detail))...)
	rl.hangup(f.Rule, "the shell failed")
	rl.last.Store(true)
}

// afterLast logs a guest frame after the shell's last one. Nothing more
// reaches wb.
func (rl *relay) afterLast(resp *guestv1.ShellResponse) {
	_, fail := rl.Checker.Check(resp.GetFrame())
	rl.Log.LogAttrs(context.Background(), slog.LevelWarn, "guest frame after the last one", rl.with(
		slog.String("rule", fail.Rule), slog.String("decision", "drop"), slog.String("detail", fail.Detail))...)
}

// streamEnd ends the shell whose guest stream closed or failed: lost
// when the link dropped, refused before the guest's first frame, and
// closed otherwise.
func (rl *relay) streamEnd(err error, out *Outcome) Outcome {
	lost := rl.isLost(err)
	if lost {
		rl.Log.LogAttrs(context.Background(), slog.LevelWarn, "shell lost", rl.with(
			slog.String("rule", RuleLost), slog.String("decision", "lose"), slog.String("error", err.Error()))...)
	}
	if out != nil {
		out.Lost = lost
		return *out
	}
	f := rl.endFailure(err, lost)
	rl.toClient(f.Frame())
	rl.Log.LogAttrs(context.Background(), slog.LevelWarn, "shell failed", rl.with(
		slog.String("rule", f.Rule), slog.String("decision", "fail"), slog.String("detail", f.Detail))...)
	return Outcome{Rule: f.Rule, Lost: lost}
}

func (rl *relay) endFailure(err error, lost bool) *Failure {
	slot := rl.Shell.Slot
	switch {
	case lost:
		return &Failure{Rule: RuleLost, Detail: err.Error(), Message: fmt.Sprintf(
			"the link to wb-guestd in the %s VM dropped, and the shell was lost. Run wb shell again.", slot)}
	case !rl.got:
		// The guest's reason is guest text: the log gets it escaped and
		// cut, and wb never sees it.
		return &Failure{Rule: RuleRefused, Detail: "guest status " + LogText(status.Convert(err).Message()), Message: fmt.Sprintf(
			"wb-guestd in the %s VM refused the shell. The wb-hostd log has its reason.", slot)}
	}
	return &Failure{Rule: RuleClosed, Detail: LogText(err.Error()), Message: fmt.Sprintf(
		"wb-guestd in the %s VM ended the shell's stream without an exit status.", slot)}
}

// isLost reports whether the link dropped under the guest stream. A
// failure in transport waits LostWait for the link to show it.
func (rl *relay) isLost(err error) bool {
	select {
	case <-rl.Down:
		return true
	default:
	}
	if c := status.Code(err); c != codes.Unavailable && c != codes.Canceled {
		return false
	}
	select {
	case <-rl.Down:
		return true
	case <-rl.After(LostWait):
		return false
	}
}

// timeout cancels a guest stream that didn't close within HangupWait.
func (rl *relay) timeout(out *Outcome) Outcome {
	rl.Log.LogAttrs(context.Background(), slog.LevelWarn, "shell stream cancelled", rl.with(
		slog.String("rule", RuleHangupTimeout), slog.String("decision", "cancel"), slog.Duration("wait", HangupWait))...)
	rl.Cancel()
	if out != nil {
		return *out
	}
	f := &Failure{Rule: RuleHangupTimeout, Message: fmt.Sprintf(
		"wb-guestd didn't end the shell within %s of its hangup, and wb-hostd closed it.", HangupWait)}
	rl.toClient(f.Frame())
	return Outcome{Rule: RuleHangupTimeout}
}

// toClient sends a frame to wb. Once a send failed, wb is gone, and the
// relay reads the guest on to the end.
func (rl *relay) toClient(f *terminalv1.ServerFrame) {
	if rl.clientGone {
		return
	}
	if err := rl.Client.Send(&hostdv1.ShellResponse{Msg: &hostdv1.ShellResponse_Frame{Frame: f}}); err != nil {
		rl.clientGone = true
	}
}

// readGuest reads the guest stream, one frame in flight, until it fails
// or the relay is done.
func (rl *relay) readGuest() <-chan guestRecv {
	ch := make(chan guestRecv)
	go func() {
		for {
			resp, err := rl.Guest.Recv()
			select {
			case ch <- guestRecv{resp, err}:
			case <-rl.done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

// readClient passes wb's frames to the guest. When wb's stream ends for
// any reason, or wb sends a hangup or a message that isn't a frame, it
// has the guest hang up the shell (S06-09-session-lifecycle, "Closing
// the terminal ends the session"). That is the only way a closed wb
// ends a shell: no keepalive or idle timeout applies.
func (rl *relay) readClient() {
	for {
		req, err := rl.Client.Recv()
		if err != nil {
			rl.hangup(RuleHangup, "wb's stream ended: "+err.Error())
			return
		}
		f := req.GetFrame()
		switch {
		case f == nil:
			rl.hangup(RuleClientFrame, "wb sent a message that isn't a frame")
			return
		case f.GetHangup() != nil:
			rl.hangup(RuleHangup, "wb sent a hangup")
			return
		case f.GetResume() != nil || f.GetSignal() == terminalv1.Signal_SIGNAL_CONT:
			rl.Checker.Resumed()
		}
		if !rl.toGuest(&guestv1.ShellRequest{Msg: &guestv1.ShellRequest_Frame{Frame: f}}) {
			return
		}
	}
}

var hangupFrame = &terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Hangup{Hangup: &terminalv1.Hangup{}}}

// hangup sends wb-guestd a hangup, once, unless the shell has ended or
// the relay is done. The send runs on its own, as it can wait on flow
// control, and the loop cancels the stream HangupWait later.
func (rl *relay) hangup(rule, reason string) {
	if rl.last.Load() {
		return
	}
	select {
	case <-rl.done:
		return
	default:
	}
	rl.hangupOnce.Do(func() {
		rl.Log.LogAttrs(context.Background(), slog.LevelInfo, "shell hangup", rl.with(
			slog.String("rule", rule), slog.String("decision", "hangup"), slog.String("reason", reason))...)
		close(rl.hungUp)
		go rl.toGuest(&guestv1.ShellRequest{Msg: &guestv1.ShellRequest_Frame{Frame: hangupFrame}})
	})
}

// toGuest sends a frame to the guest, one at a time, and reports whether
// it was sent.
func (rl *relay) toGuest(req *guestv1.ShellRequest) bool {
	rl.sendMu.Lock()
	defer rl.sendMu.Unlock()
	return rl.Guest.Send(req) == nil
}

func (rl *relay) with(attrs ...slog.Attr) []slog.Attr {
	return append(append([]slog.Attr(nil), rl.attrs...), attrs...)
}
