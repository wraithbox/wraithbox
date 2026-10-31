package shells

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// floodGuest answers each shell with floodBytes of output and an exit.
type floodGuest struct {
	guestv1.UnimplementedGuestServiceServer
}

const floodBytes = 4 << 20

func (floodGuest) Shell(stream guestv1.GuestService_ShellServer) error {
	data := make([]byte, MaxFrameData)
	for range floodBytes / MaxFrameData {
		f := outFrame(terminalv1.Stream_STREAM_STDOUT, data)
		if err := stream.Send(&guestv1.ShellResponse{Frame: f}); err != nil {
			return err
		}
	}
	return stream.Send(&guestv1.ShellResponse{Frame: exitCode(0)})
}

// stallClient is a wb whose Send blocks until stall is closed, and then
// fails, as a stopped wb's stream does once it goes.
type stallClient struct {
	stall chan struct{}
}

func (c *stallClient) Context() context.Context { return context.Background() }

func (c *stallClient) Recv() (*hostdv1.ShellRequest, error) {
	<-c.stall
	return nil, io.EOF
}

func (c *stallClient) Send(*hostdv1.ShellResponse) error {
	<-c.stall
	return errors.New("wb went")
}

// countClient is a wb that reads every frame.
type countClient struct {
	bytes atomic.Int64
	done  chan struct{}
}

func (c *countClient) Context() context.Context { return context.Background() }

func (c *countClient) Recv() (*hostdv1.ShellRequest, error) {
	<-c.done
	return nil, io.EOF
}

func (c *countClient) Send(resp *hostdv1.ShellResponse) error {
	c.bytes.Add(int64(len(resp.GetFrame().GetOutput().GetData())))
	return nil
}

// TestStalledClientDoesNotStallOthers runs two shells over one guest
// connection with static 64 KiB windows. The first one's wb reads
// nothing, and the second one's output still all arrives: the relay
// holds no queue, and flow control stops only the stalled stream
// (SEC13-bounded-resources).
func TestStalledClientDoesNotStallOthers(t *testing.T) {
	ln := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.InitialWindowSize(64<<10), grpc.InitialConnWindowSize(64<<10))
	guestv1.RegisterGuestServiceServer(srv, floodGuest{})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///guest",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithInitialWindowSize(64<<10), grpc.WithInitialConnWindowSize(64<<10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	g := NewRegistry(&fakeKiller{}, nil)
	t.Cleanup(g.Close)

	run := func(id string, c Client) <-chan Outcome {
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := guestv1.NewGuestServiceClient(conn).Shell(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := make(chan Outcome, 1)
		r := &Relay{
			Shell: open(t, g, id, "p-"+id), Client: c, Guest: stream, Cancel: cancel,
			Checker: NewChecker(&terminalv1.Setup{}), Log: slog.New(slog.DiscardHandler),
		}
		go func() { out <- r.Run() }()
		return out
	}

	stalled := &stallClient{stall: make(chan struct{})}
	stalledOut := run("a", stalled)
	reader := &countClient{done: make(chan struct{})}
	defer close(reader.done)
	select {
	case o := <-run("b", reader):
		if o.Exit == nil || reader.bytes.Load() != floodBytes {
			t.Fatalf("second shell: %+v after %d bytes", o, reader.bytes.Load())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("the second shell stalled after %d bytes", reader.bytes.Load())
	}
	select {
	case o := <-stalledOut:
		t.Fatalf("the stalled shell ended: %+v", o)
	default:
	}
	close(stalled.stall)
	select {
	case o := <-stalledOut:
		if o.Exit == nil {
			t.Fatalf("stalled shell: %+v", o)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the stalled shell didn't end once its wb went")
	}
}
