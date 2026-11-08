package main

import (
	"bytes"
	"encoding/base64"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/confirm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/trust"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmcmd"
)

// TestC02GuestTrustVM boots the work VM as TestC11HostdVM does, with a
// fake wb-proxyd end in the child that issues the VM's CA with
// wb-proxyd's CA authority and the software fake key (trustVar), and
// runs the CA record's boot order from the link's boot time call. In
// `wb shell`s of a project it adds, it checks the trust files, the
// variables and the system keychains of S09-08-guest-trust, and runs
// the clients of X04-tls-inspection against a TLS server on the guest's
// loopback that presents a leaf the CA signed. The leaf, its key and
// the server reach the guest only on the shell's standard input
// (SEC02-no-host-fs-share). Then it drops the CA with `wb vm reset-ca`,
// starts the VM again with a new CA, and checks that the old leaf is
// refused, the new one accepted, and no file holds the old CA.
//
// It is needs-vm and needs-host, like TestC11HostdVM, and claims and
// removes <data> the same way. It runs with the variables and the `go
// test` command of `mise run go:test-vm`, with `-run
// '^TestC02GuestTrustVM$'` in place of that task's own. It logs each
// client's outcome with "result:", and a client the fixture image
// doesn't have as not run.
func TestC02GuestTrustVM(t *testing.T) {
	r := newVMRun(t)
	leaves := filepath.Join(r.scratch, "trust")
	if err := os.RemoveAll(leaves); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(leaves, 0o700); err != nil {
		t.Fatal(err)
	}
	r.childEnv = []string{trustVar + "=" + leaves}
	server := buildTLSServer(t, r.scratch)
	r.cloneFixture()
	// child.out is appended to: only this run's part is read.
	var before int64
	if fi, err := os.Stat(filepath.Join(r.scratch, "child.out")); err == nil {
		before = fi.Size()
	}
	r.startChild(filepath.Join(r.app, "Contents", "MacOS", "wb-hostd"))
	defer r.checkFakeLog(before)

	gen1 := r.startWithCA()
	id := r.addProject("c02trust")
	first := r.recordCAs(1)
	r.checkTrustFiles(id, first, nil)
	oldLeaf := r.readLeaf(leaves, gen1)
	r.checkClients(id, server, oldLeaf, "the first CA's leaf", true)

	if code, _ := r.wb("stop", "work"); code != 0 {
		t.Fatalf("wb vm stop work: exit %d, want 0", code)
	}
	r.resetCA()
	gen2 := r.startWithCA()
	second := r.recordCAs(1)
	if second[0].ID == first[0].ID {
		t.Fatalf("the record holds CA %s after wb vm reset-ca and a start, want a new one", first[0].ID)
	}
	r.checkTrustFiles(id, second, first)
	r.checkClients(id, server, oldLeaf, "the dropped CA's leaf", false)
	r.checkClients(id, server, r.readLeaf(leaves, gen2), "the new CA's leaf", true)
	if code, _ := r.wb("stop", "work"); code != 0 {
		t.Errorf("wb vm stop work: exit %d, want 0", code)
	}
}

// buildTLSServer builds testdata/tlsserver for the guest, darwin/arm64,
// into dir, and returns the binary.
func buildTLSServer(t *testing.T, dir string) []byte {
	t.Helper()
	out := filepath.Join(dir, "tlsserver")
	build := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "./testdata/tlsserver")
	build.Env = append(os.Environ(), "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=0")
	if msg, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build testdata/tlsserver: %v\n%s", err, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// startWithCA starts the work VM, waits until the fake wb-proxyd end
