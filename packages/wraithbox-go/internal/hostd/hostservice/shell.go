package hostservice

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/shells"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/startup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/sessionid"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/termsetup"
)

// The rules of ShellService.
const (
	// RuleShellStartInvalid: the first message isn't an open, or holds a
	// project id, a terminal setup or an environment out of the
	// contract's form.
	RuleShellStartInvalid = "shell-start-invalid"
	// RuleShellOpen: a shell opened, for the log.
	ruleShellOpen = "shell-open"
)

// The shell's waits. They are values in code.
const (
	// DefaultStartWait bounds the wait for the VM to start, its link, its
	// data mount, its first-connection kills and the project user: a cold
	// boot and the guest's first answers.
	DefaultStartWait = 3 * time.Minute
	// DefaultStartPoll is how often the wait checks again.
	DefaultStartPoll = 250 * time.Millisecond
	// idAttempts bounds the draws of a shell id that an open shell has.
	shellIDAttempts = 4
)

// ShellVMs is the VM manager as a shell uses it. *vm.Manager implements
// it.
type ShellVMs interface {
	StartRun(ctx context.Context, slot vm.Slot) (uint64, error)
	Status(slot vm.Slot) vm.Status
}

// ShellLinks is the guest links as a shell uses them.
// *guestconn.Links implements it.
type ShellLinks interface {
	CheckSessionStart(slot vm.Slot, generation uint64) error
	EnsureProjectUser(ctx context.Context, slot vm.Slot, generation uint64, projectID string) (state.ProjectUser, error)
	UnlockUser(ctx context.Context, slot vm.Slot, generation uint64, u state.ProjectUser) error
	OpenShell(ctx context.Context, slot vm.Slot, generation uint64) (guestv1.GuestService_ShellClient, <-chan struct{}, error)
}

// ShellAudit is the audit writer as a shell uses it. *audit.Writer
// implements it.
type ShellAudit interface {
	CanOpenSession() error
	Write(ctx context.Context, r audit.Record) error
	Record(r audit.Record)
}

// ShellServer serves wraithbox.hostd.v1.ShellService: the debug shell of
// `wb shell` (S05-03-management-commands, "`wb shell`";
// S06-08-projects-and-sessions, "Debug shells in V1-M2").
type ShellServer struct {
	hostdv1.UnimplementedShellServiceServer

	Store  ProjectStore
	VMs    ShellVMs
	Links  ShellLinks
	Shells *shells.Registry
	// Audit records each shell's open and end. Every shell is refused
	// without it (SEC10-audit).
	Audit ShellAudit
	// Refusing is set in refusing mode, and every shell is then refused
	// with its rule.
	Refusing *startup.Refusing
	// Log gets the decisions. nil takes slog.Default().
	Log *slog.Logger
	// StartWait and StartPoll are DefaultStartWait and DefaultStartPoll
	// when 0.
	StartWait time.Duration
	StartPoll time.Duration
	// OpenWait bounds the open of the guest stream. It is
	// guestlink.ShellOpenTimeout when 0.
	OpenWait time.Duration
	// After is time.After, Now time.Now and Rand crypto/rand when nil.
	After func(time.Duration) <-chan time.Time
	Now   func() time.Time
	Rand  io.Reader
}

var _ hostdv1.ShellServiceServer = (*ShellServer)(nil)

// opening is a shell between its open and its start in the guest.
type opening struct {
	open    *hostdv1.ShellOpen
	project state.Project
	sh      *shells.Shell
	user    state.ProjectUser
}

// Shell opens a debug shell as the project's user in the project's VM,
// and relays it until it ends (S05-03-management-commands, "`wb
// shell`"). A refusal before the opened message is a status with the
// rule. After it, every end is a frame: the exit, or a failure with its
// rule.
func (s *ShellServer) Shell(stream hostdv1.ShellService_ShellServer) error {
	ctx := stream.Context()
	o, err := s.open(ctx, stream)
	if err != nil {
		return s.refuseShell(ctx, err)
	}
	// The unlock runs to its answer when wb goes, so a gone wb can't leave
	// the user unlocked with no shell to end it.
	if err := s.Links.UnlockUser(context.WithoutCancel(ctx), o.sh.Slot, o.sh.Generation(), o.user); err != nil {
		// The guest may have unlocked the user and lost the answer. End
		// locks and kills it now, or on the next link when this one
		// dropped (S13-01-process-hardening, "Locked users").
		o.sh.End(ctx, false)
		s.recordEnd(o, shellEnd{decision: "fail", rule: refusalOf(err).rule, detail: "the project user wasn't unlocked"})
		return s.refuseShell(ctx, err)
	}
	// From here the user is unlocked, so every end locks it again when no
	// other shell of the project is open.
	end := s.run(ctx, stream, o)
	o.sh.End(ctx, end.lost)
	s.recordEnd(o, end)
	return nil
}

