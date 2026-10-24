package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
)

const own = "1.2.3-dev+abc"

// fixture is a wb-hostd with temporary <config>, <data> and <logs>, and
// a bundle with only Contents/MacOS/wb-hostd.
type fixture struct {
	t    *testing.T
	o    options
	done chan error
	stop context.CancelFunc
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	// <data>/run/wb-hostd.sock must fit sun_path, which t.TempDir's
	// path, with the test's name in it, doesn't.
	data, err := os.MkdirTemp("", "hd") //nolint:usetesting // sun_path length, see above
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	exe := filepath.Join(t.TempDir(), "Wraith Box.app", "Contents", "MacOS", "wb-hostd")
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, o: options{
		Paths: paths.Paths{
			Config: t.TempDir(), Data: data, Logs: filepath.Join(t.TempDir(), "logs"), Run: filepath.Join(data, "run"),
		},
		Executable: exe, Version: own,
	}}
}

func (f *fixture) writeConfig(content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.o.Paths.Config, "config.toml"), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// start runs wb-hostd. The test's end stops it, as SIGTERM does, and
// checks that it exits cleanly.
func (f *fixture) start() {
	f.t.Helper()
	ctx, stop := context.WithCancel(context.WithoutCancel(f.t.Context()))
	f.stop, f.done = stop, make(chan error, 1)
	go func() { f.done <- run(ctx, f.o) }()
	f.t.Cleanup(func() {
		if err := f.shutdown(); err != nil {
			f.t.Errorf("run: %v", err)
		}
	})
}

func (f *fixture) shutdown() error {
	if f.stop == nil {
		return nil
	}
	f.stop()
	f.stop = nil
	select {
	case err := <-f.done:
		return err
	case <-time.After(30 * time.Second):
		return errors.New("wb-hostd didn't stop")
	}
}

// client dials wb-hostd's endpoint as wb does.
func (f *fixture) client() hostdv1.HostServiceClient {
	f.t.Helper()
	conn, err := grpc.NewClient("passthrough:///"+localipc.HostdEndpoint,
		grpccreds.ClientOptions(nil, grpccreds.New(nil, own), f.o.Paths.Run, localipc.HostdEndpoint)...)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = conn.Close() })
	return hostdv1.NewHostServiceClient(conn)
}

func (f *fixture) hostdLog() string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.o.Paths.Logs, "wb-hostd.log"))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// status asks for the work VM's status, waiting for the endpoint.
func (f *fixture) status() (*hostdv1.GetVMStatusResponse, error) {
	ctx, cancel := context.WithTimeout(f.t.Context(), 30*time.Second)
	defer cancel()
	return f.client().GetVMStatus(ctx, &hostdv1.GetVMStatusRequest{Vm: hostdv1.VM_VM_WORK}, grpc.WaitForReady(true))
}

// wantRule checks that err is the refusal of rule, from the endpoint.
func wantRule(t *testing.T, err error, rule string) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		t.Fatalf("err = %v, want FailedPrecondition", err)
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == hostservice.Domain && info.GetReason() == rule {
			return
		}
	}
	t.Fatalf("err = %v, want the rule %s", err, rule)
}

// An unknown key in config.toml puts wb-hostd in refusing mode: its
// endpoint answers with the rule, it opens no state.db, and it spawns
// no wb-vmd (S17-05-upgrades-and-migrations, "Refusing mode").
func TestUnknownKeyRefusing(t *testing.T) {
	f := newFixture(t)
	f.writeConfig("vm_cpu_count = 2\nno_such_key = 1\n")
	f.start()
	_, err := f.status()
	wantRule(t, err, "config-key-unknown")
	if err := f.shutdown(); err != nil {
		t.Fatal(err)
	}
	log := f.hostdLog()
	for _, want := range []string{`"rule":"config-key-unknown"`, `"msg":"wb-hostd is in refusing mode"`, `"msg":"wb-hostd stopped"`} {
		if !strings.Contains(log, want) {
			t.Errorf("wb-hostd.log misses %s:\n%s", want, log)
		}
	}
	if _, err := os.Stat(filepath.Join(f.o.Paths.Data, "state.db")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state.db: %v, want none in refusing mode", err)
	}
	entries, err := os.ReadDir(f.o.Paths.Logs)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "wb-vmd") {
			t.Errorf("a wb-vmd log %s in refusing mode", e.Name())
		}
	}
}

// A wb-hostd that doesn't run from Contents/MacOS/wb-hostd of a bundle
// has no wb-vmd to spawn, and is in refusing mode with its rule.
func TestNotInBundleRefusing(t *testing.T) {
	f := newFixture(t)
	f.o.Executable = filepath.Join(t.TempDir(), "wb-hostd")
	if err := os.WriteFile(f.o.Executable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	f.start()
	_, err := f.status()
	wantRule(t, err, ruleNotInBundle)
}

// With a valid configuration, wb-hostd serves the VM manager, and has
// started no VM. A wb-vmd socket a crashed wb-hostd left is gone.
func TestServesStoppedVMs(t *testing.T) {
	f := newFixture(t)
	if err := os.Mkdir(f.o.Paths.Run, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(f.o.Paths.Run, "vmd-work-4.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	_ = ln.Close()
	f.start()
	res, err := f.status()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the leftover wb-vmd socket is still there: %v", err)
	}
	if s := res.GetStatus(); s.GetPhase() != hostdv1.VMPhase_VM_PHASE_STOPPED || s.GetGeneration() != 0 {
		t.Errorf("status %v", s)
	}
	if err := f.shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.o.Paths.Data, "state.db")); err != nil {
		t.Errorf("state.db: %v", err)
	}
}
