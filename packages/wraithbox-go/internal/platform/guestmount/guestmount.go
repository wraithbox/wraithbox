// Package guestmount is the guest's side of the data volume: it finds
// the volume by its UUID, moves its mount to the home path, and reads
// back the mount and the mount root (S06-05-disks, "Mounting the data
// volume"). wb-guestd runs it as root in the guest. The steps and the
// checks are in internal/guestd. This package only makes the calls, on
// darwin with diskutil, statfs and lstat.
//
// Linux and Windows guests are later (S12-platforms). Until they are
// built, every call there returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
package guestmount

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/plist"
)

// MountOptions are the options of the data volume's mount at the home
// path: not shown in the Finder, setuid and device files ignored, and
// ownership on (S06-05-disks, "The mount", step 3).
const MountOptions = "nobrowse,nosuid,nodev,owners"

// Volume is what diskutil reports of a volume.
type Volume struct {
	// UUID is the volume UUID diskutil reports, as it reports it.
	UUID string
	// Device is the volume's device, such as disk5s1, without /dev/.
	Device string
	// MountPoint is where the volume is mounted, "" when it isn't.
	MountPoint string
	// Encrypted is set when diskutil reports the volume encrypted at
	// rest, or encrypted by FileVault, and when its report doesn't say.
	// A guest-made volume on Apple silicon is encrypted at rest without
	// FileVault (X08-data-disk, answer 1), so the check reads both.
	Encrypted bool
	// EncryptionReported is false when the report has no encryption
	// property, which sets Encrypted.
	EncryptionReported bool
}

// Mount is what statfs reports of the file system that holds a path.
type Mount struct {
	// From is the mounted device, such as /dev/disk5s1.
	From string
	// On is the mount point.
	On string
	// The flags the data mount checks (S06-05-disks, "Before every
	// session start").
	NoSUID          bool
	NoDev           bool
	NoBrowse        bool
	IgnoreOwnership bool
}

// Dir is what lstat reports of a path, never following a link.
type Dir struct {
	IsDir  bool
	IsLink bool
	UID    uint32
	GID    uint32
	// Perm is the permission bits, without the type, setuid, setgid and
	// sticky bits.
	Perm fs.FileMode
	// Special is set when the setuid, setgid or sticky bit is set.
	Special bool
}

// device matches an APFS volume's device, such as disk5s1.
var device = regexp.MustCompile(`^disk[0-9]{1,4}s[0-9]{1,4}$`)

// maxText is how much of a value from diskutil goes into an error.
const maxText = 64

// encryptionKeys are the properties of `diskutil info -plist` that say a
// volume is encrypted. Encryption is the one the guest's volumes set
// when they are encrypted at rest. FileVault is the one the text form
// shows. EncryptionThisVolumeProper is set for a volume encrypted on its
// own.
var encryptionKeys = []string{"Encryption", "FileVault", "EncryptionThisVolumeProper"}

// ParseInfo reads the output of `diskutil info -plist <volume>`: the
// volume's UUID, device, mount point and encryption. A report without a
// UUID or a device of the form diskNsM is an error. A report without an
// encryption property reads as encrypted.
func ParseInfo(out []byte) (Volume, error) {
	v, err := plist.Decode(out)
	if err != nil {
		return Volume{}, err
	}
	root, ok := v.(map[string]any)
	if !ok {
		return Volume{}, errors.New("guestmount: the volume report isn't a dict")
	}
	uuid, _ := root["VolumeUUID"].(string)
	if uuid == "" {
		return Volume{}, errors.New("guestmount: the volume report has no VolumeUUID")
	}
	dev, _ := root["DeviceIdentifier"].(string)
	if !device.MatchString(dev) {
		return Volume{}, fmt.Errorf("guestmount: the volume report's device %q isn't a volume", cut(dev))
	}
	mp, _ := root["MountPoint"].(string)
	vol := Volume{UUID: uuid, Device: dev, MountPoint: mp}
	for _, key := range encryptionKeys {
		on, ok := root[key].(bool)
		if !ok {
			continue
		}
		if key == "Encryption" {
			vol.EncryptionReported = true
		}
		vol.Encrypted = vol.Encrypted || on
	}
	if !vol.EncryptionReported {
		vol.Encrypted = true
	}
	return vol, nil
}

// ValidDevice reports whether s names an APFS volume's device, such as
// disk5s1.
func ValidDevice(s string) bool { return device.MatchString(s) }

func cut(s string) string {
	if len(s) > maxText {
		s = s[:maxText]
	}
	return strings.ToValidUTF8(s, "?")
}
