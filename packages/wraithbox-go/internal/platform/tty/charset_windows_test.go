package tty

import (
	"errors"
	"testing"
)

// TestHostCharsetUnsupported: on windows HostCharset fails with an error
// that wraps errors.ErrUnsupported, and its Charset isn't UTF-8 even when
// the POSIX variables say UTF-8, so a caller that drops the error still
// refuses (S12-01-platform-interfaces, "Unimplemented platforms fail
// clearly").
func TestHostCharsetUnsupported(t *testing.T) {
	cs, err := HostCharset(func(string) (string, bool) { return "en_US.UTF-8", true })
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("HostCharset: %v, want errors.ErrUnsupported", err)
	}
	if cs.UTF8 {
		t.Errorf("HostCharset = %+v, want UTF8 false", cs)
	}
}
