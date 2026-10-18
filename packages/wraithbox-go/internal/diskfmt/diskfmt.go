// Package diskfmt runs the steps that format a VM's new data disk, for
// wb-diskfmt (S06-13-data-disk-format, "Formatting a data disk"; S04-04-vm-processes,
// "`wb-diskfmt`").
//
// Formatting a blank image the host itself created is the one exception
// SEC15-no-guest-disk-mount makes. The steps keep it to that one file:
//
//   - a leftover temporary file is unlinked, never attached;
//   - the image is created under the temporary name with O_CREAT|O_EXCL
//     and mode 0600, made sparse, and its descriptor kept open;
//   - before the attach and right before the format, lstat on the name
//     and fstat on the descriptor must give the same file, with one link
//     and no block allocated, and before the rename the same file with
//     one link (the identity check);
//   - after the attach and right before the format, the device's backing
//     image must be the temporary name (the backing path check);
//   - only the temporary name is ever attached, and only a device this
//     package attached is detached;
//   - the rename to the final name fails when a file has that name.
//
// The steps run against two small interfaces, Files and Tool, so the
// tests run with fakes on any OS. internal/platform/diskimage has the
// macOS implementations: system calls and diskutil.
package diskfmt

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// Rule names the rule behind a refusal, for the log and for wb-hostd.
type Rule string

// The rules of this package. Each refusal has its own.
const (
	// RuleSizeInvalid: the requested size is outside MinSizeGiB to
	// MaxSizeGiB.
	RuleSizeInvalid Rule = "diskfmt-size-invalid"
	// RuleTempDelete: a leftover temporary file can't be unlinked, as
	// when the name is a directory.
	RuleTempDelete Rule = "diskfmt-temp-delete"
	// RuleCreate: the image can't be created under the temporary name.
	RuleCreate Rule = "diskfmt-create"
	// RuleIdentity: the name and the kept descriptor aren't the same
	// file, or it has another link, or a block allocated before the
	// format.
	RuleIdentity Rule = "diskfmt-identity"
	// RuleBackingPath: the attached device's backing image isn't the
	// temporary name, or can't be read.
	RuleBackingPath Rule = "diskfmt-backing-path"
	// RuleDiskutil: a diskutil step failed.
	RuleDiskutil Rule = "diskfmt-diskutil"
	// RuleVolumeUUID: the format reported no volume UUID in canonical
	// form.
	RuleVolumeUUID Rule = "diskfmt-volume-uuid"
	// RuleFinalExists: a file has the final name, so the rename refused.
	RuleFinalExists Rule = "diskfmt-final-exists"
	// RuleRename: the rename failed for another reason.
	RuleRename Rule = "diskfmt-rename"
)

// The size bounds and the default, in GiB (S06-05-disks, "Disks"). The
// image is sparse, so the size is the most it can grow to. The upper
// bound keeps the free-space check's count of the room a disk can still
// grow meaningful (SEC13-bounded-resources).
const (
	MinSizeGiB     = 8
	MaxSizeGiB     = 1024
	DefaultSizeGiB = 128
)

// SlotEnv is the variable that names wb-diskfmt's slot, work or
// isolated. It is part of the fixed environment of the launcher entries
// wb-diskfmt-work and wb-diskfmt-isolated, as WB_VMD_SLOT is of
// wb-vmd's (S04-04-vm-processes, "`wb-diskfmt`").
const SlotEnv = "WB_DISKFMT_SLOT"

// The bounds of the exchange between wb-hostd and wb-diskfmt
// (proto/wraithbox/diskfmt/v1). Each message is one datagram of at most
// MaxRecord bytes, and a refusal's text is cut to MaxDetail bytes on
// both ends (S04-04-vm-processes, "Reaching an instance").
const (
	MaxRecord = 4096
	MaxDetail = 1024
)

// VolumeName is the name the format gives the APFS volume. wb-guestd
// finds the volume by its UUID, never by this name (S06-05-disks, "The
// mount").
const VolumeName = "WraithBoxData"

// Refusal is the error of every refusal in this package.
type Refusal struct {
	Rule   Rule
	Detail string

	err error
}

func (r *Refusal) Error() string { return string(r.Rule) + ": " + r.Detail }

// Unwrap returns the cause, such as diskutil's error.
func (r *Refusal) Unwrap() error { return r.err }

// CheckSize refuses a size outside MinSizeGiB to MaxSizeGiB with
// RuleSizeInvalid.
func CheckSize(gib uint32) *Refusal {
	if gib < MinSizeGiB || gib > MaxSizeGiB {
		return &Refusal{
			Rule:   RuleSizeInvalid,
			Detail: fmt.Sprintf("a data disk is %d to %d GiB, not %d", MinSizeGiB, MaxSizeGiB, gib),
		}
	}
	return nil
}