// shellEnd is how a shell that opened ended, for its audit record.
type shellEnd struct {
	// decision is end, fail or lose.
	decision string
	rule     string
	detail   string
	exit     *terminalv1.Exit
	lost     bool
}

// endOf is the end of a relay's outcome.
func endOf(out shells.Outcome) shellEnd {
	switch {
	case out.Lost:
		return shellEnd{decision: "lose", rule: out.Rule, lost: true}
	case out.Exit != nil:
		return shellEnd{decision: "end", rule: shells.RuleExit, exit: out.Exit}
	}
	return shellEnd{decision: "fail", rule: out.Rule}
}

// recordEnd records the end of a shell whose open was recorded
// (S09-11-audit).
func (s *ShellServer) recordEnd(o *opening, end shellEnd) {
	session := shellSession(o.sh, o.user)
	session.Detail = end.detail
	switch st := end.exit.GetStatus().(type) {
	case *terminalv1.Exit_Code:
		code := int(st.Code)
		session.ExitCode = &code
	case *terminalv1.Exit_Signal:
		signal := int(st.Signal)
		session.Signal = &signal
	}
	s.Audit.Record(audit.ShellEnded(string(o.sh.Slot), o.sh.Generation(), end.decision, end.rule, session))
}

// shellSession is the shell as its audit records hold it.
func shellSession(sh *shells.Shell, u state.ProjectUser) audit.Session {
	return audit.Session{ID: sh.ID, ProjectID: sh.ProjectID, User: projectuser.UserName(u.UID), Interactive: sh.Interactive}
}

// open reads and checks the first message, finds the project and its
// VM, registers the shell, and waits until the guest can start it.
func (s *ShellServer) open(ctx context.Context, stream hostdv1.ShellService_ShellServer) (*opening, error) {
	if r := s.Refusing; r != nil {
		return nil, &projectRefusal{
			code: codes.FailedPrecondition, rule: r.Rule,
			msg: "wb-hostd is in refusing mode, and answers only wb status: " + r.Detail,
		}
	}
	open, err := readOpen(stream)
	if err != nil {
		return nil, err
	}
	// No shell opens unrecorded: while the audit log can't take records,
	// every open is refused with the writer's rule (SEC10-audit;
	// S09-11-audit, "Free space and failed writes").
	if s.Audit == nil {
		return nil, &projectRefusal{code: codes.Internal, rule: RuleInternal, msg: "wb-hostd has no audit log"}
	}
	if err := s.Audit.CanOpenSession(); err != nil {
		return nil, err
	}
	p, err := s.project(ctx, open.GetProjectId())
	if err != nil {
		return nil, err
	}
	slot, err := slotOfPlacement(p.Placement)
	if err != nil {
		return nil, err
	}
	sh, err := s.register(ctx, slot, p, peerPID(ctx), open.GetSetup().GetInteractive())
	if err != nil {
		return nil, err
	}
	generation, u, err := s.admit(ctx, slot, p.ID)
	if err == nil {
		err = s.recordOpen(ctx, sh, generation, u)
	}
	if err != nil {
		sh.Release()
		return nil, err
	}
	sh.Bind(generation, u)
	return &opening{open: open, project: p, sh: sh, user: u}, nil
}

// recordOpen writes the shell's open record and waits for it: the shell
// opens only once its record is written, and is refused with the
// writer's rule when it can't be (SEC10-audit).
func (s *ShellServer) recordOpen(ctx context.Context, sh *shells.Shell, generation uint64, u state.ProjectUser) error {
	return s.Audit.Write(ctx, audit.ShellOpened(string(sh.Slot), generation, ruleShellOpen, shellSession(sh, u), auditPeer(ctx)))
}

// readOpen reads the first message and checks its form
// (SEC11-root-gains-nothing: wb-guestd checks it again).
func readOpen(stream hostdv1.ShellService_ShellServer) (*hostdv1.ShellOpen, error) {
	req, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("read the open: %w", err)
	}
	open := req.GetOpen()
	switch {
	case open == nil:
		return nil, startInvalid("the first message isn't an open")
	case checkID(open.GetProjectId()) != nil:
		return nil, startInvalid("the project id %q isn't %d lowercase hexadecimal characters", truncate(open.GetProjectId()), projectIDLen)
	case !termsetup.ValidSize(open.GetSetup()):
		return nil, startInvalid("the terminal setup is missing, or its window size is outside 1 to 65535")
	case !termsetup.ValidEnv(open.GetSetup().GetEnv()):
		return nil, startInvalid("the environment holds a name other than %v, a name twice, or a value that isn't 1 to %d bytes of printable ASCII",
			termsetup.EnvNames, termsetup.MaxEnvValue)
	}
	return open, nil
}

