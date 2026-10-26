package guestusers_test

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// TestSystemReads reads the local node of the Mac the test runs on, and
// writes nothing: root's user record and the wheel group, a record that
// doesn't exist, and the list of every user.
func TestSystemReads(t *testing.T) {
	s := &guestusers.System{Log: slog.New(slog.DiscardHandler)}
	rec, found, err := s.Read(t.Context(), guestusers.Users, "root")
	if err != nil || !found {
		t.Fatalf("Read root: found %v, %v", found, err)
	}
	if !slices.Equal(rec[guestusers.AttrUniqueID], []string{"0"}) {
		t.Errorf("root's UniqueID %v", rec[guestusers.AttrUniqueID])
	}
	if _, found, err := s.Read(t.Context(), guestusers.Groups, "wheel"); err != nil || !found {
		t.Errorf("Read wheel: found %v, %v", found, err)
	}
	if _, found, err := s.Read(t.Context(), guestusers.Users, "wbp_no_such_user"); err != nil || found {
		t.Errorf("Read of a missing user: found %v, %v", found, err)
	}
	list, err := s.List(t.Context(), guestusers.Users, []string{guestusers.AttrRecordName, guestusers.AttrUniqueID})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(list, func(r guestusers.Record) bool {
		return slices.Contains(r[guestusers.AttrRecordName], "root") && slices.Equal(r[guestusers.AttrUniqueID], []string{"0"})
	}) {
		t.Errorf("no root among %d users", len(list))
	}
}

// TestSystemRefusesArguments: a name, an attribute or a value dscl would
// read as something else is refused before dscl runs.
func TestSystemRefusesArguments(t *testing.T) {
	s := &guestusers.System{Log: slog.New(slog.DiscardHandler)}
	ctx := t.Context()
	for _, name := range []string{"", "-plist", "a/b", "Root", "../x", "a b", "abcdefghijabcdefghijabcdefghijabc"} {
		if _, _, err := s.Read(ctx, guestusers.Users, name); err == nil {
			t.Errorf("Read(%q) ran", name)
		}
		if err := s.Create(ctx, guestusers.Users, name, guestusers.AttrShell, "/bin/zsh"); err == nil {
			t.Errorf("Create(%q) ran", name)
		}
	}
	for _, attr := range []string{"", "-delete", "Real Name", "dsAttrTypeNative:x"} {
		if err := s.Create(ctx, guestusers.Users, "wbp10000", attr, "x"); err == nil {
			t.Errorf("Create with attribute %q ran", attr)
		}
	}
	for _, value := range []string{"", "-x", "a\nb", "a\x00b"} {
		if err := s.Create(ctx, guestusers.Users, "wbp10000", guestusers.AttrRealName, value); err == nil {
			t.Errorf("Create with value %q ran", value)
		}
	}
	if _, err := s.List(ctx, guestusers.Kind(9), nil); err == nil {
		t.Error("List of an unknown kind ran")
	}
}