// CanonicalUUID reports whether s is a UUID in canonical form: 36
// characters, uppercase hexadecimal digits in groups of 8, 4, 4, 4 and
// 12, joined by hyphens, as diskutil prints a volume UUID.
func CanonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}

// Identity is what the identity check compares.
type Identity struct {
	Dev     int64
	Ino     uint64
	Nlink   uint64
	Blocks  int64
	Regular bool
}

func (i Identity) String() string {
	return fmt.Sprintf("dev %d inode %d links %d blocks %d regular %t", i.Dev, i.Ino, i.Nlink, i.Blocks, i.Regular)
}

// Files is the file system as the steps use it. Every call acts on the
// name it gets and never follows a symbolic link there.
type Files interface {
	// Create creates path with O_CREAT|O_EXCL|O_NOFOLLOW and mode 0600,
	// and sets its size to size bytes with ftruncate, which allocates no
	// block. It fails when anything has the name.
	Create(path string, size int64) (Handle, error)
	// Lstat returns the identity of the name path. A missing name is an
	// error that wraps fs.ErrNotExist.
	Lstat(path string) (Identity, error)
	// Unlink removes the name path, and fails on a directory. A missing
	// name is an error that wraps fs.ErrNotExist.
	Unlink(path string) error
	// RenameExcl renames from to to, and fails with an error that wraps
	// fs.ErrExist when to has a file.
	RenameExcl(from, to string) error
}

// Handle is the kept descriptor of the created image.
type Handle interface {
	Stat() (Identity, error)
	Close() error
}

// Attachment is what an attach reports.
type Attachment struct {
	// Device is the whole disk's name, such as disk5.
	Device string
	// BackingPath is the device's backing image path as the attach
	// reported it.
	BackingPath string
}

// Tool is the disk tool, diskutil on macOS.
type Tool interface {
	// Attach attaches path without mounting it.
	Attach(ctx context.Context, path string) (Attachment, error)
	// BackingPath reads the backing image path of an attached device.
	BackingPath(ctx context.Context, device string) (string, error)
	// Erase formats device with one APFS volume named VolumeName.
	Erase(ctx context.Context, device string) error
	// VolumeUUID returns the UUID of the APFS volume on device.
	VolumeUUID(ctx context.Context, device string) (string, error)
	// Detach unmounts every volume of device and detaches it.
	Detach(ctx context.Context, device string) error
}

// Formatter formats one data disk in one bundle. It is used once:
// Format, then Rename, then Close.
type Formatter struct {
	// Bundle is the bundle's real path. The temporary and final names
	// are in it.
	Bundle string
	Files  Files
	Tool   Tool
	// Log gets the decisions. nil takes slog.Default().
	Log *slog.Logger

	handle Handle
	ident  Identity
}

// TempPath is the image's path under its temporary name.
func (f *Formatter) TempPath() string { return filepath.Join(f.Bundle, vmbundle.DataDiskTempName) }

// FinalPath is the image's path under its final name.
func (f *Formatter) FinalPath() string { return filepath.Join(f.Bundle, vmbundle.DataDiskName) }

func (f *Formatter) log() *slog.Logger {
	if f.Log == nil {
		return slog.Default()
	}
	return f.Log
}

// Close closes the kept descriptor.
func (f *Formatter) Close() error {
	if f.handle == nil {
		return nil
	}
	err := f.handle.Close()
	f.handle = nil
	return err
}

// refuse logs r with its rule and returns it.
func (f *Formatter) refuse(ctx context.Context, step string, r *Refusal) *Refusal {
	attrs := []slog.Attr{
		slog.String("rule", string(r.Rule)), slog.String("decision", "refuse"),
		slog.String("step", step), slog.String("bundle", f.Bundle), slog.String("reason", r.Detail),
	}
	if r.err != nil {
		attrs = append(attrs, slog.String("cause", r.err.Error()))
	}
	f.log().LogAttrs(ctx, slog.LevelError, "data disk format refused", attrs...)
	return r
}

func (f *Formatter) accept(ctx context.Context, msg string, attrs ...slog.Attr) {
	attrs = append([]slog.Attr{slog.String("decision", "accept"), slog.String("bundle", f.Bundle)}, attrs...)
	f.log().LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
}

