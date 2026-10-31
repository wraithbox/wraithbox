//go:build unix

package hostservice_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/shells"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/startup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/sessionid"
)

const shellUID = 601

// echoGuest is wb-guestd's Shell: it records the start, writes
// "ready", then echoes one input and exits 0, or exits by signal 1 on a
// hangup.
type echoGuest struct {
	guestv1.UnimplementedGuestServiceServer
	starts chan *guestv1.ShellStart
}

func (g *echoGuest) Shell(stream guestv1.GuestService_ShellServer) error {
	req, err := stream.Recv()
	if err != nil {
		return err
	}
	g.starts <- req.GetStart()
	if err := sendFrame(stream, &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Output{Output: &terminalv1.Output{
		Stream: terminalv1.Stream_STREAM_STDOUT, Data: []byte("ready"),
	}}}); err != nil {
		return err
	}
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		f := req.GetFrame()
		switch {
		case f.GetHangup() != nil:
			return sendFrame(stream, &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Signal{Signal: 1}}}})
		case f.GetInput() != nil:
			if err := sendFrame(stream, &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Output{Output: &terminalv1.Output{
				Stream: terminalv1.Stream_STREAM_STDOUT, Data: f.GetInput(),
			}}}); err != nil {
				return err
			}
			return sendFrame(stream, &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Code{Code: 0}}}})
		}
	}
}

func sendFrame(stream guestv1.GuestService_ShellServer, f *terminalv1.ServerFrame) error {
	return stream.Send(&guestv1.ShellResponse{Frame: f})
}

// shellLinks are the guest links of the shell tests: CheckSessionStart
// answers sessionErr, UnlockUser unlockErr, and OpenShell opens a stream
// to echoGuest, or waits for its context when openHeld is set.
type shellLinks struct {
	mu         sync.Mutex
	sessionErr error
	unlockErr  error
	openHeld   bool
	unlocked   []uint32
	guest      guestv1.GuestServiceClient
}

func (l *shellLinks) CheckSessionStart(vm.Slot, uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sessionErr
}

func (l *shellLinks) EnsureProjectUser(_ context.Context, _ vm.Slot, _ uint64, projectID string) (state.ProjectUser, error) {
	return state.ProjectUser{ProjectID: projectID, UID: shellUID, GID: shellUID, UserName: projectuser.UserName(shellUID)}, nil
}

func (l *shellLinks) UnlockUser(_ context.Context, _ vm.Slot, _ uint64, u state.ProjectUser) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unlocked = append(l.unlocked, u.UID)
	return l.unlockErr
}

func (l *shellLinks) OpenShell(ctx context.Context, _ vm.Slot, _ uint64) (guestv1.GuestService_ShellClient, <-chan struct{}, error) {
	if l.openHeld {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	stream, err := l.guest.Shell(ctx)
	return stream, make(chan struct{}), err
}

// recKiller records the users the registry kills.
type recKiller struct {
	mu    sync.Mutex
	kills []string
}

func (k *recKiller) KillUser(_ context.Context, slot vm.Slot, _ uint64, u state.ProjectUser) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.kills = append(k.kills, fmt.Sprintf("%s/%s", slot, u.UserName))
	return nil
}

type shellFixture struct {
	c         hostdv1.ShellServiceClient
	links     *shellLinks
	reg       *shells.Registry
	killer    *recKiller
	logs      *syncBuffer
	projectID string
	starts    chan *guestv1.ShellStart
}

// newShells is a ShellServer on a temporary state.db with one
// registered project, a running work VM, and echoGuest behind the
// links.
func newShells(t *testing.T, edit func(*hostservice.ShellServer)) *shellFixture {
	t.Helper()
	return newShellsPlaced(t, hostdv1.Placement_PLACEMENT_WORK, edit)
}

// newShellsPlaced is newShells with the project's placement.
func newShellsPlaced(t *testing.T, placement hostdv1.Placement, edit func(*hostservice.ShellServer)) *shellFixture {
	t.Helper()
	pf := newProjects(t, nil)
	p := pf.register(t, "/r/a/.git", urls(urlA), placement).GetProject()
	starts := make(chan *guestv1.ShellStart, 4)
	links := &shellLinks{guest: guestv1.NewGuestServiceClient(dialBuf(t, func(srv *grpc.Server) {
		guestv1.RegisterGuestServiceServer(srv, &echoGuest{starts: starts})
	}))}
	killer := &recKiller{}
	reg := shells.NewRegistry(killer, nil)
	t.Cleanup(reg.Close)
	logs := &syncBuffer{}
	s := &hostservice.ShellServer{
		Store: pf.db, VMs: &fakeVMs{status: vm.Status{Phase: vm.PhaseRunning, Generation: 5}},
		Links: links, Shells: reg, Log: slog.New(slog.NewJSONHandler(logs, nil)),
	}
	if edit != nil {
		edit(s)
	}
	c := hostdv1.NewShellServiceClient(dialBuf(t, func(srv *grpc.Server) { hostdv1.RegisterShellServiceServer(srv, s) }))
	return &shellFixture{c: c, links: links, reg: reg, killer: killer, logs: logs, projectID: p.GetId(), starts: starts}
}

