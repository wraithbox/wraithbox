package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// guestLogPath is wb-guestd's log in the guest, as the fixture's
// LaunchDaemon writes it (I379), under the Data volume's root.
const guestLogPath = "private/var/log/wb-guestd.log"

// keepGuestLog has a failed test copy wb-guestd's log out of the work
// VM's system disk, to <scratch>/guest/wb-guestd.log, before <data> is
// removed. wb-hostd never reads the guest's log, and the guest's
// refusals reach the host as a rule only (S06-12-guest-agent), so the
// attribute and value that a refusal names are only in this log. The
// copy is for the person who runs the test: the test logs its path and
// size, never its text, and wb-hostd doesn't see it.
func (r *vmRun) keepGuestLog() {
	t := r.t
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		if err := r.copyGuestLog(); err != nil {
			t.Logf("guest log: not copied: %v", err)
		}
	})
}

// copyGuestLog stops the work VM, so the guest flushes its log and the
// system disk is free, attaches a read-only clone of the disk without
// browsing, copies the log, and detaches it.
func (r *vmRun) copyGuestLog() error {
	// t.Context is canceled once cleanups run.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if r.child != nil {
		code, out := r.wbIn(ctx, "stop", "work")
		r.t.Logf("guest log: wb vm stop work: exit %d\n%s", code, out)
	}
	for _, pid := range r.vmdPIDs() {
		if alive(pid) {
			waitExit(r.t, pid, time.Minute)
		}
	}
	dir := filepath.Join(r.scratch, "guest")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	mnt := filepath.Join(dir, "mnt")
	if err := os.MkdirAll(mnt, 0o700); err != nil {
		return err
	}
	disk := filepath.Join(dir, "sys.wbdisk")
	if out, err := exec.CommandContext(ctx, "cp", "-c", filepath.Join(r.slotBundle(), "sys.wbdisk"), disk).CombinedOutput(); err != nil {
		return errors.New("clone the system disk: " + err.Error() + ": " + string(out))
	}
	defer func() { _ = os.Remove(disk) }()
	out, err := exec.CommandContext(ctx, "hdiutil", "attach",
		"-imagekey", "diskimage-class=CRawDiskImage",
		"-readonly", "-nobrowse", "-noverify", "-noautofsck", "-owners", "off",
		"-mountrandom", mnt, disk).CombinedOutput()
	if err != nil {
		return errors.New("attach the system disk: " + err.Error() + ": " + string(out))
	}
	devs, mounts := attached(out)
	defer detach(devs)
	for _, m := range mounts {
		src := filepath.Join(m, guestLogPath)
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		dst := filepath.Join(dir, "wb-guestd.log")
		if out, err := exec.CommandContext(ctx, "cp", "-X", src, dst).CombinedOutput(); err != nil {
			return errors.New("copy the log: " + err.Error() + ": " + string(out))
		}
		info, err := os.Stat(dst)
		if err != nil {
			return err
		}
		r.t.Logf("guest log: %d bytes copied to %s", info.Size(), dst)
		return nil
	}
	return errors.New("no volume of the system disk has " + guestLogPath)
}

// attached reads `hdiutil attach` output: one line per device, its
// path, its content hint, and its mount point when it has one.
func attached(out []byte) (devs, mounts []string) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		dev := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(dev, "/dev/disk") {
			continue
		}
		devs = append(devs, dev)
		if mp := strings.TrimSpace(fields[len(fields)-1]); len(fields) > 2 && strings.HasPrefix(mp, "/") {
			mounts = append(mounts, mp)
		}
	}
	return devs, mounts
}

// detach detaches the disk image by its first device, the whole disk,
// which detaches every volume on it.
func detach(devs []string) {
	if len(devs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_ = exec.CommandContext(ctx, "hdiutil", "detach", devs[0]).Run()
}