// Format runs steps 1 to 6 of S06-13-data-disk-format, "The shim": it deletes a
// leftover temporary file, creates the image, checks it, attaches it,
// checks the device, formats it, reads the volume UUID, and detaches
// it. It returns the volume UUID. The descriptor stays open for Rename.
func (f *Formatter) Format(ctx context.Context, sizeGiB uint32) (string, *Refusal) {
	if r := CheckSize(sizeGiB); r != nil {
		return "", f.refuse(ctx, "size", r)
	}
	if r := f.create(ctx, int64(sizeGiB)<<30); r != nil {
		return "", r
	}
	device, r := f.attach(ctx)
	if r != nil {
		return "", r
	}
	uuid, r := f.erase(ctx, device)
	if r != nil {
		return "", f.detachAfter(ctx, device, r)
	}
	if err := f.detach(ctx, device); err != nil {
		return "", f.refuse(ctx, "detach", &Refusal{
			Rule: RuleDiskutil, err: err,
			Detail: "the detach of " + device + " failed twice, so it stays attached until it is ejected or the host restarts",
		})
	}
	f.accept(ctx, "data disk formatted", slog.String("device", device), slog.String("volume_uuid", uuid))
	return uuid, nil
}

// create deletes a leftover temporary file without attaching it, then
// creates the image and checks its identity (steps 1 and 2).
func (f *Formatter) create(ctx context.Context, size int64) *Refusal {
	tmp := f.TempPath()
	switch err := f.Files.Unlink(tmp); {
	case err == nil:
		f.accept(ctx, "leftover temporary data disk deleted", slog.String("path", tmp))
	case errors.Is(err, fs.ErrNotExist):
	default:
		return f.refuse(ctx, "leftover", &Refusal{Rule: RuleTempDelete, Detail: "the leftover " + tmp + " can't be deleted", err: err})
	}
	h, err := f.Files.Create(tmp, size)
	if err != nil {
		return f.refuse(ctx, "create", &Refusal{Rule: RuleCreate, Detail: "the image " + tmp + " can't be created", err: err})
	}
	f.handle = h
	ident, err := h.Stat()
	if err != nil {
		return f.refuse(ctx, "create", &Refusal{Rule: RuleIdentity, Detail: "fstat on the new image failed", err: err})
	}
	f.ident = ident
	return f.checkIdentity(ctx, "create", true)
}

// checkIdentity is step 2: lstat on the temporary name and fstat on the
// kept descriptor give the same file, a regular one with one link, and
// before the format no block allocated.
func (f *Formatter) checkIdentity(ctx context.Context, step string, blank bool) *Refusal {
	tmp := f.TempPath()
	named, err := f.Files.Lstat(tmp)
	if err != nil {
		return f.refuse(ctx, step, &Refusal{Rule: RuleIdentity, Detail: "lstat on " + tmp + " failed", err: err})
	}
	kept, err := f.handle.Stat()
	if err != nil {
		return f.refuse(ctx, step, &Refusal{Rule: RuleIdentity, Detail: "fstat on the kept descriptor failed", err: err})
	}
	var problem string
	switch {
	case named.Dev != kept.Dev || named.Ino != kept.Ino || kept.Dev != f.ident.Dev || kept.Ino != f.ident.Ino:
		problem = fmt.Sprintf("the name is %s, the kept descriptor %s: not the file created", named, kept)
	case !named.Regular:
		problem = "the name isn't a regular file: " + named.String()
	case named.Nlink != 1:
		problem = fmt.Sprintf("the file has %d links, not 1", named.Nlink)
	case blank && named.Blocks != 0:
		problem = fmt.Sprintf("the file has %d blocks allocated before the format, not 0", named.Blocks)
	}
	if problem != "" {
		return f.refuse(ctx, step, &Refusal{Rule: RuleIdentity, Detail: problem})
	}
	return nil
}

// attach is step 3: it attaches the temporary name without mounting it,
// and checks the device it got. diskutil on macOS 27 reports no backing
// path in the attach output, so the check reads it from the I/O
// Registry. A backing path the output does report must agree.
func (f *Formatter) attach(ctx context.Context) (string, *Refusal) {
	tmp := f.TempPath()
	att, err := f.Tool.Attach(ctx, tmp)
	if err != nil {
		return "", f.refuse(ctx, "attach", &Refusal{Rule: RuleDiskutil, Detail: "the attach of " + tmp + " failed", err: err})
	}
	f.accept(ctx, "data disk attached", slog.String("device", att.Device), slog.String("reported_backing_path", cut(att.BackingPath)))
	if att.BackingPath != "" && att.BackingPath != tmp {
		return "", f.detachAfter(ctx, att.Device, f.refuse(ctx, "attach", &Refusal{
			Rule:   RuleBackingPath,
			Detail: fmt.Sprintf("the attach of %s reported the backing image %q", cut(tmp), cut(att.BackingPath)),
		}))
	}
	if r := f.checkBacking(ctx, "attach", att.Device); r != nil {
		return "", f.detachAfter(ctx, att.Device, r)
	}
	return att.Device, nil
}

