package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// The bundle's Go programs run on Apple Silicon only (NFR04-host-platforms).
const (
	bundleGOOS   = "darwin"
	bundleGOARCH = "arm64"
)

// goBuildCommand builds each Go program of the layout table into -out,
// for darwin/arm64, stamped with the version in -stamp. It runs in the
// module's directory. It builds only the table's programs, never
// `./cmd/...`, so no dev tool ends up in the bundle.
func goBuildCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("go-build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stampFile := fs.String("stamp", "", "the file that holds the run's build version")
	out := fs.String("out", "", "the directory to build into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *stampFile == "" || *out == "" || fs.NArg() != 0 {
		return errUsage
	}
	stamp, err := readStamp(*stampFile)
	if err != nil {
		return err
	}
	for _, p := range appbundle.Programs {
		if p.Source != appbundle.SourceGo {
			continue
		}
		fmt.Fprintf(stdout, "go-build: %s for %s/%s as %s\n", p.Name, bundleGOOS, bundleGOARCH, stamp)
		cmd := exec.CommandContext(ctx, "go", goBuildArgs(p.Name, stamp, *out)...) //nolint:gosec // G204: the names come from the layout table, and readStamp checked the version form, which has no space or shell byte
		cmd.Env = append(os.Environ(), goBuildEnv()...)
		cmd.Stdout, cmd.Stderr = stdout, stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go build ./cmd/%s: %w", p.Name, err)
		}
	}
	return nil
}

// goBuildEnv is what go-build sets over the environment: the bundle's
// target, and an empty GOFLAGS. A -trimpath there would leave -ldflags
// out of the build information, where the check reads the stamp
// (appbundle.ReadLinked). An empty GOFLAGS still leaves a value set
// with `go env -w`, so goBuildArgs turns -trimpath off as well.
// exec.Cmd keeps the last value of a repeated variable.
func goBuildEnv() []string {
	return []string{"GOOS=" + bundleGOOS, "GOARCH=" + bundleGOARCH, "GOFLAGS="}
}

// goBuildArgs are the arguments of `go build` for one program: no
// -trimpath, whatever GOFLAGS says (a flag on the command line wins), the
// stamp as internal/tools/buildversion links it, and the output file.
func goBuildArgs(name, stamp, out string) []string {
	return []string{
		"build",
		"-trimpath=false",
		"-ldflags=-X " + version.StampVar + "=" + stamp,
		"-o", filepath.Join(out, name),
		"./cmd/" + name,
	}
}

// readStamp reads the run's build version. A missing file, or one that
// isn't a version in the form, is an error: the bundle is never built or
// checked against no version.
func readStamp(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading the stamp (run mise run version:stamp): %w", err)
	}
	if v := string(data); version.Valid(v) {
		return v, nil
	}
	return "", fmt.Errorf("%s holds %s, which is not a build version", path, version.Label(string(data)))
}