func startInvalid(format string, args ...any) *projectRefusal {
	return &projectRefusal{code: codes.InvalidArgument, rule: RuleShellStartInvalid, msg: fmt.Sprintf(format, args...)}
}

// project reads the project of id.
func (s *ShellServer) project(ctx context.Context, id string) (state.Project, error) {
	var row state.Project
	err := s.Store.Projects(ctx, func(tx *state.ProjectTx) error {
		var err error
		row, err = byID(tx, id)
		return err
	})
	return row, err
}

// slotOfPlacement is the VM of a project's placement
// (S06-04-vms-and-placement).
func slotOfPlacement(placement string) (vm.Slot, error) {
	switch placement {
	case state.PlacementWork:
		return vm.SlotWork, nil
	case state.PlacementIsolated:
		return vm.SlotIsolated, nil
	}
	return "", fmt.Errorf("the project's placement %q is neither work nor isolated", placement)
}

// register draws a shell id of the session id form (S05-04-naming) and
// records the shell as starting, before the VM's phase is read, so a
// `wb vm stop` either lists it or ran first (vm.Manager.Stop).
func (s *ShellServer) register(ctx context.Context, slot vm.Slot, p state.Project, pid int, interactive bool) (*shells.Shell, error) {
	for range shellIDAttempts {
		id, err := sessionid.New(p.Name, s.now(), s.rand())
		if err != nil {
			return nil, err
		}
		sh, err := s.Shells.Register(id, slot, p.ID, pid, interactive)
		if errors.Is(err, shells.ErrIDTaken) {
			continue
		}
		if r, ok := errors.AsType[*shells.CapRefusal](err); ok {
			return nil, &projectRefusal{code: codes.ResourceExhausted, rule: shells.RuleSessionCap, msg: r.Error()}
		}
		if r, ok := errors.AsType[*shells.TakenRefusal](err); ok {
			return nil, s.takenRefusal(ctx, r)
		}
		return sh, err
	}
	return nil, fmt.Errorf("no free shell id in %d draws", shellIDAttempts)
}

// takenRefusal refuses a shell in the isolated VM that another project
// has, and names that project (S06-04-vms-and-placement, "Isolated slot
// to itself"; NFR06-explained-refusals). An id whose row is gone is
// named as it is.
func (s *ShellServer) takenRefusal(ctx context.Context, r *shells.TakenRefusal) *projectRefusal {
	name := r.OtherProjectID
	if p, err := s.project(ctx, r.OtherProjectID); err == nil {
		name = p.Name
	}
	return &projectRefusal{code: codes.FailedPrecondition, rule: shells.RuleIsolatedTaken, msg: fmt.Sprintf(
		"the project %s has a shell in the isolated VM, and an isolated project gets that VM to itself. "+
			"Close that shell and run wb shell again.", name)}
}

// run sends the opened message, starts the shell in the guest, and
// relays it. It returns how the shell ended.
func (s *ShellServer) run(ctx context.Context, stream hostdv1.ShellService_ShellServer, o *opening) shellEnd {
	sh, slot := o.sh, o.sh.Slot
	name := projectuser.UserName(o.user.UID)
	attrs := append([]slog.Attr{
		slog.String("rule", ruleShellOpen), slog.String("decision", "open"),
		slog.String("shell_id", sh.ID), slog.String("project", sh.ProjectID), slog.String("slot", string(slot)),
		slog.Uint64("generation", sh.Generation()), slog.String("user", name), slog.Bool("interactive", sh.Interactive),
	}, peerAttrs(ctx)...)
	s.logger().LogAttrs(ctx, slog.LevelInfo, "shell open", attrs...)
	opened := &hostdv1.ShellOpened{ShellId: sh.ID, ProjectName: o.project.Name, GuestUser: name}
	if err := stream.Send(&hostdv1.ShellResponse{Msg: &hostdv1.ShellResponse_Opened{Opened: opened}}); err != nil {
		// wb went before the shell started: nothing ran.
		return shellEnd{decision: "fail", rule: shells.RuleHangup, detail: "wb went before the shell started"}
	}
	// The guest stream outlives wb's, so a closed wb ends the shell by a
	// hangup and never by a cancel (S06-09-session-lifecycle, "Closing
	// the terminal ends the session").
	guestCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	guest, down, err := s.openGuest(guestCtx, cancel, slot, sh.Generation())
	if err != nil {
		s.logger().LogAttrs(ctx, slog.LevelWarn, "shell failed", slog.String("rule", shells.RuleRefused),
			slog.String("decision", "fail"), slog.String("shell_id", sh.ID), slog.String("error", err.Error()))
		f := &shells.Failure{Rule: shells.RuleRefused, Message: fmt.Sprintf("wb-hostd couldn't open the shell's stream to wb-guestd in the %s VM. Try again.", slot)}
		_ = stream.Send(&hostdv1.ShellResponse{Msg: &hostdv1.ShellResponse_Frame{Frame: f.Frame()}})
		return shellEnd{decision: "fail", rule: shells.RuleRefused, detail: "the guest stream didn't open"}
	}
	// An error of the send shows up as the guest stream's end.
	_ = guest.Send(&guestv1.ShellRequest{Msg: &guestv1.ShellRequest_Start{Start: &guestv1.ShellStart{
		ShellId: sh.ID, User: name, Setup: o.open.GetSetup(),
	}}})
	sh.SetState(vm.SessionRunning)
	out := (&shells.Relay{
		Shell: sh, Client: stream, Guest: guest, Cancel: cancel, Down: down,
		Checker: shells.NewChecker(o.open.GetSetup()), Log: s.logger(), After: s.After,
	}).Run()
	return endOf(out)
}

