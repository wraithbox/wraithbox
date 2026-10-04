// Command x07 runs the X07 spike: the git round trip of
// S08-workspace-and-git over a socketpair standing in for vsock, the
// abuse cases, the timings, and the risky-path flagger on real diffs.
//
// Everything happens in throwaway repositories under a temp directory.
//
// Throwaway code. Not held to the project gates.
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"x07/flagger"
	"x07/host"
	"x07/pktfilter"
)

var (
	srcURL   = flag.String("src", "https://github.com/golang/go", "large repository to clone as the user's repository")
	cacheDir = flag.String("cache", filepath.Join(os.TempDir(), "x07-cache"), "where the large clone is kept between runs")
	hostGit  = flag.String("hostgit", "/opt/homebrew/bin/git", "git binary on the host side")
	guestGit = flag.String("guestgit", "/opt/homebrew/bin/git", "git binary on the guest side")
	behind   = flag.Int("behind", 200, "commits the first clone is behind, for the incremental fetch")
	keep     = flag.Bool("keep", false, "keep the run directory")
	phases   = flag.String("phases", "all", "comma list: clone,export,push,attack,raw,flag")
	vlog     = flag.Bool("v", false, "print host decisions")
)

const sid = "S1"

type env struct {
	root, bin, home, ghome string
	user                   string // user repo (non-bare, git dir user/.git)
	logbuf                 *bytes.Buffer
	hlog                   *log.Logger
}

