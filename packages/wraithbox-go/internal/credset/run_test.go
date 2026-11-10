package credset_test

import (
	"bytes"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/credset"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore/storetest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

const (
	team = "ABCDE12345"
	// secret is the value the write tests store. No output may hold it.
	secret = "secret-VALUE-9f2c"
)

// run is one run of credset.Run over a fake store, with what it printed.
type run struct {
	code   int
	stdout string
	// log is wb-credset's standard error and the store's log.
	log  string
	fake *storetest.Fake
}

// runWith runs credset.Run with args, environ and stdin over fake, a
// store that holds the binding group of team.
func runWith(t *testing.T, fake *storetest.Fake, args, environ []string, stdin io.Reader) run {
	t.Helper()
	var stdout, stderr bytes.Buffer
	store, err := secretstore.New(storeitem.ProgramCredset, fake, slog.New(slog.NewJSONHandler(&stderr, nil)))
	if err != nil && fake.TeamID != "" {
		t.Fatal(err)
	}
	code := credset.Run(t.Context(), credset.Process{Args: args, Environ: environ, Stdin: stdin, Stdout: &stdout, Stderr: &stderr}, store)
	return run{code: code, stdout: stdout.String(), log: stderr.String(), fake: fake}
}

func newFake() *storetest.Fake {
	return storetest.New(team, storeitem.Group(team, storeitem.RoleBinding))
}

// decision returns the run's decision line, and fails the test without
// one.
func (r run) decision(t *testing.T) credset.Decision {
	t.Helper()
	d, ok := credset.ParseDecision([]byte(r.log))
	if !ok {
		t.Fatalf("no decision line in the log:\n%s", r.log)
	}
	return d
}

// noSecret fails the test when the value is anywhere in what the run
// printed or logged.
func (r run) noSecret(t *testing.T) {
	t.Helper()
	if strings.Contains(r.stdout+r.log, secret) {
		t.Errorf("the value is in the output:\nstdout: %s\nlog: %s", r.stdout, r.log)
	}
}

// TestC05CredsetRefused feeds wb-credset each argument and environment
// it refuses, and sees credset-refused with exit 1 and no store call
// (C05-credentials-and-store, "wb-credset"; S15-02-secret-store,
// "`wb-credset`").
func TestC05CredsetRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		environ []string
	}{
		{"no argument", nil, nil},
		{"one argument", []string{"write"}, nil},
		{"third argument", []string{"write", "github", secret}, nil},
		{"variable not on the list", []string{"write", "github"}, []string{"WB_VALUE=" + secret}},
		{"empty variable name", []string{"list", "-"}, []string{"=x"}},
		{"unknown operation", []string{"fetch", "github"}, nil},
		{"name with a slash", []string{"write", "a/b"}, nil},
		{"name with capitals", []string{"delete", "GitHub"}, nil},
		{"name too long", []string{"write", strings.Repeat("a", 65)}, nil},
		{"empty name", []string{"write", ""}, nil},
		{"list with a name", []string{"list", "github"}, nil},
		{"value as the name", []string{"write", secret}, nil},
		{"value as the operation", []string{secret, "github"}, nil},
		{"value as a variable name", []string{"write", "github"}, []string{secret}},
		{"value as a variable name with =", []string{"write", "github"}, []string{secret + "=x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runWith(t, newFake(), tc.args, tc.environ, strings.NewReader(secret))
			if r.code != 1 {
				t.Errorf("exit %d, want 1", r.code)
			}
			if d := r.decision(t); d.Rule != credset.RuleRefused || d.Decision != credset.DecisionRefuse {
				t.Errorf("decision %+v, want %s", d, credset.RuleRefused)
			}
			if calls := r.fake.Calls(); len(calls) != 0 {
				t.Errorf("the store saw calls %v, want none", calls)
			}
			r.noSecret(t)
		})
	}
}

