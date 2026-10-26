package guestd_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
)

const (
	testUUID   = "0F8E98A2-BC26-4E5D-92E5-FD76D3BB8802"
	otherUUID  = "11111111-2222-3333-4444-555555555555"
	testDevice = "disk5s1"
)

// goodMount is the mount Mount makes: the volume at the home path with
// every flag.
func goodMount(dev string) guestmount.Mount {
	return guestmount.Mount{From: "/dev/" + dev, On: guestd.HomePath, NoSUID: true, NoDev: true, NoBrowse: true}
}

var rootDir = guestmount.Dir{IsDir: true, Perm: 0o755}

// fakeVolumes is a guest with one data volume, which Disk Arbitration
// mounted under /Volumes, and nothing at the home path yet.
type fakeVolumes struct {
	mu sync.Mutex
	// vol is the volume diskutil reports for its UUID. present is false
	// until infoCalls reaches appearAfter.
	vol         guestmount.Volume
	appearAfter int
	never       bool
	infoCalls   int
	// mounted is the file system at the home path, nil for none: then
	// statfs reports the system volume.
	mounted *guestmount.Mount
	// point is the home path before the mount, nil when it is missing.
	point *guestmount.Dir
	// root is the volume's root, which lstat reports once it is mounted.
	root guestmount.Dir

	mountErr, unmountErr, claimErr error
	calls                          []string
	// deadline is the deadline of the last Info call's context, zero
	// when it had none.
	deadline time.Time
}

func newFakeVolumes() *fakeVolumes {
	return &fakeVolumes{
		vol:  guestmount.Volume{UUID: testUUID, Device: testDevice, MountPoint: "/Volumes/WBData", EncryptionReported: true},
		root: guestmount.Dir{IsDir: true, UID: 501, GID: 20, Perm: 0o755},
	}
}

func (f *fakeVolumes) record(call string) {
	f.calls = append(f.calls, call)
}

func (f *fakeVolumes) Info(ctx context.Context, uuid string) (guestmount.Volume, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.infoCalls++
	f.deadline, _ = ctx.Deadline()
	if f.never || f.infoCalls <= f.appearAfter || !strings.EqualFold(uuid, f.vol.UUID) {
		return guestmount.Volume{}, false, nil
	}
	return f.vol, true, nil
}

func (f *fakeVolumes) Unmount(_ context.Context, dev string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("unmount " + dev)
	if f.unmountErr != nil {
		return f.unmountErr
	}
	f.vol.MountPoint = ""
	return nil
}

func (f *fakeVolumes) Mount(_ context.Context, dev, point string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("mount " + dev + " " + point)
	if f.mountErr != nil {
		return f.mountErr
	}
	m := goodMount(dev)
	f.mounted = &m
	f.vol.MountPoint = point
	return nil
}

func (f *fakeVolumes) Statfs(path string) (guestmount.Mount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if path == guestd.HomePath && f.mounted != nil {
		return *f.mounted, nil
	}
	return guestmount.Mount{From: "/dev/disk3s1s1", On: "/", NoDev: false}, nil
}

func (f *fakeVolumes) Lstat(path string) (guestmount.Dir, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if path != guestd.HomePath {
		return guestmount.Dir{}, fs.ErrNotExist
	}
	if f.mounted != nil {
		return f.root, nil
	}
	if f.point == nil {
		return guestmount.Dir{}, &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrNotExist}
	}
	return *f.point, nil
}

func (f *fakeVolumes) Mkdir(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("mkdir " + path)
	d := rootDir
	f.point = &d
	return nil
}

func (f *fakeVolumes) Claim(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("claim " + path)
	if f.claimErr != nil {
		return f.claimErr
	}
	f.root = rootDir
	return nil
}

func (f *fakeVolumes) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

// fakeWaits is a clock that moves only when the wait asks it to.
type fakeWaits struct {
	mu  sync.Mutex
	now time.Time
}

func (w *fakeWaits) Now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.now
}

func (w *fakeWaits) After(d time.Duration) <-chan time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = w.now.Add(d)
	ch := make(chan time.Time, 1)
	ch <- w.now
	return ch
}

// newDataMount returns a DataMount on vols and a fake clock, and the
// buffer its log goes to.
func newDataMount(vols *fakeVolumes) (*guestd.DataMount, *fakeWaits, *bytes.Buffer) {
	var buf bytes.Buffer
	w := &fakeWaits{now: time.Unix(1_800_000_000, 0)}
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return guestd.NewDataMount(vols, w, log), w, &buf
}

