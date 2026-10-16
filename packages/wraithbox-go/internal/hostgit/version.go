package hostgit

import (
	"bytes"
	"strconv"
	"strings"
)

// maxDigits bounds each number of a version, so parsing never
// overflows. Git's numbers have one to three digits.
const maxDigits = 6

// version is a git release: major.minor.patch.
type version struct {
	major, minor, patch int
}

// minimum is MinVersion, parsed once.
var minimum = mustParseTriple(MinVersion)

func (v version) String() string {
	return strconv.Itoa(v.major) + "." + strconv.Itoa(v.minor) + "." + strconv.Itoa(v.patch)
}

func (v version) less(w version) bool {
	if v.major != w.major {
		return v.major < w.major
	}
	if v.minor != w.minor {
		return v.minor < w.minor
	}
	return v.patch < w.patch
}

// firstLine returns the first line of out, without its line end. ok is
// false when the line doesn't end within out and out was cut at its
// bound, so the line can't be read whole.
func firstLine(out []byte, truncated bool) (line string, ok bool) {
	first, _, found := bytes.Cut(out, []byte("\n"))
	if !found && truncated {
		return string(first), false
	}
	return strings.TrimSuffix(string(first), "\r"), true
}

// parseVersionLine reads the first line of `git version` as
// S08-08-host-git, "Where it is checked", says:
// `git version <major>.<minor>.<patch>`, then only the end of the line,
// a space and a comment, or `.windows.<n>`. Anything else, a release
// candidate such as 2.54.0.rc1 included, is unreadable.
func parseVersionLine(line string) (version, bool) {
	rest, ok := strings.CutPrefix(line, "git version ")
	if !ok {
		return version{}, false
	}
	v, suffix, ok := parseTriple(rest)
	if !ok || !validSuffix(suffix) {
		return version{}, false
	}
	return v, true
}

// parseTriple reads `<major>.<minor>.<patch>` from the start of s and
// returns what follows it.
func parseTriple(s string) (version, string, bool) {
	var parts [3]int
	for i := range parts {
		if i > 0 {
			var ok bool
			if s, ok = strings.CutPrefix(s, "."); !ok {
				return version{}, "", false
			}
		}
		n, rest, ok := number(s)
		if !ok {
			return version{}, "", false
		}
		parts[i], s = n, rest
	}
	return version{major: parts[0], minor: parts[1], patch: parts[2]}, s, true
}

// number reads one to maxDigits ASCII digits from the start of s.
func number(s string) (int, string, bool) {
	n := 0
	for n < len(s) && n <= maxDigits && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n == 0 || n > maxDigits {
		return 0, "", false
	}
	v, err := strconv.Atoi(s[:n])
	if err != nil {
		return 0, "", false
	}
	return v, s[n:], true
}

// validSuffix accepts what may follow the patch number: nothing, a space
// and a comment of printable ASCII such as `(Apple Git-157)`, or
// `.windows.<n>`.
func validSuffix(s string) bool {
	if s == "" {
		return true
	}
	if comment, ok := strings.CutPrefix(s, " "); ok {
		return comment != "" && printableASCII(comment)
	}
	if n, ok := strings.CutPrefix(s, ".windows."); ok {
		_, rest, ok := number(n)
		return ok && rest == ""
	}
	return false
}

func printableASCII(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func mustParseTriple(s string) version {
	v, rest, ok := parseTriple(s)
	if !ok || rest != "" {
		panic("hostgit: MinVersion " + s + " is not major.minor.patch")
	}
	return v
}
