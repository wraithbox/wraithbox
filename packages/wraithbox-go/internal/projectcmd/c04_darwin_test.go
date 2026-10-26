package projectcmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

const (
	urlA     = "https://a.example/org/r.git"
	urlSSH   = "git@a.example:org/r.git"
	urlB     = "https://b.example/org/r.git"
	ruleURLs = string(project.RuleURLsChanged)
	ruleRoot = string(project.RuleRootCommitGone)
)

// C04-projects, "Remotes and root commits": a changed remote URL is
// refused with both URLs named. wb project confirm then records the new
// one, clears config_trust, and writes a 5019 record with both URL
// sets, and the next start passes.
func TestC04ChangedURL(t *testing.T) {
	h := newHarness(t)
	dir := newRepo(t, filepath.Join(h.root, "r"), "root", urlA)
	id := h.mustStart(dir).Project.GetId()
	h.trust(id)

	gitT(t, dir, "remote", "set-url", "origin", urlB)
	_, err := h.start(dir)
	wantRule(t, err, ruleURLs)
	wantRefused(t, h.wb(dir, "show"), ruleURLs, "recorded URL: "+urlA, "now URL: "+urlB, "wb project confirm")

	r := h.mustWB(dir, "confirm")
	for _, want := range []string{"recorded URL: " + urlB, "placement: work", "config_trust: off"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("confirm printed %q, want %q", r.stdout, want)
		}
	}
	if op := h.mustStart(dir); op.Project.GetId() != id || op.Project.GetConfigTrust() {
		t.Errorf("after confirm: %v", op.Project)
	}
	recs := h.records("wb project confirm")
	if len(recs) != 1 {
		t.Fatalf("confirm records %v, want one", recs)
	}
	if !slices.Contains(asStrings(recs[0].Before["urls"]), urlA) || !slices.Contains(asStrings(recs[0].After["urls"]), urlB) ||
		recs[0].Before["config_trust"] != true || recs[0].After["config_trust"] != false {
		t.Errorf("confirm record %+v, want both URL sets and config_trust cleared", recs[0])
	}
}

// C04-projects, "Remotes and root commits": a switch between a remote's
// SSH and HTTPS forms passes, and a second git worktree is the same
// project.
func TestC04SSHSwitchAndWorktree(t *testing.T) {
	h := newHarness(t)
	dir := newRepo(t, filepath.Join(h.root, "r"), "root", urlSSH)
	id := h.mustStart(dir).Project.GetId()
	gitT(t, dir, "remote", "set-url", "origin", urlA)
	if got := h.mustStart(dir).Project.GetId(); got != id {
		t.Errorf("after the switch to HTTPS: project %s, want %s", got, id)
	}
	wt := filepath.Join(h.root, "wt")
	gitT(t, dir, "worktree", "add", "-q", wt)
	if op := h.mustStart(wt); op.Project.GetId() != id || op.Registered {
		t.Errorf("second worktree: project %s registered %v, want %s", op.Project.GetId(), op.Registered, id)
	}
}

// C04-projects, "Remotes and root commits": a repository without a
// remote, replaced at its path by another without a remote, is refused
// with both root commit sets named.
func TestC04ReplacedWithoutRemote(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.root, "r")
	newRepo(t, dir, "first", "")
	before := gitT(t, dir, "rev-list", "--max-parents=0", "HEAD")
	h.mustStart(dir)

	replace(t, dir, "second")
	after := gitT(t, dir, "rev-list", "--max-parents=0", "HEAD")
	_, err := h.start(dir)
	wantRule(t, err, ruleRoot)
	wantRefused(t, h.wb(dir, "show"), ruleRoot, "recorded root commit: "+before, "now root commit: "+after)
}

