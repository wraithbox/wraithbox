//go:build unix

package hostservice_test

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit/audittest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/startup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

const (
	urlA  = "https://a.example/a.git"
	urlB  = "https://b.example/b.git"
	rootA = "1111111111111111111111111111111111111111"
	rootB = "2222222222222222222222222222222222222222"
)

var gitPassed = &hostdv1.HostGit{Binary: "/usr/bin/git", Version: "2.54.0", Rule: "git-version-accepted"}

// projectFixture is a ProjectServer on a temporary state.db, served on
// a Unix socket in a temporary directory, with its log.
type projectFixture struct {
	ctx   context.Context
	c     hostdv1.ProjectServiceClient
	db    *state.DB
	logs  *syncBuffer
	audit *audittest.Recorder
}

func newProjects(t *testing.T, edit func(*hostservice.ProjectServer)) *projectFixture {
	t.Helper()
	db, err := state.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logs, rec := &syncBuffer{}, &audittest.Recorder{}
	s := &hostservice.ProjectServer{Store: db, Git: gitPassed, Audit: rec, Log: slog.New(slog.NewJSONHandler(logs, nil))}
	if edit != nil {
		edit(s)
	}
	return &projectFixture{ctx: t.Context(), c: serveProjects(t, s), db: db, logs: logs, audit: rec}
}

// serveProjects serves s on a Unix socket with wb-hostd's message size
// caps, and returns a client.
func serveProjects(t *testing.T, s *hostservice.ProjectServer) hostdv1.ProjectServiceClient {
	t.Helper()
	dir, err := os.MkdirTemp("", "ps") //nolint:usetesting // t.TempDir's path holds the test name, which pushes the socket past sun_path's 104 bytes
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
	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(16<<20)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return hostdv1.NewProjectServiceClient(conn)
}

func key(path string) *hostdv1.ProjectKey {
	return &hostdv1.ProjectKey{Realm: "native", Path: path}
}

func urls(u ...string) *hostdv1.ProjectIdentity { return &hostdv1.ProjectIdentity{Urls: u} }

func roots(r ...string) *hostdv1.ProjectIdentity { return &hostdv1.ProjectIdentity{RootCommits: r} }

func (f *projectFixture) register(t *testing.T, path string, id *hostdv1.ProjectIdentity, p hostdv1.Placement) *hostdv1.RegisterProjectResponse {
	t.Helper()
	res, err := f.c.RegisterProject(t.Context(), &hostdv1.RegisterProjectRequest{Key: key(path), Identity: id, Name: "foo", Placement: p})
	if err != nil {
		t.Fatalf("RegisterProject %s: %v", path, err)
	}
	return res
}

func (f *projectFixture) lookup(path string, id *hostdv1.ProjectIdentity, purpose hostdv1.LookupPurpose) (*hostdv1.LookupProjectResponse, error) {
	return f.c.LookupProject(f.ctx, &hostdv1.LookupProjectRequest{Key: key(path), Identity: id, Purpose: purpose})
}

