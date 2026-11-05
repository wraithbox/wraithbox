package main

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
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

func TestParseShard(t *testing.T) {
	tests := []struct {
		in      string
		want    shard
		wantErr bool
	}{
		{in: "", want: shard{index: 1, count: 1}},
		{in: "1/1", want: shard{index: 1, count: 1}},
		{in: "2/4", want: shard{index: 2, count: 4}},
		{in: "4/4", want: shard{index: 4, count: 4}},
		{in: "0/4", wantErr: true},
		{in: "5/4", wantErr: true},
		{in: "1/0", wantErr: true},
		{in: "-1/4", wantErr: true},
		{in: "1/-4", wantErr: true},
		{in: "2", wantErr: true},
		{in: "/4", wantErr: true},
		{in: "2/", wantErr: true},
		{in: "a/b", wantErr: true},
		{in: "1/2/3", wantErr: true},
		{in: " 1/2", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseShard(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseShard(%q) = %v, want an error", tt.in, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parseShard(%q) = %v, %v, want %v", tt.in, got, err, tt.want)
			}
		})
	}
}

// TestShardOf checks that the shards of a list hold every target exactly
// once, in order, and differ in size by at most one.
func TestShardOf(t *testing.T) {
	tests := []struct {
		targets int
		shards  int
	}{
		{0, 1},
		{0, 3},
		{1, 1},
		{1, 4},
		{3, 4},
		{4, 4},
		{5, 2},
		{34, 1},
		{34, 3},
		{34, 4},
		{37, 4},
		{100, 7},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d targets in %d shards", tt.targets, tt.shards), func(t *testing.T) {
			targets := make([]target, tt.targets)
			for i := range targets {
				targets[i] = target{pkg: "m/p", name: fmt.Sprintf("Fuzz%03d", i)}
			}
			var joined []target
			minLen, maxLen := tt.targets, 0
			for i := 1; i <= tt.shards; i++ {
				part := shard{index: i, count: tt.shards}.of(targets)
				joined = append(joined, part...)
				minLen, maxLen = min(minLen, len(part)), max(maxLen, len(part))
			}
			if !slices.Equal(joined, targets) {
				t.Errorf("shards joined = %v, want every target once in order: %v", joined, targets)
			}
			if maxLen-minLen > 1 {
				t.Errorf("shard sizes range from %d to %d, want a difference of at most 1", minLen, maxLen)
			}
		})
	}
}

