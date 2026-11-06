package hwkey_test

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	module     = "github.com/wraithbox/wraithbox/packages/wraithbox-go"
	hwkeyPkg   = module + "/internal/platform/hwkey"
	hwkeytest  = hwkeyPkg + "/hwkeytest"
	keyProgram = module + "/cmd/wb-proxyd"
)

// TestKeyImporters lists the dependencies of every program under cmd/
// for each GOOS, and sees that only wb-proxyd may depend on this
// package, the one program that uses the CA key (S15-03-access-rules,
// "Declared access"; SEC12-least-privilege), and none on its fake.
func TestKeyImporters(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		programs := programDeps(t, goos)
		for _, want := range []string{module + "/cmd/wb", module + "/cmd/wb-hostd", keyProgram} {
			if _, ok := programs[want]; !ok {
				t.Errorf("GOOS=%s: go list didn't name %s", goos, want)
			}
		}
		for program, deps := range programs {
			if slices.Contains(deps, hwkeytest) {
				t.Errorf("GOOS=%s: %s depends on %s", goos, program, hwkeytest)
			}
			if slices.Contains(deps, hwkeyPkg) && program != keyProgram {
				t.Errorf("GOOS=%s: %s depends on %s, and only %s may", goos, program, hwkeyPkg, keyProgram)
			}
		}
	}
}

// programDeps returns the dependencies of each program under cmd/, as
// `go list` gives them for goos.
func programDeps(t *testing.T, goos string) map[string][]string {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the test needs the go command: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), gobin, "list", "-f", `{{.ImportPath}}{{range .Deps}} {{.}}{{end}}`, module+"/cmd/...")
	cmd.Env = append(os.Environ(), "GOOS="+goos)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("GOOS=%s go list: %v", goos, err)
	}
	programs := map[string][]string{}
	for line := range strings.Lines(string(out)) {
		if fields := strings.Fields(line); len(fields) > 0 {
			programs[fields[0]] = fields[1:]
		}
	}
	return programs
}
