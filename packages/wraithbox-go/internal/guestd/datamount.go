package guestd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
)

// HomePath is where the data volume is mounted: a fixed path outside
// /Volumes, which Disk Arbitration manages, and the parent of every
// project user's home (S06-05-disks, "Mounting the data volume").
const HomePath = "/private/var/wbhomes"

// VolumeWait is how long Mount waits for the volume with the UUID to
// appear (S06-05-disks, "The mount", step 1). wb-hostd's deadline for
// the answer is longer (internal/guestlink, MountTimeout).
const VolumeWait = 30 * time.Second

// volumePoll is how often Mount asks for the volume while it waits.
const volumePoll = time.Second

// The rules of the data mount. Each refusal of MountData carries one as
// its ErrorInfo reason, and wb-hostd takes no other
// (proto/wraithbox/guest/v1, MountData).
const (
	// RuleVolumeMissing: no volume with the UUID within VolumeWait. A
	// volume the guest reformatted has a new UUID, so it reads as
	// missing too.
	RuleVolumeMissing = "data-volume-missing"
	// RuleVolumeEncrypted: the volume is encrypted at rest or by
	// FileVault, or its report doesn't say. Its key would be bound to
	// this system disk, and the next image update would lose every home
	// (X08-data-disk, answer 1).
	RuleVolumeEncrypted = "data-volume-encrypted"
	// RuleVolumeMountFailed: diskutil couldn't unmount the volume from
	// where Disk Arbitration put it, or mount it at the home path.
	RuleVolumeMountFailed = "data-volume-mount-failed"
	// RuleMountPoint: the home path exists before the mount, but isn't
	// a real directory owned by root:wheel.
	RuleMountPoint = "data-mount-point"
	// RuleMountFlags: the mount at the home path lacks nosuid, nodev or
	// nobrowse, or ignores ownership.
	RuleMountFlags = "data-mount-flags"
	// RuleMountVolume: the file system at the home path isn't the volume
	// with the UUID.
	RuleMountVolume = "data-mount-volume"
	// RuleMountRoot: the mount root isn't a real directory owned by
	// root:wheel with mode 755.
	RuleMountRoot = "data-mount-root"

	// RuleMountPending: a check before any Mount has succeeded since
	// wb-guestd started. MountData never answers with it.
	RuleMountPending = "data-mount-pending"
	// RuleMountRequest: a MountData request whose UUID isn't in
	// canonical form. It is refused with InvalidArgument, which
	// wb-hostd reads as an invalid answer: its own request was wrong.
	RuleMountRequest = "guestd-mount-invalid"
)

