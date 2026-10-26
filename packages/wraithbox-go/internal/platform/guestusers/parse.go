package guestusers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/plist"
)

// The bounds of what dscl reports. Guest root can write record values,
// so the output is guest input (S06-03-image-updates, "Creation"). A
// report over a bound is an error, never cut and read.
const (
	// MaxReadOutput is the most output of one record's read. A project
	// user's record is under 2 KiB.
	MaxReadOutput = 64 << 10
	// MaxListOutput is the most output of a list of every record of a
	// kind, with three attributes each. A fresh guest has under 300.
	MaxListOutput = 4 << 20
	// MaxValueLen is the longest value of an attribute.
	MaxValueLen = 4 << 10
	// MaxValues is the most values of one attribute.
	MaxValues = 256
)

// standardPrefix starts the name of each standard attribute in dscl's
// -plist form.
const standardPrefix = "dsAttrTypeStandard:"

// ParseRecords reads the -plist output of `dscl . -read` (one record, a
// dict) or `dscl . -readall` (a list of them, an array of dicts). Each
// dict maps an attribute's name to an array of strings. A standard
// attribute is named without its dsAttrTypeStandard: prefix. Output over
// MaxListOutput, a value over MaxValueLen or with a NUL byte, an
// attribute with more than MaxValues values, and any other shape are
// errors.
func ParseRecords(out []byte) ([]Record, error) {
	if len(out) > MaxListOutput {
		return nil, fmt.Errorf("guestusers: %d bytes of records, over %d", len(out), MaxListOutput)
	}
	v, err := plist.Decode(out)
	if err != nil {
		return nil, err
	}
	switch top := v.(type) {
	case map[string]any:
		rec, err := parseRecord(top)
		if err != nil {
			return nil, err
		}
		return []Record{rec}, nil
	case []any:
		recs := make([]Record, 0, len(top))
		for i, item := range top {
			d, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("guestusers: record %d isn't a dict", i)
			}
			rec, err := parseRecord(d)
			if err != nil {
				return nil, fmt.Errorf("guestusers: record %d: %w", i, err)
			}
			recs = append(recs, rec)
		}
		return recs, nil
	}
	return nil, errors.New("guestusers: the records are neither a dict nor an array")
}

// parseRecord reads one record's dict.
func parseRecord(d map[string]any) (Record, error) {
	rec := make(Record, len(d))
	for key, raw := range d {
		name := strings.TrimPrefix(key, standardPrefix)
		if name == "" {
			return nil, errors.New("guestusers: an attribute without a name")
		}
		values, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("guestusers: attribute %q isn't an array", cut(name))
		}
		if len(values) > MaxValues {
			return nil, fmt.Errorf("guestusers: attribute %q has %d values, over %d", cut(name), len(values), MaxValues)
		}
		strs := make([]string, 0, len(values))
		for _, item := range values {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("guestusers: a value of attribute %q isn't a string", cut(name))
			}
			if len(s) > MaxValueLen || strings.IndexByte(s, 0) >= 0 {
				return nil, fmt.Errorf("guestusers: a value of attribute %q is over %d bytes or holds NUL", cut(name), MaxValueLen)
			}
			strs = append(strs, s)
		}
		if _, dup := rec[name]; dup {
			// Both the prefixed and the bare name of one attribute.
			return nil, fmt.Errorf("guestusers: attribute %q appears twice", cut(name))
		}
		rec[name] = strs
	}
	return rec, nil
}

// maxText is how much of a value from dscl goes into an error.
const maxText = 64

func cut(s string) string {
	if len(s) > maxText {
		s = s[:maxText]
	}
	return strings.ToValidUTF8(s, "?")
}
