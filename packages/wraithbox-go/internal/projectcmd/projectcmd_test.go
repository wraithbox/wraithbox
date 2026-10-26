package projectcmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

// TestParse: what a command line holds, and each line a subcommand
// doesn't take, refused as a usage error before wb-hostd is asked.
func TestParse(t *testing.T) {
	good := []struct {
		args []string
		want invocation
	}{
		{[]string{"show"}, invocation{sub: "show"}},
		{[]string{"show", "--project", "foo"}, invocation{sub: "show", project: "foo", byFlag: true}},
		{[]string{"rm", "--project=foo"}, invocation{sub: "rm", project: "foo", byFlag: true}},
		{[]string{"move", "--project", "foo", "--", "-dir"}, invocation{sub: "move", project: "foo", byFlag: true, arg: "-dir"}},
		{[]string{"move", "../d", "--project", "foo"}, invocation{sub: "move", project: "foo", byFlag: true, arg: "../d"}},
		{[]string{"rename", "new-name"}, invocation{sub: "rename", arg: "new-name"}},
		{[]string{"place", "isolated"}, invocation{sub: "place", arg: "isolated", placement: hostdv1.Placement_PLACEMENT_ISOLATED}},
		{[]string{"place", "work", "--project", "foo"}, invocation{sub: "place", project: "foo", byFlag: true, arg: "work", placement: hostdv1.Placement_PLACEMENT_WORK}},
	}
	for _, tc := range good {
		got, err := parse(tc.args)
		if err != nil || got != tc.want {
			t.Errorf("parse(%q) = %+v, %v; want %+v", tc.args, got, err, tc.want)
		}
	}

	bad := map[string][]string{
		"no subcommand":         nil,
		"unknown subcommand":    {"list"},
		"unknown flag":          {"show", "--force"},
		"short flag":            {"show", "-p"},
		"project twice":         {"show", "--project", "a", "--project=b"},
		"empty project":         {"show", "--project", ""},
		"empty project=":        {"show", "--project="},
		"project without value": {"show", "--project"},
		"extra argument":        {"show", "x"},
		"missing argument":      {"move", "--project", "foo"},
		"two arguments":         {"rename", "a", "b"},
		"bad place":             {"place", "elsewhere"},
		"invalid name":          {"rename", "a;$(id)"},
		"name with dots":        {"rename", "a..b"},
		"dash dir without --":   {"move", "-dir", "--project", "foo"},
		"confirm --project":     {"confirm", "--project", "foo"},
	}
	for name, args := range bad {
		if inv, err := parse(args); err == nil {
			t.Errorf("%s: parse(%q) = %+v, want a usage error", name, args, inv)
		}
	}
}

// TestRunUsageExit: a usage error exits 2 and never dials wb-hostd.
func TestRunUsageExit(t *testing.T) {
	var stderr bytes.Buffer
	dialed := false
	env := Env{
		Stdout: &bytes.Buffer{}, Stderr: &stderr, Log: slog.New(slog.DiscardHandler),
		Dial: func(context.Context) (Hostd, error) { dialed = true; return nil, errors.New("dialed") },
	}
	if code := Run(t.Context(), env, ".", []string{"confirm", "--project", "foo"}); code != exitUsage || dialed {
		t.Errorf("exit %d, dialed %v: want %d without a dial", code, dialed, exitUsage)
	}
}

// getHostd answers GetProject with a project of the id a test sets.
type getHostd struct {
	hostdv1.ProjectServiceClient
	id     string
	placed bool
}

func (g *getHostd) GetProject(context.Context, *hostdv1.GetProjectRequest, ...grpc.CallOption) (*hostdv1.GetProjectResponse, error) {
	return &hostdv1.GetProjectResponse{Project: &hostdv1.Project{Id: g.id, Name: "foo"}}, nil
}

func (g *getHostd) PlaceProject(context.Context, *hostdv1.PlaceProjectRequest, ...grpc.CallOption) (*hostdv1.PlaceProjectResponse, error) {
	g.placed = true
	return &hostdv1.PlaceProjectResponse{}, nil
}

func (*getHostd) Close() error { return nil }

// TestCheckedRefusesID: a project from wb-hostd whose id isn't one is
// refused with the id rule before wb builds a path or a request from it
// (S04-06-git-confinement).
func TestCheckedRefusesID(t *testing.T) {
	for _, args := range [][]string{{"show", "--project", "foo"}, {"place", "work", "--project", "foo"}} {
		h := &getHostd{id: "../../x"}
		var stdout, stderr bytes.Buffer
		env := Env{
			Stdout: &stdout, Stderr: &stderr, Log: slog.New(slog.DiscardHandler),
			Paths: paths.Paths{Data: t.TempDir(), Config: t.TempDir(), Logs: t.TempDir()},
			Dial:  func(context.Context) (Hostd, error) { return h, nil },
		}
		code := Run(t.Context(), env, t.TempDir(), args)
		if code != exitFailure || !strings.HasSuffix(strings.TrimSpace(stderr.String()), "("+string(project.RuleIDForm)+")") {
			t.Errorf("%q: exit %d, stderr %q: want a %s refusal", args, code, stderr.String(), project.RuleIDForm)
		}
		if h.placed || stdout.Len() != 0 {
			t.Errorf("%q: placed %v, printed %q after a refused id", args, h.placed, stdout.String())
		}
	}
}
