package diskimage_test

import (
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/diskimage"
)

// The samples follow the form of hdiutil's and diskutil's -plist
// output. diskutil's attach output on macOS 27 isn't recorded yet
// (I228): the opt-in host test in cmd/wb-diskfmt runs the real one.

func plistDoc(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">` + body + `</plist>`)
}

const tmp = "/Users/a/Library/Application Support/WraithBox/vms/macos-work.noindex/data.wbdisk.tmp"

func entities(extra string) string {
	return `<dict>` + extra + `<key>system-entities</key><array>
		<dict><key>content-hint</key><string>GUID_partition_scheme</string><key>dev-entry</key><string>/dev/disk7</string><key>potentially-mountable</key><false/></dict>
		<dict><key>content-hint</key><string>Apple_APFS</string><key>dev-entry</key><string>/dev/disk7s1</string></dict>
	</array></dict>`
}

func TestParseAttach(t *testing.T) {
	for _, tc := range []struct {
		name, body, device, backing string
	}{
		{"image-path", entities(`<key>image-path</key><string>` + tmp + `</string>`), "disk7", tmp},
		{"file URL", entities(`<key>DiskImageURL</key><string>file:///Users/a/Library/Application%20Support/WraithBox/vms/macos-work.noindex/data.wbdisk.tmp</string>`), "disk7", tmp},
		{"both agree", entities(`<key>image-path</key><string>` + tmp + `</string><key>image-url</key><string>file://localhost` + strings.ReplaceAll(tmp, " ", "%20") + `</string>`), "disk7", tmp},
		{"both disagree", entities(`<key>image-path</key><string>` + tmp + `</string><key>image-url</key><string>file:///elsewhere</string>`), "disk7", ""},
		{"no backing path", entities(``), "disk7", ""},
		{"relative path", entities(`<key>image-path</key><string>data.wbdisk.tmp</string>`), "disk7", ""},
		{"remote URL", entities(`<key>DiskImageURL</key><string>https://host/x</string>`), "disk7", ""},
		{"path as data", entities(`<key>image-path</key><data>L3RtcC94</data>`), "disk7", "/tmp/x"},
		{"top-level device", `<dict><key>DeviceIdentifier</key><string>disk12</string><key>ImagePath</key><string>` + tmp + `</string></dict>`, "disk12", tmp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := diskimage.ParseAttach(plistDoc(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if got.Device != tc.device || got.BackingPath != tc.backing {
				t.Errorf("got %+v, want device %s backing %q", got, tc.device, tc.backing)
			}
		})
	}
}

func TestParseAttachRefuses(t *testing.T) {
	two := `<dict><key>system-entities</key><array>
		<dict><key>dev-entry</key><string>/dev/disk7</string></dict>
		<dict><key>dev-entry</key><string>/dev/disk8</string></dict></array></dict>`
	for name, body := range map[string]string{
		"two whole disks":  two,
		"only a slice":     `<dict><key>system-entities</key><array><dict><key>dev-entry</key><string>/dev/disk7s1</string></dict></array></dict>`,
		"no device":        `<dict><key>image-path</key><string>/x</string></dict>`,
		"device not whole": `<dict><key>DeviceIdentifier</key><string>disk7s1</string></dict>`,
		"device injection": `<dict><key>DeviceIdentifier</key><string>disk7 disk0</string></dict>`,
		"not a dict":       `<array/>`,
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := diskimage.ParseAttach(plistDoc(body)); err == nil {
				t.Fatalf("ParseAttach = %+v, want an error", got)
			}
		})
	}
}

func apfsList(containers string) []byte {
	return plistDoc(`<dict><key>Containers</key><array>` + containers + `</array></dict>`)
}

func container(store, volumes string) string {
	return `<dict><key>ContainerReference</key><string>disk9</string>
		<key>PhysicalStores</key><array><dict><key>DeviceIdentifier</key><string>` + store + `</string></dict></array>
		<key>Volumes</key><array>` + volumes + `</array></dict>`
}

func volume(name, uuid string) string {
	return `<dict><key>APFSVolumeUUID</key><string>` + uuid + `</string><key>DeviceIdentifier</key><string>disk9s1</string><key>Name</key><string>` + name + `</string></dict>`
}

const uuid = "0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0"

func TestParseVolumeUUID(t *testing.T) {
	host := container("disk0s2", volume("Macintosh HD", "11111111-2222-3333-4444-555555555555")+volume("Data", "66666666-7777-8888-9999-AAAAAAAAAAAA"))
	ours := container("disk7s2", volume(diskfmt.VolumeName, uuid))
	got, err := diskimage.ParseVolumeUUID(apfsList(host+ours), "disk7")
	if err != nil {
		t.Fatal(err)
	}
	if got != uuid {
		t.Errorf("uuid = %s, want %s", got, uuid)
	}
	for name, tc := range map[string]struct {
		list   []byte
		device string
	}{
		"no container on the device": {apfsList(host), "disk7"},
		"a store on disk70":          {apfsList(container("disk70s2", volume(diskfmt.VolumeName, uuid))), "disk7"},
		"two containers":             {apfsList(ours + ours), "disk7"},
		"two volumes":                {apfsList(container("disk7s2", volume(diskfmt.VolumeName, uuid)+volume("x", uuid))), "disk7"},
		"another volume name":        {apfsList(container("disk7s2", volume("Other", uuid))), "disk7"},
		"no uuid":                    {apfsList(container("disk7s2", `<dict><key>Name</key><string>`+diskfmt.VolumeName+`</string></dict>`)), "disk7"},
		"no containers":              {plistDoc(`<dict/>`), "disk7"},
		"device not whole":           {apfsList(ours), "disk7s2"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := diskimage.ParseVolumeUUID(tc.list, tc.device); err == nil {
				t.Fatalf("ParseVolumeUUID = %s, want an error", got)
			}
		})
	}
}

func TestFileURLPath(t *testing.T) {
	for in, want := range map[string]string{
		"file:///a/b%20c":         "/a/b c",
		"file://localhost/a/../b": "/b",
	} {
		got, err := diskimage.FileURLPath(in)
		if err != nil || got != want {
			t.Errorf("FileURLPath(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"/a/b", "file://host/a", "file:///a?x", "file:///a#x", "file:", "http:///a", "file:///a%00b"} {
		if got, err := diskimage.FileURLPath(in); err == nil {
			t.Errorf("FileURLPath(%q) = %q, want an error", in, got)
		}
	}
}

func TestValidDevice(t *testing.T) {
	for s, want := range map[string]bool{"disk0": true, "disk1234": true, "disk12345": false, "disk7s1": false, "/dev/disk7": false, "rdisk7": false, "": false} {
		if got := diskimage.ValidDevice(s); got != want {
			t.Errorf("ValidDevice(%q) = %t, want %t", s, got, want)
		}
	}
}
