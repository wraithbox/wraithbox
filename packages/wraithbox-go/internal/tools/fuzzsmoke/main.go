// Command fuzzsmoke runs every Go fuzz target of the module briefly: the
// fuzz smoke run that S11-verification-and-spikes ("Fuzzing", "CI") asks
// for. `mise run go:fuzz` runs it from the module directory.
//
// It finds the targets with `go test -list '^Fuzz' -json ./...`, so a new
// target is covered without a change here, and runs each one in its own
// `go test -run '^$' -fuzz '^<name>$'` for -fuzztime. It stops at the
// first failure and exits 1. `go test` has then written the failing input
// under the package's testdata/fuzz, where it stays, and fuzzsmoke prints
// it too, so a CI log keeps it after the runner is gone.
//
// With -shard <i>/<n> it runs only the i-th of n parts of the sorted list
// of targets, so CI can run the parts as parallel jobs. Each target is in
// exactly one part, and the parts differ in size by at most one target.
// Without -shard it runs every target, as `mise run go:fuzz` does.
//
// CI lists the targets on Linux with the default build tags, so a fuzz
// target goes in a file without a GOOS suffix or build constraint
// (S11-verification-and-spikes, "Fuzzing").
//
// It is a Go program, not a shell loop, so the task runs under Windows
// cmd.exe as S02-toolchain requires.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// rule names the requirement in every decision fuzzsmoke prints.
const rule = "S11-verification-and-spikes, Fuzzing: every fuzz target passes a smoke run"

// target is one fuzz target: its import path and its function name.
type target struct {
	pkg  string
	name string
}

// maxInputBytes caps how much of one failing input fuzzsmoke prints.
const maxInputBytes = 64 << 10

// fuzzName matches a line of `go test -list` output that names a fuzz
// target. `-list '^Fuzz'` prints the matching names one per line, among
// the package's other output.
var fuzzName = regexp.MustCompile(`^Fuzz\w*$`)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("fuzzsmoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", ".", "the Go module directory")
	fuzztime := flags.String("fuzztime", "10s", "how long to fuzz each target, as go test -fuzztime takes it")
	shardFlag := flags.String("shard", "", "run only part `i/n` of the targets, i from 1 to n; empty runs every target")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	part, err := parseShard(*shardFlag)
	if err != nil {
		fmt.Fprintf(stderr, "fuzzsmoke: %v\n", err)
		return 2
	}

	all, err := list(ctx, *dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "fuzzsmoke: FAIL: cannot list the fuzz targets: %v (%s)\n", err, rule)
		return 1
	}
	targets := part.of(all)
	if part.count > 1 {
		fmt.Fprintf(stdout, "fuzzsmoke: shard %d/%d runs %d of %d fuzz targets\n", part.index, part.count, len(targets), len(all))
	}
	if len(targets) == 0 {
		fmt.Fprintln(stdout, "fuzzsmoke: no fuzz targets")
		return 0
	}
	for _, t := range targets {
		fmt.Fprintf(stdout, "fuzzsmoke: run %s %s for %s\n", t.pkg, t.name, *fuzztime)
		// Whole seconds: some file systems keep coarse modification times.
		start := time.Now().Truncate(time.Second)
		if err := fuzz(ctx, *dir, t, *fuzztime, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "fuzzsmoke: FAIL %s %s: %v (%s)\n", t.pkg, t.name, err, rule)
			printNewInputs(ctx, *dir, t, start, stderr)
			return 1
		}
		fmt.Fprintf(stdout, "fuzzsmoke: ok %s %s\n", t.pkg, t.name)
	}
	fmt.Fprintf(stdout, "fuzzsmoke: every fuzz target passed (%d)\n", len(targets))
	return 0
}

// shard is one part of the list of targets: part index of count, with
// index from 1 to count.
type shard struct {
	index int
	count int
}

// parseShard reads the -shard option, `<i>/<n>` with 1 <= i <= n. An
// empty option is the one shard that holds every target.
func parseShard(s string) (shard, error) {
	if s == "" {
		return shard{index: 1, count: 1}, nil
	}
	is, ns, ok := strings.Cut(s, "/")
	i, iErr := strconv.Atoi(is)
	n, nErr := strconv.Atoi(ns)
	if !ok || iErr != nil || nErr != nil || n < 1 || i < 1 || i > n {
		return shard{}, fmt.Errorf("-shard %q: want <i>/<n> with 1 <= i <= n, such as 2/4", s)
	}
	return shard{index: i, count: n}, nil
}

