package secretstore_test

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	module       = "github.com/wraithbox/wraithbox/packages/wraithbox-go"
	storePkg     = module + "/internal/platform/secretstore"
	storetestPkg = storePkg + "/storetest"
)

// storePrograms are the programs that may link the secret store
// (S10-01-libraries; S15-02-secret-store, "Keychain"). wb-guestadmin is
// the one program of the wb-guestadmin-* run kinds.
var storePrograms = map[string]bool{
	module + "/cmd/wb-credset":    true,
	module + "/cmd/wb-proxyd":     true,
	module + "/cmd/wb-guestadmin": true,
}

// TestStoreImporters lists the dependencies of every program under cmd/
// for each GOOS, and sees only the programs with store access depend on
// this package, and none on its fake (S10-01-libraries). wb holds no
// group (S15-02-secret-store, "wb doesn't hold a group").
func TestStoreImporters(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		programs := programDeps(t, goos)
		for _, want := range expectedPrograms() {
			if _, ok := programs[want]; !ok {
				t.Errorf("GOOS=%s: go list didn't name %s", goos, want)
			}
		}
		for program, deps := range programs {
			if slices.Contains(deps, storetestPkg) {
				t.Errorf("GOOS=%s: %s depends on %s", goos, program, storetestPkg)
			}
			if slices.Contains(deps, storePkg) && !storePrograms[program] {
				t.Errorf("GOOS=%s: %s depends on %s, and only %v may", goos, program, storePkg, keys(storePrograms))
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

// expectedPrograms are the programs go list must name for every GOOS, so
// a list that misses them can't pass the test: wb and wb-hostd, which
// may not link the store, and each program that may.
func expectedPrograms() []string {
	out := []string{module + "/cmd/wb", module + "/cmd/wb-hostd"}
	for program := range storePrograms {
		out = append(out, program)
	}
	return out
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, strings.TrimPrefix(k, module+"/"))
	}
	slices.Sort(out)
	return out
}