// C04-projects, "Remotes and root commits": a repository registered
// with no remote and no commit records its root commit at the first
// start after a commit, with a 5019 record, and a replacement is then
// refused.
func TestC04GrowOnlyThenReplaced(t *testing.T) {
	h := newHarness(t)
	dir := newRepo(t, filepath.Join(h.root, "r"), "", "")
	op := h.mustStart(dir)
	if len(op.Project.GetIdentity().GetRootCommits()) != 0 {
		t.Fatalf("an empty repository recorded %v", op.Project.GetIdentity())
	}
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "first")
	root := gitT(t, dir, "rev-list", "--max-parents=0", "HEAD")
	if got := h.mustStart(dir).Project.GetIdentity().GetRootCommits(); !slices.Equal(got, []string{root}) {
		t.Errorf("grown identity %v, want %s", got, root)
	}
	if recs := h.records("wb claude"); len(recs) != 1 || !slices.Contains(asStrings(recs[0].After["root_commits"]), root) {
		t.Errorf("grow records %+v, want one with %s", recs, root)
	}
	replace(t, dir, "second")
	_, err := h.start(dir)
	wantRule(t, err, ruleRoot)
}

// C04-projects, "Moves and second clones": project A moved from /x to
// /y with wb project move keeps its id at /y, and a new clone at /x
// starts as a new project with a new id and none of A's settings.
func TestC04MoveThenNewClone(t *testing.T) {
	h := newHarness(t)
	x, y := filepath.Join(h.root, "x"), filepath.Join(h.root, "y")
	newRepo(t, x, "root", urlA)
	a := h.mustStart(x).Project.GetId()
	h.trust(a)
	if err := os.Rename(x, y); err != nil {
		t.Fatal(err)
	}
	r := h.mustWB(h.root, "move", y, "--project", a)
	if !strings.Contains(r.stdout, "location: "+filepath.Join(y, ".git")) {
		t.Errorf("move printed %q", r.stdout)
	}

	newRepo(t, x, "other", urlB)
	op := h.mustStart(x)
	if op.Project.GetId() == a || !op.Registered || op.Project.GetConfigTrust() {
		t.Errorf("new clone at /x: %v, registered %v; want a new project without config_trust", op.Project, op.Registered)
	}
	if row, _ := h.row(a); row.KeyPath != filepath.Join(y, ".git") || !row.ConfigTrust {
		t.Errorf("A after the new clone: %+v, want it at /y with its settings", row)
	}
}

// C04-projects, "Moves and second clones": a directory moved without wb
// project move is refused at the new path with the old project named
// and both commands written out. The move command it prints then works.
func TestC04MovedWithoutMove(t *testing.T) {
	h := newHarness(t)
	x, y := filepath.Join(h.root, "x"), filepath.Join(h.root, "y")
	newRepo(t, x, "root", urlA)
	a := h.mustStart(x).Project
	if err := os.Rename(x, y); err != nil {
		t.Fatal(err)
	}
	_, err := h.start(y)
	wantRule(t, err, string(project.RuleMatchMoved))
	r := h.wb(y, "place", "work")
	wantRefused(t, r, string(project.RuleMatchMoved), a.GetName(), a.GetId(),
		"wb project move "+y+" --project "+a.GetId(), "wb project rm --project "+a.GetId())
	if h.count() != 1 {
		t.Errorf("%d projects, want only A", h.count())
	}
	h.mustWB(h.root, "move", y, "--project", a.GetId())
	if got := h.mustStart(y).Project.GetId(); got != a.GetId() {
		t.Errorf("after the move: %s, want %s", got, a.GetId())
	}
}

// C04-projects, "Moves and second clones": a second clone while the
// first stays registers as its own project with a notice that names
// the first, and a second clone of an isolated project starts isolated
// without the first's config_trust.
func TestC04SecondClones(t *testing.T) {
	h := newHarness(t)
	first := newRepo(t, filepath.Join(h.root, "first"), "root", urlA)
	a := h.mustStart(first).Project
	second := newRepo(t, filepath.Join(h.root, "second"), "root", urlA)
	r := h.mustWB(second, "place", "work")
	if !strings.Contains(r.stderr, "the same repository is project "+a.GetName()+" ("+a.GetId()+")") {
		t.Errorf("notice %q doesn't name the first clone", r.stderr)
	}
	if h.count() != 2 {
		t.Errorf("%d projects, want 2", h.count())
	}

	iso := newRepo(t, filepath.Join(h.root, "iso"), "root", urlB)
	r = h.mustWB(iso, "place", "isolated")
	if !strings.Contains(r.stderr, "placement isolated") {
		t.Errorf("place isolated printed %q", r.stderr)
	}
	isoProject := h.mustStart(iso).Project
	h.trust(isoProject.GetId())
	clone := newRepo(t, filepath.Join(h.root, "iso2"), "root", urlB)
	op := h.mustStart(clone)
	if op.Project.GetPlacement() != hostdv1.Placement_PLACEMENT_ISOLATED || op.Project.GetConfigTrust() {
		t.Errorf("second clone of an isolated project: %v, want isolated without config_trust", op.Project)
	}
	if n := strings.Join(op.Notice(), "\n"); !strings.Contains(n, isoProject.GetId()) || !strings.Contains(n, "placement isolated") {
		t.Errorf("notice %q", n)
	}
	// wb project place work in a third clone registers it isolated too,
	// and says the request was overridden.
	third := newRepo(t, filepath.Join(h.root, "iso3"), "root", urlB)
	r = h.mustWB(third, "place", "work")
	if !strings.Contains(r.stderr, "you asked for placement work, but the project is isolated") {
		t.Errorf("place work in a clone of an isolated project printed %q", r.stderr)
	}
}