// dialBuf serves what register adds on an in-memory connection.
func dialBuf(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func pipedSetup() *terminalv1.Setup { return &terminalv1.Setup{} }

// open opens a shell and returns the stream with its opened message, or
// the refusal.
func (f *shellFixture) open(ctx context.Context, t *testing.T, open *hostdv1.ShellOpen) (hostdv1.ShellService_ShellClient, *hostdv1.ShellOpened, error) {
	t.Helper()
	stream, err := f.c.Shell(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&hostdv1.ShellRequest{Msg: &hostdv1.ShellRequest_Open{Open: open}}); err != nil {
		t.Fatal(err)
	}
	resp, err := stream.Recv()
	return stream, resp.GetOpened(), err
}

func recvFrame(t *testing.T, stream hostdv1.ShellService_ShellClient) *terminalv1.ServerFrame {
	t.Helper()
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	return resp.GetFrame()
}

func (f *shellFixture) kills() []string {
	f.killer.mu.Lock()
	defer f.killer.mu.Unlock()
	return append([]string(nil), f.killer.kills...)
}

// TestShellOpenAndExit opens a shell, relays one input and its echo,
// and sees the exit end it, the user unlocked first and killed after
// the last shell, and the open logged.
func TestShellOpenAndExit(t *testing.T) {
	f := newShells(t, nil)
	stream, opened, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	if err != nil {
		t.Fatal(err)
	}
	f.wantStarted(t, opened)
	if got := string(recvFrame(t, stream).GetOutput().GetData()); got != "ready" {
		t.Fatalf("first output = %q", got)
	}
	if err := stream.Send(&hostdv1.ShellRequest{Msg: &hostdv1.ShellRequest_Frame{Frame: &terminalv1.ClientFrame{
		Frame: &terminalv1.ClientFrame_Input{Input: []byte("hi")},
	}}}); err != nil {
		t.Fatal(err)
	}
	if got := string(recvFrame(t, stream).GetOutput().GetData()); got != "hi" {
		t.Fatalf("echo = %q", got)
	}
	if e := recvFrame(t, stream).GetExit(); e == nil || e.GetCode() != 0 {
		t.Fatalf("exit = %v", e)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("after the exit: %v", err)
	}
	f.wantKilledOnce(t)
	if !strings.Contains(f.logs.String(), `"rule":"shell-open"`) || !strings.Contains(f.logs.String(), `"rule":"shell-exit"`) {
		t.Fatalf("log = %s", f.logs.String())
	}
}

// wantStarted checks the opened message, and that wb-guestd got the
// start of the same shell as the project's user.
func (f *shellFixture) wantStarted(t *testing.T, opened *hostdv1.ShellOpened) {
	t.Helper()
	if !sessionid.Valid(opened.GetShellId()) || opened.GetProjectName() != "foo" || opened.GetGuestUser() != projectuser.UserName(shellUID) {
		t.Fatalf("opened = %+v", opened)
	}
	start := <-f.starts
	if start.GetShellId() != opened.GetShellId() || start.GetUser() != projectuser.UserName(shellUID) {
		t.Fatalf("start = %+v", start)
	}
}

// wantKilledOnce waits for the registry's kills, and checks the user was
// unlocked once and killed once.
func (f *shellFixture) wantKilledOnce(t *testing.T) {
	t.Helper()
	f.reg.Close()
	if got := f.kills(); len(got) != 1 || got[0] != "work/"+projectuser.UserName(shellUID) {
		t.Fatalf("kills = %v", got)
	}
	f.links.mu.Lock()
	defer f.links.mu.Unlock()
	if len(f.links.unlocked) != 1 {
		t.Fatalf("unlocked = %v", f.links.unlocked)
	}
}

// TestShellClientGone sees wb's stream end hang up the shell, which
// then ends by its exit and kills the user.
func TestShellClientGone(t *testing.T) {
	f := newShells(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	stream, _, err := f.open(ctx, t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	if err != nil {
		t.Fatal(err)
	}
	recvFrame(t, stream)
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for len(f.kills()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := f.kills(); len(got) != 1 {
		t.Fatalf("kills = %v", got)
	}
	if !strings.Contains(f.logs.String(), `"rule":"shell-hangup"`) {
		t.Fatalf("log = %s", f.logs.String())
	}
}

// TestShellStartInvalid feeds the first message out of its form, and
// sees each one refused with shell-start-invalid before anything runs.
func TestShellStartInvalid(t *testing.T) {
	f := newShells(t, nil)
	for _, tc := range []struct {
		name string
		req  *hostdv1.ShellRequest
	}{
		{"frame first", &hostdv1.ShellRequest{Msg: &hostdv1.ShellRequest_Frame{Frame: &terminalv1.ClientFrame{}}}},
		{"empty", &hostdv1.ShellRequest{}},
		{"project id upper", openReq("ABCDEF0123456789ABCDEF0123456789", pipedSetup())},
		{"project id short", openReq("abc", pipedSetup())},
		{"setup missing", openReq(f.projectID, nil)},
		{"interactive without size", openReq(f.projectID, &terminalv1.Setup{Interactive: true})},
		{"rows too many", openReq(f.projectID, &terminalv1.Setup{Interactive: true, Size: &terminalv1.WindowSize{Rows: 70000, Cols: 80}})},
		{"env name", openReq(f.projectID, &terminalv1.Setup{Env: []*terminalv1.EnvVar{{Name: "PATH", Value: "/bin"}}})},
		{"env twice", openReq(f.projectID, &terminalv1.Setup{Env: []*terminalv1.EnvVar{{Name: "TERM", Value: "a"}, {Name: "TERM", Value: "b"}}})},
		{"env control", openReq(f.projectID, &terminalv1.Setup{Env: []*terminalv1.EnvVar{{Name: "TERM", Value: "x\x1b"}}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := f.c.Shell(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(tc.req); err != nil {
				t.Fatal(err)
			}
			_, err = stream.Recv()
			wantRefused(t, tc.name, err, codes.InvalidArgument, hostservice.RuleShellStartInvalid)
		})
	}
	if len(f.links.unlocked) != 0 || len(f.reg.Sessions(vm.SlotWork)) != 0 {
		t.Fatalf("unlocked %v, sessions %v", f.links.unlocked, f.reg.Sessions(vm.SlotWork))
	}
	if !strings.Contains(f.logs.String(), `"rule":"shell-start-invalid"`) {
		t.Fatalf("log = %s", f.logs.String())
	}
}

func openReq(projectID string, setup *terminalv1.Setup) *hostdv1.ShellRequest {
	return &hostdv1.ShellRequest{Msg: &hostdv1.ShellRequest_Open{Open: &hostdv1.ShellOpen{ProjectId: projectID, Setup: setup}}}
}

// TestShellProjectUnknown sees a shell of an unregistered project
// refused.
func TestShellProjectUnknown(t *testing.T) {
	f := newShells(t, nil)
	_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: strings.Repeat("0", 32), Setup: pipedSetup()})
	wantRefused(t, "unknown project", err, codes.NotFound, hostservice.RuleProjectUnknown)
}

// TestShellMountPending sees a shell wait while the data mount of the
// link is pending, and be refused with shell-start-timeout when it
// stays pending, with nothing unlocked (SEC08-proj-isolation).
func TestShellMountPending(t *testing.T) {
	f := newShells(t, func(s *hostservice.ShellServer) {
		s.StartWait, s.StartPoll = 50*time.Millisecond, 5*time.Millisecond
	})
	f.links.sessionErr = &guestconn.SessionRefusal{Rule: guestconn.RuleSessionMountPending, Message: "the data mount is pending."}
	_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	msg := wantRefused(t, "mount pending", err, codes.Unavailable, hostservice.RuleShellStartTimeout)
	if !strings.Contains(msg, "the data mount is pending.") {
		t.Fatalf("message = %s", msg)
	}
	if len(f.links.unlocked) != 0 || len(f.reg.Sessions(vm.SlotWork)) != 0 {
		t.Fatalf("unlocked %v, sessions %v", f.links.unlocked, f.reg.Sessions(vm.SlotWork))
	}
}

// TestShellSessionRefused sees a refusal of the links that doesn't pass
// on its own refuse the shell at once with its rule.
func TestShellSessionRefused(t *testing.T) {
	f := newShells(t, nil)
	f.links.sessionErr = &guestconn.SessionRefusal{Rule: guestconn.RuleMountRecord, Message: "the mount record failed."}
	_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	wantRefused(t, "mount record", err, codes.FailedPrecondition, guestconn.RuleMountRecord)
}

// TestShellVMStopping sees a shell of a stopping VM refused.
func TestShellVMStopping(t *testing.T) {
	f := newShells(t, func(s *hostservice.ShellServer) {
		s.VMs = &fakeVMs{status: vm.Status{Phase: vm.PhaseStopping, Generation: 5}}
	})
	_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	wantRefused(t, "stopping", err, codes.FailedPrecondition, string(vm.RuleStopping))
}

// TestShellSessionCap sees the 65th shell of a VM refused with
// vm-session-cap.
func TestShellSessionCap(t *testing.T) {
	f := newShells(t, nil)
	for i := range shells.MaxPerVM {
		if _, err := f.reg.Register(fmt.Sprintf("other-%d", i), vm.SlotWork, "p", 1, false); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	wantRefused(t, "65th shell", err, codes.ResourceExhausted, shells.RuleSessionCap)
}

// TestShellRefusingMode sees every shell refused in refusing mode.
func TestShellRefusingMode(t *testing.T) {
	f := newShells(t, func(s *hostservice.ShellServer) {
		s.Refusing = &startup.Refusing{Rule: "state-db-unreadable", Detail: "broken"}
	})
	_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	wantRefused(t, "refusing", err, codes.FailedPrecondition, "state-db-unreadable")
}

// TestShellUnlockFailed sees a failed unlock refuse the shell and still
// lock and kill the user, which the guest may have unlocked
// (S13-01-process-hardening, "Locked users").
func TestShellUnlockFailed(t *testing.T) {
	f := newShells(t, nil)
	f.links.unlockErr = &guestconn.SessionRefusal{Rule: guestconn.RuleUnlockFailed, Message: "no answer."}
	_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	wantRefused(t, "unlock failed", err, codes.FailedPrecondition, guestconn.RuleUnlockFailed)
	f.wantKilledOnce(t)
}

// TestShellOpenHeld sees a guest stream that doesn't open within
// OpenWait end the shell with guest-shell-refused, and the user killed
// (SEC13-bounded-resources).
func TestShellOpenHeld(t *testing.T) {
	f := newShells(t, func(s *hostservice.ShellServer) { s.OpenWait = 20 * time.Millisecond })
	f.links.openHeld = true
	stream, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	if err != nil {
		t.Fatal(err)
	}
	if got := recvFrame(t, stream).GetFailure().GetRule(); got != shells.RuleRefused {
		t.Fatalf("failure rule = %q", got)
	}
	f.wantKilledOnce(t)
	if !strings.Contains(f.logs.String(), "didn't open within 20ms") {
		t.Fatalf("log = %s", f.logs.String())
	}
}

// TestShellIsolatedTaken sees a shell of an isolated project refused
// while another project has a shell in the isolated VM, with that
// project named (S06-04-vms-and-placement, "Isolated slot to itself").
func TestShellIsolatedTaken(t *testing.T) {
	f := newShellsPlaced(t, hostdv1.Placement_PLACEMENT_ISOLATED, nil)
	if _, err := f.reg.Register("other", vm.SlotIsolated, strings.Repeat("e", 32), 1, false); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	msg := wantRefused(t, "isolated taken", err, codes.FailedPrecondition, shells.RuleIsolatedTaken)
	if !strings.Contains(msg, strings.Repeat("e", 32)) {
		t.Fatalf("message = %s", msg)
	}
	if len(f.links.unlocked) != 0 {
		t.Fatalf("unlocked = %v", f.links.unlocked)
	}
}

// TestShellClientGoneWaiting sees wb's stream end while the shell waits
// for its guest logged with shell-client-gone, and nothing unlocked.
func TestShellClientGoneWaiting(t *testing.T) {
	f := newShells(t, func(s *hostservice.ShellServer) { s.StartPoll = 5 * time.Millisecond })
	f.links.sessionErr = &guestconn.SessionRefusal{Rule: guestconn.RuleSessionMountPending, Message: "the data mount is pending."}
	ctx, cancel := context.WithCancel(t.Context())
	stream, err := f.c.Shell(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(openReq(f.projectID, pipedSetup())); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(f.reg.Sessions(vm.SlotWork)) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	for !strings.Contains(f.logs.String(), `"rule":"shell-client-gone"`) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(f.logs.String(), `"rule":"shell-client-gone"`) {
		t.Fatalf("log = %s", f.logs.String())
	}
	if len(f.links.unlocked) != 0 {
		t.Fatalf("unlocked = %v", f.links.unlocked)
	}
}

// TestVMStopNamesShells sees `wb vm stop` refused while a shell is open,
// with the shell's id in the refusal.
func TestVMStopNamesShells(t *testing.T) {
	var reg *shells.Registry
	m := newManager(t, slog.New(slog.DiscardHandler), func(slot vm.Slot) []vm.Session { return reg.Sessions(slot) })
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	f := newShells(t, func(s *hostservice.ShellServer) { s.VMs = m })
	reg = f.reg
	stream, opened, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	if err != nil {
		t.Fatal(err)
	}
	recvFrame(t, stream)
	err = m.Stop(t.Context(), vm.SlotWork)
	r, ok := errors.AsType[*vm.Refusal](err)
	if !ok || r.Rule != vm.RuleLiveSessions || !strings.Contains(r.Message, opened.GetShellId()) {
		t.Fatalf("Stop = %v", err)
	}
}
