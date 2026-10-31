package sessionid

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func TestValid(t *testing.T) {
	for _, id := range []string{
		"foo-20261031-1052-ab12", "Foo.bar_x-20260101-0000-ZZZZ", "a-20261031-1052-0000",
		strings.Repeat("a", 64) + "-20261031-1052-ab12",
	} {
		if !Valid(id) {
			t.Errorf("Valid(%q) = false, want true", id)
		}
	}
	for _, id := range []string{
		"", "foo", "-foo-20261031-1052-ab12", "foo-2026103-1052-ab12", "foo-20261031-1052-ab1",
		"foo-20261031-1052-ab1!", "a..b-20261031-1052-ab12", "foo bar-20261031-1052-ab12",
		strings.Repeat("a", 65) + "-20261031-1052-ab12", "foo-20261031-1052-ab12\n", "f\x1bo-20261031-1052-ab12",
	} {
		if Valid(id) {
			t.Errorf("Valid(%q) = true, want false", id)
		}
	}
}

func TestNew(t *testing.T) {
	now := time.Date(2026, 10, 31, 10, 52, 0, 0, time.UTC)
	id, err := New("wraithbox", now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "wraithbox-20261031-1052-") || !Valid(id) {
		t.Errorf("New = %q", id)
	}
	// Bytes at or above the unbiased bound are drawn again.
	id, err = New("p", now, bytes.NewReader(append(bytes.Repeat([]byte{255}, 16), make([]byte, 16)...)))
	if err != nil || id != "p-20261031-1052-aaaa" {
		t.Errorf("New = %q, %v", id, err)
	}
	if _, err := New("a..b", now, rand.Reader); err == nil {
		t.Error("New took a name with ..")
	}
	if _, err := New("p", now, bytes.NewReader(nil)); err == nil {
		t.Error("New took an empty random source")
	}
}
