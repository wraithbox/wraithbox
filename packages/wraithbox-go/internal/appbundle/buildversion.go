package appbundle

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
)

// MaxInfoPlistSize is the largest Info.plist ReadBuildVersion reads. The
// app's Info.plist that Xcode and the build write is a few KiB, so a
// larger file isn't one of ours, and counts as no version.
const MaxInfoPlistSize = 256 << 10

// ErrNoBuildVersion wraps every reason ReadBuildVersion and BuildVersion
// find no version: a binary or malformed property list, an over-size
// file, a missing or repeated key, or a value that isn't a string.
var ErrNoBuildVersion = errors.New("no " + BuildVersionKey)

// ReadBuildVersion returns the BuildVersionKey string of the XML
// property list at path, such as a bundle's Contents/Info.plist
// (S17-01-bundle, "The bundle"). wb setup reads it to check its own
// bundle and to recognize a link into a Wraith Box bundle
// (S17-02-setup), and wb-hostd can read it to notice an upgrade
// (S04-07-supervision, "Upgrades"). It reads at most MaxInfoPlistSize
// bytes. The file is the user's own, never guest bytes.
func ReadBuildVersion(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxInfoPlistSize+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > MaxInfoPlistSize {
		return "", fmt.Errorf("%w: %s is over %d bytes", ErrNoBuildVersion, path, MaxInfoPlistSize)
	}
	return BuildVersion(data)
}

// plistDoc is the part of an XML property list BuildVersion reads: the
// children of the top-level dict, in order. A nested value is one item,
// whose content isn't read.
type plistDoc struct {
	XMLName xml.Name `xml:"plist"`
	Dict    struct {
		Items []plistItem `xml:",any"`
	} `xml:"dict"`
}

type plistItem struct {
	XMLName xml.Name
	Text    string `xml:",chardata"`
}

// BuildVersion returns the BuildVersionKey string of an XML property
// list. The key must be in the top-level dict exactly once, and its
// value must be a string element. Anything else is an error that wraps
// ErrNoBuildVersion.
func BuildVersion(data []byte) (string, error) {
	if bytes.HasPrefix(data, []byte("bplist")) {
		return "", fmt.Errorf("%w: a binary property list", ErrNoBuildVersion)
	}
	var doc plistDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("%w: not an XML property list: %w", ErrNoBuildVersion, err)
	}
	items := doc.Dict.Items
	found := -1
	for i, it := range items {
		if it.XMLName.Local != "key" || it.Text != BuildVersionKey {
			continue
		}
		if found >= 0 {
			return "", fmt.Errorf("%w: the key is there twice", ErrNoBuildVersion)
		}
		found = i
	}
	switch {
	case found < 0:
		return "", fmt.Errorf("%w: the key is missing", ErrNoBuildVersion)
	case found+1 >= len(items) || items[found+1].XMLName.Local != "string":
		return "", fmt.Errorf("%w: the key's value is not a string", ErrNoBuildVersion)
	}
	return items[found+1].Text, nil
}
