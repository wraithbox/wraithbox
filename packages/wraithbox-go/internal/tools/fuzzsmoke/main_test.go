package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestParseList(t *testing.T) {
	tests := []struct {
		name    string
		stream  string
		want    []target
		wantErr bool
	}{
		{name: "empty", stream: "", want: nil},
		{
			name: "no test files",
			stream: `{"Action":"start","Package":"m/b"}
{"Action":"output","Package":"m/b","Output":"?   \tm/b\t[no test files]\n"}
{"Action":"skip","Package":"m/b"}
`,
			want: nil,
		},
		{
			name: "sorted by package and name, other lines skipped",
			stream: `{"Action":"start","Package":"m/z"}
{"Action":"start","Package":"m/a"}
{"Action":"output","Package":"m/z","Output":"FuzzZ\n"}
{"Action":"output","Package":"m/a","Output":"FuzzB\n"}
{"Action":"output","Package":"m/a","Output":"FuzzA\n"}
{"Action":"output","Package":"m/a","Output":"ok  \tm/a\t0.1s\n"}
{"Action":"output","Package":"m/a","Output":"Fuzz target printed by init\n"}
{"Action":"build-output","ImportPath":"m/c","Output":"FuzzC\n"}
{"Action":"pass","Package":"m/a"}
`,
			want: []target{{"m/a", "FuzzA"}, {"m/a", "FuzzB"}, {"m/z", "FuzzZ"}},
		},
		{
			name:    "not JSON",
			stream:  "FuzzA\nok  \tm/a\t0.1s\n",
			wantErr: true,
		},
		{
			name:    "truncated event",
			stream:  `{"Action":"output","Package":"m/a","Output":"FuzzA\n"`,
			wantErr: true,
		},
		{
			name:    "target without a package",
			stream:  `{"Action":"output","Output":"FuzzA\n"}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseList(strings.NewReader(tt.stream))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseList() = %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseList() error = %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("parseList() = %v, want %v", got, tt.want)
			}
		})
	}
}

const passingTarget = `package a

import "testing"

func FuzzPass(f *testing.F) {
	f.Add("seed")
	f.Fuzz(func(t *testing.T, s string) {})
}
`

// failingTarget fails on any input longer than two bytes. Its only seed
// is shorter, so the fuzzer has to find the input and write it to
// testdata/fuzz.
const failingTarget = `package a

import "testing"

func FuzzFail(f *testing.F) {
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 2 {
			t.Fatalf("input of %d bytes", len(b))
		}
	})
}
`

// TestRun runs fuzzsmoke over a module in a temporary directory, the
// way `mise run go:fuzz` runs it over this one.
func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		fuzztime   string
		wantCode   int
		wantStdout string
		wantStderr string // a regular expression
		wantFile   string
	}{
		{
			name:       "no fuzz targets is a pass",
			files:      map[string]string{"a/a.go": "package a\n"},
			fuzztime:   "1s",
			wantCode:   0,
			wantStdout: "fuzzsmoke: no fuzz targets",
		},
		{
			name:       "passing target",
			files:      map[string]string{"a/a_test.go": passingTarget},
			fuzztime:   "100x",
			wantCode:   0,
			wantStdout: "fuzzsmoke: ok example.com/m/a FuzzPass",
		},
		{
			name:       "failing target fails and names the target",
			files:      map[string]string{"a/a_test.go": failingTarget},
			fuzztime:   "20s",
			wantCode:   1,
			wantStderr: `(?s)fuzzsmoke: FAIL example\.com/m/a FuzzFail: .*\nfuzzsmoke: failing input \S+FuzzFail.\w+:\ngo test fuzz v1\n`,
			wantFile:   "a/testdata/fuzz/FuzzFail",
		},
		{
			name:       "package that does not build fails the listing",
			files:      map[string]string{"a/a_test.go": "package a\n\nfunc broken( {}\n"},
			fuzztime:   "1s",
			wantCode:   1,
			wantStderr: "fuzzsmoke: FAIL: cannot list the fuzz targets",
		},
	}
	// The temporary module is not in this repository's go.work.
	t.Setenv("GOWORK", "off")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.files["go.mod"] = "module example.com/m\n\ngo 1.27\n"
			writeFiles(t, dir, tt.files)

			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"-dir", dir, "-fuzztime", tt.fuzztime}, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("run() = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, &stdout, &stderr)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout does not contain %q:\n%s", tt.wantStdout, &stdout)
			}
			if !regexp.MustCompile(tt.wantStderr).MatchString(stderr.String()) {
				t.Errorf("stderr does not match %q:\n%s", tt.wantStderr, &stderr)
			}
			if tt.wantFile != "" && countFiles(dir, tt.wantFile) == 0 {
				t.Errorf("no failing input kept in %s", tt.wantFile)
			}
		})
	}
}

// countFiles returns how many entries the directory sub of dir holds, and
// 0 when it doesn't exist.
func countFiles(dir, sub string) int {
	entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(sub)))
	if err != nil {
		return 0
	}
	return len(entries)
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
