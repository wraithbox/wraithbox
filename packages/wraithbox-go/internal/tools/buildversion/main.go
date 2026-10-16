// Command buildversion makes the one build version of a build and stamps
// it into the Go programs (S10-02-packaging-and-signing, "One build
// version"). `mise run` runs it from the module directory:
//
//	buildversion stamp     # version:stamp: write build/version at the repository root
//	buildversion check     # swift:build: check that build/version still describes the tree
//	buildversion go-build  # go:build: the same check, then build ./cmd/... into bin/ with it
//
// The version is the release number in VERSION at the repository root,
// then `+g` and the first 16 hex digits of the commit. A working tree
// with any change git sees, an untracked file included, adds `.dirty.`
// and a random nonce, so builds of a modified tree from two runs never
// match. `stamp` runs once per `mise run`, and every language's build
// task reads the same file, so Go and Swift share the nonce. Edits to a
// modified tree after the stamp aren't caught: see matchTree.
//
// A build without this stamp is unstamped and has no version
// (internal/version). So every step here fails rather than write or
// build with a version it can't vouch for.
//
// It is a Go program, not a shell script, so the tasks run under Windows
// cmd.exe as S02-toolchain requires.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// rule names the requirement in every decision buildversion prints.
const rule = "S10-02-packaging-and-signing, One build version"

const (
	// releaseFile holds the release number, relative to the repository root.
	releaseFile = "VERSION"
	// stampFile is where stamp writes the version, relative to the
	// repository root. It holds the version's bytes and nothing else, so
	// the Swift build can put the file into a Mach-O section as it is.
	stampFile = "build/version"
	// revLen is how many hex digits of the commit the version keeps.
	revLen = 16
	// nonceBytes is how many random bytes the nonce of a modified tree has.
	nonceBytes = 8
	// dirtyMark separates the revision from the nonce of a modified tree.
	dirtyMark = ".dirty."
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: buildversion stamp | check | go-build")
		return 2
	}
	var err error
	switch args[0] {
	case "stamp":
		err = stamp(ctx, ".", stdout)
	case "check":
		err = check(ctx, ".", stdout)
	case "go-build":
		err = goBuild(ctx, ".", stdout, stderr)
	default:
		fmt.Fprintf(stderr, "buildversion: unknown command %q\nusage: buildversion stamp | check | go-build\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "buildversion: FAIL: %v (%s)\n", err, rule)
		return 1
	}
	return 0
}

// tree is what git says about the working tree that dir is in.
type tree struct {
	root  string // the repository root
	rev   string // the first revLen hex digits of HEAD
	dirty bool   // any change git sees, untracked files included
}

// readTree asks git for the working tree's root, revision and state.
func readTree(ctx context.Context, dir string) (tree, error) {
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return tree{}, err
	}
	rev, err := git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return tree{}, err
	}
	if len(rev) < revLen || !isHex(rev) {
		return tree{}, fmt.Errorf("git rev-parse HEAD gave %q, not a commit hash", rev)
	}
	// --no-optional-locks: status doesn't write the index, so it never
	// takes index.lock from a git command that runs at the same time.
	status, err := git(ctx, dir, "--no-optional-locks", "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return tree{}, err
	}
	return tree{root: filepath.FromSlash(root), rev: rev[:revLen], dirty: status != ""}, nil
}

// stamp makes the version for the tree that dir is in and writes it to
// stampFile.
func stamp(ctx context.Context, dir string, stdout io.Writer) error {
	t, err := readTree(ctx, dir)
	if err != nil {
		return err
	}
	release, err := readRelease(filepath.Join(t.root, releaseFile))
	if err != nil {
		return err
	}
	nonce := ""
	if t.dirty {
		if nonce, err = newNonce(); err != nil {
			return err
		}
	}
	v := compose(release, t.rev, nonce)
	if !version.Valid(v) {
		return fmt.Errorf("the version %s is not in the version form", version.Label(v))
	}
	path := filepath.Join(t.root, filepath.FromSlash(stampFile))
	if err := writeAtomic(path, []byte(v)); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "buildversion: stamped %s into %s\n", v, stampFile)
	return nil
}