// setTrust sets config_trust on, as wb trust will (S09-03-project-settings).
func (f *projectFixture) setTrust(t *testing.T, id string) {
	t.Helper()
	err := f.db.Projects(t.Context(), func(tx *state.ProjectTx) error {
		row, _, err := tx.ByID(id)
		if err != nil {
			return err
		}
		row.ConfigTrust = true
		return tx.Update(row)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// records returns the project change (5019) audit records, each as a
// map of its command, project, decision, rule, and the project's
// settings before and after.
func (f *projectFixture) records(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range f.audit.Find("project changed") {
		if r.ClassUID() != 5019 {
			t.Errorf("project change in class %d, want 5019", r.ClassUID())
		}
		fs := r.Fields()
		m := map[string]any{"command": fs.Command, "decision": fs.Decision, "rule": fs.Rule}
		if p := fs.Project; p != nil {
			m["project"] = p.ID
			m["before"] = audittest.AsMap(p.Before)
			if p.After != nil {
				m["after"] = audittest.AsMap(p.After)
			}
		}
		out = append(out, m)
	}
	return out
}

// lastRecord returns the last 5019 record, and checks its command and
// project.
func (f *projectFixture) lastRecord(t *testing.T, command, id string) (before, after map[string]any) {
	t.Helper()
	recs := f.records(t)
	if len(recs) == 0 {
		t.Fatalf("no 5019 record for %s", command)
	}
	r := recs[len(recs)-1]
	if r["command"] != command || r["project"] != id || r["decision"] != "accept" || r["rule"] == "" {
		t.Errorf("record %v, want command %s project %s", r, command, id)
	}
	before, _ = r["before"].(map[string]any)
	after, _ = r["after"].(map[string]any)
	return before, after
}

func wantRefused(t *testing.T, what string, err error, code codes.Code, rule string) string {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: accepted, want %s", what, rule)
	}
	gotCode, gotRule, msg := ruleOf(t, err)
	if gotCode != code || gotRule != rule {
		t.Errorf("%s: %s %s (%s), want %s %s", what, gotCode, gotRule, msg, code, rule)
	}
	return msg
}

func listOf(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, s := range l {
		str, _ := s.(string)
		out = append(out, str)
	}
	return out
}

var idForm = regexp.MustCompile(`^[0-9a-f]{32}$`)

// TestProjectRegisterAndLookup: a registered key is found with its
// identity check, a second registration of it is refused, and each
// project gets its own random id.
func TestProjectRegisterAndLookup(t *testing.T) {
	f := newProjects(t, nil)
	a := f.register(t, "/r/a/.git", urls(urlA), hostdv1.Placement_PLACEMENT_UNSPECIFIED).GetProject()
	b := f.register(t, "/r/b/.git", urls(urlB), hostdv1.Placement_PLACEMENT_UNSPECIFIED).GetProject()
	for _, p := range []*hostdv1.Project{a, b} {
		if !idForm.MatchString(p.GetId()) {
			t.Errorf("id %q", p.GetId())
		}
	}
	if a.GetId() == b.GetId() {
		t.Error("two registrations have one id")
	}
	if a.GetPlacement() != hostdv1.Placement_PLACEMENT_WORK || a.GetConfigTrust() {
		t.Errorf("a new project: %v", a)
	}
	res, err := f.lookup("/r/a/.git", urls(urlA, urlA), hostdv1.LookupPurpose_LOOKUP_PURPOSE_SESSION_START)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetProject().GetId() != a.GetId() || res.GetOutcome() != hostdv1.IdentityOutcome_IDENTITY_OUTCOME_MATCHED ||
		res.GetGit().GetRule() != "git-version-accepted" {
		t.Errorf("lookup %v", res)
	}
	_, err = f.c.RegisterProject(t.Context(), &hostdv1.RegisterProjectRequest{Key: key("/r/a/.git"), Identity: urls(urlB), Name: "again"})
	msg := wantRefused(t, "second registration", err, codes.FailedPrecondition, hostservice.RuleProjectKeyTaken)
	if !strings.Contains(msg, a.GetId()) {
		t.Errorf("the refusal doesn't name the project: %s", msg)
	}
	res, err = f.lookup("/r/c/.git", urls(urlA), hostdv1.LookupPurpose_LOOKUP_PURPOSE_COMMAND)
	if err != nil {
		t.Fatal(err)
	}
	if u := res.GetUnknown(); u.GetMatchCount() != 1 || u.GetMatches()[0].GetId() != a.GetId() {
		t.Errorf("unknown key: %v", res)
	}
}

// TestProjectIdentityRefused: changed URLs and a recorded root commit
// that is gone are refused with their rules, with both identities in
// the refusal (S05-04-naming, "Identity check"; SEC08-proj-isolation).
func TestProjectIdentityRefused(t *testing.T) {
	f := newProjects(t, nil)
	f.register(t, "/r/a/.git", urls(urlA), hostdv1.Placement_PLACEMENT_UNSPECIFIED)
	f.register(t, "/r/n/.git", roots(rootA, rootB), hostdv1.Placement_PLACEMENT_UNSPECIFIED)
	for _, purpose := range []hostdv1.LookupPurpose{
		hostdv1.LookupPurpose_LOOKUP_PURPOSE_SESSION_START, hostdv1.LookupPurpose_LOOKUP_PURPOSE_COMMAND,
	} {
		_, err := f.lookup("/r/a/.git", urls(urlA, urlB), purpose)
		wantRefused(t, "URLs changed", err, codes.FailedPrecondition, string(project.RuleURLsChanged))
		m := mismatchOf(t, err)
		if !slices.Equal(m.GetRecorded().GetUrls(), []string{urlA}) || !slices.Equal(m.GetCurrent().GetUrls(), []string{urlA, urlB}) {
			t.Errorf("mismatch %v", m)
		}
		_, err = f.lookup("/r/n/.git", roots(rootB), purpose)
		wantRefused(t, "root gone", err, codes.FailedPrecondition, string(project.RuleRootCommitGone))
		m = mismatchOf(t, err)
		if len(m.GetRecorded().GetRootCommits()) != 2 || len(m.GetCurrent().GetRootCommits()) != 1 {
			t.Errorf("mismatch %v", m)
		}
	}
	if !strings.Contains(f.logs.String(), `"rule":"project-urls-changed","decision":"refuse"`) {
		t.Errorf("the refusal isn't logged with its rule:\n%s", f.logs.String())
	}
}

func mismatchOf(t *testing.T, err error) *hostdv1.IdentityMismatch {
	t.Helper()
	st, _ := status.FromError(err)
	for _, d := range st.Details() {
		if m, ok := d.(*hostdv1.IdentityMismatch); ok {
			return m
		}
	}
	t.Fatalf("%v has no IdentityMismatch", err)
	return nil
}

// TestProjectGrowOnly: an empty recorded identity is recorded at a
// session start only, with a 5019 record (S05-04-naming, "Grow-only").
func TestProjectGrowOnly(t *testing.T) {
	f := newProjects(t, nil)
	p := f.register(t, "/r/e/.git", nil, hostdv1.Placement_PLACEMENT_UNSPECIFIED).GetProject()
	res, err := f.lookup("/r/e/.git", roots(rootA), hostdv1.LookupPurpose_LOOKUP_PURPOSE_COMMAND)
	if err != nil || res.GetOutcome() != hostdv1.IdentityOutcome_IDENTITY_OUTCOME_EMPTY_RECORDED {
		t.Fatalf("command lookup: %v %v", res, err)
	}
	if n := len(f.records(t)); n != 0 {
		t.Errorf("%d records after a command lookup", n)
	}
	res, err = f.lookup("/r/e/.git", roots(rootA), hostdv1.LookupPurpose_LOOKUP_PURPOSE_SESSION_START)
	if err != nil || res.GetOutcome() != hostdv1.IdentityOutcome_IDENTITY_OUTCOME_GROWN ||
		!slices.Equal(res.GetProject().GetIdentity().GetRootCommits(), []string{rootA}) {
		t.Fatalf("session start: %v %v", res, err)
	}
	before, after := f.lastRecord(t, "wb claude", p.GetId())
	if len(listOf(before["root_commits"])) != 0 || !slices.Equal(listOf(after["root_commits"]), []string{rootA}) {
		t.Errorf("record before %v after %v", before, after)
	}
	res, err = f.lookup("/r/e/.git", roots(rootA, rootB), hostdv1.LookupPurpose_LOOKUP_PURPOSE_SESSION_START)
	if err != nil || res.GetOutcome() != hostdv1.IdentityOutcome_IDENTITY_OUTCOME_MATCHED {
		t.Errorf("after growing: %v %v", res, err)
	}
	_, err = f.lookup("/r/e/.git", roots(rootB), hostdv1.LookupPurpose_LOOKUP_PURPOSE_SESSION_START)
	wantRefused(t, "after growing", err, codes.FailedPrecondition, string(project.RuleRootCommitGone))
}

// TestProjectMatchPlacement: a clone of an isolated project starts
// isolated, without its config_trust, and a second clone of a work
// project starts work (T13-new-project-grants).
func TestProjectMatchPlacement(t *testing.T) {
	f := newProjects(t, nil)
	iso := f.register(t, "/r/iso/.git", urls(urlA), hostdv1.Placement_PLACEMENT_ISOLATED).GetProject()
	f.setTrust(t, iso.GetId())
	clone := f.register(t, "/r/iso2/.git", urls(urlA), hostdv1.Placement_PLACEMENT_WORK)
	if p := clone.GetProject(); p.GetPlacement() != hostdv1.Placement_PLACEMENT_ISOLATED || p.GetConfigTrust() {
		t.Errorf("clone of an isolated project: %v", p)
	}
	if clone.GetMatchCount() != 1 || clone.GetMatches()[0].GetId() != iso.GetId() {
		t.Errorf("matches %v", clone.GetMatches())
	}
	if !strings.Contains(f.logs.String(), `"placement_reason":"isolated-match"`) {
		t.Error("the placement decision isn't logged")
	}
	work := f.register(t, "/r/w/.git", roots(rootA), hostdv1.Placement_PLACEMENT_UNSPECIFIED).GetProject()
	f.setTrust(t, work.GetId())
	second := f.register(t, "/r/w2/.git", roots(rootB, rootA), hostdv1.Placement_PLACEMENT_UNSPECIFIED)
	if p := second.GetProject(); p.GetPlacement() != hostdv1.Placement_PLACEMENT_WORK || p.GetConfigTrust() {
		t.Errorf("second clone of a work project: %v", p)
	}
	if second.GetMatchCount() != 1 {
		t.Errorf("matches %v", second.GetMatches())
	}
}

// TestProjectConfirm: confirm records the current identity and clears
// config_trust, with both identities in its record (S05-04-naming,
// "Confirming"; SEC09-host-policy).
func TestProjectConfirm(t *testing.T) {
	f := newProjects(t, nil)
	p := f.register(t, "/r/a/.git", urls(urlA), hostdv1.Placement_PLACEMENT_ISOLATED).GetProject()
	f.setTrust(t, p.GetId())
	res, err := f.c.ConfirmProject(t.Context(), &hostdv1.ConfirmProjectRequest{Id: p.GetId(), Identity: urls(urlB)})
	if err != nil {
		t.Fatal(err)
	}
	got := res.GetProject()
	if got.GetConfigTrust() || got.GetPlacement() != hostdv1.Placement_PLACEMENT_ISOLATED ||
		!slices.Equal(got.GetIdentity().GetUrls(), []string{urlB}) {
		t.Errorf("after confirm: %v", got)
	}
	before, after := f.lastRecord(t, "wb project confirm", p.GetId())
	if before["config_trust"] != true || after["config_trust"] != false ||
		!slices.Equal(listOf(before["urls"]), []string{urlA}) || !slices.Equal(listOf(after["urls"]), []string{urlB}) {
		t.Errorf("record before %v after %v", before, after)
	}
	if _, err := f.lookup("/r/a/.git", urls(urlB), hostdv1.LookupPurpose_LOOKUP_PURPOSE_SESSION_START); err != nil {
		t.Errorf("lookup after confirm: %v", err)
	}
}

// TestProjectGet: by id and by name, and a name more than one project
// has is refused.
func TestProjectGet(t *testing.T) {
	f := newProjects(t, nil)
	ctx := t.Context()
	a := f.register(t, "/r/a/.git", urls(urlA), hostdv1.Placement_PLACEMENT_UNSPECIFIED).GetProject()
	got, err := f.c.GetProject(ctx, &hostdv1.GetProjectRequest{By: &hostdv1.GetProjectRequest_Name{Name: "foo"}})
	if err != nil || got.GetProject().GetId() != a.GetId() {
		t.Fatalf("by name: %v %v", got, err)
	}
	f.register(t, "/r/b/.git", urls(urlB), hostdv1.Placement_PLACEMENT_UNSPECIFIED)
	_, err = f.c.GetProject(ctx, &hostdv1.GetProjectRequest{By: &hostdv1.GetProjectRequest_Name{Name: "foo"}})
	wantRefused(t, "two named foo", err, codes.FailedPrecondition, hostservice.RuleProjectNameAmbiguous)
	_, err = f.c.GetProject(ctx, &hostdv1.GetProjectRequest{})
	wantRefused(t, "neither", err, codes.InvalidArgument, hostservice.RuleProjectForm)
	_, err = f.c.GetProject(ctx, &hostdv1.GetProjectRequest{By: &hostdv1.GetProjectRequest_Name{Name: "nothing"}})
	wantRefused(t, "unknown name", err, codes.NotFound, hostservice.RuleProjectUnknown)
}

// TestProjectSessionStartGit: while wb-hostd's git check hasn't passed,
// a session start is refused with the check's rule, and other lookups
// are served (S08-08-host-git, "Where it is checked").
func TestProjectSessionStartGit(t *testing.T) {
	for _, tc := range []struct {
		name string
		git  *hostdv1.HostGit
		rule string
	}{
		{"too old", &hostdv1.HostGit{Binary: "/g", Version: "2.40.0", Rule: "git-version-too-old", Message: "old"}, "git-version-too-old"},
		{"not absolute", &hostdv1.HostGit{Rule: "git-path-not-absolute", Message: "relative"}, "git-path-not-absolute"},
		{"unchecked", nil, hostservice.RuleGitUnchecked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjects(t, func(s *hostservice.ProjectServer) { s.Git = tc.git })
			_, err := f.lookup("/r/a/.git", urls(urlA), hostdv1.LookupPurpose_LOOKUP_PURPOSE_SESSION_START)
			wantRefused(t, "session start", err, codes.FailedPrecondition, tc.rule)
			if _, err := f.lookup("/r/a/.git", urls(urlA), hostdv1.LookupPurpose_LOOKUP_PURPOSE_COMMAND); err != nil {
				t.Errorf("command lookup: %v", err)
			}
		})
	}
}

// TestProjectInputRefused: each field over its cap or out of its form
// is refused with its rule before state.db is read
// (S04-00-architecture, "Input from `wb`"; SEC13-bounded-resources).
func TestProjectInputRefused(t *testing.T) {
	f := newProjects(t, nil)
	many := make([]string, 1025)
	for i := range many {
		many[i] = urlA
	}
	big := make([]string, 200)
	for i := range big {
		big[i] = "https://a.example/" + strings.Repeat("x", 8000)
	}
	cmd := hostdv1.LookupPurpose_LOOKUP_PURPOSE_COMMAND
	for _, tc := range []struct {
		name string
		req  *hostdv1.LookupProjectRequest
		rule string
	}{
		{"URL count", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: urls(many...), Purpose: cmd}, hostservice.RuleProjectOverCap},
		{"URL bytes", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: urls(big...), Purpose: cmd}, hostservice.RuleProjectOverCap},
		{"URL length", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: urls(strings.Repeat("x", 8193)), Purpose: cmd}, hostservice.RuleProjectOverCap},
		{"root count", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: roots(slices.Repeat([]string{rootA}, 1025)...), Purpose: cmd}, hostservice.RuleProjectOverCap},
		{"path length", &hostdv1.LookupProjectRequest{Key: key("/" + strings.Repeat("x", 4096)), Purpose: cmd}, hostservice.RuleProjectOverCap},
		{"URL with a space", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: urls("https://a b"), Purpose: cmd}, hostservice.RuleProjectForm},
		{"empty URL", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: urls(""), Purpose: cmd}, hostservice.RuleProjectForm},
		{"URL with a line end", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: urls("https://a\n"), Purpose: cmd}, hostservice.RuleProjectForm},
		{"upper-case root", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: roots(strings.ToUpper("abcdef" + rootA[6:])), Purpose: cmd}, hostservice.RuleProjectForm},
		{"short root", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: roots(rootA[:39]), Purpose: cmd}, hostservice.RuleProjectForm},
		{"relative path", &hostdv1.LookupProjectRequest{Key: key("r/.git"), Purpose: cmd}, hostservice.RuleProjectForm},
		{"unclean path", &hostdv1.LookupProjectRequest{Key: key("/r/../.git"), Purpose: cmd}, hostservice.RuleProjectForm},
		{"control in path", &hostdv1.LookupProjectRequest{Key: key("/r/\x1b[2J"), Purpose: cmd}, hostservice.RuleProjectForm},
		{"right-to-left override in path", &hostdv1.LookupProjectRequest{Key: key("/r/\u202e.git"), Purpose: cmd}, hostservice.RuleProjectForm},
		{"zero-width space in path", &hostdv1.LookupProjectRequest{Key: key("/r/a\u200b/.git"), Purpose: cmd}, hostservice.RuleProjectForm},
		{"user in URL", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: urls("https://tok@a.example/a.git"), Purpose: cmd}, hostservice.RuleProjectForm},
		{"query in URL authority", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: urls("http://a.example?t=x"), Purpose: cmd}, hostservice.RuleProjectForm},
		{"fragment in URL authority", &hostdv1.LookupProjectRequest{Key: key("/r"), Identity: urls("https://a.example#x"), Purpose: cmd}, hostservice.RuleProjectForm},
		{"no key", &hostdv1.LookupProjectRequest{Purpose: cmd}, hostservice.RuleProjectForm},
		{"WSL realm", &hostdv1.LookupProjectRequest{Key: &hostdv1.ProjectKey{Realm: "wsl:Ubuntu", Path: "/r"}, Purpose: cmd}, hostservice.RuleProjectRealm},
		{"no purpose", &hostdv1.LookupProjectRequest{Key: key("/r")}, hostservice.RuleProjectPurpose},
		{"purpose out of the enum", &hostdv1.LookupProjectRequest{Key: key("/r"), Purpose: 9}, hostservice.RuleProjectPurpose},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.c.LookupProject(t.Context(), tc.req)
			wantRefused(t, tc.name, err, codes.InvalidArgument, tc.rule)
		})
	}
	ctx := t.Context()
	_, err := f.c.RegisterProject(ctx, &hostdv1.RegisterProjectRequest{Key: key("/r/x/.git"), Name: "a..b"})
	wantRefused(t, "register with an invalid name", err, codes.InvalidArgument, hostservice.RuleProjectNameInvalid)
	_, err = f.c.RegisterProject(ctx, &hostdv1.RegisterProjectRequest{Key: key("/r/x/.git"), Name: "x", Placement: 5})
	wantRefused(t, "register with a placement out of the enum", err, codes.InvalidArgument, hostservice.RuleProjectPlacement)
	for _, id := range []string{"", "0123", strings.Repeat("A", 32), strings.Repeat("g", 32), strings.Repeat("0", 33)} {
		_, err := f.c.RemoveProject(ctx, &hostdv1.RemoveProjectRequest{Id: id})
		wantRefused(t, "id "+id, err, codes.InvalidArgument, hostservice.RuleProjectIDForm)
	}
	if strings.Contains(f.logs.String(), `"decision":"accept"`) {
		t.Errorf("a refused request was accepted:\n%s", f.logs.String())
	}
}

