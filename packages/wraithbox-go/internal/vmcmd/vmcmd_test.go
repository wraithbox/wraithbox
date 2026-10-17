package vmcmd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk/datadisktest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmcmd"
)

const (
	uuid1 = "0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0"
	uuid2 = "11111111-2222-3333-4444-555555555555"
)

// term is a terminal: in is what the person types.
type term struct {
	in  *strings.Reader
	out bytes.Buffer
}

func (t *term) Read(p []byte) (int, error)  { return t.in.Read(p) }
func (t *term) Write(p []byte) (int, error) { return t.out.Write(p) }
func (t *term) Close() error                { return nil }

// fixture is wb on a temporary <data>, with wb-hostd served over an
// in-memory connection: the real service, VM manager and data disk
// steps, a fake provider and a fake wb-diskfmt.
type fixture struct {
	t       *testing.T
	data    string
	db      *state.DB
	vms     *vm.Manager
	term    *term
	noTerm  bool
	dials   int
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	hostLog bytes.Buffer
	lis     *bufconn.Listener
}

func newFixture(t *testing.T, typed string) *fixture {
	t.Helper()
	f := &fixture{t: t, data: t.TempDir(), term: &term{in: strings.NewReader(typed)}}
	db, err := state.Open(t.Context(), f.data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f.db = db
	log := slog.New(slog.NewJSONHandler(&f.hostLog, nil))
	disks, err := datadisk.New(datadisk.Config{
		DataDir: f.data, Records: db, Logger: log,
		Shim: &datadisktest.Shim{DataDir: f.data, UUIDs: []string{uuid1, uuid2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(t.TempDir(), "tools.img")
	if err := os.WriteFile(tools, []byte("tools"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.vms, err = vm.New(vm.Config{
		DataDir: f.data, ToolsImage: tools, Resources: config.Defaults(), Generations: db,
		Provider: &vmtest.Provider{}, Cloner: &vmtest.CopyCloner{}, DataDisks: disks, Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.lis = bufconn.Listen(1 << 16)
	srv := grpc.NewServer()
	hostdv1.RegisterHostServiceServer(srv, &hostservice.Server{VMs: f.vms, Log: log})
	go func() { _ = srv.Serve(f.lis) }()
	t.Cleanup(srv.Stop)
	return f
}

type client struct {
	conn *grpc.ClientConn
	c    hostdv1.HostServiceClient
}

func (c *client) ResetVMData(ctx context.Context, v hostdv1.VM) (*hostdv1.ResetVMDataResponse, error) {
	return c.c.ResetVMData(ctx, &hostdv1.ResetVMDataRequest{Vm: v})
}

func (c *client) Close() error { return c.conn.Close() }

func (f *fixture) env() vmcmd.Env {
	return vmcmd.Env{
		Stdout: &f.stdout, Stderr: &f.stderr,
		OpenTerminal: func() (io.ReadWriteCloser, error) {
			if f.noTerm {
				return nil, errors.New("no controlling terminal")
			}
			return f.term, nil
		},
		DataDir: f.data,
		Allocated: func(path string) (int64, error) {
			info, err := os.Lstat(path)
			if err != nil {
				return 0, err
			}
			return info.Size(), nil
		},
		Dial: func(context.Context) (vmcmd.Hostd, error) {
			f.dials++
			conn, err := grpc.NewClient("passthrough:///bufnet",
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return f.lis.DialContext(ctx) }),
				grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return nil, err
			}
			return &client{conn: conn, c: hostdv1.NewHostServiceClient(conn)}, nil
		},
	}
}

func (f *fixture) run(args ...string) int {
	return vmcmd.Run(f.t.Context(), f.env(), args)
}

// lastLine is the last line wb wrote to standard error.
func (f *fixture) lastLine() string {
	lines := strings.Split(strings.TrimRight(f.stderr.String(), "\n"), "\n")
	return lines[len(lines)-1]
}

func (f *fixture) disk() string {
	return filepath.Join(vmbundle.Path(f.data, "work"), vmbundle.DataDiskName)
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"reset-data"},
		{"reset-data", "build"},
		{"reset-data", "work", "isolated"},
		{"reset-data", "--yes", "work"},
		{"reset-data", "work", "--yes"},
	} {
		f := newFixture(t, "yes\n")
		if code := f.run(args...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
		if f.dials != 0 || f.term.out.Len() != 0 {
			t.Errorf("%q: asked or dialed", args)
		}
	}
}

// TestRefusedWithoutTerminal: no question and no reset without a
// terminal (SEC14-no-fake-approvals).
func TestRefusedWithoutTerminal(t *testing.T) {
	f := newFixture(t, "")
	f.noTerm = true
	if code := f.run("reset-data", "work"); code != 255 {
		t.Errorf("exit %d, want 255", code)
	}
	if got := f.lastLine(); !strings.HasPrefix(got, "wraith box: ") || !strings.HasSuffix(got, "(confirm-no-terminal)") {
		t.Errorf("last line %q", got)
	}
	if f.dials != 0 {
		t.Error("wb-hostd was asked")
	}
}

// TestDeclinedChangesNothing: a declined confirmation doesn't reach
// wb-hostd, and the disk and its record stay.
func TestDeclinedChangesNothing(t *testing.T) {
	f := newFixture(t, "no\n")
	if err := f.vms.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if err := f.vms.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if code := f.run("reset-data", "work"); code != 255 {
		t.Errorf("exit %d, want 255", code)
	}
	if !strings.HasSuffix(f.lastLine(), "(confirm-declined)") || f.dials != 0 {
		t.Errorf("last line %q, dials %d", f.lastLine(), f.dials)
	}
	rec, ok, _ := f.db.DataDiskRecord(t.Context(), "macos-work")
	if !ok || rec.VolumeUUID != uuid1 {
		t.Errorf("record %+v (%t)", rec, ok)
	}
	if _, err := os.Lstat(f.disk()); err != nil {
		t.Error(err)
	}
}

// TestRefusedWhileRunning: wb-hostd refuses with vm-running, and wb
// prints the refusal with its rule.
func TestRefusedWhileRunning(t *testing.T) {
	f := newFixture(t, "yes\n")
	if err := f.vms.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if code := f.run("reset-data", "work"); code != 255 {
		t.Errorf("exit %d, want 255", code)
	}
	if got := f.lastLine(); !strings.HasSuffix(got, "(vm-running)") || !strings.Contains(got, "wb vm stop work") {
		t.Errorf("last line %q", got)
	}
	rec, _, _ := f.db.DataDiskRecord(t.Context(), "macos-work")
	if rec.VolumeUUID != uuid1 {
		t.Errorf("record %+v", rec)
	}
}

// TestAccepted: the old file is deleted, the record dropped, and a new
// disk recorded with a new UUID. wb-hostd's log line holds both UUIDs.
func TestAccepted(t *testing.T) {
	f := newFixture(t, "yes\n")
	if err := f.vms.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if err := f.vms.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.disk(), make([]byte, 3<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := f.run("reset-data", "work"); code != 0 {
		t.Fatalf("exit %d\n%s", code, f.stderr.String())
	}
	asked := f.term.out.String()
	for _, want := range []string{"work VM's data disk", f.disk(), "3.0 MiB on the host", "Claude Code state", "commits not yet pushed", "Type yes"} {
		if !strings.Contains(asked, want) {
			t.Errorf("the question misses %q:\n%s", want, asked)
		}
	}
	if !strings.Contains(f.stdout.String(), uuid1) || !strings.Contains(f.stdout.String(), uuid2) {
		t.Errorf("stdout %q", f.stdout.String())
	}
	rec, ok, _ := f.db.DataDiskRecord(t.Context(), "macos-work")
	if !ok || rec.VolumeUUID != uuid2 || !rec.Formatted {
		t.Errorf("record %+v (%t)", rec, ok)
	}
	if b, _ := os.ReadFile(f.disk()); string(b) == string(make([]byte, 3<<20)) {
		t.Error("the old file is still at the final name")
	}
	for _, want := range []string{`"old_volume_uuid":"` + uuid1 + `"`, `"new_volume_uuid":"` + uuid2 + `"`} {
		if !strings.Contains(f.hostLog.String(), want) {
			t.Errorf("wb-hostd's log misses %s", want)
		}
	}
}

// TestNoDiskYet: the question says the disk doesn't exist yet.
func TestNoDiskYet(t *testing.T) {
	f := newFixture(t, "no\n")
	f.run("reset-data", "isolated")
	if !strings.Contains(f.term.out.String(), "which doesn't exist yet") {
		t.Errorf("asked %q", f.term.out.String())
	}
}

// TestPrintsWithoutEscapes: text from wb-hostd is printed with its
// control and escape characters removed.
func TestPrintsWithoutEscapes(t *testing.T) {
	f := newFixture(t, "yes\n")
	env := f.env()
	env.Dial = func(context.Context) (vmcmd.Hostd, error) { return evil{}, nil }
	if code := vmcmd.Run(t.Context(), env, []string{"reset-data", "work"}); code != 255 {
		t.Fatalf("exit %d", code)
	}
	if got := f.lastLine(); strings.ContainsAny(got, "\x1b\a") || !strings.HasSuffix(got, "(hostd-unreachable)") {
		t.Errorf("last line %q", got)
	}
}

// TestResetMayHaveRun: when the answer doesn't arrive, wb says the
// reset may have run.
func TestResetMayHaveRun(t *testing.T) {
	for _, err := range []error{
		status.Error(codes.Unavailable, "connection closed"),
		status.Error(codes.DeadlineExceeded, "deadline"),
		context.DeadlineExceeded,
	} {
		f := newFixture(t, "yes\n")
		env := f.env()
		env.Dial = func(context.Context) (vmcmd.Hostd, error) { return failing{err}, nil }
		if code := vmcmd.Run(t.Context(), env, []string{"reset-data", "work"}); code != 255 {
			t.Fatalf("exit %d", code)
		}
		if got := f.lastLine(); !strings.HasSuffix(got, "(vm-reset-unknown)") || !strings.Contains(got, "wb vm status") {
			t.Errorf("%v: last line %q", err, got)
		}
	}
}

type failing struct{ err error }

func (f failing) ResetVMData(context.Context, hostdv1.VM) (*hostdv1.ResetVMDataResponse, error) {
	return nil, f.err
}

func (failing) Close() error { return nil }

type evil struct{}

func (evil) ResetVMData(context.Context, hostdv1.VM) (*hostdv1.ResetVMDataResponse, error) {
	return nil, errors.New("\x1b]0;owned\a\x1b[2J bad")
}

func (evil) Close() error { return nil }
