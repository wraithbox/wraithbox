package diskimage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
)

// Diskutil is the program Tool runs, at its fixed path, never from
// PATH. wb-diskfmt's profile allows starting it and nothing else
// (S04-04-vm-processes, "`wb-diskfmt`").
const Diskutil = "/usr/sbin/diskutil"

// diskutilEnv is diskutil's whole environment. Nothing of wb-diskfmt's
// own environment reaches it.
var diskutilEnv = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C"}

// What is kept of a diskutil run's output. The APFS list of a host with
// many volumes is tens of KiB. Each run's deadline is in
// internal/diskfmt, which wb-hostd's wait for the format's answer is
// derived from.
const (
	maxOutput    = 4 << 20
	maxErrOutput = 4 << 10
)

// Tool is the disk tool of internal/diskfmt on macOS: diskutil, and the
// I/O Registry for the backing path.
type Tool struct {
	// Log gets each diskutil run. nil takes slog.Default().
	Log *slog.Logger
}

// NewTool returns the disk tool.
func NewTool(log *slog.Logger) (*Tool, error) { return &Tool{Log: log}, nil }

var _ diskfmt.Tool = (*Tool)(nil)

func (t *Tool) log() *slog.Logger {
	if t.Log == nil {
		return slog.Default()
	}
	return t.Log
}

// Attach runs `diskutil image attach --noMount --plist path`.
func (t *Tool) Attach(ctx context.Context, path string) (diskfmt.Attachment, error) {
	out, err := t.run(ctx, diskfmt.AttachTimeout, "image", "attach", "--noMount", "--plist", path)
	if err != nil {
		return diskfmt.Attachment{}, err
	}
	return ParseAttach(out)
}

// BackingPath reads the backing image path of device from the I/O
// Registry, in this process, so the check before the format needs no
// other program (S06-13-data-disk-format, "The shim", step 4).
func (t *Tool) BackingPath(_ context.Context, device string) (string, error) {
	if !ValidDevice(device) {
		return "", fmt.Errorf("diskimage: %q isn't a whole disk", cut(device))
	}
	return registryBackingPath(device)
}

// Erase runs `diskutil eraseDisk APFS <VolumeName> GPT device`.
func (t *Tool) Erase(ctx context.Context, device string) error {
	if !ValidDevice(device) {
		return fmt.Errorf("diskimage: %q isn't a whole disk", cut(device))
	}
	_, err := t.run(ctx, diskfmt.EraseTimeout, "eraseDisk", "APFS", diskfmt.VolumeName, "GPT", device)
	return err
}

// VolumeUUID runs `diskutil apfs list -plist`, and returns the UUID of
// the volume on device.
func (t *Tool) VolumeUUID(ctx context.Context, device string) (string, error) {
	out, err := t.run(ctx, diskfmt.VolumeTimeout, "apfs", "list", "-plist")
	if err != nil {
		return "", err
	}
	return ParseVolumeUUID(out, device)
}

// Detach runs `diskutil eject device`, which unmounts its volumes and
// detaches the image.
func (t *Tool) Detach(ctx context.Context, device string) error {
	if !ValidDevice(device) {
		return fmt.Errorf("diskimage: %q isn't a whole disk", cut(device))
	}
	_, err := t.run(ctx, diskfmt.DetachTimeout, "eject", device)
	return err
}

// run runs diskutil with args and the fixed environment, within
// timeout, and returns its standard output. It logs the run. diskutil
// stays in wb-diskfmt's process group, with no SysProcAttr, so
// wb-hostd's kill of that group on a timeout stops it too
// (S06-13-data-disk-format, "When `wb-diskfmt` doesn't exit").
func (t *Tool) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, Diskutil, args...)
	cmd.Env = diskutilEnv
	stdout := &boundedBuffer{max: maxOutput}
	stderr := &boundedBuffer{max: maxErrOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	start := time.Now()
	err := cmd.Run()
	attrs := []slog.Attr{
		slog.String("program", Diskutil), slog.String("args", strings.Join(args, " ")),
		slog.Duration("took", time.Since(start)), slog.String("stderr", stderr.String()),
	}
	if err == nil && stdout.over {
		err = fmt.Errorf("diskimage: diskutil wrote more than %d bytes", maxOutput)
	}
	if err != nil {
		t.log().LogAttrs(ctx, slog.LevelError, "diskutil failed", append(attrs, slog.String("error", err.Error()))...)
		return nil, fmt.Errorf("diskutil %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	t.log().LogAttrs(ctx, slog.LevelInfo, "diskutil ran", attrs...)
	return stdout.Bytes(), nil
}

// boundedBuffer keeps the first max bytes written to it, and drops the
// rest, so diskutil never blocks on a full pipe and its output can't
// grow without bound.
type boundedBuffer struct {
	bytes.Buffer

	max  int
	over bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := b.max - b.Len()
	if len(p) > room {
		b.over = true
		if room > 0 {
			_, _ = b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// errNoBacking is the error for a device the I/O Registry names no
// backing image for.
var errNoBacking = errors.New("diskimage: the I/O Registry names no backing image")
