// Command fuzzsmoke runs every Go fuzz target of the module briefly: the
// fuzz smoke run that S11-verification-and-spikes ("Fuzzing", "CI") asks
// for. `mise run go:fuzz` runs it from the module directory.
//
// It finds the targets with `go test -list '^Fuzz' -json ./...`, so a new
// target is covered without a change here, and runs each one in its own
// `go test -run '^$' -fuzz '^<name>$'` for -fuzztime. It stops at the
// first failure and exits 1. `go test` has then written the failing input
// under the package's testdata/fuzz, where it stays.
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
	"regexp"
	"slices"
	"strings"
)

// rule names the requirement in every decision fuzzsmoke prints.
const rule = "S11-verification-and-spikes, Fuzzing: every fuzz target passes a smoke run"

// target is one fuzz target: its import path and its function name.
type target struct {
	pkg  string
	name string
}

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
	if err := flags.Parse(args); err != nil {
		return 2
	}

	targets, err := list(ctx, *dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "fuzzsmoke: FAIL: cannot list the fuzz targets: %v (%s)\n", err, rule)
		return 1
	}
	if len(targets) == 0 {
		fmt.Fprintln(stdout, "fuzzsmoke: no fuzz targets")
		return 0
	}
	for _, t := range targets {
		fmt.Fprintf(stdout, "fuzzsmoke: run %s %s for %s\n", t.pkg, t.name, *fuzztime)
		if err := fuzz(ctx, *dir, t, *fuzztime, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "fuzzsmoke: FAIL %s %s: %v (%s)\n", t.pkg, t.name, err, rule)
			return 1
		}
		fmt.Fprintf(stdout, "fuzzsmoke: ok %s %s\n", t.pkg, t.name)
	}
	fmt.Fprintf(stdout, "fuzzsmoke: every fuzz target passed (%d)\n", len(targets))
	return 0
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
