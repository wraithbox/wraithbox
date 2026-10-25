//go:build unix

package guestconn

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
)

const volumeUUID = "0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0"

// guestVolumes is a guest whose data volume mounts and passes the check,
// or is encrypted. It counts the claims of the mount root.
type guestVolumes struct {
	mu        sync.Mutex
	encrypted bool
	mounted   bool
	claims    []string
}

func (g *guestVolumes) Info(_ context.Context, uuid string) (guestmount.Volume, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return guestmount.Volume{UUID: uuid, Device: "disk5s1", Encrypted: g.encrypted, EncryptionReported: true}, true, nil
}

func (*guestVolumes) Unmount(context.Context, string) error { return nil }

func (g *guestVolumes) Mount(context.Context, string, string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mounted = true
	return nil
}

func (g *guestVolumes) Statfs(string) (guestmount.Mount, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.mounted {
		return guestmount.Mount{From: "/dev/disk3s1s1", On: "/"}, nil
	}
	return guestmount.Mount{From: "/dev/disk5s1", On: guestd.HomePath, NoSUID: true, NoDev: true, DontBrowse: true}, nil
}

func (*guestVolumes) Lstat(string) (guestmount.Dir, error) {
	return guestmount.Dir{IsDir: true, Perm: 0o755}, nil
}

func (*guestVolumes) Mkdir(string) error { return fs.ErrExist }

func (g *guestVolumes) Claim(path string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.claims = append(g.claims, path)
	return nil
}

func (g *guestVolumes) claimCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.claims)
}

// records is a state.db with one data disk record.
type records struct {
	mu    sync.Mutex
	rec   state.DataDisk
	found bool
	err   error
}

func (r *records) DataDiskRecord(_ context.Context, slot string) (state.DataDisk, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if slot != "macos-work" {
		return state.DataDisk{}, false, nil
	}
	return r.rec, r.found, r.err
}

func (r *records) MarkDataDiskMounted(_ context.Context, slot, uuid string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if slot != "macos-work" || uuid != r.rec.VolumeUUID {
		return state.ErrDataDiskNotRecorded
	}
	if r.rec.FirstMounted.IsZero() {
		r.rec.FirstMounted = at
	}
	return nil
}

func formatted() *records {
	return &records{rec: state.DataDisk{Slot: "macos-work", VolumeUUID: volumeUUID, Formatted: true}, found: true}
}

// waitMount waits until the work slot's data mount is in state.
func (h *harness) waitMount(want MountState) DataMount {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		dm := h.links.Status(vm.SlotWork).DataMount
		if dm.State == want {
			return dm
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("data mount is %+v, want %s", dm, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// SendMount, wb-hostd's hook, asks wb-guestd to mount the recorded
// volume with first_mount set until a mount succeeded, and records the
// first one. The guest claims the root only then. After a reconnect it
// asks again, and the guest checks the mount. A session start passes
// once the volume is mounted (S06-05-disks, "Mounting the data volume").
func TestC11GuestLinkSendMount(t *testing.T) {
	h := newHarness(t, own, own, true)
	vols := &guestVolumes{}
	recs := formatted()
	h.guestMount = guestd.NewDataMount(vols, nil, slog.New(slog.DiscardHandler))
	h.mount = SendMount(recs, func() time.Time { return time.Unix(500, 0) }, slog.New(slog.DiscardHandler))
	h.link()
	h.waitMount(MountMounted)
	if err := h.links.CheckSessionStart(vm.SlotWork, 1); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if !recs.rec.FirstMounted.Equal(time.Unix(500, 0)) || vols.claimCount() != 1 {
		t.Fatalf("first mount %v, %d claims; want recorded at 500 and one claim", recs.rec.FirstMounted, vols.claimCount())
	}
	_ = h.nextGuest().Close()
	h.clock.skipTo(t, FirstWait)
	h.nextTime()
	h.waitState(StateLinked)
	h.waitMount(MountMounted)
	if n := vols.claimCount(); n != 1 {
		t.Fatalf("%d claims after a reconnect, want 1", n)
	}
}

// A refused mount keeps the link and the VM, and every session start
// in the VM is refused with the mount's rule and the fix: stop the VM,
// then reset its data disk (NFR06-explained-refusals). A record that
// is missing refuses before the guest is asked.
func TestC11GuestLinkMountRefused(t *testing.T) {
	tests := []struct {
		name string
		recs *records
		vols *guestVolumes
		rule string
	}{
		{"encrypted", formatted(), &guestVolumes{encrypted: true}, "data-volume-encrypted"},
		{"no record", &records{}, &guestVolumes{}, RuleMountRecord},
		{"not formatted", &records{rec: state.DataDisk{VolumeUUID: volumeUUID}, found: true}, &guestVolumes{}, RuleMountRecord},
		{"record unreadable", &records{err: errors.New("disk I/O error")}, &guestVolumes{}, RuleMountRecord},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, own, own, true)
			h.guestMount = guestd.NewDataMount(tt.vols, nil, slog.New(slog.DiscardHandler))
			h.mount = SendMount(tt.recs, time.Now, slog.New(slog.DiscardHandler))
			h.link()
			dm := h.waitMount(MountRefused)
			if dm.Rule != tt.rule {
				t.Fatalf("rule %s, want %s", dm.Rule, tt.rule)
			}
			if recs := h.logs.withRule(t, tt.rule); len(recs) != 1 || recs[0]["decision"] != "refuse" {
				t.Fatalf("%s records %v, want one refusal", tt.rule, recs)
			}
			wantSessionRefused(t, h.links.CheckSessionStart(vm.SlotWork, 1), tt.rule,
				"wb vm stop work", "wb vm reset-data work")
			// The link stays up: the next time call comes after 60 s.
			if d := h.clock.nextWait(t); d != TimeInterval {
				t.Fatalf("wait after the refusal = %v, want %v", d, TimeInterval)
			}
			if h.links.Status(vm.SlotWork).State != StateLinked {
				t.Fatal("the link didn't stay up")
			}
		})
	}
}

