package trust

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestC02TrustEnv: the six variables of S09-08-guest-trust, "Variables", with
// exact values, each built from the fixed paths.
func TestC02TrustEnv(t *testing.T) {
	want := []string{
		"SSL_CERT_FILE=/etc/wraithbox/trust/bundle.pem",
		"REQUESTS_CA_BUNDLE=/etc/wraithbox/trust/bundle.pem",
		"GIT_SSL_CAINFO=/etc/wraithbox/trust/bundle.pem",
		"CARGO_HTTP_CAINFO=/etc/wraithbox/trust/bundle.pem",
		"NODE_EXTRA_CA_CERTS=/etc/wraithbox/trust/ca.pem",
		"JAVA_TOOL_OPTIONS=-Djavax.net.ssl.trustStore=/etc/wraithbox/trust/java-truststore.p12",
	}
	if got := Env(); !slices.Equal(got, want) {
		t.Fatalf("Env =\n%q\nwant\n%q", got, want)
	}
	names := Names()
	if len(names) != len(want) {
		t.Fatalf("Names = %q", names)
	}
	for i, n := range names {
		if !strings.HasPrefix(want[i], n+"=") {
			t.Errorf("Names()[%d] = %q, want the name of %q", i, n, want[i])
		}
	}
	// A caller can't change the table through the slice it got.
	Env()[0] = "SSL_CERT_FILE=/tmp/x"
	if Env()[0] != want[0] {
		t.Fatal("Env shares its slice")
	}
}

// TestPathsPlain: the trust paths hold no whitespace and no character a
// shell treats specially (S09-08-guest-trust, "Files").
func TestPathsPlain(t *testing.T) {
	plain := regexp.MustCompile(`^[A-Za-z0-9/._-]+$`)
	for _, p := range []string{Dir, CAPath, BundlePath, JavaTrustStorePath, PublicRootsPath} {
		if !plain.MatchString(p) {
			t.Errorf("%q holds a character outside [A-Za-z0-9/._-]", p)
		}
	}
}

// TestC02NoSystemTrustStore: nothing in wb-guestd adds a CA to the macOS
// guest's system trust store (S09-08-guest-trust, "System trust store"),
// because macOS asks a user to confirm a trusted root. The test reads
// every Go file outside tests of wb-guestd's own code: cmd/wb-guestd,
// internal/guestd, and the internal/platform/guest* packages.
func TestC02NoSystemTrustStore(t *testing.T) {
	forbidden := []string{
		"add-trusted-cert", "add-certificates", "SecTrustSettings", "SecCertificateAddToKeychain",
		"SecItemAdd", "trust-settings-import", "update-ca-certificates", "update-ca-trust",
	}
	platform, err := filepath.Glob("../../platform/guest*")
	if err != nil || len(platform) == 0 {
		t.Fatalf("no internal/platform/guest* packages: %v", err)
	}
	files := goFiles(t, append([]string{"../../../cmd/wb-guestd", ".."}, platform...))
	if len(files) < 20 {
		t.Fatalf("found %d Go files, want all of wb-guestd's", len(files))
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range forbidden {
			if strings.Contains(string(data), s) {
				t.Errorf("%s holds %q", path, s)
			}
		}
	}
}

// goFiles lists the Go files under dirs that aren't tests.
func goFiles(t *testing.T, dirs []string) []string {
	t.Helper()
	var files []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}
