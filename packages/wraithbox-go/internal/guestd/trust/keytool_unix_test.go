//go:build darwin || linux

package trust

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestC02TrustKeytool: when a JDK's keytool is on PATH, it lists the Java
// trust store without a password, with every certificate of the bundle
// as a trusted entry. It is skipped without a working keytool. The run
// in a macOS guest with a real JVM is M3.9e's.
func TestC02TrustKeytool(t *testing.T) {
	keytool, err := exec.LookPath("keytool")
	if err != nil {
		t.Skip("no keytool on PATH")
	}
	// macOS has a /usr/bin/keytool that fails without a JDK.
	if out, err := exec.CommandContext(t.Context(), keytool, "-help").CombinedOutput(); err != nil {
		t.Skipf("keytool doesn't run: %v\n%s", err, out)
	}
	f := newFixture(t)
	f.install(selfSigned(t, "Wraith Box CA A", true, newP256(t)), selfSigned(t, "Wraith Box CA B", true, newP256(t)))
	cmd := exec.CommandContext(t.Context(), keytool, "-list", "-storetype", "PKCS12",
		"-keystore", f.path(JavaTrustStoreFile))
	// keytool asks for the password on standard input. An empty line is
	// no password.
	cmd.Stdin = strings.NewReader("\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("keytool -list: %v\n%s", err, out)
	}
	want := len(f.roots) + 2
	if !strings.Contains(string(out), fmt.Sprintf("Your keystore contains %d entries", want)) ||
		strings.Count(string(out), "trustedCertEntry") != want {
		t.Fatalf("keytool -list doesn't show %d trusted entries:\n%s", want, out)
	}
	t.Logf("keytool -list:\n%s", out)
}