// C04-projects, "Moves and second clones": on macOS one repository
// reached as git/foo, Git/foo and through a symbolic link is one
// project.
func TestC04OnePathThreeWays(t *testing.T) {
	h := newHarness(t)
	foo := newRepo(t, filepath.Join(h.root, "git", "foo"), "root", urlA)
	id := h.mustStart(foo).Project.GetId()
	link := filepath.Join(h.root, "link")
	if err := os.Symlink(foo, link); err != nil {
		t.Fatal(err)
	}
	dirs := []string{link}
	upper := filepath.Join(h.root, "Git", "foo")
	if _, err := os.Stat(upper); err == nil {
		dirs = append(dirs, upper)
	} else {
		t.Logf("%s: the file system is case-sensitive, so only the link is checked", upper)
	}
	for _, d := range dirs {
		if op := h.mustStart(d); op.Project.GetId() != id || op.Registered {
			t.Errorf("%s: project %s registered %v, want %s", d, op.Project.GetId(), op.Registered, id)
		}
	}
}

// C04-projects, "Git directories that point elsewhere": wb project
// place in a directory whose .git file names project A's .git or a
// module in it, or whose .git is a link to A's, is refused with the
// reason, and nothing is registered.
func TestC04PlaceInPlantedGit(t *testing.T) {
	h := newHarness(t)
	a := newRepo(t, filepath.Join(h.root, "a"), "root", urlA)
	h.mustStart(a)
	mkdir(t, filepath.Join(a, ".git", "modules", "sub"))
	tests := []struct {
		name  string
		plant func(dir string)
		rule  project.Rule
	}{
		{"gitdir A", func(d string) {
			writeFile(t, filepath.Join(d, ".git"), "gitdir: "+filepath.Join(a, ".git")+"\n", 0o600)
		}, project.RuleGitFileElsewhere},
		{"gitdir module", func(d string) {
			writeFile(t, filepath.Join(d, ".git"), "gitdir: "+filepath.Join(a, ".git", "modules", "sub")+"\n", 0o600)
		}, ""},
		{"link", func(d string) {
			if err := os.Symlink(filepath.Join(a, ".git"), filepath.Join(d, ".git")); err != nil {
				t.Fatal(err)
			}
		}, project.RuleGitLink},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := mkdir(t, filepath.Join(h.root, "planted", strings.ReplaceAll(tc.name, " ", "-")))
			tc.plant(d)
			r := h.wb(d, "place", "isolated")
			if r.code != exitFailure || !strings.Contains(r.stderr, "wraith box: ") {
				t.Errorf("exit %d, stderr %q: want a refusal", r.code, r.stderr)
			}
			if tc.rule != "" && !strings.HasSuffix(strings.TrimSpace(r.stderr), "("+string(tc.rule)+")") {
				t.Errorf("stderr %q, want rule %s", r.stderr, tc.rule)
			}
			if h.count() != 1 {
				t.Errorf("%d projects after the refusal, want 1", h.count())
			}
		})
	}
}

// replace removes the repository at dir and makes another without a
// remote, whose root commit has message msg.
func replace(t *testing.T, dir, msg string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	newRepo(t, dir, msg, "")
}

func asStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, s := range list {
		str, _ := s.(string)
		out = append(out, str)
	}
	return out
}