// compose builds the version from its parts. An empty nonce is a clean
// tree.
func compose(release, rev, nonce string) string {
	v := release + "+g" + rev
	if nonce != "" {
		v += dirtyMark + nonce
	}
	return v
}

// readRelease reads the release number: MAJOR.MINOR.PATCH with an
// optional prerelease part, on one line. The build part is the stamp's.
func readRelease(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading the release number: %w", err)
	}
	release := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if !version.Valid(release) || strings.Contains(release, "+") {
		return "", fmt.Errorf("%s holds %s, not MAJOR.MINOR.PATCH with an optional prerelease part", releaseFile, version.Label(release))
	}
	return release, nil
}

func newNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("making the nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// writeAtomic writes data to path through a temporary file and a rename,
// so a build task never reads half a version.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("writing the stamp: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".version-*")
	if err != nil {
		return fmt.Errorf("writing the stamp: %w", err)
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("writing the stamp: %w", err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("writing the stamp: %w", err)
	}
	return nil
}

// check prints the version stamp wrote, after checkedStamp. The Swift
// build runs it before it links the version in.
func check(ctx context.Context, dir string, stdout io.Writer) error {
	v, err := checkedStamp(ctx, dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "buildversion: %s still describes the tree\n", v)
	return nil
}

// checkedStamp reads the version stamp wrote, and checks that it still
// describes the tree that dir is in.
func checkedStamp(ctx context.Context, dir string) (string, error) {
	t, err := readTree(ctx, dir)
	if err != nil {
		return "", err
	}
	v, err := readStamp(filepath.Join(t.root, filepath.FromSlash(stampFile)))
	if err != nil {
		return "", err
	}
	if err := matchTree(v, t); err != nil {
		return "", err
	}
	return v, nil
}

// goBuild reads the version stamp wrote, checks that it still describes
// the tree, and builds the module's programs in dir into bin/ with it.
func goBuild(ctx context.Context, dir string, stdout, stderr io.Writer) error {
	v, err := checkedStamp(ctx, dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "buildversion: go build ./cmd/... into bin/ as %s\n", v)
	cmd := exec.CommandContext(ctx, "go", "build", ldflags(v), "-o", "bin/", "./cmd/...") //nolint:gosec // G204: v passed version.Valid, so it holds no space or shell byte, and no shell parses the arguments
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	return nil
}

// ldflags is the go build flag that stamps v. One argument, and no shell
// parses it. The linker splits it on spaces, and a version in the form
// has none.
func ldflags(v string) string {
	return "-ldflags=-X " + version.StampVar + "=" + v
}

// readStamp reads the version stamp wrote. A missing or malformed file
// is an error, never an unstamped build.
func readStamp(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading the stamp (run mise run version:stamp): %w", err)
	}
	v := string(data)
	if !version.Valid(v) {
		return "", fmt.Errorf("%s holds %s, which is not in the version form", stampFile, version.Label(v))
	}
	return v, nil
}

// matchTree fails when the stamp names another commit than the tree's,
// or says clean while the tree has changes: the tree changed after the
// stamp was made. A modified tree's stamp can't be checked against its
// changes; the nonce only keeps it apart from other builds.
func matchTree(v string, t tree) error {
	_, build, _ := strings.Cut(v, "+")
	rev, nonce, dirty := strings.Cut(build, dirtyMark)
	switch {
	case rev != "g"+t.rev:
		return fmt.Errorf("the stamp %s is for another commit than g%s; run mise run build", v, t.rev)
	case dirty && nonce == "":
		return fmt.Errorf("the stamp %s has no nonce", v)
	case !dirty && t.dirty:
		return fmt.Errorf("the stamp %s is for a clean tree, and the tree has changes; run mise run build", v)
	}
	return nil
}

// git runs git in dir and returns its output without the line end.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func isHex(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