// TestProjectRefusingMode: in refusing mode every call is refused with
// the rule that put wb-hostd in it.
func TestProjectRefusingMode(t *testing.T) {
	c := serveProjects(t, &hostservice.ProjectServer{
		Refusing: &startup.Refusing{Rule: "state-db-newer", Detail: "state.db is newer"},
		Log:      slog.New(slog.DiscardHandler),
	})
	ctx := t.Context()
	_, err := c.LookupProject(ctx, &hostdv1.LookupProjectRequest{Key: key("/r"), Purpose: hostdv1.LookupPurpose_LOOKUP_PURPOSE_COMMAND})
	wantRefused(t, "lookup", err, codes.FailedPrecondition, "state-db-newer")
	_, err = c.RemoveProject(ctx, &hostdv1.RemoveProjectRequest{Id: strings.Repeat("0", 32)})
	wantRefused(t, "rm", err, codes.FailedPrecondition, "state-db-newer")
	_, err = serveProjects(t, &hostservice.ProjectServer{}).GetProject(ctx, &hostdv1.GetProjectRequest{})
	wantRefused(t, "no store", err, codes.Internal, hostservice.RuleInternal)
}

// TestProjectNoAuditLog: without the audit writer every call is
// refused, so no change goes unrecorded (SEC10-audit).
func TestProjectNoAuditLog(t *testing.T) {
	f := newProjects(t, func(s *hostservice.ProjectServer) { s.Audit = nil })
	_, err := f.c.RegisterProject(t.Context(), &hostdv1.RegisterProjectRequest{Key: key("/r/x/.git"), Name: "x"})
	wantRefused(t, "register without an audit log", err, codes.Internal, hostservice.RuleInternal)
	if !strings.Contains(f.logs.String(), "wb-hostd has no audit log") {
		t.Errorf("refusal not logged with its reason:\n%s", f.logs.String())
	}
}
