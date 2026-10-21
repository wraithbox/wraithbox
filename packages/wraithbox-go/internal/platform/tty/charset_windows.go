package tty

import (
	"errors"
	"fmt"
)

// HostCharset isn't built on windows, where the charset is the console
// output code page and the relay doesn't run yet (S12-platforms). It
// returns a Charset that isn't UTF-8 and an error that wraps
// errors.ErrUnsupported, so the relay refuses.
func HostCharset(func(string) (string, bool)) (Charset, error) {
	return Charset{Source: "the console output code page"}, errCharset
}

var errCharset = fmt.Errorf("tty: the console output code page on windows: not supported on this host yet: %w", errors.ErrUnsupported)
