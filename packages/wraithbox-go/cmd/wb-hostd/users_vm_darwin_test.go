package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// TestC09GuestUsersVM boots the work VM as TestC11HostdVM does, gives
// its data disk two project users in state.db, and checks that
// wb-guestd makes them, keeps them over a reboot, and makes them again
// after the system disk is cloned anew while the data disk stays
// (C09-guest-and-sessions, "Project users"; S06-03-image-updates,
// "Creation"). wb-hostd only logs the guest's answer, so the test reads
// "guest user ensured" for each UID. Whether a user was made, kept or
// completed is in the guest's log, which the host doesn't read: the
// in-guest checks are conformance/guestusers. It is needs-vm and
// needs-host, like TestC11HostdVM, and it claims and removes <data> the
// same way. It runs with the variables and the `go test` command of
// `mise run go:test-vm`, with `-run '^TestC09GuestUsersVM$'` in place of
// that task's own.
func TestC09GuestUsersVM(t *testing.T) {
	r := newVMRun(t)
	r.cloneFixture()
	r.startChild(filepath.Join(r.app, "Contents", "MacOS", "wb-hostd"))

	began := time.Now()
	if code, _ := r.wb("start", "work"); code != 0 {
		t.Fatalf("wb vm start work: exit %d, want 0", code)
	}
	r.waitRecord("guest data mount accepted", began, 2*time.Minute)
	ids := r.addProjectUsers(2)

	// A new link mounts again and sends every user of the data disk.
	dropped := time.Now()
	if err := r.child.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	r.waitUsers("made", ids, dropped)

	r.restart("kept over a reboot", ids, nil)
	r.restart("made again on a new system disk", ids, func() {
		src := filepath.Join(os.Getenv(bundleVar), "sys.wbdisk")
		r.command("cp", "-c", src, filepath.Join(r.slotBundle(), "sys.wbdisk"))
	})
	if code, _ := r.wb("stop", "work"); code != 0 {
		t.Errorf("wb vm stop work: exit %d, want 0", code)
	}
}

// addProjectUsers inserts n projects into state.db and has it allocate
// a project user for each on the work VM's data disk, as wb-hostd does
// for a session. It returns their UIDs.
func (r *vmRun) addProjectUsers(n int) []uint32 {
	t := r.t
	db, err := state.Open(t.Context(), r.paths.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	projects := make([]string, n)
	err = db.Projects(t.Context(), func(tx *state.ProjectTx) error {
		for i := range projects {
			id := make([]byte, 16)
			_, _ = rand.Read(id)
			projects[i] = hex.EncodeToString(id)
			if err := tx.Insert(state.Project{
				ID: projects[i], Realm: "native", KeyPath: fmt.Sprintf("/c09/%d", i), Name: "c09",
				Placement: state.PlacementWork, Created: time.Now(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uint32, 0, n)
	for _, id := range projects {
		u, _, err := db.ProjectUser(t.Context(), id, vmbundle.Name(vmbundle.SlotWork))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("measure: project %s has user %s, uid %d", id, u.UserName, u.UID)
		ids = append(ids, u.UID)
	}
	return ids
}

// restart stops the work VM, runs between when it is set, starts it
// again, and waits for the users.
func (r *vmRun) restart(what string, ids []uint32, between func()) {
	t := r.t
	if code, _ := r.wb("stop", "work"); code != 0 {
		t.Fatalf("wb vm stop work: exit %d, want 0", code)
	}
	if between != nil {
		between()
	}
	began := time.Now()
	if code, _ := r.wb("start", "work"); code != 0 {
		t.Fatalf("wb vm start work: exit %d, want 0", code)
	}
	r.waitUsers(what, ids, began)
}

// waitUsers waits until wb-hostd logged an accepted OK for every UID
// after after, and fails at once on a refusal of one.
func (r *vmRun) waitUsers(what string, ids []uint32, after time.Time) {
	t := r.t
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		for _, rec := range r.records("guest user refused", after) {
			t.Fatalf("users %s: wb-hostd refused uid %v with %v (guest rule %v)", what, rec["uid"], rec["rule"], rec["guest_rule"])
		}
		missing := 0
		for _, uid := range ids {
			if !r.ensured(uid, after) {
				missing++
			}
		}
		if missing == 0 {
			t.Logf("measure: users %s %v after the step began", what, time.Since(after))
			r.logTook(what, after)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("users %s: %d of %d not ensured after 2m", what, missing, len(ids))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// logTook logs how long the guest took for each EnsureUsers request
// after after, from wb-hostd's "guest users answered". The guest's
// deadline for one request of 128 users is 50 s
// (S06-03-image-updates, "Creation").
func (r *vmRun) logTook(what string, after time.Time) {
	for _, rec := range r.records("guest users answered", after) {
		// The JSON handler writes a duration in nanoseconds.
		took, _ := rec["took"].(float64)
		r.t.Logf("measure: users %s, a request of %v users took %v", what, rec["users"], time.Duration(took))
	}
}

func (r *vmRun) ensured(uid uint32, after time.Time) bool {
	for _, rec := range r.records("guest user ensured", after) {
		if n, ok := rec["uid"].(float64); ok && uint32(n) == uid {
			return true
		}
	}
	return false
}