// of returns the targets of this shard: a contiguous run of the sorted
// list, so the targets of one package mostly stay in one shard and share
// its build. Shard i of n takes the targets from (i-1)*len/n up to
// i*len/n, so every target is in exactly one shard, and a shard is empty
// only when there are more shards than targets.
func (s shard) of(targets []target) []target {
	lo := (s.index - 1) * len(targets) / s.count
	hi := s.index * len(targets) / s.count
	return targets[lo:hi]
}

// list runs `go test -list` over every package of the module in dir. A
// package that doesn't build fails the listing, and so the smoke run.
func list(ctx context.Context, dir string, stderr io.Writer) ([]target, error) {
	cmd := exec.CommandContext(ctx, "go", "test", "-list", "^Fuzz", "-json", "./...")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if runErr != nil {
		// The JSON stream holds the build errors; show it before failing.
		_, _ = io.Copy(stderr, &out)
		return nil, fmt.Errorf("go test -list: %w", runErr)
	}
	return parseList(&out)
}

// listEvent is the part of a `go test -json` event that parseList reads.
type listEvent struct {
	Action  string
	Package string
	Output  string
}

// parseList reads the `go test -list -json` stream and returns the fuzz
// targets, sorted by package and name. A line that isn't a JSON event
// is an error, not something to skip.
func parseList(r io.Reader) ([]target, error) {
	var targets []target
	dec := json.NewDecoder(bufio.NewReader(r))
	for {
		var ev listEvent
		err := dec.Decode(&ev)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading go test -list -json output: %w", err)
		}
		name := strings.TrimSuffix(ev.Output, "\n")
		if ev.Action != "output" || !fuzzName.MatchString(name) {
			continue
		}
		if ev.Package == "" {
			return nil, fmt.Errorf("fuzz target %s has no package in go test -list -json output", name)
		}
		targets = append(targets, target{pkg: ev.Package, name: name})
	}
	slices.SortFunc(targets, func(a, b target) int {
		if c := strings.Compare(a.pkg, b.pkg); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	return targets, nil
}

// fuzz runs one target for fuzztime, with the output of `go test` passed
// through. `-run '^$'` skips the package's other tests, which go:test
// runs.
func fuzz(ctx context.Context, dir string, t target, fuzztime string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "go", "test", "-run", "^$", "-fuzz", "^"+t.name+"$", "-fuzztime", fuzztime, t.pkg) //nolint:gosec // G204: the arguments come from the flags and from go test -list over the developer's own module, and no shell parses them
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go test -fuzz: %w", err)
	}
	return nil
}

// printNewInputs prints each input that go test wrote under the package's
// testdata/fuzz/<name> since start, with its path. An older file there is
// part of the committed corpus and is left out.
func printNewInputs(ctx context.Context, dir string, t target, since time.Time, w io.Writer) {
	dirs, err := packageDirs(ctx, dir)
	if err != nil {
		fmt.Fprintf(w, "fuzzsmoke: cannot find the failing input: %v\n", err)
		return
	}
	corpus := filepath.Join(dirs[t.pkg], "testdata", "fuzz", t.name)
	entries, err := os.ReadDir(corpus)
	if err != nil {
		fmt.Fprintf(w, "fuzzsmoke: no failing input in %s: %v\n", corpus, err)
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(since) {
			continue
		}
		printInput(filepath.Join(corpus, e.Name()), w)
	}
}

// packageDirs maps the import path of each package of the module in dir
// to its directory.
func packageDirs(ctx context.Context, dir string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-f", "{{.ImportPath}} {{.Dir}}", "./...")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	dirs := map[string]string{}
	for line := range strings.Lines(string(out)) {
		// An import path has no space, and a directory may.
		pkg, pkgDir, ok := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		if ok {
			dirs[pkg] = pkgDir
		}
	}
	return dirs, nil
}

// printInput prints the path and contents of one failing input, cut at
// maxInputBytes.
func printInput(path string, w io.Writer) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(w, "fuzzsmoke: cannot read the failing input: %v\n", err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		fmt.Fprintf(w, "fuzzsmoke: cannot read the failing input: %v\n", err)
		return
	}
	fmt.Fprintf(w, "fuzzsmoke: failing input %s:\n", path)
	_, _ = w.Write(data[:min(len(data), maxInputBytes)])
	if len(data) > maxInputBytes {
		fmt.Fprintf(w, "\nfuzzsmoke: cut at %d bytes\n", maxInputBytes)
	}
}
