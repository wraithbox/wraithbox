package appbundle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeBundle makes a small bundle at dir that holds marker in a program
// file and a resource, and a symbolic link where the file system can make
// one. It reports whether it made the link.
func writeBundle(t *testing.T, dir, marker string) (linked bool) {
	t.Helper()
	for path, data := range map[string]string{
		"Contents/MacOS/wb":        "program " + marker,
		"Contents/Info.plist":      "plist " + marker,
		"Contents/Resources/extra": "only in " + marker,
	} {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Windows makes a symbolic link only with a privilege the test may
	// not have.
	return os.Symlink("MacOS/wb", filepath.Join(dir, "Contents", "link")) == nil
}

// TestC12BundleInstallSwap installs two bundles in turn at one
// destination, and sees the second replace the first with no file of the
// first left, and nothing left next to it (S17-05-upgrades-and-migrations,
// "Upgrades").
func TestC12BundleInstallSwap(t *testing.T) {
	src := t.TempDir()
	first, second := filepath.Join(src, "first.app"), filepath.Join(src, "second.app")
	writeBundle(t, first, "first")
	linked := writeBundle(t, second, "second")
	if err := os.Remove(filepath.Join(second, "Contents", "Resources", "extra")); err != nil {
		t.Fatal(err)
	}
	apps := filepath.Join(t.TempDir(), "Applications")
	dest := filepath.Join(apps, AppName)

	var log bytes.Buffer
	if err := Install(first, dest, verifyOK, &log); err != nil {
		t.Fatal(err)
	}
	assertFile(t, dest, "Contents/MacOS/wb", "program first")
	if strings.Contains(log.String(), "2. rename") {
		t.Errorf("the first install renamed an installed bundle aside:\n%s", log.String())
	}

	log.Reset()
	if err := Install(second, dest, verifyOK, &log); err != nil {
		t.Fatal(err)
	}
	assertFile(t, dest, "Contents/MacOS/wb", "program second")
	assertFile(t, dest, "Contents/Info.plist", "plist second")
	if _, err := os.Lstat(filepath.Join(dest, "Contents", "Resources", "extra")); !os.IsNotExist(err) {
		t.Errorf("a file of the first bundle is left: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(dest, "Contents", "link")); linked && (err != nil || filepath.ToSlash(target) != "MacOS/wb") {
		t.Errorf("the link is %q, %v; want MacOS/wb", target, err)
	}
	assertSameMode(t, filepath.Join(second, "Contents", "MacOS", "wb"), filepath.Join(dest, "Contents", "MacOS", "wb"))
	for _, step := range []string{"1. copy", "2. rename", "3. rename", "4. delete"} {
		if !strings.Contains(log.String(), step) {
			t.Errorf("the second install has no step %q:\n%s", step, log.String())
		}
	}
	entries, err := os.ReadDir(apps)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{AppName}) {
		t.Errorf("next to the installed bundle: %v, want only %s", names, AppName)
	}
}

func verifyOK(string) error { return nil }

// TestInstallRefuses sees each wrong source or destination refused, and
// a destination that isn't a bundle left as it was.
func TestInstallRefuses(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.app")
	writeBundle(t, src, "x")
	file := filepath.Join(src, "Contents", "Info.plist")
	notBundle := filepath.Join(t.TempDir(), AppName)
	if err := os.MkdirAll(filepath.Join(notBundle, "Documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][2]string{
		"a missing bundle":           {filepath.Join(src, "missing"), filepath.Join(t.TempDir(), AppName)},
		"a file":                     {file, filepath.Join(t.TempDir(), AppName)},
		"itself":                     {src, src},
		"a path inside src":          {src, filepath.Join(src, "Contents", AppName)},
		"a destination named else":   {src, t.TempDir()},
		"a destination not a bundle": {src, notBundle},
	} {
		if err := Install(args[0], args[1], verifyOK, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: installed", name)
		}
	}
	if _, err := os.Stat(filepath.Join(notBundle, "Documents")); err != nil {
		t.Errorf("the directory that isn't a bundle changed: %v", err)
	}
}

// TestInstallVerifiesTheCopy: a copy that doesn't verify is never
// renamed in, and the installed bundle stays.
func TestInstallVerifiesTheCopy(t *testing.T) {
	src := t.TempDir()
	first, second := filepath.Join(src, "first.app"), filepath.Join(src, "second.app")
	writeBundle(t, first, "first")
	writeBundle(t, second, "second")
	dest := filepath.Join(t.TempDir(), AppName)
	if err := Install(first, dest, verifyOK, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var verified string
	refuse := func(path string) error {
		verified = path
		return errors.New("refused")
	}
	if err := Install(second, dest, refuse, &bytes.Buffer{}); err == nil {
		t.Fatal("installed a copy that doesn't verify")
	}
	if verified == "" || verified == second {
		t.Errorf("verified %q, want the copy", verified)
	}
	assertFile(t, dest, "Contents/MacOS/wb", "program first")
}

func assertSameMode(t *testing.T, src, dst string) {
	t.Helper()
	want, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.Stat(dst)
	if err != nil || got.Mode() != want.Mode() {
		t.Errorf("the copy's mode is %v, %v; want %v", got, err, want.Mode())
	}
}

func assertFile(t *testing.T, root, rel, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || string(got) != want {
		t.Errorf("%s holds %q, %v; want %q", rel, got, err, want)
	}
}