// checkBacking is the backing path check: the I/O Registry must name the
// temporary name as device's backing image.
func (f *Formatter) checkBacking(ctx context.Context, step, device string) *Refusal {
	tmp := f.TempPath()
	backing, err := f.Tool.BackingPath(ctx, device)
	if err != nil {
		return f.refuse(ctx, step, &Refusal{Rule: RuleBackingPath, Detail: "the backing image of " + device + " can't be read", err: err})
	}
	if backing != tmp {
		return f.refuse(ctx, step, &Refusal{
			Rule:   RuleBackingPath,
			Detail: fmt.Sprintf("%s is backed by %q, not %s", device, cut(backing), tmp),
		})
	}
	return nil
}

// erase is steps 4 and 5: the identity and backing path checks right
// before the format, the format, and the volume UUID. A name replaced
// before the attach, or after it, and not put back is found by the
// identity check here, and the device detached before any format. A
// swap that is put back before this check isn't found: a residual
// within the host user (S06-13-data-disk-format, "The one exception").
func (f *Formatter) erase(ctx context.Context, device string) (string, *Refusal) {
	if r := f.checkIdentity(ctx, "erase", true); r != nil {
		return "", r
	}
	if r := f.checkBacking(ctx, "erase", device); r != nil {
		return "", r
	}
	if err := f.Tool.Erase(ctx, device); err != nil {
		return "", f.refuse(ctx, "erase", &Refusal{Rule: RuleDiskutil, Detail: "the format of " + device + " failed", err: err})
	}
	uuid, err := f.Tool.VolumeUUID(ctx, device)
	if err != nil {
		return "", f.refuse(ctx, "volume", &Refusal{Rule: RuleDiskutil, Detail: "the volume UUID on " + device + " can't be read", err: err})
	}
	if !CanonicalUUID(uuid) {
		return "", f.refuse(ctx, "volume", &Refusal{Rule: RuleVolumeUUID, Detail: fmt.Sprintf("the volume UUID %q isn't in canonical form", cut(uuid))})
	}
	return uuid, nil
}

// detach detaches device, and tries once more when the first try
// fails, as when Disk Arbitration still holds the new volume. A device
// left attached stays so until the host restarts.
func (f *Formatter) detach(ctx context.Context, device string) error {
	err := f.Tool.Detach(ctx, device)
	if err == nil {
		return nil
	}
	f.log().LogAttrs(ctx, slog.LevelWarn, "data disk detach failed, trying again",
		slog.String("device", device), slog.String("cause", err.Error()))
	return f.Tool.Detach(ctx, device)
}

// detachAfter detaches the device this formatter attached, after a
// refusal r, and returns r. A failed detach is logged with the device.
func (f *Formatter) detachAfter(ctx context.Context, device string, r *Refusal) *Refusal {
	if err := f.Tool.Detach(ctx, device); err != nil {
		f.log().LogAttrs(ctx, slog.LevelError, "data disk detach failed",
			slog.String("rule", string(RuleDiskutil)), slog.String("decision", "refuse"),
			slog.String("device", device), slog.String("cause", err.Error()))
		return r
	}
	f.accept(ctx, "data disk detached after a refusal", slog.String("device", device), slog.String("refused_by", string(r.Rule)))
	return r
}

// Rename is step 8: it checks the identity once more, without the block
// count, and renames the temporary name to the final one with
// RENAME_EXCL. A file at the final name is kept, and the rename refused
// with RuleFinalExists.
func (f *Formatter) Rename(ctx context.Context) *Refusal {
	if f.handle == nil {
		return f.refuse(ctx, "rename", &Refusal{Rule: RuleRename, Detail: "no image was formatted"})
	}
	if r := f.checkIdentity(ctx, "rename", false); r != nil {
		return r
	}
	tmp, final := f.TempPath(), f.FinalPath()
	err := f.Files.RenameExcl(tmp, final)
	switch {
	case errors.Is(err, fs.ErrExist):
		return f.refuse(ctx, "rename", &Refusal{Rule: RuleFinalExists, Detail: final + " exists, and is kept", err: err})
	case err != nil:
		return f.refuse(ctx, "rename", &Refusal{Rule: RuleRename, Detail: "the rename of " + tmp + " failed", err: err})
	}
	f.accept(ctx, "data disk renamed", slog.String("from", tmp), slog.String("to", final))
	return nil
}

// maxReported bounds a value from the tool that a refusal repeats.
const maxReported = 128

func cut(s string) string {
	if len(s) <= maxReported {
		return s
	}
	return strings.ToValidUTF8(s[:maxReported], "") + " [cut]"
}
