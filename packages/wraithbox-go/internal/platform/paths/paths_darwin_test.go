package paths

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// stubUser makes lookupUser return a user with home for the test.
func stubUser(t *testing.T, home string) {
	t.Helper()
	old := lookupUser
	lookupUser = func() (*user.User, error) {
		return &user.User{Uid: "501", Username: "u", HomeDir: home}, nil
	}
	t.Cleanup(func() { lookupUser = old })
}

func TestCurrentFromUserDatabase(t *testing.T) {
	stubUser(t, "/Users/someone/")
	got, err := Current(nil)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	want := Paths{
		Config: "/Users/someone/.config/wraithbox",
		Data:   "/Users/someone/Library/Application Support/WraithBox",
		Logs:   "/Users/someone/Library/Logs/WraithBox",
		Run:    "/Users/someone/Library/Application Support/WraithBox/run",
	}
	if got != want {
		t.Errorf("Current = %+v, want %+v", got, want)
	}
}

// TestCurrentIgnoresHome: HOME and the other variables don't change the
// paths (S04-03-self-sandboxing, S12-01-platform-interfaces, "Paths").
// The real lookup runs here, so `CGO_ENABLED=0 go test` checks it
// without cgo too.
func TestCurrentIgnoresHome(t *testing.T) {
	before, err := Current(nil)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	t.Setenv("XDG_CONFIG_HOME", fake)
	t.Setenv("USER", "nobody")
	got, err := Current(nil)
	if err != nil {
		t.Fatalf("Current with HOME=%s: %v", fake, err)
	}
	if got != before {
		t.Errorf("Current with HOME=%s = %+v, want %+v", fake, got, before)
	}
	if strings.HasPrefix(got.Config, fake) {
		t.Errorf("Current.Config = %q, from HOME", got.Config)
	}
}

func TestCurrentRefusesHome(t *testing.T) {
	for _, home := range []string{"", "relative/home", "./home"} {
		t.Run(home, func(t *testing.T) {
			stubUser(t, home)
			var buf bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&buf, nil))
			_, err := Current(log)
			var r *Refusal
			if !errors.As(err, &r) || r.Rule != RuleHomeInvalid {
				t.Fatalf("Current with home %q: %v, want %s", home, err, RuleHomeInvalid)
			}
			if !strings.Contains(buf.String(), `"rule":"paths-home-invalid"`) {
				t.Errorf("log = %s, want the rule", buf.String())
			}
		})
	}
}

func TestCurrentLookupError(t *testing.T) {
	old := lookupUser
	lookupUser = func() (*user.User, error) { return nil, user.UnknownUserIdError(501) }
	t.Cleanup(func() { lookupUser = old })
	if _, err := Current(nil); err == nil {
		t.Error("Current with a failed lookup: want an error")
	}
}

// TestRealResolvesLinks: a root that is a symbolic link resolves to its
// target, and Run follows the resolved Data (X23-sandboxed-daemons,
// condition 5).
func TestRealResolvesLinks(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := fromHome(home)
	elsewhere := filepath.Join(home, "elsewhere")
	for _, dir := range []string{p.Config, p.Logs, elsewhere, filepath.Dir(p.Data)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(elsewhere, p.Data); err != nil {
		t.Fatal(err)
	}
	got, err := p.Real()
	if err != nil {
		t.Fatalf("Real: %v", err)
	}
	want := Paths{Config: p.Config, Data: elsewhere, Logs: p.Logs, Run: filepath.Join(elsewhere, "run")}
	if got != want {
		t.Errorf("Real = %+v, want %+v", got, want)
	}
}

func TestRealRefusesMissingRoot(t *testing.T) {
	p := fromHome(t.TempDir())
	if _, err := p.Real(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Real with no roots: %v, want fs.ErrNotExist", err)
	}
}
