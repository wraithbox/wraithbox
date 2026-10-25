package guestmount_test

import (
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
)

// report is a `diskutil info -plist` report with body as its dict.
func report(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>` + body + `</dict></plist>`)
}

const (
	uuidKey   = `<key>VolumeUUID</key><string>0F8E98A2-BC26-4E5D-92E5-FD76D3BB8802</string>`
	deviceKey = `<key>DeviceIdentifier</key><string>disk5s1</string>`
	mountKey  = `<key>MountPoint</key><string>/Volumes/WBData</string>`
)

// TestParseInfo: the report's UUID, device, mount point and encryption,
// and a volume read as encrypted when the report says so in any of its
// encryption properties, or doesn't say.
func TestParseInfo(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		encrypted bool
		reported  bool
	}{
		{"plain", uuidKey + deviceKey + mountKey +
			`<key>Encryption</key><false/><key>FileVault</key><false/>`, false, true},
		{"encrypted at rest", uuidKey + deviceKey +
			`<key>Encryption</key><true/><key>FileVault</key><false/>`, true, true},
		{"FileVault only", uuidKey + deviceKey +
			`<key>Encryption</key><false/><key>FileVault</key><true/>`, true, true},
		{"this volume proper", uuidKey + deviceKey +
			`<key>Encryption</key><false/><key>EncryptionThisVolumeProper</key><true/>`, true, true},
		{"no encryption property", uuidKey + deviceKey + `<key>FileVault</key><false/>`, true, false},
		{"encryption not a boolean", uuidKey + deviceKey + `<key>Encryption</key><string>no</string>`, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vol, err := guestmount.ParseInfo(report(c.body))
			if err != nil {
				t.Fatal(err)
			}
			if vol.UUID != "0F8E98A2-BC26-4E5D-92E5-FD76D3BB8802" || vol.Device != "disk5s1" {
				t.Errorf("volume %+v", vol)
			}
			if vol.Encrypted != c.encrypted || vol.EncryptionReported != c.reported {
				t.Errorf("encrypted %t, reported %t; want %t, %t", vol.Encrypted, vol.EncryptionReported, c.encrypted, c.reported)
			}
			if wantMP := strings.Contains(c.body, "MountPoint"); (vol.MountPoint == "/Volumes/WBData") != wantMP {
				t.Errorf("mount point %q", vol.MountPoint)
			}
		})
	}
}

// TestParseInfoRefused: a report without a UUID, or whose device isn't
// a volume's, is an error.
func TestParseInfoRefused(t *testing.T) {
	cases := map[string][]byte{
		"not a plist":   []byte("Volume UUID: 0F8E98A2"),
		"not a dict":    []byte(`<plist version="1.0"><array/></plist>`),
		"no UUID":       report(deviceKey),
		"no device":     report(uuidKey),
		"a whole disk":  report(uuidKey + `<key>DeviceIdentifier</key><string>disk5</string>`),
		"a path":        report(uuidKey + `<key>DeviceIdentifier</key><string>../disk5s1</string>`),
		"an option":     report(uuidKey + `<key>DeviceIdentifier</key><string>-force</string>`),
		"long numbers":  report(uuidKey + `<key>DeviceIdentifier</key><string>disk12345s1</string>`),
		"trailing junk": report(uuidKey + `<key>DeviceIdentifier</key><string>disk5s1 x</string>`),
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			if vol, err := guestmount.ParseInfo(out); err == nil {
				t.Fatalf("ParseInfo = %+v, want an error", vol)
			}
		})
	}
}

// FuzzParseInfo: the guest's diskutil writes the report, so its bytes
// are the guest's. A report ParseInfo takes has a UUID and a device of
// the form diskNsM, and is encrypted unless it says it isn't.
func FuzzParseInfo(f *testing.F) {
	f.Add(report(uuidKey + deviceKey + mountKey + `<key>Encryption</key><false/>`))
	f.Add(report(uuidKey + deviceKey))
	f.Add([]byte("bplist00"))
	f.Fuzz(func(t *testing.T, out []byte) {
		vol, err := guestmount.ParseInfo(out)
		if err != nil {
			return
		}
		if vol.UUID == "" || !guestmount.ValidDevice(vol.Device) {
			t.Fatalf("took %+v", vol)
		}
		if !vol.EncryptionReported && !vol.Encrypted {
			t.Fatalf("read a report without encryption as not encrypted: %+v", vol)
		}
	})
}
