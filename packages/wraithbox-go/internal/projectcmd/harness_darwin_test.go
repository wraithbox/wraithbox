package projectcmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

// The tests run wb's side with /usr/bin/git through a stand-in that
// answers the version check with the minimum, because the CI runner's
// git can be older. wb-hostd's side is an in-process ProjectService on
// a temporary socket and state.db, and reports the same stand-in as its
// git. wb's git reads the user's global git configuration, as it does
// for every user (S05-04-naming, "Resolving the key"), so the remotes
// are on example hosts no insteadOf rule is likely to name.

// syncBuffer is wb-hostd's log, written from its goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// harness is wb's paths, the git stand-in, and wb-hostd's ProjectService.
type harness struct {
	t     *testing.T
	root  string
	paths paths.Paths
	git   string
	db    *state.DB
	srv   *hostservice.ProjectServer
	conn  *grpc.ClientConn
	logs  *syncBuffer
	log   bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root, err := platform.CanonicalDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, root: root, logs: &syncBuffer{}}
	h.paths = paths.Paths{
		Data: filepath.Join(root, "wb", "data"), Config: filepath.Join(root, "wb", "config"),
		Logs: filepath.Join(root, "wb", "logs"), Run: filepath.Join(root, "wb", "data", "run"),
	}
	mkdir(t, project.Root(h.paths.Data))
	h.git = filepath.Join(root, "bin", "git")
	writeFile(t, h.git, "#!/bin/sh\nif [ \"$#\" = 3 ] && [ \"$3\" = version ]; then echo 'git version 2.54.0'; exit 0; fi\nexec /usr/bin/git \"$@\"\n", 0o700)
	writeFile(t, filepath.Join(h.paths.Config, config.FileName), "git = \""+h.git+"\"\n", 0o600)

	h.db, err = state.Open(t.Context(), mkdir(t, filepath.Join(root, "hostd")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.db.Close() })
	h.srv = &hostservice.ProjectServer{
		Store: h.db, Log: slog.New(slog.NewJSONHandler(h.logs, nil)),
		Git: &hostdv1.HostGit{Binary: h.git, Version: "2.54.0", Rule: "git-version-accepted"},
	}
	h.conn = serve(t, h.srv)
	return h
}

// serve serves s on a Unix socket with wb-hostd's message size caps.
func serve(t *testing.T, s *hostservice.ProjectServer) *grpc.ClientConn {
	t.Helper()
	dir, err := os.MkdirTemp("", "pc") //nolint:usetesting // t.TempDir's path holds the test name, which pushes the socket past sun_path's 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "p.sock")
	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.MaxRecvMsgSize(hostservice.MaxMessageSize), grpc.MaxSendMsgSize(hostservice.MaxMessageSize))
	hostdv1.RegisterProjectServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// nopClose is the harness's connection, which the test closes.
type nopClose struct{ hostdv1.ProjectServiceClient }

func (nopClose) Close() error { return nil }

func (h *harness) client() hostdv1.ProjectServiceClient {
	return hostdv1.NewProjectServiceClient(h.conn)
}

// opener is wb's session-start entry on the harness.
func (h *harness) opener() *project.Opener {
	return &project.Opener{Paths: h.paths, Hostd: h.client(), Log: slog.New(slog.NewJSONHandler(&h.log, nil))}
}

// start is a session start in dir (S05-04-naming, "Which commands").
func (h *harness) start(dir string) (*project.Opened, error) {
	return h.opener().Open(h.t.Context(), dir)
}

// mustStart is a session start that must pass.
func (h *harness) mustStart(dir string) *project.Opened {
	h.t.Helper()
	op, err := h.start(dir)
	if err != nil {
		h.t.Fatalf("session start in %s: %v", dir, err)
	}
	return op
}

// result is what one wb project command printed, and its status.
type result struct {
	code           int
	stdout, stderr string
}

