package appbundle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// infoPlist is an app Info.plist as plutil writes it, with WBBuildVersion
// after a nested dict and an array, so the reader has to skip them.
const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>wb-notify</string>
	<key>LSUIElement</key>
	<true/>
	<key>Nested</key>
	<dict>
		<key>WBBuildVersion</key>
		<string>9.9.9</string>
	</dict>
	<key>List</key>
	<array>
		<string>WBBuildVersion</string>
	</array>
	<key>WBBuildVersion</key>
	<string>0.1.0-dev+g1234abc</string>
</dict>
</plist>
`

func TestReadBuildVersion(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	tests := []struct {
		name string
		path string
		want string
		why  string // in the error when no version is read
	}{
		{"good XML plist", write("good.plist", infoPlist), "0.1.0-dev+g1234abc", ""},
		{"binary plist", write("binary.plist", "bplist00\xd1\x01\x02_\x10\x0eWBBuildVersion"), "", "binary"},
		{"over-size plist", write("big.plist", strings.Replace(infoPlist, "<dict>", "<dict>"+strings.Repeat(" ", MaxInfoPlistSize), 1)), "", "over"},
		{"missing key", write("missing.plist", strings.Replace(infoPlist, "<key>WBBuildVersion</key>\n\t<string>0.1.0", "<key>Other</key>\n\t<string>0.1.0", 1)), "", "missing"},
		{"key twice", write("twice.plist", strings.Replace(infoPlist, "</dict>\n</plist>", "<key>WBBuildVersion</key><string>2.0.0</string></dict>\n</plist>", 1)), "", "twice"},
		{"value not a string", write("int.plist", strings.Replace(infoPlist, "<string>0.1.0-dev+g1234abc</string>", "<integer>1</integer>", 1)), "", "not a string"},
		{"key last", write("last.plist", strings.Replace(infoPlist, "<string>0.1.0-dev+g1234abc</string>", "", 1)), "", "not a string"},
		{"not XML", write("text.plist", "WBBuildVersion = 1.0.0"), "", "not an XML"},
		{"not a plist", write("other.xml", "<dict><key>WBBuildVersion</key><string>1.0.0</string></dict>"), "", "not an XML"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadBuildVersion(tc.path)
			if tc.why == "" {
				if err != nil || got != tc.want {
					t.Fatalf("ReadBuildVersion = %q, %v; want %q", got, err, tc.want)
				}
				return
			}
			if got != "" || !errors.Is(err, ErrNoBuildVersion) || !strings.Contains(err.Error(), tc.why) {
				t.Fatalf("ReadBuildVersion = %q, %v; want ErrNoBuildVersion with %q", got, err, tc.why)
			}
		})
	}
}

func TestReadBuildVersionMissingFile(t *testing.T) {
	_, err := ReadBuildVersion(filepath.Join(t.TempDir(), "none.plist"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not exist", err)
	}
}
