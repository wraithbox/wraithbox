package grpccreds

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
)

// server serves the standard health service over localipc with the
// credentials, and counts the calls that reach a handler.
type server struct {
	run     string
	log     *syncBuffer
	calls   atomic.Int32
	peers   chan AuthInfo
	grpcSrv *grpc.Server
}

func startServer(t *testing.T, own string) *server {
	t.Helper()
	data, err := os.MkdirTemp("", "gc") //nolint:usetesting // t.TempDir's path holds the test name, which pushes run/<name>.sock past sun_path's 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	s := &server{run: filepath.Join(data, "run"), log: &syncBuffer{}, peers: make(chan AuthInfo, 4)}
	log := slog.New(slog.NewJSONHandler(s.log, nil))
	ln, err := localipc.Listen(context.Background(), log, s.run, "hostd")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	s.grpcSrv = grpc.NewServer(grpc.Creds(New(log, own)),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			s.calls.Add(1)
			if p, ok := peer.FromContext(ctx); ok {
				if info, ok := p.AuthInfo.(AuthInfo); ok {
					s.peers <- info
				}
			}
			return h(ctx, req)
		}))
	healthpb.RegisterHealthServer(s.grpcSrv, health.NewServer())
	go func() { _ = s.grpcSrv.Serve(ln) }()
	t.Cleanup(s.grpcSrv.Stop)
	return s
}

func (s *server) check(t *testing.T, own string) (*Credentials, error) {
	t.Helper()
	creds := New(nil, own)
	conn, err := grpc.NewClient("passthrough:///hostd", ClientOptions(nil, creds, s.run, "hostd")...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return creds, err
}

func TestHealthOverMatchingVersions(t *testing.T) {
	s := startServer(t, "1.2.3+abc")
	if _, err := s.check(t, "1.2.3+abc"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	info := <-s.peers
	if info.Version != "1.2.3+abc" {
		t.Errorf("AuthInfo.Version = %q, want the client's", info.Version)
	}
	if want := (localipc.Peer{UID: os.Geteuid(), PID: os.Getpid()}); info.Peer != want {
		t.Errorf("AuthInfo.Peer = %+v, want %+v", info.Peer, want)
	}
}

// TestHealthOverMismatch: the client's call fails, the server logs
// peer-version-mismatch, and no handler runs (S04-08-records-and-bounds,
// "Between host processes").
func TestHealthOverMismatch(t *testing.T) {
	s := startServer(t, "1.2.3")
	creds, err := s.check(t, "1.2.4")
	if err == nil {
		t.Fatal("Check with another version: want an error")
	}
	r := creds.LastRefusal()
	if r == nil || r.Rule != hostversion.RuleMismatch || r.OwnLabel() != `"1.2.4"` || r.PeerLabel() != `"1.2.3"` {
		t.Errorf("client's LastRefusal = %v, want %s with both versions", r, hostversion.RuleMismatch)
	}
	s.grpcSrv.Stop() // waits for the server's handshakes, so the log is whole
	if !strings.Contains(s.log.String(), `"rule":"peer-version-mismatch"`) {
		t.Errorf("server log = %s, want peer-version-mismatch", s.log.String())
	}
	if n := s.calls.Load(); n != 0 {
		t.Errorf("%d calls reached a handler, want 0", n)
	}
}