// openGuest opens the guest stream on ctx, and calls cancel when the
// open takes longer than OpenWait, so a guest that holds the open back
// can't hang the shell (SEC13-bounded-resources). The bound covers the
// open only: the stream runs on until ctx ends.
func (s *ShellServer) openGuest(ctx context.Context, cancel context.CancelFunc, slot vm.Slot, generation uint64) (guestv1.GuestService_ShellClient, <-chan struct{}, error) {
	wait := s.OpenWait
	if wait <= 0 {
		wait = guestlink.ShellOpenTimeout
	}
	timer := time.AfterFunc(wait, cancel)
	guest, down, err := s.Links.OpenShell(ctx, slot, generation)
	if !timer.Stop() {
		return nil, nil, fmt.Errorf("the guest stream didn't open within %s", wait)
	}
	return guest, down, err
}

// refuseShell logs a refused shell with its rule and the calling wb, and
// returns its status. A refusal of the VM manager or of the guest links
// keeps its rule and message.
func (s *ShellServer) refuseShell(ctx context.Context, err error) error {
	r := refusalOf(err)
	attrs := append([]slog.Attr{
		slog.String("rule", r.rule), slog.String("decision", "refuse"),
		slog.String("method", "Shell"), slog.String("reason", r.msg),
	}, peerAttrs(ctx)...)
	s.logger().LogAttrs(ctx, slog.LevelWarn, "shell refused", attrs...)
	return r.status()
}

func refusalOf(err error) *projectRefusal {
	if r, ok := errors.AsType[*projectRefusal](err); ok {
		return r
	}
	if r, ok := errors.AsType[*guestconn.SessionRefusal](err); ok {
		return &projectRefusal{code: codes.FailedPrecondition, rule: r.Rule, msg: r.Message}
	}
	if r, ok := errors.AsType[*vm.Refusal](err); ok {
		return &projectRefusal{code: codes.FailedPrecondition, rule: string(r.Rule), msg: r.Message}
	}
	if r, ok := errors.AsType[*audit.Refusal](err); ok {
		return &projectRefusal{code: codes.FailedPrecondition, rule: r.Rule, msg: r.Message}
	}
	if r, ok := errors.AsType[*waiting](err); ok {
		return &projectRefusal{code: codes.Unavailable, rule: r.rule, msg: r.msg}
	}
	return &projectRefusal{code: codes.Internal, rule: RuleInternal, msg: "wb-hostd failed: " + err.Error()}
}

// peerPID is the process ID of the calling wb, which `wb vm stop` names
// (S06-09-session-lifecycle, "Idle suspend"). 0 over a connection
// without localipc's check.
func peerPID(ctx context.Context) int {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return 0
	}
	info, ok := p.AuthInfo.(grpccreds.AuthInfo)
	if !ok {
		return 0
	}
	return info.Peer.PID
}

func (s *ShellServer) logger() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *ShellServer) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (s *ShellServer) rand() io.Reader {
	if s.Rand == nil {
		return rand.Reader
	}
	return s.Rand
}

func (s *ShellServer) after(d time.Duration) <-chan time.Time {
	if s.After == nil {
		return time.After(d)
	}
	return s.After(d)
}
