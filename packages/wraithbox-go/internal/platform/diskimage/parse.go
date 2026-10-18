package diskimage

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/plist"
)

// wholeDisk matches a whole disk's name, such as disk5, not a slice such
// as disk5s1.
var wholeDisk = regexp.MustCompile(`^disk[0-9]{1,4}$`)

// ValidDevice reports whether s names a whole disk, such as disk5.
func ValidDevice(s string) bool { return wholeDisk.MatchString(s) }

// The keys that name the backing image in the attach output and in the
// I/O Registry, in the order they are read. A path key holds a path, a
// URL key a file URL. diskutil's attach output on macOS 27 names none,
// and the I/O Registry names DiskImageURL. The parser takes each form
// it may have, and a backing path it can't find, or two that disagree,
// reads as none. The backing path check reads the I/O Registry, and
// refuses an attach output that names another path (internal/diskfmt).
var (
	pathKeys = []string{"image-path", "ImagePath"}
	urlKeys  = []string{"DiskImageURL", "image-url", "ImageURL"}
)

// ParseAttach reads the output of `diskutil image attach --noMount
// --plist`: the whole disk the image was attached as, and the backing
// image path the output names. A device it can't find is an error. A
// backing path it can't find is "", so the caller detaches the device
// and refuses.
func ParseAttach(out []byte) (diskfmt.Attachment, error) {
	v, err := plist.Decode(out)
	if err != nil {
		return diskfmt.Attachment{}, err
	}
	root, ok := v.(map[string]any)
	if !ok {
		return diskfmt.Attachment{}, errors.New("diskimage: the attach output isn't a dict")
	}
	device, entity, err := attachedDisk(root)
	if err != nil {
		return diskfmt.Attachment{}, err
	}
	return diskfmt.Attachment{Device: device, BackingPath: backingIn(root, entity)}, nil
}

// attachedDisk finds the whole disk in the attach output: the one
// system-entities entry whose dev-entry is a whole disk, or a top-level
// device key. It returns the entry, or nil, too.
func attachedDisk(root map[string]any) (string, map[string]any, error) {
	if entities, ok := root["system-entities"].([]any); ok {
		return entityDisk(entities)
	}
	for _, key := range []string{"dev-entry", "DeviceIdentifier", "BSDName"} {
		name, ok := root[key].(string)
		if !ok {
			continue
		}
		name = strings.TrimPrefix(name, "/dev/")
		if !ValidDevice(name) {
			return "", nil, fmt.Errorf("diskimage: the attach output's %s %q isn't a whole disk", key, cut(name))
		}
		return name, nil, nil
	}
	return "", nil, errors.New("diskimage: the attach output names no device")
}

// entityDisk returns the one entry of system-entities whose dev-entry
// is a whole disk. Slices are skipped, and two whole disks are an
// error.
func entityDisk(entities []any) (string, map[string]any, error) {
	var device string
	var found map[string]any
	for _, e := range entities {
		entry, _ := e.(map[string]any)
		name, _ := entry["dev-entry"].(string)
		name = strings.TrimPrefix(name, "/dev/")
		if !ValidDevice(name) {
			continue
		}
		if found != nil {
			return "", nil, fmt.Errorf("diskimage: the attach output names two whole disks, %s and %s", device, name)
		}
		device, found = name, entry
	}
	if found == nil {
		return "", nil, errors.New("diskimage: the attach output names no whole disk")
	}
	return device, found, nil
}

// backingIn returns the one backing path the dicts name, or "" when
// they name none, or more than one.
func backingIn(dicts ...map[string]any) string {
	found := ""
	for _, d := range dicts {
		for _, p := range backingValues(d) {
			// A value that isn't a path is as ambiguous as two that
			// disagree, whichever key holds it.
			if p == "" || (found != "" && p != found) {
				return ""
			}
			found = p
		}
	}
	return found
}

