package vmdlink

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/vmhandoff"
)

// own is the build version in these tests, passed explicitly
// (S10-02-packaging-and-signing, "One build version").
const own = "1.2.3-dev+abc"

// fakeVMD serves ConnectGuest as wb-vmd does: it opens a stream
// socketpair, serves wb-guestd's GuestService on the guest end, and sends
// the host end on the hand-off channel before it answers.
type fakeVMD struct {
	vmdv1.UnimplementedVMServiceServer

	tx *fdchan.Channel
	// refuse, when set, is the rule ConnectGuest is refused with.
	refuse string
	// silent answers without a hand-off.
	silent bool
	// port, when set, replaces the destination port of the record.
	port uint32

	mu      sync.Mutex
	servers []*grpc.Server
}

func (v *fakeVMD) ConnectGuest(_ context.Context, req *vmdv1.ConnectGuestRequest) (*vmdv1.ConnectGuestResponse, error) {
	if v.refuse != "" {
		st, err := status.New(codes.InvalidArgument, v.refuse).WithDetails(&errdetails.ErrorInfo{Reason: v.refuse, Domain: "wb-vmd"})
		if err != nil {
			return nil, err
		}
		return nil, st.Err()
	}
	if v.silent {
		return &vmdv1.ConnectGuestResponse{}, nil
	}
	if err := v.handOff(req); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &vmdv1.ConnectGuestResponse{}, nil
}

func (v *fakeVMD) handOff(req *vmdv1.ConnectGuestRequest) error {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return err
	}
	host := os.NewFile(uintptr(fds[0]), "host")
	defer func() { _ = host.Close() }()
	guestFile := os.NewFile(uintptr(fds[1]), "guest")
	guest, err := net.FileConn(guestFile)
	_ = guestFile.Close()
	if err != nil {
		return err
	}
	srv := guestd.NewServer(own, slog.New(slog.DiscardHandler))
	v.mu.Lock()
	v.servers = append(v.servers, srv)
	v.mu.Unlock()
	go func() { _ = srv.Serve(newOneConn(guest)) }()
	port := req.GetGuestPort()
	if v.port != 0 {
		port = v.port
	}
	record, err := proto.Marshal(&vmdv1.HandoffMessage{
		RequestId:       req.GetRequestId(),
		Generation:      req.GetGeneration(),
		Direction:       vmdv1.Direction_DIRECTION_HOST_TO_GUEST,
		SourcePort:      49152,
		DestinationPort: port,
	})
	if err != nil {
		return err
	}
	return v.tx.Send(record, host)
}

func (v *fakeVMD) stop() {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, s := range v.servers {
		s.Stop()
	}
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

// newLink serves vmd over an in-memory connection, and returns a link to
// it over a real hand-off socketpair.
func newLink(t *testing.T, vmd *fakeVMD) *Link {
	t.Helper()
	a, b, err := fdchan.Socketpair(vmhandoff.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	vmd.tx, err = fdchan.New(a, vmhandoff.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	rx, err := vmhandoff.NewReceiver(b, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	lis := bufconn.Listen(1 << 16)
	srv := grpc.NewServer()
	vmdv1.RegisterVMServiceServer(srv, vmd)
	go func() { _ = srv.Serve(lis) }()
	cc, err := grpc.NewClient("passthrough:///wb-vmd",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	l := New(vmdv1.NewVMServiceClient(cc), rx, slog.New(slog.DiscardHandler))
	t.Cleanup(func() {
		_ = l.Close()
		_ = cc.Close()
		srv.Stop()
		vmd.stop()
		_ = vmd.tx.Close()
	})
	return l
}

// TestHello is C11-host-daemons, "Hand-off receiver": the end-to-end
// hello, from ConnectGuest through the hand-off to wb-guestd's answer,
// with a fake wb-vmd and a real hand-off socketpair.
func TestHello(t *testing.T) {
	l := newLink(t, &fakeVMD{})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	g, err := l.Hello(ctx, 1, own)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()
	if g.Version() != own {
		t.Fatalf("version %q, want %q", g.Version(), own)
	}
}

// TestConnectRefusals: wb-vmd's refusal keeps its rule, an answer
// without a hand-off is refused with RuleHandoffMissing, and a hand-off
// for another port with the receiver's rule.
func TestConnectRefusals(t *testing.T) {
	cases := []struct {
		name          string
		vmd           *fakeVMD
		rule, vmdRule string
	}{
		{"wb-vmd refuses", &fakeVMD{refuse: "guest-port-not-reserved"}, RuleConnectRefused, "guest-port-not-reserved"},
		{"wb-vmd's rule is not a rule name", &fakeVMD{refuse: "bad\nrule"}, RuleConnectRefused, `"bad\nrule"`},
		{"no hand-off", &fakeVMD{silent: true}, RuleHandoffMissing, ""},
		{"another port", &fakeVMD{port: 22}, string(vmhandoff.RuleUnexpected), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := newLink(t, c.vmd)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			conn, err := l.ConnectGuest(ctx, 1, 1023)
			if conn != nil {
				_ = conn.Close()
			}
			r, ok := errors.AsType[*Refusal](err)
			if !ok || r.Rule != c.rule || r.VMDRule != c.vmdRule {
				t.Fatalf("ConnectGuest = %v, want rule %s with wb-vmd rule %q", err, c.rule, c.vmdRule)
			}
		})
	}
}
