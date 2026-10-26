package codesign

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// The tools ReadTeam and ReadProfile run, by absolute path, with only
// toolEnv as their environment. So neither a PATH entry nor a variable
// such as DYLD_INSERT_LIBRARIES in wb's own environment changes what
// they do.
const (
	codesignPath = "/usr/bin/codesign"
	securityPath = "/usr/bin/security"
)

var toolEnv = []string{"PATH=/usr/bin:/bin"}

// ReadTeam returns the team of the code signature of the program or
// bundle at path, or "" for a signature without one, such as an ad-hoc
// signature (ParseTeam). It runs `codesign -d --verbose=1`. An unsigned
// file, or one codesign can't read, is an error.
func ReadTeam(ctx context.Context, path string) (string, error) {
	out, err := run(ctx, codesignPath, "-d", "--verbose=1", "--", path)
	if err != nil {
		return "", fmt.Errorf("codesign -d %s: %w: %s", path, err, strings.TrimSpace(out.stderr))
	}
	// codesign prints the details on standard error.
	return ParseTeam(out.stderr)
}

// ReadProfile decodes the provisioning profile at path with `security
// cms -D -i`, and parses it (ParseProfile). Output past MaxPlistSize
// bytes is dropped, and DecodePlist refuses the rest for its size.
func ReadProfile(ctx context.Context, path string) (Profile, error) {
	out, err := run(ctx, securityPath, "cms", "-D", "-i", path)
	if err != nil {
		return Profile{}, fmt.Errorf("security cms -D %s: %w: %s", path, err, strings.TrimSpace(out.stderr))
	}
	return ParseProfile([]byte(out.stdout))
}

type output struct{ stdout, stderr string }

// run runs a tool with toolEnv, and returns what it printed.
func run(ctx context.Context, tool string, args ...string) (output, error) {
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = toolEnv
	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return output{stdout.String(), stderr.String()}, err
}

// limitedBuffer keeps the first MaxPlistSize+1 bytes written to it, and
// drops the rest.
type limitedBuffer struct{ buf bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := MaxPlistSize + 1 - b.buf.Len()
	if room <= 0 {
		return len(p), nil
	}
	if room < len(p) {
		b.buf.Write(p[:room])
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) String() string { return b.buf.String() }
