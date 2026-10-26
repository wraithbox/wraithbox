package projectcmd

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostgit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

// C04-projects, "Project commands": wb project place isolated in a new
// clone registers it isolated, and on a registered project of another
// placement it is refused with the reason that names I232. The same
// placement again changes nothing.
func TestC04Place(t *testing.T) {
	h := newHarness(t)
	iso := newRepo(t, filepath.Join(h.root, "iso"), "root", urlA)
	r := h.mustWB(iso, "place", "isolated")
	if !strings.Contains(r.stderr, "registered project iso") {
		t.Errorf("place printed %q", r.stderr)
	}
	op := h.mustStart(iso)
	if op.Project.GetPlacement() != hostdv1.Placement_PLACEMENT_ISOLATED {
		t.Errorf("placement %v, want isolated", op.Project.GetPlacement())
	}
	h.mustWB(iso, "place", "isolated")

	work := newRepo(t, filepath.Join(h.root, "work"), "other", urlB)
	h.mustStart(work)
	wantRefused(t, h.wb(work, "place", "isolated"), hostservice.RuleProjectPlaceHasHome, "I232")
	wantRefused(t, h.wb(h.root, "place", "work", "--project", "iso"), hostservice.RuleProjectPlaceHasHome, "I232")
	if recs := h.records("wb project place"); len(recs) != 0 {
		t.Errorf("place wrote 5019 records %+v, but changed nothing", recs)
	}
}

// C04-projects, "Project commands": move and rename write a 5019 record
// with the project before and after, and rm one with the project before.
// rm removes the project directory and the settings files.
func TestC04ChangeRecords(t *testing.T) {
	h := newHarness(t)
	x, y := filepath.Join(h.root, "x"), filepath.Join(h.root, "y")
	newRepo(t, x, "root", urlA)
	op := h.mustStart(x)
	id := op.Project.GetId()

	if err := os.Rename(x, y); err != nil {
		t.Fatal(err)
	}
	h.mustWB(h.root, "move", "y", "--project", id)
	h.mustWB(y, "rename", "renamed")
	wantChange(t, h.records("wb project move"), "location", filepath.Join(x, ".git"), filepath.Join(y, ".git"))
	wantChange(t, h.records("wb project rename"), "name", "x", "renamed")

	settings := filepath.Join(h.paths.Config, "projects", id+".toml")
	writeFile(t, settings, "", 0o600)
	h.mustWB(y, "rm")
	if _, ok := h.row(id); ok {
		t.Error("the project is still in state.db")
	}
	for _, gone := range []string{op.Dir, settings} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}
	recs := h.records("wb project rm")
	if len(recs) != 1 || recs[0].Before["name"] != "renamed" || recs[0].After != nil {
		t.Errorf("rm records %+v, want one with the project before and nothing after", recs)
	}
}

func wantChange(t *testing.T, recs []record, field string, before, after any) {
	t.Helper()
	if len(recs) != 1 {
		t.Fatalf("records %+v, want one", recs)
	}
	if recs[0].Before[field] != before || recs[0].After[field] != after {
		t.Errorf("record %s %v -> %v, want %v -> %v", field, recs[0].Before[field], recs[0].After[field], before, after)
	}
}

// C04-projects, "Project commands": --project with a name two projects
// share is refused with both ids named, and confirm takes no --project.
func TestC04ProjectFlagRefusals(t *testing.T) {
	h := newHarness(t)
	a := h.mustStart(newRepo(t, filepath.Join(h.root, "a", "foo"), "one", urlA)).Project.GetId()
	b := h.mustStart(newRepo(t, filepath.Join(h.root, "b", "foo"), "two", urlB)).Project.GetId()
	wantRefused(t, h.wb(h.root, "show", "--project", "foo"), hostservice.RuleProjectNameAmbiguous, a, b)
	if r := h.wb(h.root, "show", "--project", a); r.code != exitOK || !strings.Contains(r.stdout, a) {
		t.Errorf("show by id: exit %d, %q", r.code, r.stdout)
	}
	if r := h.wb(h.root, "confirm", "--project", a); r.code != exitUsage {
		t.Errorf("confirm --project: exit %d, want %d", r.code, exitUsage)
	}
}

// C04-projects, "Project commands": a session start whose git binary is
// not wb-hostd's is refused, with both paths named, and so is a git
// below the minimum or a release candidate of it (S08-08-host-git).
func TestC04GitRefusals(t *testing.T) {
	h := newHarness(t)
	dir := newRepo(t, filepath.Join(h.root, "r"), "root", urlA)
	h.mustStart(dir)

	h.conn = serve(t, &hostservice.ProjectServer{
		Store: h.db, Log: slog.New(slog.DiscardHandler),
		Git: &hostdv1.HostGit{Binary: "/usr/bin/git", Version: "2.54.0", Rule: "git-version-accepted"},
	})
	_, err := h.start(dir)
	wantRule(t, err, string(project.RuleGitMismatch))
	wantRefused(t, h.wb(dir, "show"), string(project.RuleGitMismatch), "/usr/bin/git", h.git)

	for version, rule := range map[string]hostgit.Rule{"2.53.0": hostgit.RuleVersionTooOld, "2.54.0.rc1": hostgit.RuleVersionUnreadable} {
		writeFile(t, h.git, "#!/bin/sh\necho 'git version "+version+"'\n", 0o700)
		_, err := h.start(dir)
		wantRule(t, err, string(rule))
		wantRefused(t, h.wb(dir, "show"), string(rule), version, hostgit.MinVersion)
	}
}

// TestVersionRefusalNamesBoth: a wb-hostd of another build version is
// refused before a request is sent, and the line names both versions.
func TestVersionRefusalNamesBoth(t *testing.T) {
	data, err := os.MkdirTemp("", "pv") //nolint:usetesting // t.TempDir's path holds the test name, which pushes run/<name>.sock past sun_path's 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	run := filepath.Join(data, "run")
	log := slog.New(slog.DiscardHandler)
	ln, err := localipc.Listen(t.Context(), log, run, localipc.HostdEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(grpccreds.New(log, "1.2.3")))
	hostdv1.RegisterProjectServiceServer(srv, &hostdv1.UnimplementedProjectServiceServer{})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	h := newHarness(t)
	var stderr bytes.Buffer
	env := Env{
		Stdout: &bytes.Buffer{}, Stderr: &stderr, Paths: h.paths, Log: log,
		Dial: func(context.Context) (Hostd, error) { return dial(log, run) },
	}
	if code := Run(t.Context(), env, h.root, []string{"show", "--project", "foo"}); code != exitFailure {
		t.Fatalf("exit %d", code)
	}
	line := strings.TrimRight(stderr.String(), "\n")
	for _, want := range []string{"wraith box: ", `wb-hostd is version "1.2.3"`, "wb is version unstamped", "(peer-version-unstamped)"} {
		if !strings.Contains(line, want) || strings.Contains(line, "\n") {
			t.Errorf("standard error %q misses %q", line, want)
		}
	}
}
