package plist_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/plist"
)

const header = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
`

func TestDecode(t *testing.T) {
	doc := header + `<plist version="1.0">
<dict>
	<key>name</key><string>a &amp; b</string>
	<key>count</key><integer>42</integer>
	<key>ratio</key><real>0.5</real>
	<key>on</key><true/>
	<key>off</key><false/>
	<key>bytes</key><data>
	aGVs
	bG8=
	</data>
	<key>list</key><array><string>x</string><dict/><array/></array>
	<key>when</key><date>2026-10-17T00:00:00Z</date>
</dict>
</plist>`
	got, err := plist.Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name": "a & b", "count": int64(42), "ratio": 0.5, "on": true, "off": false,
		"bytes": []byte("hello"), "list": []any{"x", map[string]any{}, []any{}},
		"when": "2026-10-17T00:00:00Z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestDecodeRefuses(t *testing.T) {
	deep := strings.Repeat("<array>", plist.MaxDepth+1) + strings.Repeat("</array>", plist.MaxDepth+1)
	for name, doc := range map[string]string{
		"binary":         "bplist00\x00\x01",
		"not a plist":    `<dict></dict>`,
		"duplicate key":  `<plist><dict><key>a</key><string>1</string><key>a</key><string>2</string></dict></plist>`,
		"value as key":   `<plist><dict><string>a</string><string>1</string></dict></plist>`,
		"missing value":  `<plist><dict><key>a</key></dict></plist>`,
		"bad integer":    `<plist><integer>4x</integer></plist>`,
		"bad data":       `<plist><data>!!</data></plist>`,
		"unknown":        `<plist><blob/></plist>`,
		"child in text":  `<plist><string>a<b/></string></plist>`,
		"too deep":       `<plist>` + deep + `</plist>`,
		"truncated":      `<plist><dict><key>a</key><string>b`,
		"entity":         `<plist><string>&custom;</string></plist>`,
		"empty document": ``,
		"second value":   `<plist><string>a</string><string>b</string></plist>`,
		"element after":  `<plist><string>a</string></plist><plist/>`,
		"text after":     `<plist><string>a</string></plist>x`,
		"key as a value": `<plist><dict><key>a</key><key>b</key></dict></plist>`,
		"hex integer":    `<plist><integer>0x10</integer></plist>`,
		"underscores":    `<plist><integer>1_000</integer></plist>`,
	} {
		t.Run(name, func(t *testing.T) {
			if v, err := plist.Decode([]byte(doc)); err == nil {
				t.Fatalf("Decode = %#v, want an error", v)
			}
		})
	}
	if _, err := plist.Decode([]byte("bplist00")); !errors.Is(err, plist.ErrBinary) {
		t.Errorf("binary: err = %v, want ErrBinary", err)
	}
}

// FuzzDecode: no input panics or nests past MaxDepth. The input is a
// tool's output, not a guest's, so this guards the decoder itself.
func FuzzDecode(f *testing.F) {
	f.Add([]byte(header + `<plist><dict><key>a</key><array><integer>1</integer></array></dict></plist>`))
	f.Add([]byte(`<plist><data>aGk=</data></plist>`))
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = plist.Decode(data)
	})
}
