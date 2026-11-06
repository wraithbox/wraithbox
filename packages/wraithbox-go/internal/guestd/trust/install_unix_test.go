//go:build darwin || linux

package trust

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"

	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fileowner"
)

// checkFiles checks the trust directory and its three files after an
// install of cas: the modes and the owner, ca.pem holds only the CAs in
// order, bundle.pem begins with the public roots and then holds the
// CAs, and the Java trust store holds exactly the bundle's
// certificates.
func (f *fixture) checkFiles(cas ...[]byte) {
	f.t.Helper()
	f.checkMode(f.in.dir, fs.ModeDir|0o755)
	for _, name := range []string{CAFile, BundleFile, JavaTrustStoreFile} {
		f.checkMode(f.path(name), 0o644)
	}
	if got := pemCerts(f.t, f.read(CAFile)); !equalDERs(got, cas) {
		f.t.Errorf("%s holds %d certificates, want only the %d CAs in order", CAFile, len(got), len(cas))
	}
	want := make([][]byte, 0, len(f.roots)+len(cas))
	for _, r := range f.roots {
		want = append(want, r.Raw)
	}
	want = append(want, cas...)
	bundle := pemCerts(f.t, f.read(BundleFile))
	if !equalDERs(bundle, want) {
		f.t.Errorf("%s holds %d certificates, want the %d public roots and then the %d CAs",
			BundleFile, len(bundle), len(f.roots), len(cas))
	}
	store, err := pkcs12.DecodeTrustStore(f.read(JavaTrustStoreFile), "")
	if err != nil {
		f.t.Fatalf("DecodeTrustStore: %v", err)
	}
	got := make([][]byte, 0, len(store))
	for _, c := range store {
		got = append(got, c.Raw)
	}
	if !equalDERs(got, bundle) {
		f.t.Errorf("%s holds %d certificates, not exactly the bundle's %d", JavaTrustStoreFile, len(got), len(bundle))
	}
	if names := slices.Sorted(maps.Keys(f.snapshot())); !slices.Equal(names, []string{BundleFile, CAFile, JavaTrustStoreFile}) {
		f.t.Errorf("the trust directory holds %q, want only the three files", names)
	}
}

// checkMode checks a path's mode and that this process's user owns it.
func (f *fixture) checkMode(path string, want fs.FileMode) {
	f.t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		f.t.Fatal(err)
	}
	if info.Mode() != want {
		f.t.Errorf("%s: mode %v, want %v", path, info.Mode(), want)
	}
	uid, err := fileowner.UID(info)
	if err != nil || uid != f.in.owner {
		f.t.Errorf("%s: owner %d, %v; want %d", path, uid, err, f.in.owner)
	}
}

func equalDERs(a, b [][]byte) bool { return slices.EqualFunc(a, b, bytes.Equal) }

// TestC02TrustFiles: one CA and then two give the three files with the
// right modes and owner, and an install with one CA after one with two
// leaves only that CA in every file. The directory is made when it is
// missing, under a umask that would take its mode below 0755.
func TestC02TrustFiles(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	f := newFixture(t)
	caA := selfSigned(t, "Wraith Box CA A", true, newP256(t))
	caB := selfSigned(t, "Wraith Box CA B", true, newP256(t))
	f.install(caA)
	f.checkFiles(caA)
	f.install(caA, caB)
	f.checkFiles(caA, caB)
	f.install(caB)
	f.checkFiles(caB)
	for _, name := range []string{CAFile, BundleFile} {
		if bytes.Contains(f.read(name), encodePEM([]*x509.Certificate{parse(t, caA)})) {
			t.Errorf("%s still holds the dropped CA", name)
		}
	}
}

// TestC02TrustRefused: a request that isn't 1 to 3 distinct P-256 CAs is
// refused with trust-install-refused, the files are unchanged, and the
// log names the rule and the fingerprint.
func TestC02TrustRefused(t *testing.T) {
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := func() []byte { return selfSigned(t, "Wraith Box CA", true, newP256(t)) }
	one := ca()
	for _, tc := range []struct {
		name string
		ders [][]byte
	}{
		{"none", nil},
		{"four", [][]byte{ca(), ca(), ca(), ca()}},
		{"leaf", [][]byte{selfSigned(t, "leaf", false, newP256(t))}},
		{"RSA key", [][]byte{rsaCA(t)}},
		{"P-384 key", [][]byte{selfSigned(t, "P-384 CA", true, p384)}},
		{"garbage", [][]byte{[]byte("not a certificate")}},
		{"empty", [][]byte{{}}},
		{"trailing bytes", [][]byte{append(ca(), 0)}},
		{"over 1024 bytes", [][]byte{bytes.Repeat([]byte{0x30}, MaxCASize+1)}},
		{"twice", [][]byte{one, one}},
		{"one good and one leaf", [][]byte{ca(), selfSigned(t, "leaf", false, newP256(t))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			first := ca()
			f.install(first)
			before := f.snapshot()
			f.wantRefusal(f.in.Install(t.Context(), tc.ders), RuleRefused)
			if after := f.snapshot(); !maps.Equal(before, after) {
				t.Fatal("the trust directory changed")
			}
			if len(tc.ders) > 0 && !strings.Contains(f.logs.String(), Fingerprint(tc.ders[0])) {
				t.Errorf("logs lack the fingerprint:\n%s", f.logs)
			}
		})
	}
}

