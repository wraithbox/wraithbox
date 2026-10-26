package guestusers_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

const plistHead = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">`

func doc(body string) []byte { return []byte(plistHead + body + `</plist>`) }

// userDict is a record as `dscl -plist . -read /Users/wbp10000` prints
// it.
const userDict = `<dict>
	<key>dsAttrTypeStandard:GeneratedUID</key>
	<array><string>0F1E2D3C-4B5A-4978-8796-A5B4C3D2E1F0</string></array>
	<key>dsAttrTypeStandard:RecordName</key>
	<array><string>wbp10000</string></array>
	<key>dsAttrTypeStandard:UniqueID</key>
	<array><string>10000</string></array>
	<key>dsAttrTypeNative:extra</key>
	<array><string>a</string><string>b</string></array>
</dict>`

// TestParseRecords: one record's dict and a list's array of dicts, with
// the standard prefix taken off and other names kept whole.
func TestParseRecords(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		n    int
	}{
		{"one record", userDict, 1},
		{"a list", `<array>` + userDict + userDict + `</array>`, 2},
		{"an empty list", `<array/>`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recs, err := guestusers.ParseRecords(doc(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) != tc.n {
				t.Fatalf("%d records, want %d", len(recs), tc.n)
			}
			for _, rec := range recs {
				if !slices.Equal(rec[guestusers.AttrUniqueID], []string{"10000"}) ||
					!slices.Equal(rec[guestusers.AttrRecordName], []string{"wbp10000"}) {
					t.Errorf("record %v", rec)
				}
				if !slices.Equal(rec["dsAttrTypeNative:extra"], []string{"a", "b"}) {
					t.Errorf("native attribute %v", rec["dsAttrTypeNative:extra"])
				}
			}
		})
	}
}

// TestParseRecordsRefused: every other shape, and a value or a count
// over its bound, is an error.
func TestParseRecordsRefused(t *testing.T) {
	long := strings.Repeat("x", guestusers.MaxValueLen+1)
	many := strings.Repeat(`<string>x</string>`, guestusers.MaxValues+1)
	for name, body := range map[string]string{
		"a string":            `<string>x</string>`,
		"an array of strings": `<array><string>x</string></array>`,
		"a value not an array": `<dict><key>dsAttrTypeStandard:UniqueID</key>` +
			`<string>10000</string></dict>`,
		"an integer value": `<dict><key>dsAttrTypeStandard:UniqueID</key>` +
			`<array><integer>10000</integer></array></dict>`,
		"a long value":    `<dict><key>dsAttrTypeStandard:RealName</key><array><string>` + long + `</string></array></dict>`,
		"too many values": `<dict><key>dsAttrTypeStandard:RealName</key><array>` + many + `</array></dict>`,
		"an attribute twice": `<dict><key>dsAttrTypeStandard:UniqueID</key><array/>` +
			`<key>UniqueID</key><array/></dict>`,
		"an empty name": `<dict><key>dsAttrTypeStandard:</key><array/></dict>`,
		"a NUL":         `<dict><key>RealName</key><array><string>a&#0;b</string></array></dict>`,
	} {
		t.Run(name, func(t *testing.T) {
			if recs, err := guestusers.ParseRecords(doc(body)); err == nil {
				t.Errorf("parsed %v", recs)
			}
		})
	}
	if _, err := guestusers.ParseRecords(make([]byte, guestusers.MaxListOutput+1)); err == nil {
		t.Error("output over MaxListOutput parsed")
	}
}

// FuzzDirectoryRecord: the parser of dscl's records, whose values guest
// root can write, never panics, and what it accepts keeps its bounds
// (S06-03-image-updates, "Creation").
func FuzzDirectoryRecord(f *testing.F) {
	f.Add(doc(userDict))
	f.Add(doc(`<array>` + userDict + `</array>`))
	f.Add(doc(`<dict><key>RealName</key><array><string></string></array></dict>`))
	f.Add([]byte(`<plist><array><dict/></array></plist>`))
	f.Fuzz(func(t *testing.T, out []byte) {
		recs, err := guestusers.ParseRecords(out)
		if err != nil {
			return
		}
		for _, rec := range recs {
			for name, values := range rec {
				if name == "" || len(values) > guestusers.MaxValues {
					t.Fatalf("attribute %q with %d values", name, len(values))
				}
				for _, v := range values {
					if len(v) > guestusers.MaxValueLen || strings.IndexByte(v, 0) >= 0 {
						t.Fatalf("value of %d bytes in %q", len(v), name)
					}
				}
			}
		}
	})
}
