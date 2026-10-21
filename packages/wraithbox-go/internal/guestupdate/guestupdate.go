// Package guestupdate is wb-guestd's update at start from the guest
// tools disk (S06-12-guest-agent, "Update at start"). Before wb-guestd
// listens for the host, AtStart waits for the disk and compares
// wb-guestd's own build version with the disk's VERSION. When they
// differ, it installs the disk's programs and runs the installed
// wb-guestd in its place.
//
// A refusal never stops wb-guestd. It is logged with its rule, and
// wb-guestd serves as it is: wb-hostd accepts only a wb-guestd of its
// own build version, so a guest that didn't update gets no session
// (S06-12-guest-agent, "The host checks, not the guest").
package guestupdate

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guesttools"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

const (
	// DiskWait is how long AtStart waits for the tools disk. Disk
	// Arbitration mounts it during boot, which may be after launchd has
	// started wb-guestd.
	DiskWait = 30 * time.Second
	// pollEvery is how often AtStart looks for the disk while it waits.
	pollEvery = 100 * time.Millisecond
	// MaxProgramSize caps each program AtStart copies from the disk.
	MaxProgramSize = 256 << 20
	// SelfName is wb-guestd's name on the disk and in the install
	// directory.
	SelfName = "wb-guestd"
)

// The rules of this package, for the log. The rules of
// internal/platform/guesttools name a disk that isn't there or isn't the
// one wb-hostd attached, and an unsafe install directory.
const (
	// RuleVersionInvalid: the disk's VERSION is missing, isn't a regular
	// file, or doesn't hold a build version of at most version.MaxLen
	// bytes.
	RuleVersionInvalid = "guestd-update-version-invalid"
	// RuleProgramInvalid: a program of the layout is missing from the
	// disk, isn't a regular file, or is larger than MaxProgramSize.
	// Nothing is installed.
	RuleProgramInvalid = "guestd-update-program-invalid"
	// RuleNoEffect: the installed wb-guestd is already the disk's, yet
	// the disk's VERSION isn't wb-guestd's own version. Installing again
	// would loop, so wb-guestd serves as it is.
	RuleNoEffect = "guestd-update-no-effect"
	// RuleInstallFailed: copying a program into the install directory
	// failed. The programs installed before it stay, and the next start
	// tries again, because wb-guestd is installed last.
	RuleInstallFailed = "guestd-update-install-failed"
	// RuleExecFailed: running the installed wb-guestd failed. wb-guestd
	// exits, and the service manager starts the installed one.
	RuleExecFailed = "guestd-update-exec-failed"
	// RuleUnsupported: the guest OS has no tools disk yet
	// (internal/platform/guesttools).
	RuleUnsupported = "guestd-update-unsupported"
)

// Refusal is the error of a refusal in this package.
type Refusal struct {
	Rule string
	err  error
}

func (r *Refusal) Error() string { return "guestupdate: " + r.err.Error() + " (" + r.Rule + ")" }

// Unwrap returns the cause.
func (r *Refusal) Unwrap() error { return r.err }

// Platform is what the update needs from the guest OS:
// internal/platform/guesttools, or a fake in tests.
type Platform interface {
	// CheckMount checks that the tools disk is mounted at path.
	CheckMount(path string) error
	// PrepareDir makes the install directory when it is missing, and
	// checks that only root can change it.
	PrepareDir(dir string) error
	// Exec runs the program at path in this process's place. It
	// returns only when that fails.
	Exec(path string) error
}

type system struct{}

func (system) CheckMount(path string) error { return guesttools.CheckMount(path) }
func (system) PrepareDir(dir string) error  { return guesttools.PrepareDir(dir) }
func (system) Exec(path string) error       { return guesttools.Exec(path) }

// Config is what AtStart runs with. Only Own and Log are set outside
// tests.
type Config struct {
	// Own is wb-guestd's own build version, normally version.Build().
	Own string
	Log *slog.Logger
	// Disk is the tools disk's mount point. "" takes
	// guesttools.MountPoint.
	Disk string
	// Dir is the install directory. "" takes guesttools.InstallDir.
	Dir string
	// Programs are the names of the programs to install. nil takes
	// Programs().
	Programs []string
	// Platform nil takes internal/platform/guesttools.
	Platform Platform
	// Wait is how long to wait for the disk. 0 takes DiskWait.
	Wait time.Duration
	// MaxSize caps each program. 0 takes MaxProgramSize.
	MaxSize int64
}