// A mount the guest leaves unanswered drops the link with
// guest-mount-timeout, and the next link asks again. A later success
// replaces the refusal.
func TestC11GuestLinkMountTimeout(t *testing.T) {
	h := newHarness(t, own, own, true)
	var mu sync.Mutex
	calls := 0
	h.mount = func(context.Context, vm.Slot, uint64, *guestlink.Link) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return &guestlink.MountRefusal{Rule: guestlink.RuleMountTimeout}
		}
		return nil
	}
	h.start()
	h.clock.skipTo(t, FirstWait)
	if d := h.clock.nextWait(t); d != FirstWait {
		t.Fatalf("wait after the timeout = %v, want a reconnect after %v", d, FirstWait)
	}
	if dm := h.waitMount(MountRefused); dm.Rule != guestlink.RuleMountTimeout {
		t.Fatalf("rule %s, want %s", dm.Rule, guestlink.RuleMountTimeout)
	}
	h.clock.advance(FirstWait)
	h.nextTime()
	h.waitMount(MountMounted)
	if err := h.links.CheckSessionStart(vm.SlotWork, 1); err != nil {
		t.Fatalf("session start after the mount: %v", err)
	}
}

// A session start is refused while no link is up, for another
// generation, and while the data mount is pending.
func TestCheckSessionStartPending(t *testing.T) {
	h := newHarness(t, own, own, true)
	wantSessionRefused(t, h.links.CheckSessionStart(vm.SlotWork, 1), RuleSessionNoLink)
	block := make(chan struct{})
	h.mount = func(ctx context.Context, _ vm.Slot, _ uint64, _ *guestlink.Link) error {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil
	}
	h.start()
	h.clock.skipTo(t, FirstWait)
	h.waitState(StateLinked)
	wantSessionRefused(t, h.links.CheckSessionStart(vm.SlotWork, 1), RuleSessionMountPending)
	wantSessionRefused(t, h.links.CheckSessionStart(vm.SlotWork, 2), RuleSessionNoLink)
	close(block)
	h.waitMount(MountMounted)
	if err := h.links.CheckSessionStart(vm.SlotWork, 1); err != nil {
		t.Fatalf("session start: %v", err)
	}
}

func wantSessionRefused(t *testing.T, err error, rule string, says ...string) {
	t.Helper()
	r, ok := errors.AsType[*SessionRefusal](err)
	if !ok || !errors.Is(err, ErrSessionRefused) {
		t.Fatalf("session start = %v, want a *SessionRefusal", err)
	}
	if r.Rule != rule {
		t.Fatalf("rule %s (%v), want %s", r.Rule, err, rule)
	}
	for _, s := range says {
		if !strings.Contains(r.Message, s) {
			t.Errorf("message %q doesn't say %q", r.Message, s)
		}
	}
}