func wantRule(t *testing.T, r *guestd.MountRefusal, rule string) {
	t.Helper()
	if r == nil {
		t.Fatalf("passed, want %s", rule)
	}
	if r.Rule != rule {
		t.Fatalf("refused with %s (%s), want %s", r.Rule, r.Detail, rule)
	}
}

// wantLogged: the log holds a refusal record with the rule and what was
// found.
func wantLogged(t *testing.T, log *bytes.Buffer, rule string) {
	t.Helper()
	for line := range strings.SplitSeq(log.String(), "\n") {
		if strings.Contains(line, `"rule":"`+rule+`"`) && strings.Contains(line, `"decision":"refuse"`) &&
			strings.Contains(line, `"found":`) {
			return
		}
	}
	t.Fatalf("no refusal with rule %s and the mount found in the log:\n%s", rule, log)
}

// TestMountFresh: the first mount of a new volume unmounts it from
// /Volumes, makes the missing mount point, mounts it at the home path,
// claims its root, and passes the check (S06-05-disks, "The mount").
func TestMountFresh(t *testing.T) {
	vols := newFakeVolumes()
	d, _, log := newDataMount(vols)
	if r := d.Mount(t.Context(), testUUID, true); r != nil {
		t.Fatalf("Mount: %v", r)
	}
	want := []string{
		"mkdir " + guestd.HomePath, "unmount " + testDevice,
		"mount " + testDevice + " " + guestd.HomePath, "claim " + guestd.HomePath,
	}
	if !slices.Equal(vols.calls, want) {
		t.Fatalf("calls %q, want %q", vols.calls, want)
	}
	if !strings.Contains(log.String(), "owned by 501:20") {
		t.Errorf("the root's owner before the claim isn't logged:\n%s", log)
	}
	if r := d.Check(t.Context()); r != nil {
		t.Fatalf("Check after Mount: %v", r)
	}
}

// TestMountWaitsForVolume: a volume that appears within the wait is
// mounted.
func TestMountWaitsForVolume(t *testing.T) {
	vols := newFakeVolumes()
	vols.appearAfter = 5
	d, w, _ := newDataMount(vols)
	start := w.Now()
	if r := d.Mount(t.Context(), testUUID, true); r != nil {
		t.Fatalf("Mount: %v", r)
	}
	if waited := w.Now().Sub(start); waited != 5*time.Second {
		t.Errorf("waited %v, want 5s", waited)
	}
}

// TestMountRefusals: each refusal of the mount steps, with its rule,
// logged with what was found, and the check before a session refused
// after it.
func TestMountRefusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fakeVolumes)
		first  bool
		rule   string
		// noMount: the refusal comes before diskutil mounts anything.
		noMount bool
	}{
		{"never appears", func(f *fakeVolumes) { f.never = true }, true, guestd.RuleVolumeMissing, true},
		{"encrypted at rest", func(f *fakeVolumes) { f.vol.Encrypted = true }, true, guestd.RuleVolumeEncrypted, true},
		{"encryption not reported", func(f *fakeVolumes) {
			f.vol.Encrypted, f.vol.EncryptionReported = true, false
		}, true, guestd.RuleVolumeEncrypted, true},
		{"unmount fails", func(f *fakeVolumes) { f.unmountErr = errors.New("busy") }, true, guestd.RuleVolumeMountFailed, true},
		{"mount fails", func(f *fakeVolumes) { f.mountErr = errors.New("refused") }, true, guestd.RuleVolumeMountFailed, false},
		{"mount point is a link", func(f *fakeVolumes) {
			f.point = &guestmount.Dir{IsLink: true, Perm: 0o755}
		}, true, guestd.RuleMountPoint, true},
		{"mount point owned by a user", func(f *fakeVolumes) {
			f.point = &guestmount.Dir{IsDir: true, UID: 501, GID: 0, Perm: 0o755}
		}, true, guestd.RuleMountPoint, true},
		{"mount point in another group", func(f *fakeVolumes) {
			f.point = &guestmount.Dir{IsDir: true, UID: 0, GID: 20, Perm: 0o755}
		}, true, guestd.RuleMountPoint, true},
		{"claim fails", func(f *fakeVolumes) { f.claimErr = errors.New("EPERM") }, true, guestd.RuleMountRoot, false},
		{"root not claimed without first_mount", func(*fakeVolumes) {}, false, guestd.RuleMountRoot, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vols := newFakeVolumes()
			c.change(vols)
			d, _, log := newDataMount(vols)
			r := d.Mount(t.Context(), testUUID, c.first)
			wantRule(t, r, c.rule)
			wantLogged(t, log, c.rule)
			if c.noMount && vols.called("mount ") {
				t.Errorf("mounted before the refusal: %q", vols.calls)
			}
			if !c.first && vols.called("claim ") {
				t.Error("claimed the root without first_mount")
			}
			wantRule(t, d.Check(t.Context()), guestd.RuleMountPending)
		})
	}
}

