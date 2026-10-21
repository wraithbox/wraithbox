//go:build darwin || linux

package tty

import "strings"

// localeVars are the variables that name the locale of the character
// type, in the order setlocale(LC_CTYPE, "") reads them on darwin and
// linux: the first one that is set and not empty wins.
var localeVars = []string{"LC_ALL", "LC_CTYPE", "LANG"}

// HostCharset reads the host terminal's charset from the locale
// variables lookup finds, with setlocale's precedence. When none is
// set, the locale is C, whose charset is ASCII, so it isn't UTF-8. On
// darwin and linux it returns no error.
func HostCharset(lookup func(string) (string, bool)) (Charset, error) {
	for _, name := range localeVars {
		v, ok := lookup(name)
		if !ok || v == "" {
			continue
		}
		return Charset{Source: name, Value: v, UTF8: isUTF8Locale(v)}, nil
	}
	return Charset{}, nil
}

// isUTF8Locale reports whether a locale name,
// language[_territory][.codeset][@modifier], names the UTF-8 codeset.
// The codeset is compared as glibc normalizes it, letters and digits
// only and in lowercase, so UTF-8, utf8 and UTF8 all match. A name that
// is a codeset alone, such as the UTF-8 that macOS takes for LC_CTYPE,
// matches too. C, POSIX, a name without a codeset and any other
// codeset don't.
func isUTF8Locale(name string) bool {
	name, _, _ = strings.Cut(name, "@")
	if _, codeset, ok := strings.Cut(name, "."); ok {
		name = codeset
	}
	return normalizeCodeset(name) == "utf8"
}

// normalizeCodeset keeps the letters and digits of s, in lowercase.
func normalizeCodeset(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + ('a' - 'A'))
		}
	}
	return b.String()
}
