package appbundle

import (
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// buildProgram builds cmd/wb-credset for darwin/arm64 into dir, as the
// bundle build does, stamped with stamp unless it is "".
func buildProgram(t *testing.T, dir, stamp string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a program")
	}
	out := filepath.Join(dir, "wb-credset")
	args := []string{"build", "-o", out}
	if stamp != "" {
		args = append(args, "-ldflags=-X "+version.StampVar+"="+stamp)
	}
	cmd := exec.CommandContext(t.Context(), "go", append(args, "../../cmd/wb-credset")...)
	cmd.Env = append(os.Environ(), "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=0")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, msg)
	}
	return out
}

func TestReadLinkedGo(t *testing.T) {
	dir := t.TempDir()
	want := Linked{Stamp: testStamp, GoMain: testModule + "/cmd/wb-credset"}
	stamped := buildProgram(t, filepath.Join(dir, "stamped"), testStamp)
	if got, err := ReadLinked(stamped); err != nil || got != want {
		t.Errorf("ReadLinked of a stamped program = %+v, %v; want %+v", got, err, want)
	}
	bare := buildProgram(t, filepath.Join(dir, "bare"), "")
	if got, err := ReadLinked(bare); err != nil || got != (Linked{GoMain: want.GoMain}) {
		t.Errorf("ReadLinked of an unstamped program = %+v, %v; want no stamp", got, err)
	}
	if isMachO, err := IsMachO(bare); err != nil || !isMachO {
		t.Errorf("IsMachO of a darwin program = %v, %v", isMachO, err)
	}
}

// TestReadLinkedRefusesOtherFiles: a text file and a fat file are
// errors, so the check stops rather than pass a program it can't read.
func TestReadLinkedRefusesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"text":  []byte("#!/bin/sh\n"),
		"fat64": {0xca, 0xfe, 0xba, 0xbf, 0, 0, 0, 1},
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLinked(path); err == nil {
			t.Errorf("ReadLinked read the %s file", name)
		}
	}
}

func TestGoStamp(t *testing.T) {
	cases := []struct {
		ldflags string
		want    string
	}{
		{"-X " + version.StampVar + "=" + testStamp, testStamp},
		{"-s -w -X " + version.StampVar + "=" + testStamp, testStamp},
		{"-X main.stamp=" + testStamp, ""},
		{"-X " + version.StampVar + "x=" + testStamp, ""},
		{"", ""},
	}
	for _, tc := range cases {
		info := &buildinfo.BuildInfo{Settings: []debug.BuildSetting{{Key: "-ldflags", Value: tc.ldflags}}}
		if got := goStamp(info); got != tc.want {
			t.Errorf("goStamp(%q) = %q, want %q", tc.ldflags, got, tc.want)
		}
	}
}

func TestIsMachOMagic(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		data []byte
		want bool
	}{
		"thin64-le": {[]byte{0xcf, 0xfa, 0xed, 0xfe, 0, 0}, true},
		"thin32-le": {[]byte{0xce, 0xfa, 0xed, 0xfe}, true},
		"thin64-be": {[]byte{0xfe, 0xed, 0xfa, 0xcf}, true},
		"fat":       {[]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 2}, true},
		"fat-le":    {[]byte{0xbe, 0xba, 0xfe, 0xca}, true},
		"fat64":     {[]byte{0xca, 0xfe, 0xba, 0xbf, 0, 0, 0, 2}, true},
		"fat64-le":  {[]byte{0xbf, 0xba, 0xfe, 0xca}, true},
		"elf":       {[]byte{0x7f, 'E', 'L', 'F'}, false},
		"short":     {[]byte{0xcf, 0xfa}, false},
		"empty":     {nil, false},
	}
	for name, tc := range cases {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, tc.data, 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := IsMachO(path); err != nil || got != tc.want {
			t.Errorf("%s: IsMachO = %v, %v; want %v", name, got, err, tc.want)
		}
	}
}

// TestWalk finds a Mach-O file by its magic number wherever it is and
// whatever its name, every profile, and the symbolic links, without
// following them.
func TestWalk(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"Contents/MacOS/wb":                                      {0xcf, 0xfa, 0xed, 0xfe},
		"Contents/Resources/notes.txt":                           {0xcf, 0xfa, 0xed, 0xfe, 1},
		"Contents/Resources/universal":                           {0xca, 0xfe, 0xba, 0xbf, 0, 0, 0, 2},
		"Contents/Resources/readme":                              []byte("text"),
		"Contents/Helpers/wb-proxyd.app/Contents/" + ProfileName: []byte("profile"),
	}
	for rel, data := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	linked := os.Symlink(filepath.Join(root, "Contents", "MacOS", "wb"), filepath.Join(root, "Contents", "MacOS", "wb-link")) == nil

	got, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Contents/MacOS/wb", "Contents/Resources/notes.txt", "Contents/Resources/universal"}; !slices.Equal(got.MachO, want) {
		t.Errorf("Mach-O files %v, want %v", got.MachO, want)
	}
	if want := []string{"Contents/Helpers/wb-proxyd.app/Contents/" + ProfileName}; !slices.Equal(got.Profiles, want) {
		t.Errorf("profiles %v, want %v", got.Profiles, want)
	}
	if linked && !slices.Equal(got.Links, []string{"Contents/MacOS/wb-link"}) {
		t.Errorf("links %v, want the one link", got.Links)
	}
}

func TestWalkMissingRoot(t *testing.T) {
	if _, err := Walk(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("Walk of a missing root succeeded")
	}
}