// TestMountVolumeNeverAppears: a volume that doesn't appear is refused
// after the whole wait, never before.
func TestMountVolumeNeverAppears(t *testing.T) {
	vols := newFakeVolumes()
	vols.never = true
	d, w, _ := newDataMount(vols)
	start := w.Now()
	wantRule(t, d.Mount(t.Context(), testUUID, true), guestd.RuleVolumeMissing)
	if waited := w.Now().Sub(start); waited != guestd.VolumeWait {
		t.Errorf("refused after %v, want after the wait of %v", waited, guestd.VolumeWait)
	}
}

// TestMountAgain: a second Mount, after a reconnect or a restart of
// wb-guestd, finds the volume at the home path and checks it without a
// second mount (X08-data-disk, answer 12).
func TestMountAgain(t *testing.T) {
	vols := newFakeVolumes()
	d, _, _ := newDataMount(vols)
	if r := d.Mount(t.Context(), testUUID, true); r != nil {
		t.Fatalf("first Mount: %v", r)
	}
	restarted, _, _ := newDataMount(vols)
	for _, dm := range []*guestd.DataMount{d, restarted} {
		vols.calls = nil
		if r := dm.Mount(t.Context(), testUUID, false); r != nil {
			t.Fatalf("second Mount: %v", r)
		}
		if len(vols.calls) != 0 {
			t.Fatalf("the second Mount called %q, want nothing", vols.calls)
		}
		if r := dm.Check(t.Context()); r != nil {
			t.Fatalf("Check: %v", r)
		}
	}
	// Another UUID finds this volume at the home path, and is refused.
	r := restarted.Mount(t.Context(), otherUUID, false)
	wantRule(t, r, guestd.RuleMountVolume)
	if vols.called("mount ") {
		t.Fatal("mounted again for another UUID")
	}
}

// TestCheck: the check before a session refuses a mount without each
// flag, one that ignores ownership, another volume at the home path,
// and a mount root that is a link, has another owner or another mode,
// each with its rule, logged with the mount found (S06-05-disks,
// "Before every session start"; SEC08-proj-isolation).
func TestCheck(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fakeVolumes)
		rule   string
	}{
		{"nosuid off", func(f *fakeVolumes) { f.mounted.NoSUID = false }, guestd.RuleMountFlags},
		{"nodev off", func(f *fakeVolumes) { f.mounted.NoDev = false }, guestd.RuleMountFlags},
		{"nobrowse off", func(f *fakeVolumes) { f.mounted.NoBrowse = false }, guestd.RuleMountFlags},
		{"ownership ignored", func(f *fakeVolumes) { f.mounted.IgnoreOwnership = true }, guestd.RuleMountFlags},
		{"another volume", func(f *fakeVolumes) { f.mounted.From = "/dev/disk6s1" }, guestd.RuleMountVolume},
		{"unmounted", func(f *fakeVolumes) { f.mounted = nil }, guestd.RuleMountVolume},
		{"volume gone", func(f *fakeVolumes) { f.never = true }, guestd.RuleMountVolume},
		{"volume encrypted", func(f *fakeVolumes) { f.vol.Encrypted = true }, guestd.RuleVolumeEncrypted},
		{"root is a link", func(f *fakeVolumes) { f.root = guestmount.Dir{IsLink: true, Perm: 0o755} }, guestd.RuleMountRoot},
		{"root not a directory", func(f *fakeVolumes) { f.root = guestmount.Dir{Perm: 0o755} }, guestd.RuleMountRoot},
		{"root owned by a user", func(f *fakeVolumes) { f.root.UID = 501 }, guestd.RuleMountRoot},
		{"root in another group", func(f *fakeVolumes) { f.root.GID = 20 }, guestd.RuleMountRoot},
		{"root mode 777", func(f *fakeVolumes) { f.root.Perm = 0o777 }, guestd.RuleMountRoot},
		{"root mode 700", func(f *fakeVolumes) { f.root.Perm = 0o700 }, guestd.RuleMountRoot},
		{"root sticky", func(f *fakeVolumes) { f.root.Special = true }, guestd.RuleMountRoot},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vols := newFakeVolumes()
			d, _, log := newDataMount(vols)
			if r := d.Mount(t.Context(), testUUID, true); r != nil {
				t.Fatalf("Mount: %v", r)
			}
			vols.mu.Lock()
			c.change(vols)
			vols.mu.Unlock()
			wantRule(t, d.Check(t.Context()), c.rule)
			wantLogged(t, log, c.rule)
		})
	}
}