func (c Config) withDefaults() Config {
	if c.Disk == "" {
		c.Disk = guesttools.MountPoint
	}
	if c.Dir == "" {
		c.Dir = guesttools.InstallDir
	}
	if c.Programs == nil {
		c.Programs = Programs()
	}
	if c.Platform == nil {
		c.Platform = system{}
	}
	if c.Wait == 0 {
		c.Wait = DiskWait
	}
	if c.MaxSize == 0 {
		c.MaxSize = MaxProgramSize
	}
	return c
}

// Programs returns the names of the programs on the tools disk, from
// the layout table of internal/appbundle, with wb-guestd last.
func Programs() []string {
	var names []string
	for _, p := range appbundle.Programs {
		if p.Place == appbundle.PlaceToolsDisk && p.Name != SelfName {
			names = append(names, p.Name)
		}
	}
	return append(names, SelfName)
}

// AtStart is the update at start. It returns nil when wb-guestd is to
// serve as it is: the disk's version is its own, or the update was
// refused, and the refusal logged with its rule. After an install it
// runs the installed wb-guestd in this process's place, and returns only
// when that fails: wb-guestd then exits non-zero, and the service manager
// starts the installed one. It returns ctx's error when ctx ends while it
// waits for the disk.
func AtStart(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
	if err := waitDisk(ctx, cfg); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		refused(ctx, cfg, "", err)
		return nil
	}
	if err := cfg.Platform.PrepareDir(cfg.Dir); err != nil {
		refused(ctx, cfg, "", err)
		return nil
	}
	installed, diskVersion, err := install(ctx, cfg)
	if err != nil {
		refused(ctx, cfg, diskVersion, err)
		return nil
	}
	if !installed {
		return nil
	}
	path := filepath.Join(cfg.Dir, SelfName)
	err = &Refusal{Rule: RuleExecFailed, err: cfg.Platform.Exec(path)}
	cfg.Log.LogAttrs(ctx, slog.LevelError, "update not run",
		slog.String("rule", RuleExecFailed), slog.String("decision", "exit"),
		slog.String("path", path), slog.String("error", err.Error()))
	return err
}

// waitDisk checks the disk's mount until it passes, every pollEvery, for
// at most cfg.Wait. It returns the last refusal when the wait ends, and
// at once when the platform has no tools disk.
func waitDisk(ctx context.Context, cfg Config) error {
	start := time.Now()
	logged := false
	for {
		err := cfg.Platform.CheckMount(cfg.Disk)
		if err == nil {
			if logged {
				cfg.Log.LogAttrs(ctx, slog.LevelInfo, "tools disk mounted",
					slog.String("path", cfg.Disk), slog.Duration("waited", time.Since(start)))
			}
			return nil
		}
		if errors.Is(err, errors.ErrUnsupported) || time.Since(start) >= cfg.Wait {
			return err
		}
		if !logged {
			cfg.Log.LogAttrs(ctx, slog.LevelInfo, "waiting for the tools disk",
				slog.String("path", cfg.Disk), slog.Duration("wait", cfg.Wait), slog.String("error", err.Error()))
			logged = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollEvery):
		}
	}
}

// refused logs a refusal of the update, with the rule of err.
func refused(ctx context.Context, cfg Config, diskVersion string, err error) {
	rule := RuleInstallFailed
	var ur *Refusal
	var tr *guesttools.Refusal
	switch {
	case errors.As(err, &ur):
		rule = ur.Rule
	case errors.As(err, &tr):
		rule = tr.Rule
	case errors.Is(err, errors.ErrUnsupported):
		rule = RuleUnsupported
	}
	attrs := []slog.Attr{
		slog.String("rule", rule), slog.String("decision", "serve as it is"),
		slog.String("version", version.Label(cfg.Own)),
	}
	if diskVersion != "" {
		attrs = append(attrs, slog.String("disk_version", version.Label(diskVersion)))
	}
	cfg.Log.LogAttrs(ctx, slog.LevelWarn, "update refused", append(attrs, slog.String("error", err.Error()))...)
}
