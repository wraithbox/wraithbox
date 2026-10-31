// Package sessionid makes and checks session ids, the form of
// S05-04-naming, "Session id and branch":
// `<project-name>-<yyyymmdd>-<hhmm>-<4 random chars>`. A debug shell's id
// has the same form (S05-03-management-commands, "`wb shell`").
// wb-hostd makes them, and wb and wb-guestd check the ones they get.
package sessionid

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// MaxLen is the longest id, in bytes, as the contracts bound it
// (proto/wraithbox/hostd/v1/shell.proto, ShellOpened.shell_id).
const MaxLen = 128

// form is the id form: a project name of 1 to 64 bytes of letters,
// digits, `.`, `_` and `-` that starts with a letter or digit, then the
// date, the time and 4 random letters or digits.
var form = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}-[0-9]{8}-[0-9]{4}-[A-Za-z0-9]{4}$`)

// Valid reports whether id is of the session id form, at most MaxLen
// bytes, and without `..`, so `wb/<id>` is a valid branch name.
func Valid(id string) bool {
	return len(id) <= MaxLen && form.MatchString(id) && !strings.Contains(id, "..")
}

// alphabet is what the random characters are drawn from.
const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// randomChars is the count of random characters.
const randomChars = 4

// unbiased is the largest multiple of len(alphabet) that fits a byte. A
// byte at or above it is drawn again, so every character is as likely
// as the next.
const unbiased = 256 / len(alphabet) * len(alphabet)

// New makes the id of a session of the project named name that starts at
// now, in now's time zone, with random characters drawn from r. It
// refuses a name that gives an id out of the form.
func New(name string, now time.Time, r io.Reader) (string, error) {
	chars := make([]byte, 0, randomChars)
	buf := make([]byte, 16)
	for len(chars) < randomChars {
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", fmt.Errorf("sessionid: random characters: %w", err)
		}
		for _, b := range buf {
			if int(b) < unbiased && len(chars) < randomChars {
				chars = append(chars, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	id := fmt.Sprintf("%s-%s-%s", name, now.Format("20060102-1504"), chars)
	if !Valid(id) {
		return "", errors.New("sessionid: the project name gives no valid session id")
	}
	return id, nil
}