// TestCheckPending: before any Mount, the check refuses.
func TestCheckPending(t *testing.T) {
	d, _, log := newDataMount(newFakeVolumes())
	wantRule(t, d.Check(t.Context()), guestd.RuleMountPending)
	wantLogged(t, log, guestd.RuleMountPending)
}

// TestSameMount: the mount of an opened root passes only when it is the
// one the last Check passed. Another mount, a Check that failed since,
// and no Check at all are refused with data-mount-volume and logged.
func TestSameMount(t *testing.T) {
	vols := newFakeVolumes()
	d, _, log := newDataMount(vols)
	wantRule(t, d.SameMount(t.Context(), goodMount(testDevice)), guestd.RuleMountVolume)
	if r := d.Mount(t.Context(), testUUID, true); r != nil {
		t.Fatalf("Mount: %v", r)
	}
	if r := d.Check(t.Context()); r != nil {
		t.Fatalf("Check: %v", r)
	}
	if r := d.SameMount(t.Context(), goodMount(testDevice)); r != nil {
		t.Errorf("the checked mount refused: %v", r)
	}
	wantRule(t, d.SameMount(t.Context(), goodMount("disk6s1")), guestd.RuleMountVolume)
	vols.mu.Lock()
	vols.mounted.NoSUID = false
	vols.mu.Unlock()
	wantRule(t, d.Check(t.Context()), guestd.RuleMountFlags)
	wantRule(t, d.SameMount(t.Context(), goodMount(testDevice)), guestd.RuleMountVolume)
	wantLogged(t, log, guestd.RuleMountVolume)
}

// TestMountAgainFirstMount: a Mount with first_mount that finds the
// volume at the home path claims its root once the mount passed: the
// answer to the first Mount may have been lost. A mount that fails its
// check isn't claimed.
func TestMountAgainFirstMount(t *testing.T) {
	vols := newFakeVolumes()
	d, _, _ := newDataMount(vols)
	if r := d.Mount(t.Context(), testUUID, false); r == nil {
		t.Fatal("Mount without first_mount passed a root owned by 501:20")
	}
	vols.calls = nil
	if r := d.Mount(t.Context(), testUUID, true); r != nil {
		t.Fatalf("Mount with first_mount: %v", r)
	}
	if !slices.Equal(vols.calls, []string{"claim " + guestd.HomePath}) {
		t.Fatalf("calls %q, want only the claim", vols.calls)
	}

	vols.root = guestmount.Dir{IsDir: true, UID: 501, GID: 20, Perm: 0o755}
	vols.mounted.NoSUID = false
	vols.calls = nil
	wantRule(t, d.Mount(t.Context(), testUUID, true), guestd.RuleMountFlags)
	if vols.called("claim ") {
		t.Fatal("claimed the root of a mount without nosuid")
	}
}

// TestMountDeadline: Mount runs under MountDeadline, which is longer
// than the wait for the volume and shorter than wb-hostd's deadline for
// the answer, and a deadline that passes ends the wait for the volume
// with a refusal.
func TestMountDeadline(t *testing.T) {
	if guestd.MountDeadline <= guestd.VolumeWait || guestd.MountDeadline >= guestlink.MountTimeout {
		t.Fatalf("MountDeadline %v, want between VolumeWait %v and MountTimeout %v",
			guestd.MountDeadline, guestd.VolumeWait, guestlink.MountTimeout)
	}
	vols := newFakeVolumes()
	d, _, _ := newDataMount(vols)
	if r := d.Mount(context.Background(), testUUID, true); r != nil {
		t.Fatalf("Mount: %v", r)
	}
	if vols.deadline.IsZero() || vols.deadline.After(time.Now().Add(guestd.MountDeadline)) {
		t.Fatalf("Mount ran with deadline %v, want one at most %v ahead", vols.deadline, guestd.MountDeadline)
	}

	vols = newFakeVolumes()
	vols.never = true
	d, w, _ := newDataMount(vols)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	begun := w.Now()
	wantRule(t, d.Mount(ctx, testUUID, true), guestd.RuleVolumeMissing)
	if waited := w.Now().Sub(begun); waited >= guestd.VolumeWait {
		t.Fatalf("waited %v past the deadline, want the wait to end", waited)
	}
}
