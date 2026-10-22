package vmcmd

import (
	"testing"
	"unicode/utf8"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// FuzzGuestText: whatever version the guest reported, wb prints valid
// UTF-8 of at most version.MaxLen bytes, with no code point of the
// escaped set (SEC11-root-gains-nothing).
func FuzzGuestText(f *testing.F) {
	for _, s := range []string{"", "1.2.3", "\x1b[2J1.2.3", "\"\\x1b\"", "日本語のバージョン番号はとても長いです。日本語のバージョン番号はとても長いです。", "\xff\u202e"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := guestText(in)
		if !utf8.ValidString(out) || len(out) > version.MaxLen {
			t.Fatalf("guestText(%q) = %q", in, out)
		}
		for _, r := range out {
			if guesttext.Escaped(r) {
				t.Fatalf("guestText(%q) = %q, holds %U", in, out, r)
			}
		}
	})
}