func main() {
	bothGit := flag.String("bothgit", "", "git binary for both sides")
	flag.Parse()
	if *bothGit != "" {
		*hostGit, *guestGit = *bothGit, *bothGit
	}
	root, err := os.MkdirTemp("", "x07-run-")
	must(err)
	if !*keep {
		defer os.RemoveAll(root)
	}
	e := &env{root: root, bin: filepath.Join(root, "bin"), home: filepath.Join(root, "hosthome"), ghome: filepath.Join(root, "guesthome")}
	for _, d := range []string{e.bin, e.home, e.ghome} {
		must(os.MkdirAll(d, 0o755))
	}
	must(os.WriteFile(filepath.Join(e.ghome, ".gitconfig"), []byte("[user]\n\tname = guest\n\temail = guest@example.invalid\n[init]\n\tdefaultBranch = main\n"), 0o644))
	e.logbuf = &bytes.Buffer{}
	var w io.Writer = e.logbuf
	if *vlog {
		w = io.MultiWriter(e.logbuf, os.Stderr)
	}
	e.hlog = log.New(w, "host ", 0)

	out, err := exec.Command("go", "build", "-o", filepath.Join(e.bin, "git-remote-wb"), "./cmd/git-remote-wb").CombinedOutput()
	if err != nil {
		log.Fatalf("build helper: %v\n%s", err, out)
	}
	hv, _ := exec.Command(*hostGit, "version").Output()
	gv, _ := exec.Command(*guestGit, "version").Output()
	fmt.Printf("# X07 run %s\nhost git: %sguest git: %s\n", time.Now().Format(time.RFC3339), hv, gv)

	ph := map[string]bool{}
	for _, p := range strings.Split(*phases, ",") {
		ph[p] = true
	}
	all := ph["all"]
	e.setupUser()
	if all || ph["clone"] || ph["push"] || ph["attack"] {
		e.phaseClone()
	}
	if all || ph["export"] || ph["push"] || ph["attack"] {
		e.phaseExport()
	}
	if all || ph["push"] || ph["attack"] {
		e.phasePush()
	}
	if all || ph["attack"] {
		e.phaseAttack()
	}
	if all || ph["raw"] {
		e.phaseRaw()
	}
	if all || ph["flag"] {
		e.phaseFlag()
	}
	fmt.Printf("\n## Host decision log (last 60 lines)\n```\n%s```\n", tail(e.logbuf.String(), 60))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func tail(s string, n int) string {
	l := strings.Split(s, "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

// hgit runs host-side setup git with an isolated configuration.
func (e *env) hgit(dir string, args ...string) string {
	out, err := e.hgitErr(dir, args...)
	if err != nil {
		log.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return out
}

func (e *env) hgitErr(dir string, args ...string) (string, error) {
	cmd := exec.Command(*hostGit, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + e.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=x07", "GIT_AUTHOR_EMAIL=x07@example.invalid", "GIT_COMMITTER_NAME=x07", "GIT_COMMITTER_EMAIL=x07@example.invalid", "LC_ALL=C"}
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

func (e *env) ggitEnv() []string {
	return []string{"PATH=" + e.bin + ":/usr/bin:/bin", "HOME=" + e.ghome, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(e.ghome, ".gitconfig"), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0"}
}

// ggit runs guest-side git with no host connection.
func (e *env) ggit(dir string, args ...string) (string, error) {
	cmd := exec.Command(*guestGit, args...)
	cmd.Dir = dir
	cmd.Env = e.ggitEnv()
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

// socketpair returns the host end as a conn and the guest end as a file.
func socketpair() (host.Conn, *os.File) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	must(err)
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	hf := os.NewFile(uintptr(fds[0]), "host")
	c, err := net.FileConn(hf)
	must(err)
	hf.Close()
	return c.(*net.UnixConn), os.NewFile(uintptr(fds[1]), "guest")
}

type result struct {
	out string
	dur time.Duration
	err error
	herr error
}

// tunnel runs one guest git command whose remote helper reaches the host
// through a fresh socketpair, served by host.Serve.
func (e *env) tunnel(cfg *host.Config, dir string, args ...string) result {
	hc, gf := socketpair()
	done := make(chan error, 1)
	go func() { done <- host.Serve(hc, cfg) }()
	cmd := exec.Command(*guestGit, args...)
	cmd.Dir = dir
	cmd.Env = append(e.ggitEnv(), "WB_FD=3")
	cmd.ExtraFiles = []*os.File{gf}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	t0 := time.Now()
	err := cmd.Start()
	gf.Close()
	if err == nil {
		err = cmd.Wait()
	}
	d := time.Since(t0)
	var herr error
	select {
	case herr = <-done:
	case <-time.After(10 * time.Second):
		herr = fmt.Errorf("host did not finish")
		hc.Close()
	}
	return result{out: strings.TrimSpace(buf.String()), dur: d, err: err, herr: herr}
}

func (e *env) cfg(serve, landing string, adv ...string) *host.Config {
	return &host.Config{Git: *hostGit, ServeRepo: serve, Landing: landing, SessionID: sid, Advertise: adv,
		MaxInput: 64 << 20, Home: e.home, Log: e.hlog}
}

var recvRe = regexp.MustCompile(`Receiving objects: 100% \(([0-9]+)/[0-9]+\), ([0-9.]+ [KMG]iB)`)
var writeRe = regexp.MustCompile(`Writing objects: 100% \(([0-9]+)/[0-9]+\), ([0-9.]+ [KMG]iB|[0-9]+ bytes)`)

func xfer(out string) string {
	if m := recvRe.FindAllStringSubmatch(out, -1); m != nil {
		x := m[len(m)-1]
		return x[1] + " objects, " + x[2]
	}
	if m := writeRe.FindAllStringSubmatch(out, -1); m != nil {
		x := m[len(m)-1]
		return x[1] + " objects, " + x[2]
	}
	return "-"
}

func secs(d time.Duration) string { return fmt.Sprintf("%.2f s", d.Seconds()) }

// --- user repository ---

var oldC, newC, secretC string

func (e *env) setupUser() {
	must(os.MkdirAll(*cacheDir, 0o755))
	e.user = filepath.Join(*cacheDir, "user")
	if _, err := os.Stat(filepath.Join(e.user, ".git")); err != nil {
		t0 := time.Now()
		e.hgit(*cacheDir, "clone", "--quiet", *srcURL, "user")
		fmt.Printf("\nhost's own clone of %s: %s (network, not part of the round trip)\n", *srcURL, secs(time.Since(t0)))
	}
	newC = e.hgit(e.user, "rev-parse", "refs/remotes/origin/HEAD")
	oldC = e.hgit(e.user, "rev-parse", fmt.Sprintf("%s~%d", newC, *behind))
	e.hgit(e.user, "update-ref", "refs/heads/x07-main", oldC)
	e.hgit(e.user, "symbolic-ref", "HEAD", "refs/heads/x07-main")
	// A local branch the user never meant to share, plus a note.
	blob := strings.TrimSpace(e.hgitIn(e.user, "SECRET-TOKEN-x07\n", "hash-object", "-w", "--stdin"))
	tree := strings.TrimSpace(e.hgitIn(e.user, "100644 blob "+blob+"\tsecret.txt\n", "mktree"))
	secretC = e.hgit(e.user, "commit-tree", tree, "-p", oldC, "-m", "local secret")
	e.hgit(e.user, "update-ref", "refs/heads/secret", secretC)
	e.hgit(e.user, "update-ref", "refs/notes/commits", secretC)
	n := e.hgit(e.user, "for-each-ref", "--format=x")
	cnt := e.hgit(e.user, "count-objects", "-vH")
	fmt.Printf("\nuser repository: %d refs; first clone at %s (tip~%d), tip %s\n%s\n", strings.Count(n, "x"), oldC[:12], *behind, newC[:12], indent(cnt))
}

func (e *env) hgitIn(dir, stdin string, args ...string) string {
	cmd := exec.Command(*hostGit, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + e.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=x07", "GIT_AUTHOR_EMAIL=x07@example.invalid", "GIT_COMMITTER_NAME=x07", "GIT_COMMITTER_EMAIL=x07@example.invalid"}
	cmd.Stdin = strings.NewReader(stdin)
	b, err := cmd.Output()
	if err != nil {
		log.Fatalf("git %v: %v", args, err)
	}
	return string(b)
}

func indent(s string) string { return "    " + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n    ") }

// --- phase: clone and fetch straight from the user's repository ---

var guestRepo string

func (e *env) phaseClone() {
	fmt.Printf("\n## Clone and fetch, upload-pack on the user's repository (direct)\n\n")
	e.hgit(e.user, "update-ref", "refs/heads/x07-main", oldC)
	cfg := e.cfg(filepath.Join(e.user, ".git"), "", "refs/heads/x07-main")

	// Baseline: plain local clone through upload-pack, no tunnel.
	t0 := time.Now()
	out, err := e.ggit(e.root, "clone", "--progress", "--no-local", "--single-branch", "-b", "x07-main", filepath.Join(e.user, ".git"), "baseline")
	fmt.Printf("| baseline `git clone --no-local --single-branch` (no tunnel) | %s | %s | err=%v |\n", secs(time.Since(t0)), xfer(out), err)

	for _, v := range []string{"2", "0"} {
		r := e.tunnel(cfg, e.root, "-c", "protocol.version="+v, "ls-remote", "wb::project")
		fmt.Printf("\nls-remote through the tunnel, protocol v%s (err=%v):\n%s\n", v, r.err, indent(r.out))
	}
	r := e.tunnel(cfg, e.root, "clone", "--progress", "wb::project", "guest")
	fmt.Printf("\n| first clone through the tunnel (v2) | %s | %s | err=%v host=%v |\n", secs(r.dur), xfer(r.out), r.err, r.herr)
	guestRepo = filepath.Join(e.root, "guest")
	if r.err != nil {
		fmt.Println(indent(r.out))
		log.Fatal("clone failed")
	}
	r0 := e.tunnel(cfg, e.root, "-c", "protocol.version=0", "clone", "--progress", "wb::project", "guest-v0")
	fmt.Printf("| first clone through the tunnel (v0) | %s | %s | err=%v |\n", secs(r0.dur), xfer(r0.out), r0.err)
	os.RemoveAll(filepath.Join(e.root, "guest-v0"))
	os.RemoveAll(filepath.Join(e.root, "baseline"))

	e.hgit(e.user, "update-ref", "refs/heads/x07-main", newC)
	r = e.tunnel(cfg, guestRepo, "fetch", "--progress", "origin")
	fmt.Printf("| incremental fetch, %d commits (v2) | %s | %s | err=%v |\n", *behind, secs(r.dur), xfer(r.out), r.err)
	r = e.tunnel(cfg, guestRepo, "fetch", "--progress", "origin")
	fmt.Printf("| no-op fetch (v2) | %s | %s | err=%v |\n", secs(r.dur), xfer(r.out), r.err)
	_, _ = e.ggit(guestRepo, "merge", "--ff-only", "-q", "origin/x07-main")

	// A commit only on a release branch, reachable from no advertised ref.
	rel := e.hgit(e.user, "for-each-ref", "--count=1", "--sort=-committerdate", "--format=%(objectname)", "refs/remotes/origin/release-branch.*")
	// A blob no ref reaches at all.
	dangling := strings.TrimSpace(e.hgitIn(e.user, fmt.Sprintf("DANGLING-x07 %d\n", time.Now().UnixNano()), "hash-object", "-w", "--stdin"))

	// Each case runs in a fresh copy of the guest clone, so an object
	// fetched by one case cannot make a later one look successful.
	hidden := func(title string, cfg *host.Config) {
		fmt.Printf("\n%s (each must fail):\n\n", title)
		for _, a := range []struct{ name, v, ref string }{
			{"want the secret branch's commit by id", "0", secretC},
			{"want the secret branch's commit by id", "2", secretC},
			{"want a release-branch commit not reachable from the advertised ref", "0", rel},
			{"want a release-branch commit not reachable from the advertised ref", "2", rel},
			{"want a dangling blob no ref reaches", "0", dangling},
			{"want a dangling blob no ref reaches", "2", dangling},
			{"fetch refs/heads/secret by name", "2", "refs/heads/secret"},
			{"fetch refs/notes/commits by name", "2", "refs/notes/commits"},
		} {
			dir := filepath.Join(e.root, "probe")
			os.RemoveAll(dir)
			_, _ = e.ggit(e.root, "clone", "-q", "--shared", "--no-checkout", guestRepo, dir)
			_, _ = e.ggit(dir, "remote", "set-url", "origin", "wb::project")
			r := e.tunnel(cfg, dir, "-c", "protocol.version="+a.v, "fetch", "origin", a.ref)
			if r.err == nil && a.ref != "" && !strings.HasPrefix(a.ref, "refs/") {
				if _, err := e.ggit(dir, "cat-file", "-e", a.ref); err != nil {
					r.err = fmt.Errorf("fetch said ok but object absent")
				}
			}
			fmt.Printf("- %s (client v%s): %s\n", a.name, a.v, verdict(r.err == nil, firstLine(r.out)))
			os.RemoveAll(dir)
		}
	}
	hidden("Hidden data, direct mode, the guest's protocol version honored", cfg)
	v0 := *cfg
	v0.ForceV0 = true
	hidden("Hidden data, direct mode, host forces protocol v0", &v0)
	r = e.tunnel(&v0, e.root, "clone", "--progress", "wb::project", "guest-forced-v0")
	fmt.Printf("\n| first clone, host forces v0 | %s | %s | err=%v |\n", secs(r.dur), xfer(r.out), r.err)
	os.RemoveAll(filepath.Join(e.root, "guest-forced-v0"))
	hiddenCfg = cfg
	hiddenFn = hidden
}

var (
	hiddenCfg *host.Config
	hiddenFn  func(string, *host.Config)
)

func guestHas(e *env, oid string) string {
	out, err := e.ggit(guestRepo, "cat-file", "-t", oid)
	if err != nil {
		return "missing"
	}
	return out
}

func verdict(succeeded bool, msg string) string {
	if succeeded {
		return "**SUCCEEDED (leak/bypass)**: " + msg
	}
	return "refused: `" + msg + "`"
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "remote:") || strings.HasPrefix(l, "!") {
			return l
		}
	}
	l := strings.Split(strings.TrimSpace(s), "\n")
	return l[len(l)-1]
}

// --- phase: export repository holding only the advertised refs ---

var export string

func (e *env) phaseExport() {
	fmt.Printf("\n## Export repository (only the advertised branch's objects)\n\n")
	export = filepath.Join(e.root, "export.git")
	e.hgit(e.root, "init", "-q", "--bare", export)
	e.hgit(e.user, "update-ref", "refs/heads/x07-main", oldC)
	t0 := time.Now()
	e.hgit(export, "fetch", "-q", "--no-tags", filepath.Join(e.user, ".git"), "+refs/heads/x07-main:refs/heads/x07-main")
	fmt.Printf("| build export.git from the user's repository | %s |\n", secs(time.Since(t0)))
	e.hgit(export, "symbolic-ref", "HEAD", "refs/heads/x07-main")
	cfg := e.cfg(export, "", "refs/heads/x07-main")
	r := e.tunnel(cfg, e.root, "clone", "--progress", "wb::project", "guest-export")
	fmt.Printf("| first clone from export.git (v2) | %s | %s | err=%v |\n", secs(r.dur), xfer(r.out), r.err)
	e.hgit(e.user, "update-ref", "refs/heads/x07-main", newC)
	t0 = time.Now()
	e.hgit(export, "fetch", "-q", "--no-tags", filepath.Join(e.user, ".git"), "+refs/heads/x07-main:refs/heads/x07-main")
	fmt.Printf("| update export.git, %d commits | %s |\n", *behind, secs(time.Since(t0)))
	r = e.tunnel(cfg, filepath.Join(e.root, "guest-export"), "fetch", "--progress", "origin")
	fmt.Printf("| incremental fetch from export.git (v2) | %s | %s | err=%v |\n", secs(r.dur), xfer(r.out), r.err)
	os.RemoveAll(filepath.Join(e.root, "guest-export"))
	if hiddenFn != nil {
		hiddenFn("Hidden data, export.git, the guest's protocol version honored", cfg)
	}
	du, _ := exec.Command("du", "-sh", export).Output()
	fmt.Printf("- export.git size: %s", du)
}

// --- phase: push into the landing repository ---

var landing string

func (e *env) phasePush() {
	fmt.Printf("\n## Push into the landing repository\n\n")
	work := func(n int, tag string) {
		for i := 0; i < n; i++ {
			p := filepath.Join(guestRepo, "x07", fmt.Sprintf("%s-%d.txt", tag, i))
			must(os.MkdirAll(filepath.Dir(p), 0o755))
			must(os.WriteFile(p, []byte(fmt.Sprintf("change %s %d\n", tag, i)), 0o644))
			out, err := e.ggit(guestRepo, "add", "-A")
			if err == nil {
				out, err = e.ggit(guestRepo, "commit", "-q", "-m", "x07 "+tag)
			}
			if err != nil {
				log.Fatalf("commit: %v %s", err, out)
			}
		}
	}
	_, _ = e.ggit(guestRepo, "checkout", "-q", "-b", "work")
	work(3, "a")

	// Landing without alternates: the guest must send the whole history.
	plain := filepath.Join(e.root, "landing-plain.git")
	e.hgit(e.root, "init", "-q", "--bare", plain)
	cfg := e.cfg("", plain)
	cfg.MaxInput = 64 << 20
	r := e.tunnel(cfg, guestRepo, "push", "--progress", "wb::project", "HEAD:refs/heads/wb/"+sid+"/work")
	fmt.Printf("| push to an empty landing repository, 64 MiB limit | %s | %s | %s |\n", secs(r.dur), xfer(r.out), verdict(r.err == nil, firstLine(r.out)))
	cfg.MaxInput = 4 << 30
	r = e.tunnel(cfg, guestRepo, "push", "--progress", "wb::project", "HEAD:refs/heads/wb/"+sid+"/work")
	fmt.Printf("| push to an empty landing repository, 4 GiB limit | %s | %s | err=%v |\n", secs(r.dur), xfer(r.out), r.err)
	os.RemoveAll(plain)

	// Landing with alternates pointing at export.git.
	landing = filepath.Join(e.root, "landing.git")
	e.hgit(e.root, "init", "-q", "--bare", landing)
	must(os.WriteFile(filepath.Join(landing, "objects", "info", "alternates"), []byte(filepath.Join(export, "objects")+"\n"), 0o644))
	// receive-pack advertises the alternate's refs as ".have" lines only
	// if it can see them, so keep export.git's refs readable.
	cfg = e.cfg("", landing)
	r = e.tunnel(cfg, guestRepo, "push", "--progress", "wb::project", "HEAD:refs/heads/wb/"+sid+"/work")
	fmt.Printf("| push 3 commits, landing has export.git as alternate | %s | %s | err=%v host=%v |\n", secs(r.dur), xfer(r.out), r.err, r.herr)
	if r.err != nil {
		fmt.Println(indent(r.out))
	}
	work(5, "b")
	r = e.tunnel(cfg, guestRepo, "push", "--progress", "wb::project", "HEAD:refs/heads/wb/"+sid+"/work")
	fmt.Printf("| incremental push, 5 more commits | %s | %s | err=%v |\n", secs(r.dur), xfer(r.out), r.err)
	// A 20 MiB incompressible file.
	big := make([]byte, 20<<20)
	_, _ = rand.Read(big)
	must(os.WriteFile(filepath.Join(guestRepo, "x07", "big.bin"), big, 0o644))
	_, _ = e.ggit(guestRepo, "add", "-A")
	_, _ = e.ggit(guestRepo, "commit", "-q", "-m", "big")
	r = e.tunnel(cfg, guestRepo, "push", "--progress", "wb::project", "HEAD:refs/heads/wb/"+sid+"/work")
	fmt.Printf("| push a 20 MiB random file | %s | %s | err=%v |\n", secs(r.dur), xfer(r.out), r.err)

	// wb land: the user's git fetches from the landing repository.
	t0 := time.Now()
	out, err := e.hgitErr(e.user, "fetch", landing, "+refs/heads/wb/"+sid+"/*:refs/heads/wb/"+sid+"/*")
	fmt.Printf("| `wb land`: user's git fetches refs/heads/wb/%s/* from landing | %s | | err=%v %s |\n", sid, secs(time.Since(t0)), err, firstLine(out))
	got := e.hgit(e.user, "for-each-ref", "--format=%(refname)", "refs/heads/wb/")
	fmt.Printf("\nrefs in the user's repository after landing: %s; HEAD still %s\n", strings.ReplaceAll(got, "\n", ", "), e.hgit(e.user, "symbolic-ref", "HEAD"))
	e.hgit(e.user, "update-ref", "-d", "refs/heads/wb/"+sid+"/work")
}

// --- phase: push attacks through a real git client ---

func (e *env) landingState() string {
	refs, _ := e.hgitErr(landing, "for-each-ref", "--format=%(refname) %(objectname)")
	h := sha256.New()
	n := 0
	_ = filepath.WalkDir(filepath.Join(landing, "objects"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
			io.WriteString(h, p)
		}
		return nil
	})
	return fmt.Sprintf("%s|%d|%x", refs, n, h.Sum(nil)[:6])
}

func (e *env) userState() string {
	h := sha256.New()
	_ = filepath.WalkDir(filepath.Join(e.user, ".git"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				fmt.Fprintf(h, "%s %d %d\n", p, fi.Size(), fi.ModTime().UnixNano())
			}
		}
		return nil
	})
	return fmt.Sprintf("%x", h.Sum(nil)[:8])
}

func (e *env) phaseAttack() {
	e.attack(false)
	e.attack(true)
}

func (e *env) attack(noFilter bool) {
	mode := "pktfilter in front of receive-pack"
	if noFilter {
		mode = "receive.hideRefs alone, no filter"
	}
	fmt.Printf("\n## Push abuse through a real git client, %s (each must be refused, landing unchanged)\n\n", mode)
	cfg := e.cfg("", landing)
	cfg.MaxInput = 16 << 20
	cfg.NoFilter = noFilter
	{
		// The bare session ref, into a fresh landing repository where no
		// refs/heads/wb/S1/* exists to block it.
		fresh := filepath.Join(e.root, "landing-fresh.git")
		e.hgit(e.root, "init", "-q", "--bare", fresh)
		must(os.WriteFile(filepath.Join(fresh, "objects", "info", "alternates"), []byte(filepath.Join(export, "objects")+"\n"), 0o644))
		fc := *cfg
		fc.Landing = fresh
		r := e.tunnel(&fc, guestRepo, "push", "wb::project", "refs/remotes/origin/x07-main:refs/heads/wb/"+sid)
		refs, _ := e.hgitErr(fresh, "for-each-ref", "--format=%(refname)")
		fmt.Printf("- push to refs/heads/wb/%s itself, fresh landing: %s; landing refs now: %q\n", sid, verdict(r.err == nil, firstLine(r.out)), refs)
		os.RemoveAll(fresh)
	}
	before := e.landingState()
	try := func(name string, args ...string) {
		r := e.tunnel(cfg, guestRepo, append([]string{"push", "wb::project"}, args...)...)
		after := e.landingState()
		ch := "landing unchanged"
		if after != before {
			ch = "**landing CHANGED**"
		}
		fmt.Printf("- %s: %s; %s\n", name, verdict(r.err == nil, firstLine(r.out)), ch)
		if r.err == nil || *vlog {
			fmt.Println(indent(r.out))
		}
		before = after
	}
	try("push to refs/heads/main", "HEAD:refs/heads/main")
	try("push a tag", "HEAD:refs/tags/x07")
	try("push into another session", "HEAD:refs/heads/wb/S2/work")
	try("push to the session prefix itself", "HEAD:refs/heads/wb/"+sid)
	try("lookalike session S10", "HEAD:refs/heads/wb/S10/work")
	try("delete the session branch", ":refs/heads/wb/"+sid+"/work")
	try("one allowed and one forbidden ref", "HEAD:refs/heads/wb/"+sid+"/ok", "HEAD:refs/heads/main")
	try("push option", "--push-option=x", "HEAD:refs/heads/wb/"+sid+"/opt")
	try("over the 16 MiB size limit (20 MiB file already pushed, so add 20 MiB more)", e.bigCommit()+":refs/heads/wb/"+sid+"/big2")

	// Malformed objects, built with --literally so the guest's git does
	// not stop them, pushed to an allowed ref.
	for _, m := range e.badObjects() {
		try("malformed object: "+m.name, m.commit+":refs/heads/wb/"+sid+"/"+m.ref)
	}
}

func (e *env) bigCommit() string {
	big := make([]byte, 20<<20)
	_, _ = rand.Read(big)
	blob := e.ggitIn(guestRepo, string(big), "hash-object", "-w", "--stdin")
	tree := e.ggitIn(guestRepo, "100644 blob "+blob+"\tbig2.bin\n", "mktree")
	c, _ := e.ggit(guestRepo, "commit-tree", tree, "-p", "HEAD", "-m", "big2")
	return c
}

func (e *env) ggitIn(dir, stdin string, args ...string) string {
	cmd := exec.Command(*guestGit, args...)
	cmd.Dir = dir
	cmd.Env = e.ggitEnv()
	cmd.Stdin = strings.NewReader(stdin)
	b, err := cmd.Output()
	if err != nil {
		log.Fatalf("guest git %v: %v", args, err)
	}
	return strings.TrimSpace(string(b))
}

type bad struct{ name, commit, ref string }

func hexToBin(h string) string {
	var b []byte
	for i := 0; i+1 < len(h); i += 2 {
		var x byte
		fmt.Sscanf(h[i:i+2], "%02x", &x)
		b = append(b, x)
	}
	return string(b)
}

func (e *env) badObjects() []bad {
	head, _ := e.ggit(guestRepo, "rev-parse", "HEAD")
	blob := e.ggitIn(guestRepo, "#!/bin/sh\necho pwned\n", "hash-object", "-w", "--stdin")
	sub := e.ggitIn(guestRepo, "100755 blob "+blob+"\tpost-checkout\n", "mktree")
	hooks := e.ggitIn(guestRepo, "040000 tree "+sub+"\thooks\n", "mktree")
	var out []bad
	mk := func(name, ref, treeRaw string) {
		t := e.ggitIn(guestRepo, treeRaw, "hash-object", "-t", "tree", "--literally", "-w", "--stdin")
		c, _ := e.ggit(guestRepo, "commit-tree", t, "-p", head, "-m", name)
		out = append(out, bad{name, c, ref})
	}
	// tree entry format: "<mode> <name>\0<20-byte id>"
	mk("tree with a .git directory holding hooks", "dotgit", "40000 .git\x00"+hexToBin(hooks))
	mk("tree with a .GIT directory (case-insensitive file systems)", "dotgitcase", "40000 .GIT\x00"+hexToBin(hooks))
	mk("tree with a git~1 directory (NTFS short name)", "gitshort", "40000 git~1\x00"+hexToBin(hooks))
	mk("tree with a .gitmodules symlink", "gmsym", "120000 .gitmodules\x00"+hexToBin(blob))
	mk("tree with a .gitattributes symlink", "gasym", "120000 .gitattributes\x00"+hexToBin(blob))
	mk("tree with a bad mode", "mode", "100666 x\x00"+hexToBin(blob))
	mk("tree with a ../ path", "dotdot", "100644 ..\x00"+hexToBin(blob))
	tree, _ := e.ggit(guestRepo, "rev-parse", "HEAD^{tree}")
	c := e.ggitIn(guestRepo, "tree "+tree+"\nparent "+head+"\nauthor x <x> notatime +0000\ncommitter x <x> 1 +0000\n\nbad date\n", "hash-object", "-t", "commit", "--literally", "-w", "--stdin")
	out = append(out, bad{"commit with a malformed author date", c, "baddate"})
	gm := e.ggitIn(guestRepo, "[submodule \"x\"]\n\tpath = x\n\turl = --upload-pack=touch pwned\n", "hash-object", "-w", "--stdin")
	t := e.ggitIn(guestRepo, "100644 blob "+gm+"\t.gitmodules\n", "mktree")
	c2, _ := e.ggit(guestRepo, "commit-tree", t, "-p", head, "-m", "gitmodules url option")
	out = append(out, bad{".gitmodules with a URL that starts with a dash (CVE-2018-17456 class)", c2, "gmurl"})
	return out
}

// --- phase: raw protocol abuse, no git client ---

func (e *env) raw(cfg *host.Config, send func(w host.Conn, r *bufio.Reader) string) (string, error) {
	hc, gf := socketpair()
	gc, err := net.FileConn(gf)
	must(err)
	gf.Close()
	g := gc.(*net.UnixConn)
	done := make(chan error, 1)
	go func() { done <- host.Serve(hc, cfg) }()
	res := send(g, bufio.NewReader(g))
	g.Close()
	var herr error
	select {
	case herr = <-done:
	case <-time.After(10 * time.Second):
		herr = fmt.Errorf("host still running after 10 s")
	}
	return res, herr
}

func readAll(r *bufio.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 1<<16))
	s := string(b)
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return fmt.Sprintf("%q", s)
}

// readAdv reads pkt-lines until the first flush.
func readAdv(r *bufio.Reader) []string {
	var l []string
	for {
		p, flush, _, err := pktfilter.ReadPkt(r)
		if err != nil || flush {
			return l
		}
		l = append(l, string(p))
	}
}

func (e *env) phaseRaw() {
	fmt.Printf("\n## Raw protocol abuse (no git client; each must be refused)\n\n")
	if landing == "" {
		landing = filepath.Join(e.root, "landing.git")
		e.hgit(e.root, "init", "-q", "--bare", landing)
	}
	userBefore := e.userState()
	landBefore := e.landingState()
	cfg := e.cfg(filepath.Join(e.user, ".git"), landing, "refs/heads/x07-main")
	z := strings.Repeat("0", 40)
	head := e.hgit(e.user, "rev-parse", "refs/heads/x07-main")
	report := func(name string, res string, herr error) {
		fmt.Printf("- %s: host err=`%v`; guest saw %s\n", name, herr, res)
	}
	hdr := func(s string) []byte { return pktfilter.Pkt(s) }

	for _, h := range []string{
		"wb-connect /bin/sh -\n",
		"wb-connect git-upload-archive -\n",
		"wb-connect git-upload-pack version=2:x\n",
		"wb-connect git-upload-pack --upload-pack=touch -\n",
		"not a header",
	} {
		res, herr := e.raw(cfg, func(w host.Conn, r *bufio.Reader) string {
			w.Write(hdr(h))
			return readAll(r)
		})
		report(fmt.Sprintf("header %q", h), res, herr)
	}
	res, herr := e.raw(cfg, func(w host.Conn, r *bufio.Reader) string {
		w.Write([]byte("ffff"))
		return readAll(r)
	})
	report("oversized header pkt", res, herr)

	// Write to the user's repository over upload-pack: send a push
	// command list and a pack where upload-pack expects wants.
	res, herr = e.raw(cfg, func(w host.Conn, r *bufio.Reader) string {
		w.Write(hdr("wb-connect git-upload-pack -\n"))
		_, _, _, _ = pktfilter.ReadPkt(r) // ok
		readAdv(r) // advertisement
		w.Write(pktfilter.Pkt(head + " " + strings.Repeat("1", 40) + " refs/heads/x07-main\x00report-status\n"))
		w.Write([]byte("0000PACK\x00\x00\x00\x02\x00\x00\x00\x00"))
		w.CloseWrite()
		return readAll(r)
	})
	report("push commands sent to upload-pack", res, herr)

	// v2: object-info for the secret blob, and fetch by id.
	secretBlob := e.hgit(e.user, "rev-parse", secretC+":secret.txt")
	for _, cmd := range []string{"object-info", "fetch"} {
		res, herr = e.raw(cfg, func(w host.Conn, r *bufio.Reader) string {
			w.Write(hdr("wb-connect git-upload-pack version=2\n"))
			_, _, _, _ = pktfilter.ReadPkt(r)
			caps := readAdv(r)
			w.Write(pktfilter.Pkt("command=" + cmd + "\n"))
			w.Write([]byte("0001"))
			if cmd == "object-info" {
				w.Write(pktfilter.Pkt("size\n"))
				w.Write(pktfilter.Pkt("oid " + secretBlob + "\n"))
			} else {
				w.Write(pktfilter.Pkt("want " + secretC + "\n"))
				w.Write(pktfilter.Pkt("done\n"))
			}
			w.Write([]byte("0000"))
			w.CloseWrite()
			return fmt.Sprintf("caps=%q reply=%s", caps, readAll(r))
		})
		report("v2 "+cmd+" for the secret object", res, herr)
	}

	// receive-pack: framing garbage, and packs that pass the filter.
	recv := func(name string, body []byte) {
		res, herr := e.raw(cfg, func(w host.Conn, r *bufio.Reader) string {
			w.Write(hdr("wb-connect git-receive-pack -\n"))
			_, _, _, _ = pktfilter.ReadPkt(r)
			readAdv(r)
			w.Write(body)
			w.CloseWrite()
			return readAll(r)
		})
		a := e.landingState()
		ch := "landing unchanged"
		if a != landBefore {
			ch = "**landing CHANGED**"
		}
		landBefore = a
		report(name+"; "+ch, res, herr)
	}
	cmdOID := func(ref, oid string) []byte {
		return append(pktfilter.Pkt(z+" "+oid+" "+ref+"\x00report-status side-band-64k\n"), "0000"...)
	}
	cmd := func(ref string) []byte { return cmdOID(ref, head) }
	pack := e.realPack()
	flip := append([]byte(nil), pack...)
	flip[len(flip)/2] ^= 0xff
	for _, nf := range []bool{false, true} {
		cfg.NoFilter = nf
		fmt.Printf("\nreceive-pack, NoFilter=%v:\n\n", nf)
		recv("bad pkt length", []byte("zzzz"))
		recv("command list never ends (EOF)", pktfilter.Pkt(z+" "+head+" refs/heads/wb/"+sid+"/raw\x00report-status\n"))
		recv("ref with a NUL in a later command", append(append(pktfilter.Pkt(z+" "+head+" refs/heads/wb/"+sid+"/a\x00report-status\n"), pktfilter.Pkt(z+" "+head+" refs/heads/wb/"+sid+"/b\x00x\n")...), "0000"...))
		recv("forbidden ref refs/heads/main", cmd("refs/heads/main"))
		recv("forbidden ref refs/heads/wb/"+sid+" (the prefix itself)", cmd("refs/heads/wb/"+sid))
		recv("allowed ref, object that does not exist, no pack", cmdOID("refs/heads/wb/"+sid+"/raw1", strings.Repeat("ab", 20)))
		recv("allowed ref, garbage instead of a pack", append(cmd("refs/heads/wb/"+sid+"/raw2"), "PACKgarbagegarbage"...))
		recv("allowed ref, pack header claiming 4 billion objects", append(cmd("refs/heads/wb/"+sid+"/raw3"), "PACK\x00\x00\x00\x02\xff\xff\xff\xff"...))
		recv(fmt.Sprintf("allowed ref, real pack truncated to half (%d of %d bytes)", len(pack)/2, len(pack)), append(cmd("refs/heads/wb/"+sid+"/raw4"), pack[:len(pack)/2]...))
		recv("allowed ref, real pack with one byte flipped", append(cmd("refs/heads/wb/"+sid+"/raw5"), flip...))
	}
	cfg.NoFilter = false
	fmt.Printf("\nuser repository unchanged by everything above: %v\n", userBefore == e.userState())
}

// realPack builds a pack holding one new commit on top of x07-main.
func (e *env) realPack() []byte {
	dir := filepath.Join(e.root, "packsrc")
	_, _ = e.ggit(e.root, "init", "-q", dir)
	must(os.WriteFile(filepath.Join(dir, "f"), bytes.Repeat([]byte("data "), 4000), 0o644))
	_, _ = e.ggit(dir, "add", "f")
	_, _ = e.ggit(dir, "commit", "-q", "-m", "x")
	c, _ := e.ggit(dir, "rev-parse", "HEAD")
	cmd := exec.Command(*guestGit, "pack-objects", "--stdout", "--revs")
	cmd.Dir = dir
	cmd.Env = e.ggitEnv()
	cmd.Stdin = strings.NewReader(c + "\n")
	b, err := cmd.Output()
	must(err)
	return b
}

// --- phase: risky-path flagger on real history ---

func (e *env) phaseFlag() {
	fmt.Printf("\n## Risky-path flagger on real diffs\n\n")
	repos := []struct{ name, url string }{
		{"golang/go", ""},
		{"wraithbox/wraithbox", "https://github.com/wraithbox/wraithbox"},
		{"expressjs/express", "https://github.com/expressjs/express"},
	}
	for _, rp := range repos {
		dir := filepath.Join(e.user, ".git")
		if rp.url != "" {
			dir = filepath.Join(e.root, "flag-"+strings.ReplaceAll(rp.name, "/", "-")+".git")
			e.hgit(e.root, "clone", "-q", "--bare", rp.url, dir)
		}
		e.flagRepo(rp.name, dir, 400)
	}
}

func (e *env) flagRepo(name, dir string, n int) {
	revs := strings.Fields(e.hgit(dir, "rev-list", "--first-parent", "--no-merges", fmt.Sprintf("--max-count=%d", n), "HEAD"))
	read := func(oid string) ([]byte, error) {
		cmd := exec.Command(*hostGit, "cat-file", "blob", oid)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + e.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		return cmd.Output()
	}
	byRule := map[string]int{}
	flagged := 0
	var examples []string
	var total time.Duration
	for _, c := range revs {
		t0 := time.Now()
		cmd := exec.Command(*hostGit, "diff-tree", "-r", "-z", "--raw", "--no-renames", "--no-textconv", "--no-ext-diff", "--root", c)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + e.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		// diff-tree prints the commit id first (NUL-terminated).
		if i := bytes.IndexByte(out, 0); i == 40 {
			out = out[i+1:]
		}
		cs, err := flagger.ParseRaw(out)
		if err != nil {
			fmt.Printf("  parse error on %s: %v\n", c[:12], err)
			continue
		}
		fs := flagger.Flag(cs, read)
		total += time.Since(t0)
		if len(fs) > 0 {
			flagged++
			seen := map[string]bool{}
			for _, f := range fs {
				if !seen[f.Rule] {
					byRule[f.Rule]++
					seen[f.Rule] = true
				}
			}
			if len(examples) < 6 {
				subj := e.hgit(dir, "log", "-1", "--format=%s", c)
				examples = append(examples, fmt.Sprintf("%s %q: %s %s (%s)", c[:10], trunc(subj, 50), fs[0].Rule, fs[0].Path, fs[0].Detail))
			}
		}
	}
	var keys []string
	for k := range byRule {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, byRule[k]))
	}
	fmt.Printf("- %s: %d of %d commits flagged (%.0f%%); per rule: %s; mean %.1f ms per commit\n", name, flagged, len(revs),
		100*float64(flagged)/float64(max(1, len(revs))), strings.Join(parts, ", "), float64(total.Microseconds())/1000/float64(max(1, len(revs))))
	for _, x := range examples {
		fmt.Printf("    - %s\n", x)
	}
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
