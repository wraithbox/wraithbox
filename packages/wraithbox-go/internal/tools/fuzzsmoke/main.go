// Command fuzzsmoke fuzzes every Go fuzz target of the module for a share
// of a time budget. It runs the fuzz smoke run that
// S11-verification-and-spikes ("Fuzzing", "CI") asks for, and the longer
// daily fuzz run. `mise run go:fuzz` and `mise run go:fuzz-long` run it
// from the module directory.
//
// It finds the targets with `go test -list '^Fuzz' -json ./...`, so a new
// target is covered without a change here, and runs each one in its own
// `go test -run '^$' -fuzz '^<name>$'`.
//
// It fuzzes -jobs targets at once, 4 by default, and gives each `go test`
// an equal part of the CPUs as fuzz workers (-parallel). -budget is how
// long the fuzzing takes in all. The targets run in rounds of -jobs, and
// each fuzzes for the budget divided by the number of rounds, at most
// -fuzztime. Building the test binaries takes time on top. With
// -budget 0 each target fuzzes for -fuzztime, which may then be a count
// such as 100x.
//
// After the first failure it starts no more targets, lets the running
// ones finish, and exits 1. `go test` has then written the failing input
// under the package's testdata/fuzz, where it stays, and fuzzsmoke prints
// it too, so a CI log keeps it after the runner is gone. Each target's
// output is printed whole when the target is done, so the outputs of
// targets that run at once don't mix.
//
// With -shard <i>/<n> it runs only the i-th of n parts of the sorted list
// of targets, so CI can run the parts as parallel jobs. Each target is in
// exactly one part, and the parts differ in size by at most one target.
// The budget is the shard's. Without -shard it runs every target.
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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// shareStep is what a target's share of the budget is cut down to a
// multiple of, so the time it prints reads as 5.4s and not 5.454545454s.
const shareStep = 100 * time.Millisecond

