// x18-guestd: the wb-guestd stand-in for X18-vsock-handoff. Throwaway.
//
// Runs in the macOS guest as a root LaunchDaemon. Listens on AF_VSOCK port
// 1024 and serves gRPC: grpc.health.v1.Health, plus x18.Guest/Echo,
// x18.Guest/Events (what this process saw, so the host can read the guest's
// side of a save and restore) and x18.Guest/Dial (connect to the host on
// vsock and serve gRPC on that connection too).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"x18/vs"
)

type event struct {
	Wall   string  `json:"wall"`
	SinceS float64 `json:"sinceStartS"`
	Kind   string  `json:"kind"`
	Detail string  `json:"detail,omitempty"`
}

var (
	start  = time.Now()
	mu     sync.Mutex
	events []event
	connID atomic.Int64
)

func record(kind, detail string) {
	e := event{Wall: time.Now().UTC().Format(time.RFC3339Nano), SinceS: time.Since(start).Seconds(), Kind: kind, Detail: detail}
	mu.Lock()
	events = append(events, e)
	if len(events) > 2000 {
		events = events[len(events)-2000:]
	}
	mu.Unlock()
	log.Printf("%s %s", kind, detail)
}

// tracedConn records the first read or write error of a connection.
type tracedConn struct {
	net.Conn
	id   int64
	once sync.Once
}

func (c *tracedConn) note(op string, err error) {
	if err != nil {
		c.once.Do(func() { record("conn-error", fmt.Sprintf("conn %d %s: %v", c.id, op, err)) })
	}
}

func (c *tracedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.note("read", err)
	return n, err
}

func (c *tracedConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.note("write", err)
	return n, err
}

func (c *tracedConn) Close() error {
	record("conn-close", fmt.Sprintf("conn %d", c.id))
	return c.Conn.Close()
}

type tracedListener struct{ net.Listener }

func (l tracedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		record("accept-error", err.Error())
		return nil, err
	}
	id := connID.Add(1)
	record("accept", fmt.Sprintf("conn %d local %s remote %s", id, c.LocalAddr(), c.RemoteAddr()))
	return &tracedConn{Conn: c, id: id}, nil
}

// x18.Guest, registered by hand with a raw-bytes codec (no .proto).
type guestServer interface{}

type guest struct{ srv *grpc.Server }

func (g *guest) echo(_ context.Context, in []byte) ([]byte, error) { return in, nil }

func (g *guest) events(_ context.Context, _ []byte) ([]byte, error) {
	mu.Lock()
	defer mu.Unlock()
	return json.Marshal(events)
}

func (g *guest) dial(_ context.Context, in []byte) ([]byte, error) {
	var req struct {
		Port uint32 `json:"port"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	t := time.Now()
	c, err := vs.Dial(2, req.Port) // VMADDR_CID_HOST
	if err != nil {
		record("dial-error", err.Error())
		return json.Marshal(map[string]any{"ok": false, "error": err.Error()})
	}
	id := connID.Add(1)
	record("dial", fmt.Sprintf("conn %d local %s remote %s", id, c.LocalAddr(), c.RemoteAddr()))
	go func() {
		err := g.srv.Serve(vs.NewOneConn(&tracedConn{Conn: c, id: id}))
		record("dial-serve-end", fmt.Sprint(err))
	}()
	return json.Marshal(map[string]any{"ok": true, "ms": vs.Ms(t), "local": c.LocalAddr().String(), "remote": c.RemoteAddr().String()})
}

func unary(name string, f func(*guest, context.Context, []byte) ([]byte, error)) grpc.MethodDesc {
	return grpc.MethodDesc{
		MethodName: name,
		Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			var in []byte
			if err := dec(&in); err != nil {
				return nil, err
			}
			out, err := f(srv.(*guest), ctx, in)
			return &out, err
		},
	}
}

var guestDesc = grpc.ServiceDesc{
	ServiceName: "x18.Guest",
	HandlerType: (*guestServer)(nil),
	Methods: []grpc.MethodDesc{
		unary("Echo", (*guest).echo),
		unary("Events", (*guest).events),
		unary("Dial", (*guest).dial),
	},
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	cid, err := vs.LocalCID()
	record("start", fmt.Sprintf("pid %d cid %d err %v", os.Getpid(), cid, err))

	srv := grpc.NewServer(
		grpc.ForceServerCodec(vs.Codec{}),
		grpc.MaxRecvMsgSize(64<<20),
		grpc.MaxSendMsgSize(64<<20),
		grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
			record("stream-start", info.FullMethod)
			err := h(srv, ss)
			record("stream-end", fmt.Sprintf("%s: %v", info.FullMethod, err))
			return err
		}),
	)
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	srv.RegisterService(&guestDesc, &guest{srv: srv})

	// Retry: the vsock driver may not be up the moment launchd starts us.
	var l *vs.Listener
	for i := 0; ; i++ {
		l, err = vs.Listen(1024)
		if err == nil {
			break
		}
		if i%50 == 0 {
			record("listen-error", err.Error())
		}
		time.Sleep(100 * time.Millisecond)
	}
	record("listening", "vsock port 1024")
	go func() {
		t := time.NewTicker(5 * time.Second)
		last := time.Now()
		for range t.C {
			// A gap much larger than 5 s means the VM was paused, saved, or restored.
			if gap := time.Since(last); gap > 8*time.Second {
				record("clock-gap", gap.String())
			}
			last = time.Now()
		}
	}()
	err = srv.Serve(tracedListener{l})
	record("serve-end", fmt.Sprint(err))
	os.Exit(1)
}
