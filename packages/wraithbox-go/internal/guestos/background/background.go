// Package background holds the background lists of S07-04-dns-refusals,
// "Quiet refusals": for each guest OS release, the exact names its OS
// looks up by itself, which wb-netd refuses without an approval event.
//
// The lists ship with Wraith Box, one embedded file per release.
// wb-hostd picks the list from the image it booted, never from anything
// the guest reports, and nothing the guest sends adds a name to it.
package background

import (
	"bufio"
	"embed"
	"fmt"
	"slices"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
)

//go:embed *.txt
var lists embed.FS

// Releases returns the guest OS releases that have a list, such as
// "macos-27", in order.
func Releases() []string {
	entries, err := lists.ReadDir(".")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".txt"))
	}
	slices.Sort(out)
	return out
}

// List returns the background list of a guest OS release, in the order
// of its file. It fails for a release without a list, and for a list
// with a name that fails the name form rule, isn't in canonical form, or
// is there twice: a list is exact names, never suffixes or wildcards.
func List(release string) ([]dnsname.Name, error) {
	if !slices.Contains(Releases(), release) {
		return nil, fmt.Errorf("background: no list for the guest OS release %q", release)
	}
	data, err := lists.ReadFile(release + ".txt")
	if err != nil {
		return nil, fmt.Errorf("background: %w", err)
	}
	return parse(release, string(data))
}

// parse reads a list: one name per line, with blank lines and lines
// that start with # skipped.
func parse(release, data string) ([]dnsname.Name, error) {
	var out []dnsname.Name
	seen := map[dnsname.Name]bool{}
	sc := bufio.NewScanner(strings.NewReader(data))
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		name, r := dnsname.FromHost(text)
		switch {
		case r != nil:
			return nil, fmt.Errorf("background: %s line %d: %w", release, line, r)
		case name.String() != text:
			return nil, fmt.Errorf("background: %s line %d: %q isn't in canonical form", release, line, text)
		case seen[name]:
			return nil, fmt.Errorf("background: %s line %d: %s is listed twice", release, line, name)
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, sc.Err()
}
