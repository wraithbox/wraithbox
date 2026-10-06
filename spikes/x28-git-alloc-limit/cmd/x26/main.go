// Command x26 runs the X26-pre-receive-check spike: does a pre-receive
// check that wb-hostd owns keep refused pushes and oversized objects out
// of landing.git, what does it cost, and what bounds the memory git uses
// while it unpacks a push?
//
// It talks to `git receive-pack` the way a hostile guest would: it
// writes the command list and a pack it built byte by byte (packgen),
// after the X07 ref filter (pktfilter) has passed the command list.
// Everything happens in throwaway repositories under .scratch or a temp
// directory.
//
// Throwaway code. Not held to the project gates.
package main

import (
	"bytes"
	"crypto/sha1"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"x26/packgen"
	"x26/packscan"
	"x26/pktfilter"
)

var (
	gitBin   = flag.String("git", "/opt/homebrew/bin/git", "git binary (host side)")
	workDir  = flag.String("work", "", "parent of the run directory (default: temp dir)")
	bigRepo  = flag.String("big", "", "bare clone of a large repository for the cost phase (golang/go)")
	phases   = flag.String("phases", "cases,memory,cost", "comma list: cases,memory,cost")
	maxBomb  = flag.Int("maxbomb", 2048, "largest delta-bomb result to try, MiB")
	keep     = flag.Bool("keep", false, "keep the run directory")
	costRuns = flag.Int("runs", 5, "repetitions for timings")
)

const (
	sid      = "S1"
	prefix   = "refs/heads/wb/" + sid + "/"
	maxInput = 16 << 20  // receive.maxInputSize as in X07
	maxObj   = 100 << 20 // per-object inflated cap used in this spike
	maxTotal = 1 << 30   // total inflated cap per push used in this spike
)

var zero = strings.Repeat("0", 40)

type H struct {
	root, bin, hooks, home, hookLog string
}