// TestC05CredsetWrite sees write store exactly the bytes on standard
// input in the binding group, and log no value.
func TestC05CredsetWrite(t *testing.T) {
	for _, value := range []string{secret, secret + "\n", "\x00\xff" + secret, strings.Repeat("x", credset.MaxValue-len(secret)) + secret} {
		fake := newFake()
		r := runWith(t, fake, []string{"write", "github"}, []string{}, strings.NewReader(value))
		if r.code != 0 {
			t.Fatalf("exit %d, want 0\n%s", r.code, r.log)
		}
		if d := r.decision(t); d.Rule != credset.RuleDone || d.Binding != "github" || d.Op != "write" {
			t.Errorf("decision %+v", d)
		}
		want := []storetest.Call{{Access: storeitem.AccessWrite, Group: storeitem.Group(team, storeitem.RoleBinding), Item: "wraithbox/binding/github"}}
		if calls := fake.Calls(); !slices.Equal(calls, want) {
			t.Errorf("calls %v, want %v", calls, want)
		}
		got, status := fake.Fetch(t.Context(), storeitem.Group(team, storeitem.RoleBinding), "wraithbox/binding/github")
		if status != secretstore.StatusOK || string(got) != value {
			t.Errorf("stored %q (status %v), want the %d bytes on standard input", got, status, len(value))
		}
		r.noSecret(t)
	}
}

// TestC05CredsetWriteRefusedValue sees an empty value and one over the
// cap refused with credset-refused, and nothing written.
func TestC05CredsetWriteRefusedValue(t *testing.T) {
	for name, value := range map[string]string{
		"empty":        "",
		"over the cap": strings.Repeat("y", credset.MaxValue) + secret,
	} {
		t.Run(name, func(t *testing.T) {
			r := runWith(t, newFake(), []string{"write", "github"}, nil, strings.NewReader(value))
			if r.code != 1 {
				t.Errorf("exit %d, want 1", r.code)
			}
			if d := r.decision(t); d.Rule != credset.RuleRefused {
				t.Errorf("decision %+v, want %s", d, credset.RuleRefused)
			}
			if calls := r.fake.Calls(); len(calls) != 0 {
				t.Errorf("the store saw calls %v, want none", calls)
			}
			r.noSecret(t)
		})
	}
}

// TestC05CredsetList sees list print the binding names, sorted, after
// one List call and no Fetch (S15-03-access-rules, rule 1).
func TestC05CredsetList(t *testing.T) {
	fake := newFake()
	group := storeitem.Group(team, storeitem.RoleBinding)
	for _, name := range []string{"zeta", "github", "anthropic"} {
		fake.Write(t.Context(), group, "wraithbox/binding/"+name, []byte(secret))
	}
	before := len(fake.Calls())
	r := runWith(t, fake, []string{"list", "-"}, nil, strings.NewReader(""))
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.log)
	}
	if r.stdout != "anthropic\ngithub\nzeta\n" {
		t.Errorf("stdout %q", r.stdout)
	}
	want := []storetest.Call{{Access: storeitem.AccessList, Group: group}}
	if calls := fake.Calls()[before:]; !slices.Equal(calls, want) {
		t.Errorf("calls %v, want only %v", calls, want)
	}
	r.noSecret(t)
}

// TestC05CredsetDeleteMissing sees a delete of an item that isn't there
// exit 0, with the store's rule in the decision.
func TestC05CredsetDeleteMissing(t *testing.T) {
	r := runWith(t, newFake(), []string{"delete", "github"}, nil, strings.NewReader(""))
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.log)
	}
	if d := r.decision(t); d.Decision != credset.DecisionAccept || d.Rule != string(storeitem.RuleNotFound) {
		t.Errorf("decision %+v", d)
	}
}

// TestC05CredsetDelete sees a delete remove the item.
func TestC05CredsetDelete(t *testing.T) {
	fake := newFake()
	group := storeitem.Group(team, storeitem.RoleBinding)
	fake.Write(t.Context(), group, "wraithbox/binding/github", []byte(secret))
	r := runWith(t, fake, []string{"delete", "github"}, nil, strings.NewReader(""))
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.log)
	}
	if d := r.decision(t); d.Rule != credset.RuleDone {
		t.Errorf("decision %+v", d)
	}
	if _, st := fake.Fetch(t.Context(), group, "wraithbox/binding/github"); st != secretstore.StatusNotFound {
		t.Errorf("after delete, status %v, want not found", st)
	}
}