// wrote the leaf of the run's CA set, and returns the run's generation.
func (r *vmRun) startWithCA() uint64 {
	t := r.t
	began := time.Now()
	if code, _ := r.wb("start", "work"); code != 0 {
		t.Fatalf("wb vm start work: exit %d, want 0", code)
	}
	r.waitRecord("guest data mount accepted", began, 2*time.Minute)
	r.waitRecord("CA installed", began, 2*time.Minute)
	_, out := r.wb("status", "work")
	generation, err := strconv.ParseUint(generationOf(t, out), 10, 64)
	if err != nil {
		t.Fatalf("wb vm status: %v", err)
	}
	t.Logf("measure: start to the CA install %v, generation %d", time.Since(began), generation)
	return generation
}

// readLeaf waits for the leaf the fake wrote for generation.
func (r *vmRun) readLeaf(dir string, generation uint64) []byte {
	t := r.t
	deadline := time.Now().Add(time.Minute)
	for {
		b, err := os.ReadFile(leafPath(dir, generation))
		if err == nil {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("no leaf of generation %d: %v", generation, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// checkFakeLog fails the test when the child's fake wb-proxyd end was
// ended for a reply, or the boot order failed, in the child's output
// past offset.
func (r *vmRun) checkFakeLog(offset int64) {
	b, err := os.ReadFile(filepath.Join(r.scratch, "child.out"))
	if err != nil || int64(len(b)) < offset {
		r.t.Errorf("the child's output: %d bytes, %v", len(b), err)
		return
	}
	for line := range strings.Lines(string(b[offset:])) {
		if strings.Contains(line, msgFakeEnded) || strings.Contains(line, msgBootFailed) {
			r.t.Errorf("child: %s", line)
		}
	}
}

// recordCAs returns the work VM's CAs in state.db, oldest first, and
// fails the test unless there are n.
func (r *vmRun) recordCAs(n int) []state.CA {
	t := r.t
	db, err := state.Open(t.Context(), r.paths.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var cas []state.CA
	err = db.CAs(t.Context(), func(tx *state.CATx) error {
		cas, err = tx.List(string(vm.SlotWork))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cas) != n {
		t.Fatalf("the work VM's record holds %d CAs, want %d", len(cas), n)
	}
	return cas
}

// resetCA runs `wb vm reset-ca work`, and answers its confirmation.
func (r *vmRun) resetCA() {
	t := r.t
	var out bytes.Buffer
	term := &answerTerminal{in: strings.NewReader(confirm.Answer + "\n")}
	env := vmcmd.SystemEnv(slog.New(slog.DiscardHandler))
	env.Stdout, env.Stderr = &out, &out
	env.OpenTerminal = func() (io.ReadWriteCloser, error) { return term, nil }
	code := vmcmd.Run(t.Context(), env, []string{"reset-ca", "work"})
	t.Logf("wb vm reset-ca work: exit %d\n%s\nterminal:\n%s", code, out.String(), term.out.String())
	if code != 0 {
		t.Fatalf("wb vm reset-ca work: exit %d, want 0", code)
	}
}

// answerTerminal is wb's own terminal for a confirmation: it reads the
// answer from in, and keeps what wb writes.
type answerTerminal struct {
	in  io.Reader
	out bytes.Buffer
}

func (a *answerTerminal) Read(p []byte) (int, error)  { return a.in.Read(p) }
func (a *answerTerminal) Write(p []byte) (int, error) { return a.out.Write(p) }
func (a *answerTerminal) Close() error                { return nil }

// trustScript prints the user, the environment, the owner, mode and
// type of the trust directory, its parent and each entry in it, the
// trust files' bytes, and the certificates of the system keychains.
const trustScript = "id -un\n" +
	"echo --env--; env; echo --end--\n" +
	"echo --stat--\n" +
	"stat -f '%Su %Lp %Sp %N' /etc/wraithbox " + trust.Dir + "\n" +
	"for f in $(ls -A " + trust.Dir + "); do stat -f '%Su %Lp %Sp %N' " + trust.Dir + "/$f; done\n" +
	"echo --end-stat--\n" +
	"for f in " + trust.CAFile + " " + trust.BundleFile + " " + trust.JavaTrustStoreFile + "; do\n" +
	"  echo \"--file $f--\"; base64 -i " + trust.Dir + "/$f; echo --end-file--\n" +
	"done\n" +
	"echo --keychains--\n" +
	"security find-certificate -a -Z /Library/Keychains/System.keychain /System/Library/Keychains/SystemRootCertificates.keychain 2>&1\n" +
	"echo \"--end-keychains $?--\"\n"

// checkTrustFiles runs trustScript as the project user of id, and checks
// the trust variables, the files and their modes against cas, the
// record's CAs (S09-08-guest-trust, "Files"), that no file holds a CA of
// dropped, and that no system keychain holds any of them
// (S09-08-guest-trust, "System trust store").
func (r *vmRun) checkTrustFiles(id string, cas, dropped []state.CA) {
	t := r.t
	res := r.runShell(id, shellIO{stdin: strings.NewReader(trustScript), lookup: hostVars})
	if res.code != 0 {
		t.Errorf("the trust script: exit %d\n%s", res.code, res.stderr)
	}
	user, _, _ := strings.Cut(res.stdout, "\n")
	checkEnv(t, res.stdout, user)
	checkTrustModes(t, between(res.stdout, "--stat--\n", "--end-stat--"))
	want := ders(cas)
	gone := ders(dropped)
	caFile := pemCerts(t, trust.CAFile, decodeFile(t, res.stdout, trust.CAFile))
	if !slices.EqualFunc(caFile, want, bytes.Equal) {
		t.Errorf("%s holds %d certificates, not exactly the record's %d CAs", trust.CAFile, len(caFile), len(want))
	}
	bundle := pemCerts(t, trust.BundleFile, decodeFile(t, res.stdout, trust.BundleFile))
	if len(bundle) <= len(want) || !slices.EqualFunc(bundle[len(bundle)-len(want):], want, bytes.Equal) {
		t.Errorf("%s holds %d certificates, want the public roots and then the record's %d CAs", trust.BundleFile, len(bundle), len(want))
	}
	store, err := pkcs12.DecodeTrustStore(decodeFile(t, res.stdout, trust.JavaTrustStoreFile), "")
	if err != nil {
		t.Errorf("%s: %v", trust.JavaTrustStoreFile, err)
	}
	java := make([][]byte, 0, len(store))
	for _, c := range store {
		java = append(java, c.Raw)
	}
	if !sameSet(java, bundle) {
		t.Errorf("%s holds %d certificates, not the bundle's %d", trust.JavaTrustStoreFile, len(java), len(bundle))
	}
	for _, files := range []struct {
		name  string
		certs [][]byte
	}{{trust.CAFile, caFile}, {trust.BundleFile, bundle}, {trust.JavaTrustStoreFile, java}} {
		for _, g := range gone {
			if slices.ContainsFunc(files.certs, func(c []byte) bool { return bytes.Equal(c, g) }) {
				t.Errorf("%s holds the dropped CA", files.name)
			}
		}
	}
	checkKeychains(t, res.stdout, append(slices.Clone(cas), dropped...))
}

// checkTrustModes checks the stat lines of trustScript: the parent is a
// directory owned by root that its group and others can't write, the
// trust directory has mode 0755, and it holds exactly the three files,
// each a regular file owned by root with mode 0644.
func checkTrustModes(t *testing.T, block string) {
	t.Helper()
	var files []string
	for line := range strings.Lines(block) {
		f := strings.Fields(line)
		if len(f) != 4 {
			t.Errorf("stat line %q, want owner, mode, permissions and path", line)
			continue
		}
		t.Logf("result: %s", strings.Join(f, " "))
		if name, ok := checkTrustEntry(t, f[0], f[1], f[2], f[3]); ok {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	if want := []string{trust.BundleFile, trust.CAFile, trust.JavaTrustStoreFile}; !slices.Equal(files, want) {
		t.Errorf("%s holds %v, want exactly %v", trust.Dir, files, want)
	}
}

// checkTrustEntry checks one stat line of trustScript, and returns the
// name of a file in the trust directory.
func checkTrustEntry(t *testing.T, owner, mode, perms, path string) (string, bool) {
	t.Helper()
	m, err := strconv.ParseUint(mode, 8, 32)
	if err != nil || owner != "root" {
		t.Errorf("%s: owner %s, mode %s; want root", path, owner, mode)
	}
	switch path {
	case "/etc/wraithbox":
		if perms[0] != 'd' || m&0o022 != 0 {
			t.Errorf("%s: %s, want a directory its group and others can't write", path, perms)
		}
		return "", false
	case trust.Dir:
		if perms[0] != 'd' || m != 0o755 {
			t.Errorf("%s: %s, want a directory with mode 0755", path, perms)
		}
		return "", false
	}
	if perms[0] != '-' || m != 0o644 {
		t.Errorf("%s: %s, want a regular file with mode 0644", path, perms)
	}
	return filepath.Base(path), true
}

// checkKeychains checks the `security find-certificate -Z` output of
// trustScript: it listed the system keychains' certificates, and none
// is a CA of cas.
func checkKeychains(t *testing.T, stdout string, cas []state.CA) {
	t.Helper()
	block := between(stdout, "--keychains--\n", "--end-keychains")
	if !strings.Contains(stdout, "--end-keychains 0--") {
		t.Errorf("security find-certificate failed:\n%s", block)
	}
	var hashes []string
	for line := range strings.Lines(block) {
		if h, ok := strings.CutPrefix(strings.TrimSpace(line), "SHA-256 hash: "); ok {
			hashes = append(hashes, strings.ToLower(h))
		}
	}
	t.Logf("result: the system keychains hold %d certificates", len(hashes))
	if len(hashes) == 0 {
		t.Errorf("security find-certificate listed no certificate, so the check proves nothing:\n%s", block)
	}
	for _, ca := range cas {
		if slices.Contains(hashes, ca.SHA256) {
			t.Errorf("a system keychain holds the CA %s (S09-08-guest-trust, \"System trust store\")", ca.SHA256)
		}
	}
	if strings.Contains(block, "Wraith Box") {
		t.Errorf("a system keychain holds a certificate that names Wraith Box:\n%s", block)
	}
}

// between returns the text of s after start and before end, or "".
func between(s, start, end string) string {
	_, rest, ok := strings.Cut(s, start)
	if !ok {
		return ""
	}
	block, _, _ := strings.Cut(rest, end)
	return block
}

// decodeFile returns the bytes of the trust file name in trustScript's
// output.
func decodeFile(t *testing.T, stdout, name string) []byte {
	t.Helper()
	text := between(stdout, "--file "+name+"--\n", "--end-file--")
	b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
	if err != nil || len(b) == 0 {
		t.Errorf("%s: %d bytes, %v", name, len(b), err)
	}
	return b
}

// pemCerts returns the DER bytes of each CERTIFICATE block of b, and
// fails the test on any other block.
func pemCerts(t *testing.T, name string, b []byte) [][]byte {
	t.Helper()
	var out [][]byte
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			t.Errorf("%s holds a %s block", name, block.Type)
			continue
		}
		out = append(out, block.Bytes)
	}
	return out
}

func ders(cas []state.CA) [][]byte {
	out := make([][]byte, 0, len(cas))
	for _, ca := range cas {
		out = append(out, ca.Certificate)
	}
	return out
}

// sameSet reports whether a and b hold the same certificates, in any
// order.
func sameSet(a, b [][]byte) bool {
	sa := slices.SortedFunc(slices.Values(a), bytes.Compare)
	sb := slices.SortedFunc(slices.Values(b), bytes.Compare)
	return slices.EqualFunc(sa, sb, bytes.Equal)
}
