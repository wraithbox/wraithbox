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

func TestReadStampGo(t *testing.T) {
	dir := t.TempDir()
	stamped := buildProgram(t, filepath.Join(dir, "stamped"), testStamp)
	if got, err := ReadStamp(stamped); err != nil || got != testStamp {
		t.Errorf("ReadStamp of a stamped program = %q, %v; want %q", got, err, testStamp)
	}
	bare := buildProgram(t, filepath.Join(dir, "bare"), "")
	if got, err := ReadStamp(bare); err != nil || got != "" {
		t.Errorf("ReadStamp of an unstamped program = %q, %v; want none", got, err)
	}
	if isMachO, err := IsMachO(bare); err != nil || !isMachO {
		t.Errorf("IsMachO of a darwin program = %v, %v", isMachO, err)
	}
}

func TestReadStampRefusesOtherFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "text")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStamp(path); err == nil {
		t.Error("ReadStamp read a stamp from a text file")
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
	if want := []string{"Contents/MacOS/wb", "Contents/Resources/notes.txt"}; !slices.Equal(got.MachO, want) {
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