// fuzzName matches a line of `go test -list` output that names a fuzz
// target. `-list '^Fuzz'` prints the matching names one per line, among
// the package's other output.
var fuzzName = regexp.MustCompile(`^Fuzz\w*$`)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o, ok := parseFlags(args, stderr)
	if !ok {
		return 2
	}

	all, err := list(ctx, o.dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "fuzzsmoke: FAIL: cannot list the fuzz targets: %v (%s)\n", err, rule)
		return 1
	}
	targets := o.part.of(all)
	if o.part.count > 1 {
		fmt.Fprintf(stdout, "fuzzsmoke: shard %d/%d runs %d of %d fuzz targets\n", o.part.index, o.part.count, len(targets), len(all))
	}
	if len(targets) == 0 {
		fmt.Fprintln(stdout, "fuzzsmoke: no fuzz targets")
		return 0
	}

	p, err := newPlan(o, len(targets), runtime.NumCPU())
	if err != nil {
		fmt.Fprintf(stderr, "fuzzsmoke: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "fuzzsmoke: targets: %d, time each: %s, at a time: %d, workers each: %d\n", len(targets), p.fuzztime, p.jobs, p.workers)
	failed, skipped := fuzzAll(ctx, o.dir, targets, p, stdout, stderr)
	if failed > 0 {
		fmt.Fprintf(stderr, "fuzzsmoke: FAIL: %d of %d fuzz targets failed, %d not started after the failure (%s)\n", failed, len(targets), skipped, rule)
		return 1
	}
	fmt.Fprintf(stdout, "fuzzsmoke: every fuzz target passed (%d)\n", len(targets))
	return 0
}

// options are the command-line flags of one run.
type options struct {
	dir      string
	fuzztime string        // the longest one target fuzzes, as go test takes it
	longest  time.Duration // fuzztime as a duration, when budget is set
	budget   time.Duration // 0 for none
	jobs     int
	part     shard
}

// parseFlags reads the flags, and prints why when they are wrong.
func parseFlags(args []string, stderr io.Writer) (options, bool) {
	flags := flag.NewFlagSet("fuzzsmoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var o options
	flags.StringVar(&o.dir, "dir", ".", "the Go module directory")
	flags.StringVar(&o.fuzztime, "fuzztime", "5s", "the longest one target fuzzes, as go test -fuzztime takes it")
	flags.DurationVar(&o.budget, "budget", 60*time.Second, "how long the fuzzing takes in all, shared equally by the targets; 0 fuzzes each target for -fuzztime")
	flags.IntVar(&o.jobs, "jobs", 4, "how many targets fuzz at once")
	shardFlag := flags.String("shard", "", "run only part `i/n` of the targets, i from 1 to n; empty runs every target")
	if err := flags.Parse(args); err != nil {
		return options{}, false
	}
	var err error
	o.part, err = parseShard(*shardFlag)
	switch {
	case err != nil:
	case o.jobs < 1:
		err = fmt.Errorf("-jobs %d: want at least 1", o.jobs)
	case o.budget < 0:
		err = fmt.Errorf("-budget %s: want 0 or more", o.budget)
	case o.budget > 0:
		o.longest, err = time.ParseDuration(o.fuzztime)
		if err != nil {
			err = fmt.Errorf("-budget %s needs -fuzztime as a duration such as 10s, not %q", o.budget, o.fuzztime)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "fuzzsmoke: %v\n", err)
		return options{}, false
	}
	return o, true
}

// plan is how a run fuzzes its targets.
type plan struct {
	fuzztime string // each target's go test -fuzztime
	jobs     int    // how many targets fuzz at once
	workers  int    // each target's go test -parallel
}

// newPlan shares the budget of o among n targets, o.jobs at a time, on
// numCPU CPUs. The targets run in rounds of jobs, so each gets the budget
// divided by the number of rounds, cut to a multiple of shareStep and at
// most o.longest. n is at least 1.
func newPlan(o options, n, numCPU int) (plan, error) {
	p := plan{fuzztime: o.fuzztime, jobs: min(o.jobs, n)}
	p.workers = max(1, numCPU/p.jobs)
	if o.budget == 0 {
		return p, nil
	}
	rounds := (n + p.jobs - 1) / p.jobs
	share := min(o.longest, o.budget/time.Duration(rounds)).Truncate(shareStep)
	if share <= 0 {
		return plan{}, fmt.Errorf("-budget %s leaves less than %s for each of %d targets, %d at a time", o.budget, shareStep, n, p.jobs)
	}
	p.fuzztime = share.String()
	return p, nil
}

// result is how fuzzing one target went.
type result struct {
	t      target
	ran    bool
	start  time.Time
	output bytes.Buffer // go test's stdout and stderr
	err    error
}

// fuzzAll fuzzes the targets, p.jobs at a time, and reports each one when
// it is done. After the first failure it starts no more targets. It
// returns how many targets failed and how many it didn't start.
func fuzzAll(ctx context.Context, dir string, targets []target, p plan, stdout, stderr io.Writer) (failed, skipped int) {
	work := make(chan target)
	results := make(chan *result)
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range p.jobs {
		wg.Go(func() { fuzzWorker(ctx, dir, p, work, results, &stop) })
	}
	go func() {
		for _, t := range targets {
			work <- t
		}
		close(work)
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if !r.ran {
			skipped++
			continue
		}
		if r.err != nil {
			failed++
		}
		report(ctx, dir, p, r, stdout, stderr)
	}
	return failed, skipped
}

// fuzzWorker fuzzes the targets it takes from work, one at a time, and
// passes each result on. Once stop is set it passes the targets on
// without fuzzing them. A failure sets stop before its result is passed
// on, so this worker's next target already sees it.
func fuzzWorker(ctx context.Context, dir string, p plan, work <-chan target, results chan<- *result, stop *atomic.Bool) {
	for t := range work {
		r := &result{t: t}
		if !stop.Load() {
			fuzz(ctx, dir, p, r)
		}
		if r.err != nil {
			stop.Store(true)
		}
		results <- r
	}
}

// report prints the output of one target that ran, and whether it
// passed. For a failure it prints the input that made it fail.
func report(ctx context.Context, dir string, p plan, r *result, stdout, stderr io.Writer) {
	fmt.Fprintf(stdout, "fuzzsmoke: fuzzed %s %s for %s\n", r.t.pkg, r.t.name, p.fuzztime)
	_, _ = stdout.Write(r.output.Bytes())
	if r.err != nil {
		fmt.Fprintf(stderr, "fuzzsmoke: FAIL %s %s: %v (%s)\n", r.t.pkg, r.t.name, r.err, rule)
		printNewInputs(ctx, dir, r.t, r.start, stderr)
		return
	}
	fmt.Fprintf(stdout, "fuzzsmoke: ok %s %s\n", r.t.pkg, r.t.name)
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

// fuzz runs one target for p.fuzztime with p.workers fuzz workers, and
// keeps the output of `go test` in r. `-run '^$'` skips the package's
// other tests, which go:test runs.
func fuzz(ctx context.Context, dir string, p plan, r *result) {
	r.ran = true
	// Whole seconds: some file systems keep coarse modification times.
	r.start = time.Now().Truncate(time.Second)
	cmd := exec.CommandContext(ctx, "go", "test", "-run", "^$", "-fuzz", "^"+r.t.name+"$", "-fuzztime", p.fuzztime, "-parallel", strconv.Itoa(p.workers), r.t.pkg) //nolint:gosec // G204: the arguments come from the flags and from go test -list over the developer's own module, and no shell parses them
	cmd.Dir = dir
	// One buffer for both, so the lines stay in order. exec writes to
	// it from one goroutine at a time when Stdout and Stderr are equal.
	cmd.Stdout = &r.output
	cmd.Stderr = &r.output
	if err := cmd.Run(); err != nil {
		r.err = fmt.Errorf("go test -fuzz: %w", err)
	}
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
