package hostgit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests run this test binary as a stand-in git. Each stand-in is a
// link to (or copy of) the binary named fakePrefix + mode, and its
// name picks what it does. The name is the only signal it gets, because
// Command passes it no variable of the test's environment. A stand-in
// named plain git is the spy that a PATH lookup would find.
const fakePrefix = "git-fake-"

func TestMain(m *testing.M) {
	name := filepath.Base(os.Args[0])
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if name == "git" {
		os.Exit(runFake("spy", os.Args[1:]))
	}
	if mode, ok := strings.CutPrefix(name, fakePrefix); ok {
		os.Exit(runFake(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeOutput is what the env stand-in prints.
type fakeOutput struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
	Dir  string   `json:"dir"`
}

func runFake(mode string, args []string) int {
	switch mode {
	case "old":
		fmt.Print("git version 2.53.9\n")
	case "rc":
		fmt.Print("git version 2.54.0.rc1\n")
	case "ok":
		fmt.Print("git version 2.54.0 (Apple Git-157)\nsecond line\n")
	case "fail":
		fmt.Fprint(os.Stderr, "xcrun: error: invalid active developer path\n")
		return 1
	case "hang":
		time.Sleep(time.Hour)
	case "flood":
		// More than versionMaxOutput, with no line end.
		fmt.Print(strings.Repeat("v", 64<<10))
	case "env":
		dir, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(fakeOutput{Args: args, Env: os.Environ(), Dir: dir})
	case "spy":
		// Leaves a mark next to itself, so a test can see that it ran.
		if self, err := os.Executable(); err == nil {
			_ = os.WriteFile(filepath.Join(filepath.Dir(self), "ran"), nil, 0o600)
		}
		fmt.Print("git version 2.54.0\n")
	default:
		fmt.Fprintf(os.Stderr, "unknown stand-in mode %q\n", mode)
		return 2
	}
	return 0
}

// fakeGit puts a stand-in git with the given mode in a new directory and
// returns its absolute path.
func fakeGit(t *testing.T, mode string) string {
	t.Helper()
	return install(t, t.TempDir(), fakePrefix+mode)
}

// install links (or copies) this test binary into dir as name, with the
// binary's .exe extension on Windows.
func install(t *testing.T, dir, name string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(filepath.Ext(self), ".exe") {
		name += ".exe"
	}
	dst := filepath.Join(dir, name)
	if err := os.Link(self, dst); err == nil {
		return dst
	}
	copyFile(t, self, dst)
	return dst
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o700); err != nil {
		t.Fatal(err)
	}
}