// backingValues returns each backing path d names under the path and
// URL keys. A value that isn't an absolute path or a file URL reads as
// "", which no comparison accepts.
func backingValues(d map[string]any) []string {
	if d == nil {
		return nil
	}
	var out []string
	for _, key := range pathKeys {
		if v, ok := d[key]; ok {
			out = append(out, absPath(stringOrBytes(v)))
		}
	}
	for _, key := range urlKeys {
		if v, ok := d[key]; ok {
			p, err := FileURLPath(stringOrBytes(v))
			if err != nil {
				p = ""
			}
			out = append(out, p)
		}
	}
	return out
}

func stringOrBytes(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return strings.TrimRight(string(s), "\x00")
	}
	return ""
}

// absPath returns p cleaned when it is absolute, and "" otherwise. The
// paths are macOS paths from diskutil and the I/O Registry, so they are
// read with slashes on every OS.
func absPath(p string) string {
	if !path.IsAbs(p) || strings.ContainsRune(p, 0) {
		return ""
	}
	return path.Clean(p)
}

// FileURLPath returns the path of a file URL, such as
// file:///Users/a/Library/Application%20Support/x, decoded.
func FileURLPath(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("diskimage: %q isn't a URL: %w", cut(s), err)
	}
	if u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("diskimage: %q isn't a local file URL", cut(s))
	}
	p := absPath(u.Path)
	if p == "" {
		return "", fmt.Errorf("diskimage: %q has no absolute path", cut(s))
	}
	return p, nil
}

// ParseVolumeUUID reads the output of `diskutil apfs list -plist`, and
// returns the UUID of the one volume in the one APFS container whose
// physical store is on device. The volume must be named
// diskfmt.VolumeName, as the format names it.
func ParseVolumeUUID(out []byte, device string) (string, error) {
	if !ValidDevice(device) {
		return "", fmt.Errorf("diskimage: %q isn't a whole disk", cut(device))
	}
	v, err := plist.Decode(out)
	if err != nil {
		return "", err
	}
	root, _ := v.(map[string]any)
	containers, ok := root["Containers"].([]any)
	if !ok {
		return "", errors.New("diskimage: the APFS list has no Containers array")
	}
	var match map[string]any
	for _, c := range containers {
		container, ok := c.(map[string]any)
		if !ok || !storeOn(container, device) {
			continue
		}
		if match != nil {
			return "", fmt.Errorf("diskimage: two APFS containers have a physical store on %s", device)
		}
		match = container
	}
	if match == nil {
		return "", fmt.Errorf("diskimage: no APFS container has a physical store on %s", device)
	}
	return onlyVolume(match, device)
}

// storeOn reports whether one of container's physical stores is device
// or a slice of it.
func storeOn(container map[string]any, device string) bool {
	stores, _ := container["PhysicalStores"].([]any)
	for _, s := range stores {
		store, _ := s.(map[string]any)
		name, _ := store["DeviceIdentifier"].(string)
		if name == device || strings.HasPrefix(name, device+"s") {
			return true
		}
	}
	return false
}

func onlyVolume(container map[string]any, device string) (string, error) {
	volumes, _ := container["Volumes"].([]any)
	if len(volumes) != 1 {
		return "", fmt.Errorf("diskimage: the APFS container on %s has %d volumes, not 1", device, len(volumes))
	}
	volume, _ := volumes[0].(map[string]any)
	if name, _ := volume["Name"].(string); name != diskfmt.VolumeName {
		return "", fmt.Errorf("diskimage: the volume on %s is named %q, not %s", device, cut(name), diskfmt.VolumeName)
	}
	uuid, ok := volume["APFSVolumeUUID"].(string)
	if !ok {
		return "", fmt.Errorf("diskimage: the volume on %s has no APFSVolumeUUID", device)
	}
	return uuid, nil
}

// maxReported bounds a value from the tool that an error repeats.
const maxReported = 128

func cut(s string) string {
	if len(s) <= maxReported {
		return s
	}
	return strings.ToValidUTF8(s[:maxReported], "") + " [cut]"
}