func TestNewPlan(t *testing.T) {
	tests := []struct {
		name      string
		opts      options
		n, numCPU int
		want      plan
		wantErr   string
	}{
		{
			name: "local smoke run: 43 targets in 11 rounds of 4 share 60s",
			opts: options{fuzztime: "10s", longest: 10 * time.Second, budget: time.Minute, jobs: 4},
			n:    43, numCPU: 8,
			want: plan{fuzztime: "5.4s", jobs: 4, workers: 2},
		},
		{
			name: "a share is at most -fuzztime",
			opts: options{fuzztime: "10s", longest: 10 * time.Second, budget: time.Minute, jobs: 4},
			n:    11, numCPU: 4,
			want: plan{fuzztime: "10s", jobs: 4, workers: 1},
		},
		{
			name: "no more jobs than targets, and the CPUs go to the jobs that run",
			opts: options{fuzztime: "10s", longest: 10 * time.Second, budget: time.Minute, jobs: 4},
			n:    2, numCPU: 8,
			want: plan{fuzztime: "10s", jobs: 2, workers: 4},
		},
		{
			name: "daily run: 43 targets in 11 rounds of 4 share 10m",
			opts: options{fuzztime: "10m", longest: 10 * time.Minute, budget: 10 * time.Minute, jobs: 4},
			n:    43, numCPU: 4,
			want: plan{fuzztime: "54.5s", jobs: 4, workers: 1},
		},
		{
			name: "no budget: every target fuzzes for -fuzztime",
			opts: options{fuzztime: "100x", jobs: 4},
			n:    43, numCPU: 8,
			want: plan{fuzztime: "100x", jobs: 4, workers: 2},
		},
		{
			name: "a budget too small for the targets is refused",
			opts: options{fuzztime: "10s", longest: 10 * time.Second, budget: time.Second, jobs: 1},
			n:    11, numCPU: 8,
			wantErr: "-budget 1s leaves less than 100ms for each of 11 targets, 1 at a time",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newPlan(tt.opts, tt.n, tt.numCPU)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("newPlan() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("newPlan() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("newPlan() = %+v, want %+v", got, tt.want)
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
		budget     string // empty is 0: each target fuzzes for fuzztime
		jobs       string // empty is 4, the default
		shard      string
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
			name:       "a shard runs only its part",
			files:      map[string]string{"a/a_test.go": passingTarget, "a/b_test.go": failingTarget},
			fuzztime:   "100x",
			shard:      "2/2",
			wantCode:   0,
			wantStdout: "fuzzsmoke: shard 2/2 runs 1 of 2 fuzz targets\nfuzzsmoke: targets: 1, time each: 100x, at a time: 1",
		},
		{
			name:       "a shard with no targets is a pass",
			files:      map[string]string{"a/a_test.go": passingTarget},
			fuzztime:   "100x",
			shard:      "1/2",
			wantCode:   0,
			wantStdout: "fuzzsmoke: shard 1/2 runs 0 of 1 fuzz targets\nfuzzsmoke: no fuzz targets",
		},
		{
			name:       "a malformed shard is refused",
			files:      map[string]string{"a/a_test.go": passingTarget},
			fuzztime:   "100x",
			shard:      "3/2",
			wantCode:   2,
			wantStderr: `fuzzsmoke: -shard "3/2": want <i>/<n> with 1 <= i <= n`,
		},
		{
			name:       "the budget is shared by the targets",
			files:      map[string]string{"a/a_test.go": passingTarget, "b/b_test.go": strings.Replace(passingTarget, "package a", "package b", 1)},
			fuzztime:   "10s",
			budget:     "2s",
			jobs:       "1",
			wantCode:   0,
			wantStdout: "fuzzsmoke: targets: 2, time each: 1s, at a time: 1",
		},
		{
			name:       "targets fuzz at once",
			files:      map[string]string{"a/a_test.go": passingTarget, "b/b_test.go": strings.Replace(passingTarget, "package a", "package b", 1)},
			fuzztime:   "10s",
			budget:     "1s",
			jobs:       "2",
			wantCode:   0,
			wantStdout: "fuzzsmoke: targets: 2, time each: 1s, at a time: 2",
		},
		{
			name:       "no target starts after a failure",
			files:      map[string]string{"a/a_test.go": passingTarget, "a/b_test.go": failingTarget},
			fuzztime:   "20s",
			jobs:       "1",
			wantCode:   1,
			wantStderr: `fuzzsmoke: FAIL: 1 of 2 fuzz targets failed, 1 not started after the failure`,
		},
		{
			name:       "a budget with a count for -fuzztime is refused",
			files:      map[string]string{"a/a_test.go": passingTarget},
			fuzztime:   "100x",
			budget:     "60s",
			wantCode:   2,
			wantStderr: `fuzzsmoke: -budget 1m0s needs -fuzztime as a duration such as 10s, not "100x"`,
		},
		{
			name:       "fewer than one job is refused",
			files:      map[string]string{"a/a_test.go": passingTarget},
			fuzztime:   "100x",
			jobs:       "0",
			wantCode:   2,
			wantStderr: `fuzzsmoke: -jobs 0: want at least 1`,
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
			// An empty -shard is the same as none: every target.
			args := []string{"-dir", dir, "-fuzztime", tt.fuzztime, "-budget", cmp.Or(tt.budget, "0"), "-jobs", cmp.Or(tt.jobs, "4"), "-shard", tt.shard}
			code := run(context.Background(), args, &stdout, &stderr)
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