// TestC05CredsetStoreErrors sees a locked store give store-locked, and
// -34018 on a write and a delete give store-write-failed, each with exit
// 1 (S15-02-secret-store, "Failed store calls").
func TestC05CredsetStoreErrors(t *testing.T) {
	item := "wraithbox/binding/github"
	for _, tc := range []struct {
		name   string
		args   []string
		access storeitem.Access
		item   string
		status secretstore.Status
		rule   storeitem.Rule
	}{
		{"locked write", []string{"write", "github"}, storeitem.AccessWrite, item, secretstore.StatusLocked, storeitem.RuleLocked},
		{"locked delete", []string{"delete", "github"}, storeitem.AccessDelete, item, secretstore.StatusLocked, storeitem.RuleLocked},
		{"locked list", []string{"list", "-"}, storeitem.AccessList, "", secretstore.StatusLocked, storeitem.RuleLocked},
		{"entitlement write", []string{"write", "github"}, storeitem.AccessWrite, item, secretstore.StatusMissingEntitlement, storeitem.RuleWriteFailed},
		{"entitlement delete", []string{"delete", "github"}, storeitem.AccessDelete, item, secretstore.StatusMissingEntitlement, storeitem.RuleWriteFailed},
		{"entitlement list", []string{"list", "-"}, storeitem.AccessList, "", secretstore.StatusMissingEntitlement, storeitem.RuleReadFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFake()
			fake.SetStatus(tc.access, tc.item, tc.status)
			r := runWith(t, fake, tc.args, nil, strings.NewReader(secret))
			if r.code != 1 {
				t.Errorf("exit %d, want 1", r.code)
			}
			d := r.decision(t)
			if d.Rule != string(tc.rule) || d.Status != int32(tc.status) {
				t.Errorf("decision %+v, want %s with status %d", d, tc.rule, tc.status)
			}
			r.noSecret(t)
		})
	}
}

// TestC05CredsetNeedsTeam sees a store opened without a team refuse the
// write with keychain-needs-team before any backend call
// (C05-credentials-and-store, "Builds without a team").
func TestC05CredsetNeedsTeam(t *testing.T) {
	fake := storetest.New("")
	r := runWith(t, fake, []string{"write", "github"}, nil, strings.NewReader(secret))
	if r.code != 1 {
		t.Errorf("exit %d, want 1", r.code)
	}
	if d := r.decision(t); d.Rule != string(storeitem.RuleNeedsTeam) {
		t.Errorf("decision %+v, want %s", d, storeitem.RuleNeedsTeam)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("calls %v, want none", calls)
	}
	r.noSecret(t)
}

// TestEnviron sees the environment wb starts wb-credset with be empty
// and not nil, whatever wb's own environment holds.
func TestEnviron(t *testing.T) {
	env := credset.Environ(func(string) (string, bool) { return secret, true })
	if env == nil || len(env) != 0 {
		t.Errorf("Environ = %#v, want an empty, non-nil list", env)
	}
}

// TestParseDecision sees the parser skip lines that aren't the decision,
// and a rule outside the rule form.
func TestParseDecision(t *testing.T) {
	in := `not json
{"msg":"secret store refused","rule":"keychain-needs-team"}
{"msg":"wb-credset decision","rule":"Bad Rule","decision":"refuse"}
{"msg":"wb-credset decision","rule":"store-locked","decision":"refuse","op":"write","binding":"github","status":-25308}
`
	d, ok := credset.ParseDecision([]byte(in))
	if !ok || d.Rule != "store-locked" || d.Status != -25308 || d.Binding != "github" {
		t.Errorf("ParseDecision = %+v, %v", d, ok)
	}
	if _, ok := credset.ParseDecision([]byte(`{"msg":"wb-credset decision","rule":"x y"}`)); ok {
		t.Error("a rule outside the form was accepted")
	}
}
