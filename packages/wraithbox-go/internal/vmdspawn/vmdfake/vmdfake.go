// Package vmdfake is a fake wb-vmd for tests on a macOS host, with no
// VM: a test binary that runs Main from its TestMain, and that the test
// spawns through internal/vmdspawn. It takes the start interface of
// wb-vmd (S04-03-self-sandboxing, "How `wb-vmd` is started"):
//
//   - fd 0 is the lifeline: at EOF it logs lifeline-closed and exits 0;
//   - fd 2 is the log pipe, where it writes JSON lines;
//   - fd 3 is the listening gRPC socket, where it runs the version
//     exchange on each connection and serves wraithbox.vmd.v1;
//   - fd 4 is the hand-off channel, where ConnectGuest sends one end of
//     a new stream connection, with internal/guestd's GuestService
//     served on the other.
//
// Start publishes STARTING and RUNNING, and Stop STOPPED. A start with
// MemoryMiB FailMemoryMiB fails as the framework's would: wb-vmd
// publishes ERROR with the error, then refuses with vm-start-failed.
// After a start with EndStreamsMemoryMiB, every WatchState stream ends.
// After a start with CrashMemoryMiB, the VM fails as it runs: wb-vmd
// publishes ERROR with the error, and exits 1 once it has sent it.
//
// It is not test code because two packages' tests use it. Nothing but
// tests imports it.
package vmdfake

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/vmhandoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdspawn"
)

// FailMemoryMiB is the memory size whose start fails.
const FailMemoryMiB = 1

// EndStreamsMemoryMiB is the memory size whose start succeeds, after
// which the fake ends every WatchState stream with an error and keeps
// running.
const EndStreamsMemoryMiB = 2

// CrashMemoryMiB is the memory size whose start succeeds, after which
// the VM fails with CrashReason and the fake exits 1.
const CrashMemoryMiB = 3

// CrashReason is the failure reason of the VM error after a start with
// CrashMemoryMiB.
const CrashReason = "fake-crash"

// endStreamText is the status message of an ended WatchState stream.
// It is longer than wb-hostd logs any text of wb-vmd's.
var endStreamText = "the fake ended the stream: " + strings.Repeat("x", 4096)

// The rules of the fake's refusals, as wb-vmd names them.
const (
	RuleGeneration  = "vm-generation"
	RuleStartFailed = "vm-start-failed"
)

// Is reports whether this process was started as wb-vmd: its whole
// environment is WB_VMD_SLOT.
func Is() bool {
	env := os.Environ()
	return len(env) == 1 && strings.HasPrefix(env[0], vmdspawn.SlotVar+"=")
}

// Main runs the fake wb-vmd with build version own, and returns its
// exit code. check, when set, runs first, and an error from it is logged
// and exits 3.
func Main(own string, check func() error) int {
	enc := json.NewEncoder(os.Stderr)
	logLine := func(rec map[string]any) { _ = enc.Encode(rec) }
	if check != nil {
		if err := check(); err != nil {
			logLine(map[string]any{"event": "check-failed", "error": err.Error()})
			return 3
		}
	}
	tx, err := fdchan.New(os.NewFile(4, "handoff"), vmhandoff.MaxRecord)
	if err != nil {
		logLine(map[string]any{"event": "handoff-failed", "error": err.Error()})
		return 1
	}
	ln, err := net.FileListener(os.NewFile(3, "grpc"))
	if err != nil {
		logLine(map[string]any{"event": "listen-failed", "error": err.Error()})
		return 1
	}
	vmd := &server{own: own, tx: tx, state: &vmdv1.VMState{State: vmdv1.State_STATE_STOPPED}, errorSent: make(chan struct{})}
	srv := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	vmdv1.RegisterVMServiceServer(srv, vmd)
	go func() { _ = srv.Serve(&exchangeListener{Listener: ln, own: own}) }()
	logLine(map[string]any{"event": "started", "pid": os.Getpid(), "slot": strings.TrimPrefix(os.Environ()[0], vmdspawn.SlotVar+"=")})
	_, _ = io.Copy(io.Discard, os.Stdin)
	logLine(map[string]any{"event": "lifeline-closed"})
	return 0
}

// exchangeListener runs the version exchange on each connection before
// gRPC sees it, as wb-vmd's listener does. A refused connection is
// closed, and the next one accepted.
type exchangeListener struct {
	net.Listener
	own string
}

func (l *exchangeListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if _, err := hostversion.Exchange(context.Background(), slog.New(slog.DiscardHandler), c, l.own); err != nil {
			_ = c.Close()
			continue
		}
		return c, nil
	}
}

// server is the fake VMService.
type server struct {
	vmdv1.UnimplementedVMServiceServer

	own string
	tx  *fdchan.Channel

	// errorSent is closed once a stream has sent the ERROR of a crash.
	errorSent     chan struct{}
	errorSentOnce sync.Once

	mu       sync.Mutex
	state    *vmdv1.VMState
	started  uint64
	watchers []chan *vmdv1.VMState
}

func refusal(code codes.Code, rule, msg string) error {
	st, err := status.New(code, msg).WithDetails(&errdetails.ErrorInfo{Reason: rule, Domain: "wb-vmd"})
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	return st.Err()
}

// publish sets the state and sends it to every watcher. A watcher that
// is behind misses it, as no test publishes that many.
func (s *server) publish(st *vmdv1.VMState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = st
	for _, w := range s.watchers {
		select {
		case w <- st:
		default:
		}
	}
}

