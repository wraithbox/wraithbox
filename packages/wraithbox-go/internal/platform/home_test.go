package platform

import (
	"errors"
	"os/user"
	"path/filepath"
	"testing"
)

// TestUserHomeIgnoresEnvironment: HOME comes from the user database,
// never from the environment (S05-04-naming, "Resolving the key").
func TestUserHomeIgnoresEnvironment(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "not-home")
	t.Setenv("HOME", fake)
	t.Setenv("USERPROFILE", fake)
	got, err := UserHome()
	if err != nil {
		t.Fatalf("UserHome: %v", err)
	}
	if got == fake || !filepath.IsAbs(got) {
		t.Errorf("UserHome = %q, want an absolute path from the user database", got)
	}
}

func TestHomeOfRefuses(t *testing.T) {
	if _, err := homeOf(nil, errors.New("no entry")); err == nil {
		t.Error("homeOf with a lookup error: want an error")
	}
	if _, err := homeOf(&user.User{Username: "u", HomeDir: "relative/home"}, nil); err == nil {
		t.Error("homeOf with a relative home: want an error")
	}
	if _, err := homeOf(&user.User{Username: "u"}, nil); err == nil {
		t.Error("homeOf with no home: want an error")
	}
}
