// Command hostd stands in for wb-hostd: it confines itself to its state
// directories, then serves a Unix socket in <data>/run, writes state and
// logs, runs git against a landing repository, and spawns a netd child
// that confines itself further.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"x23/probes"
	"x23/sbxapply"
)

func main() {
	mech := flag.String("mech", "none", "none, cgo, pure")
	profile := flag.String("profile", "", "SBPL profile file")
	var params sbxapply.Params
	flag.Var(&params, "D", "profile parameter key=value")
	config := flag.String("config", "", "<config>")
	data := flag.String("data", "", "<data>")
	logs := flag.String("logs", "", "<logs>")
	sock := flag.String("sock", "", "Unix socket path (relative is fine)")
	git := flag.String("git", "/usr/bin/git", "git binary")
	netd := flag.String("netd", "", "netd binary")
	netdProfile := flag.String("netd-profile", "", "netd profile")
	netdMech := flag.String("netd-mech", "pure", "netd mechanism")
	var e probes.Env
	flag.StringVar(&e.TCPAddr, "tcp", "127.0.0.1:1", "loopback TCP listener")
	flag.StringVar(&e.ReadFile, "readfile", "", "file outside allowed dirs")
	flag.StringVar(&e.WriteDir, "writedir", "", "dir outside allowed dirs")
	useSpawner := flag.Bool("spawner", false, "start children through an unconfined spawner")
	spawnerMode := flag.Bool("spawner-mode", false, "internal: run as the spawner")
	var progs sbxapply.Params
	flag.Var(&progs, "program", "internal: spawner program name=path")
	flag.Parse()
	e.StateDir = *data
	e.RemoteAddr = "1.1.1.1:443"
	e.RemoteName = "example.com"

	if *spawnerMode {
		table := map[string]string{}
		for i := 0; i+1 < len(progs); i += 2 {
			table[progs[i]] = progs[i+1]
		}
		spawnerMain(table)
		return
	}
	var sp *net.UnixConn
	if *useSpawner {
		var err error
		sp, err = startSpawner(map[string]string{"netd": *netd})
		if err != nil {
			fmt.Fprintln(os.Stderr, "hostd: spawner:", err)
			os.Exit(1)
		}
	}

	// The child's profile is read before hostd confines itself, as an
	// embedded profile would be.
	np, _ := os.ReadFile(*netdProfile)

	d, err := sbxapply.Apply(*mech, *profile, params)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hostd: apply:", err)
		os.Exit(2)
	}
	fmt.Printf("hostd: mechanism=%s apply=%s\n", *mech, d)

	check := func(name string, err error) {
		r := probes.Result{Name: name, OK: err == nil}
		if err != nil {
			r.Err = err.Error()
		}
		fmt.Println("hostd:", r)
	}

	_, err = os.ReadFile(filepath.Join(*config, "config.toml"))
	check("read <config>/config.toml", err)
	check("write <config>/new.toml", os.WriteFile(filepath.Join(*config, "new.toml"), []byte("x"), 0o600))
	f, err := os.OpenFile(filepath.Join(*data, "state.db"), os.O_CREATE|os.O_RDWR, 0o600)
	if err == nil {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == nil {
			_, err = f.WriteString("state")
		}
		f.Close()
	}
	check("write+flock <data>/state.db", err)
	lf, err := os.OpenFile(filepath.Join(*logs, "audit.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = lf.WriteString(`{"event":"start"}` + "\n")
		lf.Close()
	}
	check("append <logs>/audit.jsonl", err)

	_ = os.Remove(*sock)
	l, err := net.Listen("unix", *sock)
	check("listen unix <data>/run/hostd.sock", err)
	if err == nil {
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				line, _ := bufio.NewReader(c).ReadString('\n')
				fmt.Fprintf(c, "hostd got %q\n", strings.TrimSpace(line))
				c.Close()
				fmt.Println("hostd: served a client on the Unix socket")
			}
		}()
	}

	// git, as the git gateway runs it (S08-workspace-and-git).
	repo := filepath.Join(*data, "projects", "p1", "landing.git")
	_ = os.RemoveAll(repo)
	work := filepath.Join(*data, "work")
	clone := filepath.Join(*data, "clone")
	_ = os.RemoveAll(work)
	_ = os.RemoveAll(clone)
	gitRun := func(name string, args ...string) {
		cmd := exec.Command(*git, args...)
		cmd.Dir = *data
		// No user or system git config: the gateway must not be steered by
		// ~/.gitconfig, and the sandbox denies reading it anyway.
		cmd.Env = []string{"HOME=" + *data, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"PATH=/usr/bin:/bin", "GIT_AUTHOR_NAME=x", "GIT_AUTHOR_EMAIL=x@x", "GIT_COMMITTER_NAME=x", "GIT_COMMITTER_EMAIL=x@x"}
		out, err := cmd.CombinedOutput()
		check(fmt.Sprintf("git %s: %s", name, firstLine(out)), err)
	}
	// git init walks up to create missing parents, which needs metadata
	// reads above <data>; create them first.
	check("mkdir landing.git, work", errors.Join(os.MkdirAll(repo, 0o700), os.MkdirAll(work, 0o700)))
	gitRun("init --bare landing.git", "init", "--bare", "-q", repo)
	gitRun("init work", "init", "-q", "-b", "main", work)
	_ = os.WriteFile(filepath.Join(work, "f.txt"), []byte("hello\n"), 0o600)
	gitRun("add", "-C", work, "add", "f.txt")
	gitRun("commit", "-C", work, "commit", "-q", "-m", "c1")
	gitRun("push (receive-pack)", "-C", work, "push", "-q", repo, "main:refs/heads/session")
	gitRun("clone (upload-pack)", "clone", "-q", "--branch", "session", repo, clone)
	_, err = os.Stat(filepath.Join(clone, "f.txt"))
	check("round trip: clone has f.txt", err)

	// A child that confines itself further.
	if *netd != "" && sp == nil {
		netdChild(*netd, *netdMech, np, e)
	}
	if sp != nil {
		netdViaSpawner(sp, *netdMech, np, e)
		r, err := spawnVia(sp, "sh -c id", os.Stdin, os.Stdout, os.Stderr)
		fmt.Printf("hostd: spawner request for a program outside the table: %q %v\n", r, err)
	}

	all := probes.Filesystem(e)
	all = append(all, probes.Network(e)...)
	all = append(all, probes.Process()...)
	for _, r := range all {
		fmt.Println("hostd probe:", r)
	}
	fmt.Println("READY")
	time.Sleep(8 * time.Second)
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(string(b), "\n")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func netdChild(bin, mech string, profile []byte, e probes.Env) {
	// Profile passed to the child on a pipe, so the child reads no file.
	pr, pw, err := os.Pipe()
	if err != nil {
		fmt.Println("hostd: pipe:", err)
		return
	}
	go func() { _, _ = pw.Write(profile); pw.Close() }()
	cmd := exec.Command(bin, "-mech", mech, "-profile", "fd:3", "-probe-only",
		"-tcp", e.TCPAddr, "-readfile", e.ReadFile, "-writedir", e.WriteDir)
	cmd.ExtraFiles = []*os.File{pr}
	out, err := cmd.CombinedOutput()
	pr.Close()
	fmt.Printf("hostd: netd child exit=%v\n", err)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fmt.Println("  child|", line)
	}
}