// uuidForm is a volume UUID in the canonical form wb-hostd records.
var uuidForm = regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}$`)

// Volumes are the calls the data mount makes. *guestmount.System makes
// them on the guest. Tests pass a fake.
type Volumes interface {
	// Info reports the volume with uuid, and found false when there is
	// none.
	Info(ctx context.Context, uuid string) (guestmount.Volume, bool, error)
	Unmount(ctx context.Context, dev string) error
	// Mount mounts dev at point with guestmount.MountOptions.
	Mount(ctx context.Context, dev, point string) error
	Statfs(path string) (guestmount.Mount, error)
	// Lstat reports path, and an error that wraps fs.ErrNotExist when it
	// is missing.
	Lstat(path string) (guestmount.Dir, error)
	// Mkdir makes path a directory owned by root:wheel with mode 755.
	Mkdir(path string) error
	// Claim makes the directory at path owned by root:wheel with mode
	// 755, never following a link.
	Claim(path string) error
}

// Waits is the time of Mount's wait for the volume. Tests pass a fake.
type Waits interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realWaits struct{}

func (realWaits) Now() time.Time                         { return time.Now() }
func (realWaits) After(d time.Duration) <-chan time.Time { return time.After(d) }

// MountRefusal is a refused mount or check, with its rule. Its detail
// says what was found.
type MountRefusal struct {
	Rule   string
	Detail string
}

func (r *MountRefusal) Error() string { return r.Rule + ": " + r.Detail }

// DataMount mounts the data volume and checks the mount
// (S06-05-disks, "Mounting the data volume"). Its methods are safe for
// concurrent use, and run one at a time.
type DataMount struct {
	vols  Volumes
	waits Waits
	log   *slog.Logger

	mu sync.Mutex
	// mounted is the UUID of the last Mount that succeeded, "" when none
	// has, or the last one failed.
	mounted string
}

// NewDataMount returns a DataMount that makes its calls with vols. nil
// waits takes the real clock.
func NewDataMount(vols Volumes, waits Waits, log *slog.Logger) *DataMount {
	if waits == nil {
		waits = realWaits{}
	}
	return &DataMount{vols: vols, waits: waits, log: log}
}

// Mount mounts the volume with uuid at HomePath and checks it, or only
// checks it when a file system is mounted there already
// (S06-05-disks, "The mount"). firstMount makes the mount root owned by
// root:wheel with mode 755 before the check. A refusal is logged with
// its rule, and every check before a session refuses until a later
// Mount succeeds.
func (d *DataMount) Mount(ctx context.Context, uuid string, firstMount bool) *MountRefusal {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mounted = ""
	attrs := []slog.Attr{slog.String("volume_uuid", uuid), slog.Bool("first_mount", firstMount)}
	r := d.mount(ctx, uuid, firstMount)
	if r != nil {
		d.log.LogAttrs(ctx, slog.LevelWarn, "data volume refused", append(attrs,
			slog.String("rule", r.Rule), slog.String("decision", "refuse"), slog.String("found", r.Detail))...)
		return r
	}
	d.mounted = uuid
	d.log.LogAttrs(ctx, slog.LevelInfo, "data volume mounted",
		append(attrs, slog.String("decision", "accept"), slog.String("path", HomePath))...)
	return nil
}

// Check is the check before a session: the volume Mount mounted is
// still the one at HomePath, with every flag, and its root is a real
// directory owned by root:wheel with mode 755 (S06-05-disks, "Before
// every session start"; SEC08-proj-isolation). Guest root can change
// the flags with `mount -u`, so it runs before every user's creation
// and every session start. It refuses with RuleMountPending until a
// Mount has succeeded. A refusal is logged with the mount found and the
// rule.
func (d *DataMount) Check(ctx context.Context) *MountRefusal {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := &MountRefusal{Rule: RuleMountPending, Detail: "no data volume mounted since wb-guestd started"}
	if d.mounted != "" {
		r = d.check(ctx, d.mounted)
	}
	if r != nil {
		d.log.LogAttrs(ctx, slog.LevelWarn, "data mount check refused",
			slog.String("rule", r.Rule), slog.String("decision", "refuse"),
			slog.String("volume_uuid", d.mounted), slog.String("found", r.Detail))
		return r
	}
	d.log.LogAttrs(ctx, slog.LevelDebug, "data mount check passed",
		slog.String("decision", "accept"), slog.String("volume_uuid", d.mounted))
	return nil
}

// mount runs the steps of S06-05-disks, "The mount".
func (d *DataMount) mount(ctx context.Context, uuid string, firstMount bool) *MountRefusal {
	// Once a user has run with a home under the mount point, macOS
	// refuses another mount there until the next boot (X08-data-disk,
	// answer 12). So a mount that is there already is only checked.
	if m, err := d.vols.Statfs(HomePath); err == nil && m.On == HomePath {
		d.log.LogAttrs(ctx, slog.LevelInfo, "data volume mounted already, checking it",
			slog.String("mount_from", m.From), slog.String("mount_on", m.On))
		return d.check(ctx, uuid)
	}
	vol, r := d.waitVolume(ctx, uuid)
	if r != nil {
		return r
	}
	if vol.Encrypted {
		return &MountRefusal{Rule: RuleVolumeEncrypted, Detail: fmt.Sprintf(
			"%s is encrypted (encryption reported: %t)", vol.Device, vol.EncryptionReported)}
	}
	if r := d.mountPoint(); r != nil {
		return r
	}
	if vol.MountPoint != "" {
		if err := d.vols.Unmount(ctx, vol.Device); err != nil {
			return &MountRefusal{Rule: RuleVolumeMountFailed, Detail: fmt.Sprintf(
				"unmount %s from %q: %v", vol.Device, cut(vol.MountPoint), err)}
		}
	}
	if err := d.vols.Mount(ctx, vol.Device, HomePath); err != nil {
		return &MountRefusal{Rule: RuleVolumeMountFailed, Detail: fmt.Sprintf("mount %s at %s: %v", vol.Device, HomePath, err)}
	}
	if firstMount {
		if r := d.claimRoot(ctx); r != nil {
			return r
		}
	}
	return d.check(ctx, uuid)
}

// waitVolume asks for the volume with uuid every volumePoll until it
// appears, for up to VolumeWait. It identifies the volume by the UUID
// only.
func (d *DataMount) waitVolume(ctx context.Context, uuid string) (guestmount.Volume, *MountRefusal) {
	deadline := d.waits.Now().Add(VolumeWait)
	var last error
	for {
		vol, found, err := d.vols.Info(ctx, uuid)
		if err == nil && found && strings.EqualFold(vol.UUID, uuid) {
			return vol, nil
		}
		switch {
		case err != nil:
			last = err
		case found:
			last = fmt.Errorf("diskutil reported volume %q for UUID %s", cut(vol.UUID), uuid)
		}
		if !d.waits.Now().Before(deadline) {
			detail := fmt.Sprintf("no volume with UUID %s after %s", uuid, VolumeWait)
			if last != nil {
				detail += fmt.Sprintf(" (last error: %v)", last)
			}
			return guestmount.Volume{}, &MountRefusal{Rule: RuleVolumeMissing, Detail: detail}
		}
		select {
		case <-d.waits.After(volumePoll):
		case <-ctx.Done():
			return guestmount.Volume{}, &MountRefusal{Rule: RuleVolumeMissing, Detail: "the wait for the volume ended: " + ctx.Err().Error()}
		}
	}
}

// mountPoint makes the home path a directory owned by root:wheel with
// mode 755 when it is missing, and checks with lstat that it is a real
// directory owned by root:wheel when it exists.
func (d *DataMount) mountPoint() *MountRefusal {
	dir, err := d.vols.Lstat(HomePath)
	if errors.Is(err, fs.ErrNotExist) {
		if err := d.vols.Mkdir(HomePath); err != nil {
			return &MountRefusal{Rule: RuleMountPoint, Detail: fmt.Sprintf("mkdir %s: %v", HomePath, err)}
		}
		dir, err = d.vols.Lstat(HomePath)
	}
	if err != nil {
		return &MountRefusal{Rule: RuleMountPoint, Detail: err.Error()}
	}
	if !dir.IsDir || dir.IsLink || dir.UID != 0 || dir.GID != 0 {
		return &MountRefusal{Rule: RuleMountPoint, Detail: HomePath + " is " + dirText(dir)}
	}
	return nil
}

// claimRoot makes the root of a volume's first mount owned by
// root:wheel with mode 755. The host formatted the volume as the host
// user, so the guest may see the root with another owner. It logs what
// it found first.
func (d *DataMount) claimRoot(ctx context.Context) *MountRefusal {
	before, err := d.vols.Lstat(HomePath)
	if err != nil {
		return &MountRefusal{Rule: RuleMountRoot, Detail: err.Error()}
	}
	d.log.LogAttrs(ctx, slog.LevelInfo, "data volume root before its first claim",
		slog.String("path", HomePath), slog.String("found", dirText(before)))
	if err := d.vols.Claim(HomePath); err != nil {
		return &MountRefusal{Rule: RuleMountRoot, Detail: fmt.Sprintf("claim %s: %v", HomePath, err)}
	}
	return nil
}

// check is the check of Check against the volume with uuid: the mount,
// then its root.
func (d *DataMount) check(ctx context.Context, uuid string) *MountRefusal {
	if r := d.checkMount(ctx, uuid); r != nil {
		return r
	}
	root, err := d.vols.Lstat(HomePath)
	if err != nil {
		return &MountRefusal{Rule: RuleMountRoot, Detail: err.Error()}
	}
	if !root.IsDir || root.IsLink || root.UID != 0 || root.GID != 0 || root.Perm != 0o755 || root.Special {
		return &MountRefusal{Rule: RuleMountRoot, Detail: HomePath + " is " + dirText(root)}
	}
	return nil
}

// checkMount checks that the file system at HomePath is the volume with
// uuid, mounted with every flag and with ownership on.
func (d *DataMount) checkMount(ctx context.Context, uuid string) *MountRefusal {
	m, err := d.vols.Statfs(HomePath)
	if err != nil {
		return &MountRefusal{Rule: RuleMountVolume, Detail: err.Error()}
	}
	vol, found, err := d.vols.Info(ctx, uuid)
	switch {
	case err != nil:
		return &MountRefusal{Rule: RuleMountVolume, Detail: fmt.Sprintf("%s; volume %s: %v", mountText(m), uuid, err)}
	case !found || !strings.EqualFold(vol.UUID, uuid):
		return &MountRefusal{Rule: RuleMountVolume, Detail: fmt.Sprintf("%s; no volume with UUID %s", mountText(m), uuid)}
	case m.On != HomePath || m.From != "/dev/"+vol.Device:
		return &MountRefusal{Rule: RuleMountVolume, Detail: fmt.Sprintf("%s; volume %s is /dev/%s", mountText(m), uuid, vol.Device)}
	case !m.NoSUID || !m.NoDev || !m.DontBrowse || m.IgnoreOwnership:
		return &MountRefusal{Rule: RuleMountFlags, Detail: mountText(m)}
	}
	return nil
}

// mountText describes a mount for the log: its device, its mount point,
// and its flags.
func mountText(m guestmount.Mount) string {
	return fmt.Sprintf("%q on %q, nosuid %t, nodev %t, nobrowse %t, ignore ownership %t",
		cut(m.From), cut(m.On), m.NoSUID, m.NoDev, m.DontBrowse, m.IgnoreOwnership)
}

// dirText describes what lstat found.
func dirText(d guestmount.Dir) string {
	kind := "not a directory"
	switch {
	case d.IsLink:
		kind = "a symbolic link"
	case d.IsDir:
		kind = "a directory"
	}
	return fmt.Sprintf("%s owned by %d:%d with mode %#o (setuid, setgid or sticky: %t)", kind, d.UID, d.GID, uint32(d.Perm), d.Special)
}

// maxShown is how much of a path or device name a log record shows.
const maxShown = 256

func cut(s string) string {
	if len(s) > maxShown {
		s = s[:maxShown]
	}
	return strings.ToValidUTF8(s, "?")
}
