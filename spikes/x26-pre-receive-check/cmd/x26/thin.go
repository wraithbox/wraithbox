package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"x26/packscan"
	"x26/pktfilter"
)

// gitIn runs git with extra environment and stdin, returning stdout.
func (h *H) gitIn(dir string, extra []string, stdin []byte, args ...string) []byte {
	cmd := exec.Command(*gitBin, args...)
	cmd.Dir = dir
	cmd.Env = append(h.env(), extra...)
	cmd.Stdin = bytes.NewReader(stdin)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("git %v: %v\n%s", args, err, errb.String())
	}
	return out
}

// phaseThin pushes one edited file the way git's own client does: a thin
// pack whose delta is based on an object the landing repository only has
// through its alternates. index-pack --fix-thin then appends that base
// to the quarantined pack. The stray check must not refuse it.
func (h *H) phaseThin() {
	if *bigRepo == "" {
		return
	}
	fmt.Printf("\n## Thin pack from git pack-objects\n\nOne edited `README.md` on top of master, packed with `git pack-objects --thin --revs`, pushed into a landing repository that borrows from the large repository.\n\n")
	head := h.git(*bigRepo, "rev-parse", "master")
	g := filepath.Join(h.root, "guest-thin.git")
	h.git(h.root, "init", "-q", "--bare", g)
	must(os.WriteFile(filepath.Join(g, "objects", "info", "alternates"), []byte(filepath.Join(*bigRepo, "objects")+"\n"), 0o644))
	content := h.gitIn(g, nil, nil, "cat-file", "blob", head+":README.md")
	content = append(content, []byte("\nx26 edit\n")...)
	blob := strings.TrimSpace(string(h.gitIn(g, nil, content, "hash-object", "-w", "--stdin")))
	idx := []string{"GIT_INDEX_FILE=" + filepath.Join(h.root, "thin.idx")}
	h.gitIn(g, idx, nil, "read-tree", head)
	h.gitIn(g, idx, nil, "update-index", "--cacheinfo", "100644,"+blob+",README.md")
	tree := strings.TrimSpace(string(h.gitIn(g, idx, nil, "write-tree")))
	commit := strings.TrimSpace(string(h.gitIn(g, nil, nil, "-c", "user.name=x26", "-c", "user.email=x26@example.invalid", "commit-tree", tree, "-p", head, "-m", "x26 thin")))
	pack := h.gitIn(g, nil, []byte(commit+"\n^"+head+"\n"), "pack-objects", "--thin", "--stdout", "--revs", "-q")
	st, err := packscan.Scan(bytes.NewReader(pack), io.Discard, packscan.Limits{MaxObjects: 1000, MaxObjectSize: maxObj, MaxDeltaSize: maxObj, MaxTotalResult: maxTotal})
	fmt.Printf("Pack: %d bytes, %d objects, %d deltas; scanner err=%v\n\n", len(pack), st.Objects, st.Deltas, err)
	fmt.Printf("| Mode | Report | Quarantine (hook) | Hook log |\n|---|---|---|---|\n")
	for i, m := range []struct {
		name string
		o    opts
	}{{"no hook", opts{}}, {"wb pre-receive", opts{hook: true}}, {"wb pre-receive + stray check", opts{hook: true, stray: true}}, {"pack scanner + wb pre-receive", opts{hook: true, scan: true}},
		{"wb pre-receive, index-pack (receive.unpackLimit=1)", opts{hook: true, unpackLimit: 1}},
		{"wb pre-receive + stray check, index-pack (receive.unpackLimit=1)", opts{hook: true, stray: true, unpackLimit: 1}}} {
		landing := filepath.Join(h.root, fmt.Sprintf("thin-landing-%d.git", i))
		h.git(h.root, "init", "-q", "--bare", landing)
		must(os.WriteFile(filepath.Join(landing, "objects", "info", "alternates"), []byte(filepath.Join(*bigRepo, "objects")+"\n"), 0o644))
		_ = os.Truncate(h.hookLog, 0)
		r := h.receive(landing, []pktfilter.Command{{Old: zero, New: commit, Ref: prefix + "thin"}}, pack, m.o)
		hl, _ := os.ReadFile(h.hookLog)
		q := ""
		if i := bytes.Index(hl, []byte("objects=")); i >= 0 {
			j := bytes.IndexByte(hl[i:], ' ')
			q = string(hl[i : i+j])
		}
		var deny []string
		for _, l := range strings.Split(string(hl), "\n") {
			if strings.Contains(l, "decision=deny") || strings.HasPrefix(l, "hook elapsed") {
				deny = append(deny, l)
			}
		}
		fmt.Printf("| %s | %s | %s | %s |\n", m.name, esc(r.report), q, esc(strings.Join(deny, " ")))
	}

	// A second push on top of the first, into landing repositories that
	// already hold the first: "--not --all" now stops at the first
	// commit, so a base that index-pack appended from the alternates is
	// reached by nothing the check walks.
	content2 := append(content, []byte("x26 second edit\n")...)
	blob2 := strings.TrimSpace(string(h.gitIn(g, nil, content2, "hash-object", "-w", "--stdin")))
	h.gitIn(g, idx, nil, "update-index", "--cacheinfo", "100644,"+blob2+",README.md")
	tree2 := strings.TrimSpace(string(h.gitIn(g, idx, nil, "write-tree")))
	commit2 := strings.TrimSpace(string(h.gitIn(g, nil, nil, "-c", "user.name=x26", "-c", "user.email=x26@example.invalid", "commit-tree", tree2, "-p", commit, "-m", "x26 thin 2")))
	pack2 := h.gitIn(g, nil, []byte(commit2+"\n^"+commit+"\n"), "pack-objects", "--thin", "--stdout", "--revs", "-q")
	fmt.Printf("\nSecond push, on top of the first, into each landing repository above that accepted the first:\n\n| Mode | Report | Quarantine (hook) | Hook log |\n|---|---|---|---|\n")
	for _, m := range []struct {
		name string
		i    int
		o    opts
	}{{"wb pre-receive + stray check (unpack-objects)", 2, opts{hook: true, stray: true}}, {"wb pre-receive + stray check, index-pack (receive.unpackLimit=1)", 5, opts{hook: true, stray: true, unpackLimit: 1}}} {
		landing := filepath.Join(h.root, fmt.Sprintf("thin-landing-%d.git", m.i))
		_ = os.Truncate(h.hookLog, 0)
		r := h.receive(landing, []pktfilter.Command{{Old: commit, New: commit2, Ref: prefix + "thin"}}, pack2, m.o)
		hl, _ := os.ReadFile(h.hookLog)
		q := ""
		if i := bytes.Index(hl, []byte("objects=")); i >= 0 {
			j := bytes.IndexByte(hl[i:], ' ')
			q = string(hl[i : i+j])
		}
		var deny []string
		for _, l := range strings.Split(string(hl), "\n") {
			if strings.Contains(l, "decision=deny") || strings.HasPrefix(l, "hook elapsed") {
				deny = append(deny, l)
			}
		}
		fmt.Printf("| %s | %s | %s | %s |\n", m.name, esc(r.report), q, esc(strings.Join(deny, " ")))
	}
}
