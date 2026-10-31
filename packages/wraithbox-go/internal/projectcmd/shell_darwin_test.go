package projectcmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/relay"
)

// shellCall is a ShellService.Shell call that answers the open with
// opened, or with refused, then sends an exit status.
type shellCall struct {
	grpc.ClientStream
	opened  *hostdv1.ShellOpened
	refused error
	open    *hostdv1.ShellOpen
	recvs   int
}

func (c *shellCall) Send(r *hostdv1.ShellRequest) error {
	if o := r.GetOpen(); o != nil {
		c.open = o
	}
	return nil
}

func (c *shellCall) Recv() (*hostdv1.ShellResponse, error) {
	c.recvs++
	switch {
	case c.recvs == 1 && c.refused != nil:
		return nil, c.refused
	case c.recvs == 1:
		return &hostdv1.ShellResponse{Msg: &hostdv1.ShellResponse_Opened{Opened: c.opened}}, nil
	case c.recvs == 2:
		exit := &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Code{Code: 3}}}}
		return &hostdv1.ShellResponse{Msg: &hostdv1.ShellResponse_Frame{Frame: exit}}, nil
	}
	return nil, io.EOF
}

func (c *shellCall) CloseSend() error { return nil }

// shellHostd is the harness's ProjectService and a ShellService that
// returns call.
type shellHostd struct {
	nopClose
	call  *shellCall
	calls int
}

func (s *shellHostd) Shell(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[hostdv1.ShellRequest, hostdv1.ShellResponse], error) {
	s.calls++
	return s.call, nil
}

// shellRun is one run of `wb shell` against the harness, with a piped
// terminal whose charset is cs.
type shellRun struct {
	result
	hostd *shellHostd
}

func (h *harness) shell(dir string, call *shellCall, cs tty.Charset, termErr error, args ...string) shellRun {
	h.t.Helper()
	var stdout, stderr bytes.Buffer
	sh := &shellHostd{nopClose: nopClose{h.client()}, call: call}
	env := Env{
		Stdout: &stdout, Stderr: &stderr, Paths: h.paths,
		Log:       slog.New(slog.NewJSONHandler(&h.log, nil)),
		DialShell: func(context.Context) (ShellHostd, error) { return sh, nil },
		Terminal: func() (relay.Config, func(), error) {
			if termErr != nil {
				return relay.Config{}, nil, termErr
			}
			return relay.Config{Charset: cs, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}, func() {}, nil
		},
		Lookup: func(name string) (string, bool) {
			if name == "TERM" {
				return "xterm\x1b", true
			}
			return "", false
		},
	}
	code := RunShell(h.t.Context(), env, dir, args)
	r := shellRun{hostd: sh}
	r.code, r.stdout, r.stderr = code, stdout.String(), stderr.String()
	return r
}

var utf8Charset = tty.Charset{Source: "LANG", Value: "en_US.UTF-8", UTF8: true}

func opened() *shellCall {
	return &shellCall{opened: &hostdv1.ShellOpened{ShellId: "myrepo-20261021-1412-k3f9", ProjectName: "myrepo", GuestUser: "wbp1"}}
}

// TestShellRelays: wb shell opens the shell of the directory's project,
// prints the label line and the variable left out, and exits with the
// shell's status.
func TestShellRelays(t *testing.T) {
	h := newHarness(t)
	repo := newRepo(t, filepath.Join(h.root, "myrepo"), "first", "")
	op := h.mustStart(repo)
	r := h.shell(repo, opened(), utf8Charset, nil)
	if r.code != 3 {
		t.Fatalf("exit %d, want 3\n%s", r.code, r.stderr)
	}
	if got := r.hostd.call.open.GetProjectId(); got != op.Project.GetId() {
		t.Errorf("opened project %q, want %q", got, op.Project.GetId())
	}
	for _, s := range []string{
		"wb: debug shell myrepo-20261021-1412-k3f9 in project myrepo, as guest user wbp1.",
		"wb: left out of the guest's environment: TERM",
	} {
		if !strings.Contains(r.stderr, s) {
			t.Errorf("stderr %q misses %q", r.stderr, s)
		}
	}
}

