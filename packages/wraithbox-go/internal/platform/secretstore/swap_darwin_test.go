//go:build darwin && cgo

package secretstore

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

const (
	// swapChildEnv makes TestC05AdhocSwap the child that reads its own
	// signature before and after the parent signs its bundle again.
	swapChildEnv = "WB_STORE_SWAP_CHILD"
	// swapBefore and swapAfter are the signing identifiers of the first
	// and the second signature of the child's bundle.
	swapBefore = "nl.wraithbox.swap-test.before"
	swapAfter  = "nl.wraithbox.swap-test.after"
	// swapExecutable is the child's executable in its bundle.
	swapExecutable = "wb-swap-test"
)

// TestC05AdhocSwap starts a copy of the test binary from a bundle signed
// ad hoc, signs the bundle again under another identifier while the copy
// runs, and sees the copy's open refused: the files on disk no longer
// match the running code, so the shim reads nothing from them
// (S15-02-secret-store, "The group of each call"; C05-credentials-and-store,
// "Access groups").
func TestC05AdhocSwap(t *testing.T) {
	if os.Getenv(swapChildEnv) == "1" {
		swapChild(t)
		return
	}
	bundle := swapBundle(t)
	signAdhoc(t, bundle, swapBefore)

	bin := filepath.Join(bundle, "Contents", "MacOS", swapExecutable)
	cmd := exec.CommandContext(t.Context(), bin, "-test.run=^TestC05AdhocSwap$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), swapChildEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	out := bufio.NewReader(stdout)
	var seen strings.Builder
	for !strings.Contains(seen.String(), "swap: ready\n") {
		line, err := out.ReadString('\n')
		seen.WriteString(line)
		if err != nil {
			_ = cmd.Wait()
			t.Fatalf("the child ended before it was ready: %v\n%s", err, seen.String())
		}
	}
	signAdhoc(t, bundle, swapAfter)
	if _, err := io.WriteString(stdin, "go\n"); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(out)
	seen.Write(rest)
	if err := cmd.Wait(); err != nil {
		t.Errorf("child: %v", err)
	}
	t.Logf("child:\n%s", seen.String())
}

// swapChild reads the signature once, waits for the parent to sign the
// bundle again, and reads it once more with a new backend, as a new Open
// would. Its failures fail the parent through its exit status.
func swapChild(t *testing.T) {
	code, err := (&keychain{}).Code()
	if err != nil || code.Identifier != swapBefore || code.Executable != swapExecutable {
		t.Fatalf("before the swap: %+v, %v; want %s, %s", code, err, swapBefore, swapExecutable)
	}
	// The parent reads this line from the child's standard output.
	if _, err := os.Stdout.WriteString("swap: ready\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatalf("waiting for the parent: %v", err)
	}

	after := &keychain{}
	code, err = after.Code()
	t.Logf("after the swap: %+v, %v", code, err)
	if err == nil || !strings.Contains(err.Error(), "OSStatus") || code.Identifier != "" {
		t.Errorf("after the swap: %+v, %v; want the signature refused", code, err)
	}
	if _, err := after.Groups(); err == nil {
		t.Error("after the swap, the groups were read")
	}
	_, err = New(storeitem.ProgramCredset, after, slog.New(slog.DiscardHandler))
	var e *Error
	if !errors.As(err, &e) || e.Rule != storeitem.RuleNeedsTeam {
		t.Errorf("open after the swap: %v, want %s", err, storeitem.RuleNeedsTeam)
	}
}

// swapBundle makes an unsigned bundle with a copy of the test binary as
// its executable, and returns its path.
func swapBundle(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "WBSwapTest.app")
	macos := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macos, swapExecutable), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>` + swapBefore + `</string>
	<key>CFBundleExecutable</key>
	<string>` + swapExecutable + `</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
</dict>
</plist>
`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
	return bundle
}

// signAdhoc signs bundle ad hoc with the signing identifier id.
func signAdhoc(t *testing.T, bundle, id string) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "/usr/bin/codesign", "--force", "--sign", "-", "--identifier", id, bundle).CombinedOutput()
	if err != nil {
		t.Fatalf("codesign %s: %v\n%s", id, err, out)
	}
}
