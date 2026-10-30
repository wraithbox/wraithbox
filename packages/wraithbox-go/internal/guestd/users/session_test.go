package users_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

func TestKnownByName(t *testing.T) {
	f := newFixture()
	u := user(10000)
	if _, ok := f.m.KnownByName(u.Name); ok {
		t.Fatal("an unknown user found")
	}
	f.wantResults(t, f.ensure(t, u), "")
	got, ok := f.m.KnownByName(u.Name)
	if !ok || got != u {
		t.Fatalf("KnownByName = %+v, %v", got, ok)
	}
	if _, ok := f.m.KnownByName(strings.ToUpper(u.Name)); ok {
		t.Error("a name in another case found")
	}
}

// TestPrivileged feeds the check each way a user can be in admin or
// wheel, and a failed read, and sees each refused.
func TestPrivileged(t *testing.T) {
	u := user(10000)
	cases := []struct {
		name  string
		group string
		rec   gu.Record
		uid   uint32
	}{
		{"member by name", "admin", gu.Record{gu.AttrGroupMembership: {u.Name}}, u.UID},
		{"member by GUID, lowercase", "admin", gu.Record{gu.AttrGroupMembers: {strings.ToLower(u.UserGUID)}}, u.UID},
		{"own group nested", "wheel", gu.Record{gu.AttrNestedGroups: {u.GroupGUID}}, u.UID},
		{"primary group", "wheel", gu.Record{gu.AttrPrimaryGroupID: {"10000"}}, u.UID},
		{"uid 0", "staff", gu.Record{}, 0},
	}
	for _, c := range cases {
		f := newFixture()
		f.dir.put(gu.Groups, c.group, c.rec)
		v := u
		v.UID = c.uid
		got, err := f.m.Privileged(t.Context(), v)
		if err != nil || !got {
			t.Errorf("%s: Privileged = %v, %v, want true", c.name, got, err)
		}
	}
	f := newFixture()
	f.dir.put(gu.Groups, "admin", gu.Record{gu.AttrGroupMembership: {"leo"}, gu.AttrPrimaryGroupID: {"80"}})
	if got, err := f.m.Privileged(t.Context(), u); err != nil || got {
		t.Errorf("a standard user: Privileged = %v, %v", got, err)
	}
	f.dir.readErr = errors.New("dscl failed")
	if _, err := f.m.Privileged(t.Context(), u); err == nil {
		t.Error("a failed read gave no error")
	}
}

func TestHold(t *testing.T) {
	f := newFixture()
	u := user(10000)
	ran := 0
	start := func() { ran++ }
	if err := f.m.Hold(u, start); !errors.Is(err, users.ErrUnknown) {
		t.Errorf("unknown user: %v", err)
	}
	f.wantResults(t, f.ensure(t, u), "")
	if err := f.m.Hold(u, start); !errors.Is(err, users.ErrLocked) {
		t.Errorf("locked user: %v", err)
	}
	if rule := f.m.Unlock(t.Context(), u.UID); rule != "" {
		t.Fatalf("Unlock = %q", rule)
	}
	other := u
	other.Home = "/Users/other"
	if err := f.m.Hold(other, start); !errors.Is(err, users.ErrUnknown) {
		t.Errorf("other values: %v", err)
	}
	if ran != 0 {
		t.Fatalf("start ran %d times on a refusal", ran)
	}
	if err := f.m.Hold(u, start); err != nil || ran != 1 {
		t.Errorf("Hold = %v, ran %d", err, ran)
	}
}