// wb runs `wb -C dir project <args>`.
func (h *harness) wb(dir string, args ...string) result {
	h.t.Helper()
	var stdout, stderr bytes.Buffer
	env := Env{
		Stdout: &stdout, Stderr: &stderr, Paths: h.paths,
		Dial: func(context.Context) (Hostd, error) { return nopClose{h.client()}, nil },
		Log:  slog.New(slog.NewJSONHandler(&h.log, nil)),
	}
	code := Run(h.t.Context(), env, dir, args)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// mustWB runs a command that must exit 0.
func (h *harness) mustWB(dir string, args ...string) result {
	h.t.Helper()
	r := h.wb(dir, args...)
	if r.code != exitOK {
		h.t.Fatalf("wb project %q in %s: exit %d\n%s", args, dir, r.code, r.stderr)
	}
	return r
}

// row returns a project's row in state.db.
func (h *harness) row(id string) (state.Project, bool) {
	h.t.Helper()
	var row state.Project
	var ok bool
	err := h.db.Projects(h.t.Context(), func(tx *state.ProjectTx) error {
		var err error
		row, ok, err = tx.ByID(id)
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return row, ok
}

// count returns the number of projects.
func (h *harness) count() int {
	h.t.Helper()
	var n int
	err := h.db.Projects(h.t.Context(), func(tx *state.ProjectTx) error {
		all, err := tx.All()
		n = len(all)
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

// trust sets a project's config_trust, as wb trust will.
func (h *harness) trust(id string) {
	h.t.Helper()
	err := h.db.Projects(h.t.Context(), func(tx *state.ProjectTx) error {
		row, _, err := tx.ByID(id)
		if err != nil {
			return err
		}
		row.ConfigTrust = true
		return tx.Update(row)
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// record is a 5019 line of wb-hostd's log.
type record struct {
	Command string         `json:"command"`
	Project string         `json:"project"`
	Before  map[string]any `json:"before"`
	After   map[string]any `json:"after"`
}

// records returns the 5019 records of command.
func (h *harness) records(command string) []record {
	h.t.Helper()
	var out []record
	sc := bufio.NewScanner(strings.NewReader(h.logs.String()))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var line struct {
			Event string `json:"event"`
			record
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			h.t.Fatalf("hostd log line %q: %v", sc.Text(), err)
		}
		if line.Event == "5019" && line.Command == command {
			out = append(out, line.record)
		}
	}
	return out
}

// gitT runs /usr/bin/git to build a fixture, in an environment of its
// own, so neither the caller's GIT_* variables nor the user's
// configuration change the fixture.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "init.defaultBranch=main", "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgSign=false",
	}, args...)
	cmd := exec.CommandContext(t.Context(), "/usr/bin/git", full...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + filepath.Join(os.TempDir(), "wraithbox-projectcmd-home-absent"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "PATH=/usr/bin:/bin", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo makes a repository at dir with one commit whose message is
// msg, so two repositories have different root commits, and with
// remote origin at url unless url is empty.
func newRepo(t *testing.T, dir, msg, url string) string {
	t.Helper()
	mkdir(t, dir)
	gitT(t, dir, "init", "-q")
	if msg != "" {
		gitT(t, dir, "commit", "-q", "--allow-empty", "-m", msg)
	}
	if url != "" {
		gitT(t, dir, "remote", "add", "origin", url)
	}
	return dir
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// wantRule fails unless err carries rule, as wb's own refusal or as
// wb-hostd's ErrorInfo reason.
func wantRule(t *testing.T, err error, rule string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want rule %s", rule)
	}
	if got, _ := failureOf(err); got != rule {
		t.Fatalf("rule %s (%v), want %s", got, err, rule)
	}
}

// wantRefused fails unless r is a refusal with rule on its last line,
// and stderr holds each of says.
func wantRefused(t *testing.T, r result, rule string, says ...string) {
	t.Helper()
	if r.code != exitFailure {
		t.Errorf("exit %d, want %d\n%s", r.code, exitFailure, r.stderr)
	}
	lines := strings.Split(strings.TrimRight(r.stderr, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "wraith box: ") || !strings.HasSuffix(last, "("+rule+")") {
		t.Errorf("last line %q, want a wraith box line with rule %s", last, rule)
	}
	for _, s := range says {
		if !strings.Contains(r.stderr, s) {
			t.Errorf("stderr %q misses %q", r.stderr, s)
		}
	}
}