// TestShellProjectFlag: --project names the project wherever wb runs.
func TestShellProjectFlag(t *testing.T) {
	h := newHarness(t)
	repo := newRepo(t, filepath.Join(h.root, "myrepo"), "first", "")
	op := h.mustStart(repo)
	r := h.shell(mkdir(t, filepath.Join(h.root, "elsewhere")), opened(), utf8Charset, nil, "--project", op.Project.GetId())
	if r.code != 3 || r.hostd.call.open.GetProjectId() != op.Project.GetId() {
		t.Errorf("exit %d, opened %q\n%s", r.code, r.hostd.call.open.GetProjectId(), r.stderr)
	}
}

// TestShellNotRegistered: a repository that isn't a project is refused
// before a shell is asked for, naming the commands that register one.
func TestShellNotRegistered(t *testing.T) {
	h := newHarness(t)
	repo := newRepo(t, filepath.Join(h.root, "myrepo"), "first", "")
	r := h.shell(repo, opened(), utf8Charset, nil)
	wantRefused(t, r.result, "project-not-registered", "wb claude", "wb project place")
	if r.hostd.calls != 0 {
		t.Errorf("Shell called %d times, want 0", r.hostd.calls)
	}
}

// TestShellTerminalUnsupported: a host without terminal support for
// wb shell is refused with exit 255.
func TestShellTerminalUnsupported(t *testing.T) {
	h := newHarness(t)
	r := h.shell(h.root, opened(), utf8Charset, errors.ErrUnsupported)
	wantRefused(t, r.result, RuleTerminalUnsupported)
}

// TestShellCharsetRefused: a charset other than UTF-8 is refused before
// wb-hostd is asked.
func TestShellCharsetRefused(t *testing.T) {
	h := newHarness(t)
	repo := newRepo(t, filepath.Join(h.root, "myrepo"), "first", "")
	h.mustStart(repo)
	r := h.shell(repo, opened(), tty.Charset{Source: "LANG", Value: "C"}, nil)
	if r.code != exitFailure || r.hostd.calls != 0 {
		t.Errorf("exit %d, Shell calls %d; want 255 and 0\n%s", r.code, r.hostd.calls, r.stderr)
	}
}

// TestShellHostdRefusal: wb-hostd's refusal prints its message and its
// rule, without the relay's wrapping.
func TestShellHostdRefusal(t *testing.T) {
	h := newHarness(t)
	repo := newRepo(t, filepath.Join(h.root, "myrepo"), "first", "")
	h.mustStart(repo)
	st, err := status.New(codes.ResourceExhausted, "the VM has 64 shells open").
		WithDetails(&errdetails.ErrorInfo{Reason: "vm-session-cap"})
	if err != nil {
		t.Fatal(err)
	}
	r := h.shell(repo, &shellCall{refused: st.Err()}, utf8Charset, nil)
	wantRefused(t, r.result, "vm-session-cap", "wraith box: the VM has 64 shells open (vm-session-cap)")
}

// TestShellOpenedInvalid: a ShellOpened out of its bounds ends wb shell
// with 255.
func TestShellOpenedInvalid(t *testing.T) {
	h := newHarness(t)
	repo := newRepo(t, filepath.Join(h.root, "myrepo"), "first", "")
	h.mustStart(repo)
	call := &shellCall{opened: &hostdv1.ShellOpened{ShellId: "../x", ProjectName: "p", GuestUser: "u"}}
	r := h.shell(repo, call, utf8Charset, nil)
	if r.code != exitFailure || !strings.Contains(r.stderr, "session id form") {
		t.Errorf("exit %d, want 255 naming the id form\n%s", r.code, r.stderr)
	}
}
