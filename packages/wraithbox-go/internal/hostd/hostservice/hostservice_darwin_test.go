package hostservice_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
)

// serveLocal serves s on a local IPC endpoint with the peer check and
// the build version exchange of version own, as wb-hostd does, and
// returns the endpoint's directory.
func serveLocal(t *testing.T, s *hostservice.Server, own string) string {
	t.Helper()
	data, err := os.MkdirTemp("", "hs") //nolint:usetesting // t.TempDir's path holds the test name, which pushes run/<name>.sock past sun_path's 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	run := filepath.Join(data, "run")
	log := slog.New(slog.DiscardHandler)
	ln, err := localipc.Listen(t.Context(), log, run, localipc.HostdEndpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(grpccreds.New(log, own)))
	hostdv1.RegisterHostServiceServer(srv, s)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return run
}

func dialLocal(t *testing.T, run, own string) (hostdv1.HostServiceClient, *grpccreds.Credentials) {
	t.Helper()
	creds := grpccreds.New(nil, own)
	conn, err := grpc.NewClient("passthrough:///"+localipc.HostdEndpoint,
		grpccreds.ClientOptions(nil, creds, run, localipc.HostdEndpoint)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return hostdv1.NewHostServiceClient(conn), creds
}

// TestOverLocalIPC: wb's calls reach the service over the local IPC
// endpoint, past the peer check and the version exchange
// (SEC12-least-privilege).
func TestOverLocalIPC(t *testing.T) {
	m := newManager(t, slog.New(slog.DiscardHandler), nil)
	run := serveLocal(t, &hostservice.Server{VMs: m}, "1.2.3")
	c, _ := dialLocal(t, run, "1.2.3")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	started, err := c.StartVM(ctx, &hostdv1.StartVMRequest{Vm: hostdv1.VM_VM_ISOLATED})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.GetVMStatus(ctx, &hostdv1.GetVMStatusRequest{Vm: hostdv1.VM_VM_ISOLATED})
	if err != nil {
		t.Fatal(err)
	}
	if s := res.GetStatus(); s.GetPhase() != hostdv1.VMPhase_VM_PHASE_RUNNING || s.GetGeneration() != started.GetGeneration() {
		t.Errorf("status %v", s)
	}
	if _, err := c.StopVM(ctx, &hostdv1.StopVMRequest{Vm: hostdv1.VM_VM_ISOLATED}); err != nil {
		t.Fatal(err)
	}
}

// TestOtherVersionRefused: a wb of another build version never reaches
// the service.
func TestOtherVersionRefused(t *testing.T) {
	vms := &fakeVMs{}
	run := serveLocal(t, &hostservice.Server{VMs: vms}, "1.2.3")
	c, creds := dialLocal(t, run, "1.2.4")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := c.StartVM(ctx, &hostdv1.StartVMRequest{Vm: hostdv1.VM_VM_WORK}, grpc.WaitForReady(false)); err == nil {
		t.Fatal("a wb of another version was served")
	}
	if creds.LastRefusal() == nil {
		t.Error("no version refusal")
	}
	if got := vms.Calls(); len(got) != 0 {
		t.Errorf("the manager was called: %v", got)
	}
}
