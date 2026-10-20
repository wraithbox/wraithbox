package project

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// MaxNameLen is the longest project name, in bytes.
const MaxNameLen = 64

// fallbackName is the default name when nothing of the directory's name
// is left.
const fallbackName = "project"

// ValidName checks a project name (S05-04-naming, "Name"): 1 to
// MaxNameLen bytes of ASCII letters, digits, `.`, `_` and `-`, starting
// with a letter or digit, and without `..`. So the session branch
// `wb/<name>-<yyyymmdd>-<hhmm>-<4 chars>` passes `git check-ref-format`,
// and the name prints without escapes and never reads as an option.
// `wb project rename` and wb-hostd check names with it.
func ValidName(name string) error {
	switch {
	case name == "":
		return errors.New("the project name is empty")
	case len(name) > MaxNameLen:
		return fmt.Errorf("the project name is longer than %d bytes", MaxNameLen)
	case !isLetterOrDigit(rune(name[0])):
		return errors.New("the project name must start with a letter or digit")
	case strings.Contains(name, ".."):
		return errors.New("the project name must not hold two dots in a row")
	}
	for i := range len(name) {
		if !isNameChar(rune(name[i])) {
			return fmt.Errorf("the project name holds %q: use letters, digits, '.', '_' and '-'", name[i])
		}
	}
	return nil
}

func isLetterOrDigit(c rune) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

func isNameChar(c rune) bool {
	return isLetterOrDigit(c) || c == '.' || c == '_' || c == '-'
}

// defaultName derives the name from the directory that holds the common
// directory (S05-04-naming, "Name"). A submodule's common directory is
// under the parent's `modules/`, so its name comes from d, the directory
// that holds its `.git` file. A common directory not named `.git`, as
// in a bare repository with linked worktrees, gives its own name
// without a `.git` suffix.
func defaultName(kind Kind, d, common string) string {
	base := filepath.Base(d)
	if kind != KindSubmodule {
		base = filepath.Base(filepath.Dir(common))
		if own := filepath.Base(common); own != gitEntry {
			base = strings.TrimSuffix(own, ".git")
		}
	}
	return nameFrom(base)
}

// nameFrom makes a valid name of any text: each character ValidName
// doesn't allow becomes `-`, `..` becomes `.-`, and characters that
// can't start a name are dropped from the start. It cuts the result to
// MaxNameLen bytes, and gives "project" when nothing is left.
func nameFrom(s string) string {
	var b strings.Builder
	for _, c := range s {
		if isNameChar(c) {
			b.WriteRune(c)
			continue
		}
		b.WriteByte('-')
	}
	name := strings.ReplaceAll(b.String(), "..", ".-")
	name = strings.TrimLeftFunc(name, func(c rune) bool { return !isLetterOrDigit(c) })
	name = name[:min(len(name), MaxNameLen)]
	if name == "" {
		return fallbackName
	}
	return name
}