// TestC02TrustDirUnsafe: a trust directory that is a symbolic link, a
// file, has mode 0777, the setgid bit, or another owner is refused with
// trust-dir-unsafe, and nothing is written.
func TestC02TrustDirUnsafe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		make  func(t *testing.T, f *fixture, elsewhere string)
		owner bool
	}{
		{name: "symbolic link", make: func(t *testing.T, f *fixture, elsewhere string) {
			mustDo(t, os.Symlink(elsewhere, f.in.dir))
		}},
		{name: "file", make: func(t *testing.T, f *fixture, _ string) {
			mustDo(t, os.WriteFile(f.in.dir, nil, 0o644))
		}},
		{name: "mode 0777", make: func(t *testing.T, f *fixture, _ string) {
			mustDo(t, os.Mkdir(f.in.dir, 0o755))
			mustDo(t, os.Chmod(f.in.dir, 0o777))
		}},
		{name: "mode 0700", make: func(t *testing.T, f *fixture, _ string) {
			mustDo(t, os.Mkdir(f.in.dir, 0o700))
		}},
		{name: "setgid", make: func(t *testing.T, f *fixture, _ string) {
			mustDo(t, os.Mkdir(f.in.dir, 0o755))
			mustDo(t, os.Chmod(f.in.dir, 0o755|os.ModeSetgid))
		}},
		{name: "another owner", owner: true, make: func(t *testing.T, f *fixture, _ string) {
			mustDo(t, os.Mkdir(f.in.dir, 0o755))
			mustDo(t, os.Chmod(f.in.dir, 0o755))
			mustDo(t, os.Chown(f.in.dir, 1, -1))
		}},
		{name: "expected owner differs", make: func(t *testing.T, f *fixture, _ string) {
			mustDo(t, os.Mkdir(f.in.dir, 0o755))
			mustDo(t, os.Chmod(f.in.dir, 0o755))
			f.in.owner++
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.owner && os.Geteuid() != 0 {
				t.Skip("changing a directory's owner needs root")
			}
			f := newFixture(t)
			elsewhere := t.TempDir()
			mustDo(t, os.MkdirAll(strings.TrimSuffix(f.in.dir, "/trust"), 0o755))
			tc.make(t, f, elsewhere)
			f.wantRefusal(f.in.Install(t.Context(), [][]byte{selfSigned(t, "Wraith Box CA", true, newP256(t))}), RuleDirUnsafe)
			wantEmpty(t, elsewhere, f.in.dir)
		})
	}
}

// TestInstallRootsFailed: public roots that are missing, not a regular
// file, or hold no certificate fail the install with
// trust-install-failed before anything is written.
func TestInstallRootsFailed(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(t *testing.T, path string)
	}{
		{"missing", func(t *testing.T, path string) { mustDo(t, os.Remove(path)) }},
		{"directory", func(t *testing.T, path string) {
			mustDo(t, os.Remove(path))
			mustDo(t, os.Mkdir(path, 0o755))
		}},
		{"no certificate", func(t *testing.T, path string) { mustDo(t, os.WriteFile(path, []byte("none"), 0o644)) }},
		{"too large", func(t *testing.T, path string) {
			mustDo(t, os.WriteFile(path, bytes.Repeat([]byte{'a'}, MaxRootsSize+1), 0o644))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.make(t, f.in.roots)
			f.wantRefusal(f.in.Install(t.Context(), [][]byte{selfSigned(t, "Wraith Box CA", true, newP256(t))}), RuleFailed)
			if s := f.snapshot(); s != nil {
				t.Errorf("wrote %v", slices.Collect(maps.Keys(s)))
			}
		})
	}
}

// TestInstallLog: a success logs one line with each CA's fingerprint,
// the rule and the decision.
func TestInstallLog(t *testing.T) {
	f := newFixture(t)
	caA := selfSigned(t, "Wraith Box CA A", true, newP256(t))
	caB := selfSigned(t, "Wraith Box CA B", true, newP256(t))
	f.install(caA, caB)
	lines := strings.Split(strings.TrimSpace(f.logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("%d log lines, want 1:\n%s", len(lines), f.logs)
	}
	for _, want := range []string{
		`"rule":"` + RuleInstalled + `"`, `"decision":"accept"`, Fingerprint(caA), Fingerprint(caB),
		`"public_roots":2`, `"public_roots_skipped":1`,
	} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("log line lacks %s:\n%s", want, lines[0])
		}
	}
}

// TestInstallReplacesLink: a trust file that is a symbolic link is
// replaced, not followed.
func TestInstallReplacesLink(t *testing.T) {
	f := newFixture(t)
	ca := selfSigned(t, "Wraith Box CA", true, newP256(t))
	f.install(ca)
	elsewhere := f.path("../outside.pem")
	mustDo(t, os.WriteFile(elsewhere, []byte("outside"), 0o644))
	mustDo(t, os.Remove(f.path(CAFile)))
	mustDo(t, os.Symlink(elsewhere, f.path(CAFile)))
	f.install(ca)
	f.checkFiles(ca)
	if data, _ := os.ReadFile(elsewhere); string(data) != "outside" {
		t.Errorf("the link's target changed to %q", data)
	}
}

// wantRefusal checks that r has rule, and that the log has the rule and
// the decision.
func (f *fixture) wantRefusal(r *Refusal, rule string) {
	f.t.Helper()
	if r == nil || r.Rule != rule {
		f.t.Fatalf("Install = %v, want %s", r, rule)
	}
	logs := f.logs.String()
	if !strings.Contains(logs, `"rule":"`+rule+`"`) || !strings.Contains(logs, `"decision":"refuse"`) {
		f.t.Errorf("logs lack the rule and decision:\n%s", logs)
	}
}

// wantEmpty checks that each path that is a directory, not a link to
// one, holds nothing.
func wantEmpty(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			continue
		}
		if names, _ := os.ReadDir(dir); len(names) != 0 {
			t.Errorf("%s holds %v", dir, names)
		}
	}
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