func main() {
	flag.Parse()
	parent := *workDir
	if parent == "" {
		parent = os.TempDir()
	}
	root, err := os.MkdirTemp(parent, "x26-run-")
	must(err)
	if !*keep {
		defer os.RemoveAll(root)
	}
	h := &H{root: root, bin: filepath.Join(root, "bin"), hooks: filepath.Join(root, "wb-hooks"), home: filepath.Join(root, "home"), hookLog: filepath.Join(root, "hook.log")}
	for _, d := range []string{h.bin, h.hooks, h.home} {
		must(os.MkdirAll(d, 0o755))
	}
	// The hooks directory wb-hostd owns holds one program, pre-receive.
	out, err := exec.Command("go", "build", "-o", filepath.Join(h.hooks, "pre-receive"), "./cmd/wb-pre-receive").CombinedOutput()
	if err != nil {
		log.Fatalf("build hook: %v\n%s", err, out)
	}
	v, _ := exec.Command(*gitBin, "version").Output()
	fmt.Printf("# X26 run %s\n\ngit: %s\nrun directory: %s (path length %d)\n", time.Now().Format(time.RFC3339), strings.TrimSpace(string(v)), root, len(root))
	fmt.Printf("receive.maxInputSize=%d, object cap %d, total cap %d\n", maxInput, maxObj, maxTotal)
	for _, p := range strings.Split(*phases, ",") {
		switch p {
		case "cases":
			h.phaseCases()
		case "memory":
			h.phaseMemory()
		case "cost":
			h.phaseCost()
		case "thin":
			h.phaseThin()
		case "alloc":
			h.phaseAlloc()
		case "alloccost":
			h.phaseAllocCost()
		}
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func (h *H) env() []string {
	return []string{"PATH=/usr/bin:/bin", "HOME=" + h.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
}

func (h *H) git(dir string, args ...string) string {
	cmd := exec.Command(*gitBin, args...)
	cmd.Dir = dir
	cmd.Env = h.env()
	b, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("git %v in %s: %v\n%s", args, dir, err, b)
	}
	return strings.TrimSpace(string(b))
}

type opts struct {
	hook        bool
	scan        bool
	stray       bool
	threads     int    // pack.threads for index-pack; 0 = git default
	ulimitKB    int    // run receive-pack under `ulimit -v`
	refFormat   string // landing ref backend, for display only
	unpackLimit int    // receive.unpackLimit; 1 forces index-pack
	maxInput    int64
	maxTotal    uint64 // 0 = the spike default total cap
	allocLimit  string // X28: GIT_ALLOC_LIMIT in receive-pack's environment; "" = unset
	noTrailer   bool   // X28: send the pack without its 20-byte checksum
}

type result struct {
	report  string // receive-pack's report-status lines
	stderr  string
	err     error
	scanErr error
	dur     time.Duration
	maxRSS  int64 // bytes, receive-pack and its children
	cpu     time.Duration // X28: user + system time of receive-pack and its children
}

// receive runs one push: the X07 filter over the command list, then
// receive-pack with the X07 settings, optionally the hook, optionally
// the pack scanner in front of git.
func (h *H) receive(landing string, cmds []pktfilter.Command, pack []byte, o opts) result {
	var req bytes.Buffer
	for i, c := range cmds {
		line := c.Old + " " + c.New + " " + c.Ref
		if i == 0 {
			line += "\x00report-status"
		}
		req.Write(pktfilter.Pkt(line + "\n"))
	}
	req.WriteString("0000")
	// First layer, as in X07: the filter must pass the command list.
	parsed, ferr := pktfilter.ReadRequest(bytes.NewReader(req.Bytes()), prefix)
	if ferr != nil {
		return result{err: fmt.Errorf("filter: %w", ferr)}
	}
	mt := o.maxTotal
	if mt == 0 {
		mt = maxTotal
	}
	mi := o.maxInput
	if mi == 0 {
		mi = maxInput
	}
	hooks := "/dev/null"
	if o.hook {
		hooks = h.hooks
	}
	args := []string{
		"-c", "core.hooksPath=" + hooks,
		"-c", "receive.fsckObjects=true",
		"-c", "receive.fsck.badFilemode=error",
		"-c", fmt.Sprintf("receive.maxInputSize=%d", mi),
		"-c", "receive.denyDeletes=true",
		"-c", "receive.autogc=false",
		"-c", "receive.advertisePushOptions=false",
		"-c", "receive.shallowUpdate=false",
		"-c", "receive.hideRefs=refs/",
		"-c", "receive.hideRefs=!" + strings.TrimSuffix(prefix, "/"),
	}
	if o.threads > 0 {
		args = append(args, "-c", fmt.Sprintf("pack.threads=%d", o.threads))
	}
	if o.unpackLimit > 0 {
		args = append(args, "-c", fmt.Sprintf("receive.unpackLimit=%d", o.unpackLimit))
	}
	args = append(args, "receive-pack", landing)
	var cmd *exec.Cmd
	if o.ulimitKB != 0 {
		// ulimitKB > 0: RLIMIT_AS (ulimit -v); < 0: RLIMIT_DATA (ulimit -d).
		flagc, kb := "-v", o.ulimitKB
		if kb < 0 {
			flagc, kb = "-d", -kb
		}
		cmd = exec.Command("/bin/sh", append([]string{"-c", fmt.Sprintf("ulimit %s %d || exit 99; exec \"$@\"", flagc, kb), "sh", *gitBin}, args...)...)
	} else {
		cmd = exec.Command(*gitBin, args...)
	}
	cmd.Dir = h.home
	cmd.Env = append(h.env(),
		"WB_GIT="+*gitBin, "WB_REF_PREFIX="+prefix, "WB_HOOK_LOG="+h.hookLog,
		fmt.Sprintf("WB_MAX_OBJECT_SIZE=%d", maxObj), fmt.Sprintf("WB_MAX_TOTAL=%d", mt),
		"WB_PATH_MAX=1024", "WB_NAME_MAX=255")
	if o.stray {
		cmd.Env = append(cmd.Env, "WB_CHECK_STRAY=1")
	}
	if o.allocLimit != "" {
		cmd.Env = append(cmd.Env, "GIT_ALLOC_LIMIT="+o.allocLimit)
	}
	if o.noTrailer && len(pack) >= 20 {
		pack = pack[:len(pack)-20]
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	stdin, err := cmd.StdinPipe()
	must(err)
	t0 := time.Now()
	must(cmd.Start())
	_, _ = stdin.Write(parsed.Raw)
	var scanErr error
	if o.scan {
		lim := packscan.Limits{MaxObjects: 10_000_000, MaxObjectSize: maxObj, MaxDeltaSize: maxObj, MaxTotalResult: mt}
		_, scanErr = packscan.Scan(bytes.NewReader(pack), stdin, lim)
		if scanErr != nil {
			// Refused: git has a truncated pack and fails on EOF.
			_ = stdin.Close()
		}
	} else {
		_, _ = stdin.Write(pack)
	}
	_ = stdin.Close()
	err = cmd.Wait()
	r := result{err: err, scanErr: scanErr, dur: time.Since(t0), stderr: stderr.String()}
	if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
		r.maxRSS = ru.Maxrss // bytes on darwin
		r.cpu = time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	}
	r.report = reportLines(stdout.Bytes())
	return r
}

// reportLines pulls the report-status lines out of receive-pack's output
// (after the ref advertisement).
func reportLines(b []byte) string {
	var lines []string
	r := bytes.NewReader(b)
	for {
		p, flush, _, err := pktfilter.ReadPkt(r)
		if err != nil {
			break
		}
		if flush {
			continue
		}
		s := strings.TrimSuffix(string(p), "\n")
		if strings.HasPrefix(s, "unpack ") || strings.HasPrefix(s, "ok ") || strings.HasPrefix(s, "ng ") {
			if i := strings.Index(s, "refs/heads/wb/S1/"); i >= 0 && len(s) > 120 {
				s = s[:i+30] + "…" + s[len(s)-40:]
			}
			lines = append(lines, s)
		}
	}
	return strings.Join(lines, "; ")
}

type objState struct {
	loose, packs int
	packBytes    int64
	tmp          []string
}

func (s objState) String() string {
	return fmt.Sprintf("loose=%d packs=%d (%d B) tmp=%d", s.loose, s.packs, s.packBytes, len(s.tmp))
}

// landingObjects counts what is in landing.git/objects, without git.
func landingObjects(landing string) objState {
	var s objState
	od := filepath.Join(landing, "objects")
	_ = filepath.WalkDir(od, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(od, p)
		if d.IsDir() && strings.HasPrefix(d.Name(), "tmp_objdir") {
			s.tmp = append(s.tmp, rel)
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		parts := strings.Split(rel, string(filepath.Separator))
		switch {
		case len(parts) == 2 && len(parts[0]) == 2 && parts[0] != "in":
			s.loose++
		case len(parts) == 2 && parts[0] == "pack" && strings.HasSuffix(parts[1], ".pack"):
			s.packs++
			if fi, err := d.Info(); err == nil {
				s.packBytes += fi.Size()
			}
		}
		return nil
	})
	return s
}

func (s objState) total() int64 { return int64(s.loose) + s.packBytes }

// ---- objects for the cases

type commitObjs struct {
	entries []packgen.Entry
	id      string
}

// newCommit is a blob, a tree and a commit with parent, all new.
func newCommit(parent string, tag string) commitObjs {
	blob := []byte("content " + tag + "\n")
	bid := packgen.ObjectID(packgen.Blob, blob)
	tree := packgen.TreeOne("f.txt", bid)
	tid := packgen.ObjectID(packgen.Tree, tree)
	var p []byte
	if parent != "" {
		p = hexID(parent)
	}
	c := packgen.CommitOf(tid, p, "x26 "+tag)
	return commitObjs{
		entries: []packgen.Entry{packgen.Whole(packgen.Blob, blob), packgen.Whole(packgen.Tree, tree), packgen.Whole(packgen.Commit, c)},
		id:      packgen.Hex(packgen.ObjectID(packgen.Commit, c)),
	}
}

func hexID(s string) []byte {
	b := make([]byte, 20)
	_, err := fmt.Sscanf(s, "%x", &b)
	must(err)
	return b
}

func (h *H) newLanding(name, refFormat string) (landing, base string) {
	landing = filepath.Join(h.root, name+".git")
	args := []string{"init", "-q", "--bare"}
	if refFormat != "" {
		args = append(args, "--ref-format="+refFormat)
	}
	h.git(h.root, append(args, landing)...)
	c0 := newCommit("", "base-"+name)
	r := h.receive(landing, []pktfilter.Command{{Old: zero, New: c0.id, Ref: prefix + "main"}}, packgen.Pack(c0.entries), opts{})
	if !strings.Contains(r.report, "ok "+prefix+"main") {
		log.Fatalf("base push: %s %s %v", r.report, r.stderr, r.err)
	}
	return landing, c0.id
}

type tcase struct {
	name, what string
	setup      func(h *H, landing, base string)
	cmds       func(base, next string) []pktfilter.Command
	pack       func(base string, next commitObjs) []byte
}

func longRef(compLen, n int) string {
	var comps []string
	for i := 0; i < n; i++ {
		comps = append(comps, strings.Repeat(string(rune('a'+i)), compLen))
	}
	return prefix + strings.Join(comps, "/")
}

func one(ref string) func(base, next string) []pktfilter.Command {
	return func(base, next string) []pktfilter.Command {
		return []pktfilter.Command{{Old: zero, New: next, Ref: ref}}
	}
}

func cases() []tcase {
	updRef := func(ref string) func(h *H, l, b string) {
		return func(h *H, l, b string) { h.git(l, "update-ref", ref, b) }
	}
	packRefs := func(h *H, l, b string) { h.git(l, "pack-refs", "--all") }
	both := func(fs ...func(h *H, l, b string)) func(h *H, l, b string) {
		return func(h *H, l, b string) {
			for _, f := range fs {
				f(h, l, b)
			}
		}
	}
	return []tcase{
		{name: "valid", what: "create a new session branch",
			cmds: one(prefix + "feature")},
		{name: "long-path", what: "ref of 1020 bytes (4 components of 250): passes the S08 filter, path too long with the state directory in front",
			cmds: one(longRef(250, 4))},
		{name: "long-component", what: "one component of 253 bytes: passes the 255-byte filter rule, its .lock file does not fit NAME_MAX",
			cmds: one(prefix + strings.Repeat("c", 253))},
		{name: "stale-old", what: "update main with an old id that is not its value",
			cmds: func(base, next string) []pktfilter.Command {
				return []pktfilter.Command{{Old: next, New: next, Ref: prefix + "main"}}
			}},
		{name: "create-existing", what: "create main (old id zero) although it exists",
			cmds: one(prefix + "main")},
		{name: "update-missing", what: "update a ref that does not exist (old id given)",
			cmds: func(base, next string) []pktfilter.Command {
				return []pktfilter.Command{{Old: base, New: next, Ref: prefix + "nosuch"}}
			}},
		{name: "df-file-exists", what: "push main/x while main exists",
			cmds: one(prefix + "main/x")},
		{name: "df-dir-exists", what: "push dir while dir/x exists",
			setup: updRef(prefix + "dir/x"), cmds: one(prefix + "dir")},
		{name: "df-in-push", what: "push p and p/q in one push",
			cmds: func(base, next string) []pktfilter.Command {
				return []pktfilter.Command{{Old: zero, New: next, Ref: prefix + "p"}, {Old: zero, New: next, Ref: prefix + "p/q"}}
			}},
		{name: "df-packed", what: "push main/x while main exists as a packed ref",
			setup: packRefs, cmds: one(prefix + "main/x")},
		{name: "case-file", what: "push MAIN while main exists",
			cmds: one(prefix + "MAIN")},
		{name: "case-dir", what: "push DIR/y while dir/x exists",
			setup: updRef(prefix + "dir/x"), cmds: one(prefix + "DIR/y")},
		{name: "case-in-push", what: "push abc and ABC in one push",
			cmds: func(base, next string) []pktfilter.Command {
				return []pktfilter.Command{{Old: zero, New: next, Ref: prefix + "abc"}, {Old: zero, New: next, Ref: prefix + "ABC"}}
			}},
		{name: "case-packed", what: "push MAIN while main exists as a packed ref",
			setup: both(packRefs), cmds: one(prefix + "MAIN")},
		{name: "missing-object", what: "the push names a commit the pack does not contain",
			cmds: func(base, next string) []pktfilter.Command {
				return []pktfilter.Command{{Old: zero, New: strings.Repeat("1", 40), Ref: prefix + "missing"}}
			}},
		{name: "stale-lock", what: "a leftover feature.lock file (from a crashed earlier push) blocks the ref",
			setup: func(h *H, l, b string) {
				must(os.MkdirAll(filepath.Join(l, "refs", "heads", "wb", sid), 0o755))
				must(os.WriteFile(filepath.Join(l, "refs", "heads", "wb", sid, "feature.lock"), nil, 0o644))
			},
			cmds: one(prefix + "feature")},
		{name: "big-object", what: "a commit whose blob inflates to 200 MiB (zeros; about 200 KiB on the wire)",
			cmds: one(prefix + "big"),
			pack: func(base string, _ commitObjs) []byte {
				const n = 200 << 20
				bid := zeroBlobID(n)
				tree := packgen.TreeOne("big.bin", bid)
				tid := packgen.ObjectID(packgen.Tree, tree)
				c := packgen.CommitOf(tid, hexID(base), "big")
				bigCommit = packgen.Hex(packgen.ObjectID(packgen.Commit, c))
				return packgen.Pack([]packgen.Entry{
					{Type: packgen.Blob, Size: n, Body: packgen.Zeros(n)},
					packgen.Whole(packgen.Tree, tree), packgen.Whole(packgen.Commit, c),
				})
			}},
		{name: "stray", what: "a valid push that also carries a blob no ref reaches",
			cmds: one(prefix + "stray"),
			pack: func(base string, next commitObjs) []byte {
				return packgen.Pack(append(next.entries, packgen.Whole(packgen.Blob, []byte("nobody points at me\n"))))
			}},
	}
}

var bigCommit string

func zeroBlobID(n uint64) []byte {
	hh := sha1.New()
	fmt.Fprintf(hh, "blob %d\x00", n)
	_ = packgen.Zeros(n)(hh)
	return hh.Sum(nil)
}

func (h *H) phaseCases() {
	fmt.Printf("\n## Cases\n\nEach case runs against a fresh landing repository holding one commit at `%smain`. \"Left behind\" is what the push added to `objects/` of the landing repository: loose objects, packs, and quarantine directories.\n\n", prefix)
	modes := []struct {
		name      string
		o         opts
		refFormat string
	}{
		{"files, no hook", opts{}, ""},
		{"files, wb pre-receive", opts{hook: true}, ""},
		{"files, wb pre-receive + stray check", opts{hook: true, stray: true}, ""},
		{"reftable, no hook", opts{}, "reftable"},
	}
	for _, tc := range cases() {
		fmt.Printf("### %s\n\n%s\n\n| Mode | Report | Left behind | Refs after (besides main) | Hook log |\n|---|---|---|---|---|\n", tc.name, tc.what)
		for mi, m := range modes {
			if m.o.stray && tc.name != "stray" && tc.name != "valid" {
				continue
			}
			if m.refFormat == "reftable" && tc.name == "stale-lock" {
				continue // reftable has no loose ref files to leave a lock beside
			}
			landing, base := h.newLanding(fmt.Sprintf("%s-%d", tc.name, mi), m.refFormat)
			if tc.setup != nil {
				tc.setup(h, landing, base)
			}
			next := newCommit(base, tc.name)
			var pack []byte
			if tc.pack != nil {
				pack = tc.pack(base, next)
			} else {
				pack = packgen.Pack(next.entries)
			}
			newID := next.id
			if tc.name == "big-object" {
				newID = bigCommit
			}
			before := landingObjects(landing)
			_ = os.Truncate(h.hookLog, 0)
			r := h.receive(landing, tc.cmds(base, newID), pack, m.o)
			after := landingObjects(landing)
			left := fmt.Sprintf("+%d loose, +%d packs (%d B), tmp dirs %d", after.loose-before.loose, after.packs-before.packs, after.packBytes-before.packBytes, len(after.tmp))
			hl, _ := os.ReadFile(h.hookLog)
			hookLine := ""
			for _, l := range strings.Split(strings.TrimSpace(string(hl)), "\n") {
				if strings.Contains(l, "decision=deny") || strings.HasPrefix(l, "hook elapsed") {
					if len(l) > 160 {
						l = l[:160] + "…"
					}
					hookLine += l + " "
				}
			}
			rep := r.report
			if rep == "" {
				rep = fmt.Sprintf("err=%v stderr=%q", r.err, tail(r.stderr, 200))
			}
			var refs []string
			for _, l := range strings.Split(h.git(landing, "for-each-ref", "--format=%(refname)"), "\n") {
				if l == "" || l == prefix+"main" {
					continue
				}
				l = strings.TrimPrefix(l, prefix)
				if len(l) > 40 {
					l = l[:20] + "…"
				}
				refs = append(refs, l)
			}
			fmt.Printf("| %s | %s | %s | %s | %s |\n", m.name, esc(rep), left, strings.Join(refs, ", "), esc(strings.TrimSpace(hookLine)))
		}
		fmt.Println()
	}
}

func esc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// ---- memory

// blobPush wraps entries whose results are zero-filled blobs of sizes
// into a tree and a commit, so the push names a ref that reaches them
// and the hook runs.
func blobPush(sizes []uint64, entries []packgen.Entry) ([]byte, string) {
	var tree bytes.Buffer
	for i, n := range sizes {
		fmt.Fprintf(&tree, "100644 b%03d\x00", i)
		tree.Write(zeroBlobID(n))
	}
	tid := packgen.ObjectID(packgen.Tree, tree.Bytes())
	c := packgen.CommitOf(tid, nil, "bomb")
	entries = append(entries, packgen.Whole(packgen.Tree, tree.Bytes()), packgen.Whole(packgen.Commit, c))
	return packgen.Pack(entries), packgen.Hex(packgen.ObjectID(packgen.Commit, c))
}

// deltaBombs is one zero-filled base and one OFS_DELTA per result size.
func deltaBombs(base uint64, results []uint64) ([]byte, string) {
	entries := []packgen.Entry{{Type: packgen.Blob, Size: base, Body: packgen.Zeros(base)}}
	for _, r := range results {
		d := packgen.CopyDelta(base, r)
		entries = append(entries, packgen.Entry{Type: packgen.OfsDelta, Size: uint64(len(d)), Body: packgen.Bytes(d), BaseIdx: 0})
	}
	return blobPush(append([]uint64{base}, results...), entries)
}

func zlibBomb(n uint64) ([]byte, string) {
	return blobPush([]uint64{n}, []packgen.Entry{{Type: packgen.Blob, Size: n, Body: packgen.Zeros(n)}})
}

func (h *H) phaseMemory() {
	fmt.Printf("\n## Memory while git unpacks\n\nmaxrss is ru_maxrss of receive-pack as wait4 reports it, which includes its reaped children (index-pack or unpack-objects, the hook). Every pack here is far under receive.maxInputSize, and its commit reaches every object, so the hook runs.\n\n")
	fmt.Printf("| Pack | Wire bytes | Mode | Result | Time | maxrss |\n|---|---|---|---|---|---|\n")
	type bomb struct {
		name string
		pack func() ([]byte, string)
	}
	var bombs []bomb
	for mib := 256; mib <= *maxBomb; mib *= 2 {
		n := uint64(mib) << 20
		bombs = append(bombs, bomb{fmt.Sprintf("one delta, result %d MiB (base 8 MiB)", mib), func() ([]byte, string) { return deltaBombs(8<<20, []uint64{n}) }})
	}
	bombs = append(bombs,
		bomb{"8 deltas, results 256 MiB each (base 8 MiB)", func() ([]byte, string) {
			var rs []uint64
			for i := 0; i < 8; i++ {
				rs = append(rs, 256<<20-uint64(i))
			}
			return deltaBombs(8<<20, rs)
		}},
		bomb{"8 deltas, results 96 MiB each (base 8 MiB), all under the 100 MiB cap", func() ([]byte, string) {
			var rs []uint64
			for i := 0; i < 8; i++ {
				rs = append(rs, 96<<20-uint64(i))
			}
			return deltaBombs(8<<20, rs)
		}},
		bomb{"64 deltas, results 96 MiB each (base 8 MiB), all under the cap", func() ([]byte, string) {
			var rs []uint64
			for i := 0; i < 64; i++ {
				rs = append(rs, 96<<20-uint64(i))
			}
			return deltaBombs(8<<20, rs)
		}},
		bomb{"chain of 8 deltas, each the base of the next, results 96 MiB each, all under the cap", func() ([]byte, string) {
			entries := []packgen.Entry{{Type: packgen.Blob, Size: 8 << 20, Body: packgen.Zeros(8 << 20)}}
			sizes := []uint64{8 << 20}
			prev := uint64(8 << 20)
			for i := 0; i < 8; i++ {
				r := 96<<20 - uint64(i)
				d := packgen.CopyDelta(prev, r)
				entries = append(entries, packgen.Entry{Type: packgen.OfsDelta, Size: uint64(len(d)), Body: packgen.Bytes(d), BaseIdx: len(entries) - 1})
				sizes = append(sizes, r)
				prev = r
			}
			return blobPush(sizes, entries)
		}},
		bomb{"blob 400 MiB of zeros", func() ([]byte, string) { return zlibBomb(400 << 20) }},
		bomb{"blob 1024 MiB of zeros", func() ([]byte, string) { return zlibBomb(1 << 30) }},
	)
	modes := []struct {
		name string
		o    opts
	}{
		{"no hook (unpack-objects: under 100 objects)", opts{}},
		{"wb pre-receive (unpack-objects)", opts{hook: true}},
		{"wb pre-receive, index-pack (receive.unpackLimit=1)", opts{hook: true, unpackLimit: 1}},
		{"wb pre-receive, index-pack, pack.threads=1", opts{hook: true, unpackLimit: 1, threads: 1}},
		{"pack scanner + wb pre-receive, index-pack", opts{hook: true, scan: true, unpackLimit: 1}},
		{"pack scanner + wb pre-receive, index-pack, pack.threads=1", opts{hook: true, scan: true, unpackLimit: 1, threads: 1}},
		{"no hook, ulimit -v 512 MiB", opts{ulimitKB: 512 << 10}},
		{"no hook, ulimit -d 512 MiB", opts{ulimitKB: -(512 << 10)}},
	}
	for bi, b := range bombs {
		pack, head := b.pack()
		for _, m := range modes {
			if m.o.ulimitKB != 0 && bi > 0 {
				continue
			}
			landing, _ := h.newLanding(fmt.Sprintf("mem-%d", time.Now().UnixNano()), "")
			before := landingObjects(landing)
			r := h.receive(landing, []pktfilter.Command{{Old: zero, New: head, Ref: prefix + "bomb"}}, pack, m.o)
			after := landingObjects(landing)
			res := r.report
			if r.scanErr != nil {
				res = "scanner: " + r.scanErr.Error()
			} else if res == "" {
				res = fmt.Sprintf("err=%v %s", r.err, tail(strings.ReplaceAll(r.stderr, "\n", " "), 120))
			}
			if strings.Contains(res, "abnormal") || strings.Contains(res, "unpacker error") {
				res += " stderr: " + tail(strings.ReplaceAll(strings.TrimSpace(r.stderr), "\n", " / "), 160)
			}
			res += fmt.Sprintf(" (landing +%d B)", after.total()-before.total())
			fmt.Printf("| %s | %d | %s | %s | %.2fs | %d MiB |\n", b.name, len(pack), m.name, esc(res), r.dur.Seconds(), r.maxRSS>>20)
			_ = os.RemoveAll(landing)
		}
	}
}

// ---- cost

func (h *H) phaseCost() {
	if *bigRepo == "" {
		fmt.Printf("\n## Cost\n\nskipped: no -big repository\n")
		return
	}
	fmt.Printf("\n## Cost\n\nLarge repository: %s, master at %s.\n\n", *bigRepo, h.git(*bigRepo, "rev-parse", "--short", "master"))
	head := h.git(*bigRepo, "rev-parse", "master")

	// Whole history as one push into an empty landing repository, as a
	// landing repository without borrowing would get (X07).
	pf := filepath.Join(h.root, "whole.pack")
	cmd := exec.Command(*gitBin, "pack-objects", "--stdout", "--revs", "-q")
	cmd.Dir = *bigRepo
	cmd.Env = h.env()
	cmd.Stdin = strings.NewReader("master\n")
	f, err := os.Create(pf)
	must(err)
	cmd.Stdout = f
	must(cmd.Run())
	f.Close()
	pack, err := os.ReadFile(pf)
	must(err)
	fmt.Printf("Whole-history pack: %d MiB.\n\n", len(pack)>>20)

	// Scanner alone, to io.Discard.
	var scanTimes []time.Duration
	var st packscan.Stats
	for i := 0; i < *costRuns; i++ {
		t0 := time.Now()
		st, err = packscan.Scan(bytes.NewReader(pack), io.Discard, packscan.Limits{MaxObjects: 10_000_000, MaxObjectSize: maxObj, MaxDeltaSize: maxObj, MaxTotalResult: 1 << 40})
		scanTimes = append(scanTimes, time.Since(t0))
		if err != nil {
			fmt.Printf("scanner error: %v\n", err)
			break
		}
	}
	fmt.Printf("| Step | Median (range) | Runs |\n|---|---|---|\n")
	fmt.Printf("| Pack scanner alone over the whole-history pack (%d objects, %d deltas, %d MiB inflated) | %s | %d |\n", st.Objects, st.Deltas, st.TotalResult>>20, med(scanTimes), len(scanTimes))

	modes := []struct {
		name string
		o    opts
	}{
		{"receive-pack, no hook", opts{}},
		{"receive-pack + wb pre-receive", opts{hook: true}},
		{"receive-pack + wb pre-receive + stray check", opts{hook: true, stray: true}},
		{"pack scanner + receive-pack + wb pre-receive", opts{hook: true, scan: true}},
	}
	for _, m := range modes {
		m.o.maxInput = 1 << 30
		m.o.maxTotal = 1 << 40 // the whole history inflates to about 9 GiB; measure the full check
		var ts, hookTs []time.Duration
		var rss int64
		var rep string
		for i := 0; i < *costRuns; i++ {
			landing := filepath.Join(h.root, fmt.Sprintf("cost-%d.git", time.Now().UnixNano()))
			h.git(h.root, "init", "-q", "--bare", landing)
			_ = os.Truncate(h.hookLog, 0)
			r := h.receive(landing, []pktfilter.Command{{Old: zero, New: head, Ref: prefix + "whole"}}, pack, m.o)
			ts = append(ts, r.dur)
			if r.maxRSS > rss {
				rss = r.maxRSS
			}
			rep = r.report
			if d, ok := hookElapsed(h.hookLog); ok {
				hookTs = append(hookTs, d)
			}
			_ = os.RemoveAll(landing)
		}
		extra := ""
		if len(hookTs) > 0 {
			extra = fmt.Sprintf("; hook alone %s", med(hookTs))
		}
		fmt.Printf("| Whole history, %s (%s%s, maxrss %d MiB) | %s | %d |\n", m.name, esc(tail(rep, 60)), extra, rss>>20, med(ts), len(ts))
	}

	// A typical push: a landing repository that borrows from the export
	// repository (here the large clone itself), one new commit.
	landing := filepath.Join(h.root, "borrow.git")
	h.git(h.root, "init", "-q", "--bare", landing)
	must(os.WriteFile(filepath.Join(landing, "objects", "info", "alternates"), []byte(filepath.Join(*bigRepo, "objects")+"\n"), 0o644))
	for _, m := range []struct {
		name string
		o    opts
	}{{"no hook", opts{}}, {"wb pre-receive", opts{hook: true}}, {"wb pre-receive + stray check", opts{hook: true, stray: true}}, {"pack scanner + wb pre-receive", opts{hook: true, scan: true}}} {
		var ts, hookTs []time.Duration
		var rep string
		for i := 0; i < *costRuns; i++ {
			c := newCommit(head, fmt.Sprintf("small-%s-%d", m.name, i))
			_ = os.Truncate(h.hookLog, 0)
			r := h.receive(landing, []pktfilter.Command{{Old: zero, New: c.id, Ref: fmt.Sprintf("%ssmall-%d-%d", prefix, len(m.name), i)}}, packgen.Pack(c.entries), m.o)
			ts = append(ts, r.dur)
			rep = r.report
			if d, ok := hookElapsed(h.hookLog); ok {
				hookTs = append(hookTs, d)
			}
		}
		extra := ""
		if len(hookTs) > 0 {
			extra = fmt.Sprintf("; hook alone %s", med(hookTs))
		}
		if b, _ := os.ReadFile(h.hookLog); bytes.Contains(b, []byte("rule=object-size objects=")) {
			i := bytes.Index(b, []byte("objects="))
			j := bytes.IndexByte(b[i:], ' ')
			extra += "; hook saw " + string(b[i:i+j]) + " in the quarantine"
		}
		fmt.Printf("| Push of one new commit, landing borrows from the large repository, %s (%s%s) | %s | %d |\n", m.name, esc(tail(rep, 50)), extra, med(ts), len(ts))
	}
}

func hookElapsed(p string) (time.Duration, bool) {
	b, _ := os.ReadFile(p)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "hook elapsed=") {
			f := strings.Fields(strings.TrimPrefix(l, "hook elapsed="))
			if d, err := time.ParseDuration(f[0]); err == nil {
				return d, true
			}
		}
	}
	return 0, false
}

func med(ds []time.Duration) string {
	if len(ds) == 0 {
		return "-"
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	r := func(d time.Duration) string { return fmt.Sprintf("%.2f s", d.Seconds()) }
	if len(s) == 1 {
		return r(s[0])
	}
	return fmt.Sprintf("%s (%s to %s)", r(s[len(s)/2]), r(s[0]), r(s[len(s)-1]))
}
