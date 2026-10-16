package platform

import "testing"

// TestLoginEnvNotSet asks launchctl for a variable no session sets. The
// test never sets a login variable, because that would change the
// session of the user who runs it.
func TestLoginEnvNotSet(t *testing.T) {
	got, err := loginEnv(t.Context(), "WRAITHBOX_TEST_NEVER_SET_7F3A")
	if err != nil {
		t.Fatalf("loginEnv: %v", err)
	}
	if got != "" {
		t.Errorf("loginEnv = %q, want not set", got)
	}
}

// TestLoginXDGConfigHome checks only the shape of the answer: the
// maintainer's session may or may not set the variable.
func TestLoginXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/wraithbox-test-from-the-caller")
	got, err := LoginXDGConfigHome(t.Context())
	if err != nil {
		t.Fatalf("LoginXDGConfigHome: %v", err)
	}
	if got == "/wraithbox-test-from-the-caller" {
		t.Errorf("LoginXDGConfigHome = %q, the caller's environment", got)
	}
}

func TestLoginValue(t *testing.T) {
	tests := []struct {
		name, out, want string
	}{
		{"not set", "", ""},
		{"absolute", "/Users/me/.config\n", "/Users/me/.config"},
		{"no newline", "/Users/me/.config", "/Users/me/.config"},
		{"relative", "me/.config\n", ""},
		{"tilde", "~/.config\n", ""},
		{"two lines", "/a\n/b\n", ""},
		{"nul", "/a\x00b\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := loginValue(tt.out); got != tt.want {
				t.Errorf("loginValue(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}

func TestCappedBuffer(t *testing.T) {
	c := &cappedBuffer{limit: 4}
	for _, p := range []string{"12", "3456", "78"} {
		n, err := c.Write([]byte(p))
		if n != len(p) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", p, n, err)
		}
	}
	if string(c.buf) != "1234" || !c.over {
		t.Errorf("buf = %q, over = %v; want %q, true", c.buf, c.over, "1234")
	}
}