func (s *server) Start(_ context.Context, req *vmdv1.StartRequest) (*vmdv1.StartResponse, error) {
	g := req.GetGeneration()
	s.mu.Lock()
	ok := g > s.started
	if ok {
		s.started = g
	}
	s.mu.Unlock()
	if !ok {
		return nil, refusal(codes.FailedPrecondition, RuleGeneration, fmt.Sprintf("generation %d isn't above the last one started", g))
	}
	s.publish(&vmdv1.VMState{Generation: g, State: vmdv1.State_STATE_STARTING})
	if req.GetMemoryMib() == FailMemoryMiB {
		vmErr := &vmdv1.VMError{Code: 2, FailureReason: "fake", Description: "the fake framework refused to start"}
		s.publish(&vmdv1.VMState{Generation: g, State: vmdv1.State_STATE_ERROR, Error: vmErr})
		return nil, refusal(codes.FailedPrecondition, RuleStartFailed, "the VM failed to start")
	}
	s.publish(&vmdv1.VMState{Generation: g, State: vmdv1.State_STATE_RUNNING})
	switch req.GetMemoryMib() {
	case EndStreamsMemoryMiB:
		s.endStreams()
	case CrashMemoryMiB:
		go s.crash(g)
	}
	return &vmdv1.StartResponse{}, nil
}

// crash publishes ERROR with CrashReason, waits until a stream has sent
// it, and exits 1, as a wb-vmd whose VM failed does.
func (s *server) crash(g uint64) {
	s.publish(&vmdv1.VMState{
		Generation: g, State: vmdv1.State_STATE_ERROR,
		Error: &vmdv1.VMError{Code: 3, FailureReason: CrashReason, Description: "the fake VM failed"},
	})
	select {
	case <-s.errorSent:
	case <-time.After(5 * time.Second):
	}
	// Send has queued the state, and the transport writes it soon after.
	time.Sleep(100 * time.Millisecond)
	os.Exit(1)
}

// endStreams ends every WatchState stream after the states it has.
func (s *server) endStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.watchers {
		close(w)
	}
	s.watchers = nil
}

func (s *server) checkCurrent(g uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g == 0 || g != s.started {
		return refusal(codes.FailedPrecondition, RuleGeneration, fmt.Sprintf("generation %d isn't the current one, %d", g, s.started))
	}
	return nil
}

func (s *server) Stop(_ context.Context, req *vmdv1.StopRequest) (*vmdv1.StopResponse, error) {
	if err := s.checkCurrent(req.GetGeneration()); err != nil {
		return nil, err
	}
	s.publish(&vmdv1.VMState{Generation: req.GetGeneration(), State: vmdv1.State_STATE_STOPPED})
	return &vmdv1.StopResponse{}, nil
}

func (s *server) GetState(context.Context, *vmdv1.GetStateRequest) (*vmdv1.GetStateResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &vmdv1.GetStateResponse{State: s.state}, nil
}

func (s *server) WatchState(_ *vmdv1.WatchStateRequest, stream grpc.ServerStreamingServer[vmdv1.WatchStateResponse]) error {
	w := make(chan *vmdv1.VMState, 64)
	s.mu.Lock()
	w <- s.state
	s.watchers = append(s.watchers, w)
	s.mu.Unlock()
	for {
		select {
		case st, ok := <-w:
			if !ok {
				return status.Error(codes.Unavailable, endStreamText)
			}
			if err := stream.Send(&vmdv1.WatchStateResponse{State: st}); err != nil {
				return err
			}
			if st.GetError().GetFailureReason() == CrashReason {
				s.errorSentOnce.Do(func() { close(s.errorSent) })
			}
		case <-stream.Context().Done():
			return nil
		}
	}
}

// ConnectGuest opens a stream connection, serves GuestService on one
// end, and sends the other on the hand-off channel before it answers.
func (s *server) ConnectGuest(_ context.Context, req *vmdv1.ConnectGuestRequest) (*vmdv1.ConnectGuestResponse, error) {
	if err := s.checkCurrent(req.GetGeneration()); err != nil {
		return nil, err
	}
	host, guest, err := streamPair()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer func() { _ = host.Close() }()
	srv := guestd.NewServer(s.own, slog.New(slog.DiscardHandler))
	go func() { _ = srv.Serve(newOneConn(guest)) }()
	record, err := proto.Marshal(&vmdv1.HandoffMessage{
		RequestId:       req.GetRequestId(),
		Generation:      req.GetGeneration(),
		Direction:       vmdv1.Direction_DIRECTION_HOST_TO_GUEST,
		SourcePort:      49152,
		DestinationPort: req.GetGuestPort(),
	})
	if err == nil {
		err = s.tx.Send(record, host)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &vmdv1.ConnectGuestResponse{}, nil
}

// streamPair makes a connected pair of Unix stream sockets through a
// listener in a new directory, which it removes. It has no OS code.
func streamPair() (host *net.UnixConn, guest net.Conn, err error) {
	dir, err := os.MkdirTemp("", "vf")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	addr := &net.UnixAddr{Name: dir + "/s", Net: "unix"}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = ln.Close() }()
	host, err = net.DialUnix("unix", nil, addr)
	if err != nil {
		return nil, nil, err
	}
	guest, err = ln.Accept()
	if err != nil {
		_ = host.Close()
		return nil, nil, err
	}
	return host, guest, nil
}

// oneConn is a net.Listener that returns one connection, then waits
// until it is closed.
type oneConn struct {
	c    chan net.Conn
	done chan struct{}
	once sync.Once
	addr net.Addr
}

func newOneConn(c net.Conn) *oneConn {
	l := &oneConn{c: make(chan net.Conn, 1), done: make(chan struct{}), addr: c.LocalAddr()}
	l.c <- c
	return l
}

func (l *oneConn) Accept() (net.Conn, error) {
	select {
	case c := <-l.c:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *oneConn) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *oneConn) Addr() net.Addr { return l.addr }
