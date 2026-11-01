package secretstore_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore/storetest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

const team = "ABCDE12345"

// secretValue is the value the tests write, and never find in a log.
const secretValue = "secret-value-for-the-log-check"

var (
	groupBinding = storeitem.Group(team, storeitem.RoleBinding)
	groupCA      = storeitem.Group(team, storeitem.RoleCA)
	groupWbadmin = storeitem.Group(team, storeitem.RoleWbadmin)
)

// opened is a store over a fake, with its log.
type opened struct {
	store secretstore.Store
	fake  *storetest.Fake
	log   *bytes.Buffer
	err   error
}

func open(t *testing.T, program storeitem.Program, fake *storetest.Fake) *opened {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	s, err := secretstore.New(program, fake, log)
	if s == nil {
		t.Fatal("New returned no Store")
	}
	t.Cleanup(func() {
		if strings.Contains(buf.String(), secretValue) {
			t.Errorf("the log holds the value:\n%s", buf.String())
		}
	})
	return &opened{store: s, fake: fake, log: &buf, err: err}
}

func name(t *testing.T, s string) storeitem.Name {
	t.Helper()
	n, err := storeitem.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// requireRule checks that err is an *Error with rule, and that the log
// has a line with the rule, the call and the item.
func requireRule(t *testing.T, o *opened, err error, rule storeitem.Rule, call storeitem.Access, item string) {
	t.Helper()
	var e *secretstore.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v, want a *secretstore.Error with %s", err, rule)
	}
	if e.Rule != rule {
		t.Fatalf("rule %s (%v), want %s", e.Rule, err, rule)
	}
	for line := range strings.Lines(o.log.String()) {
		if strings.Contains(line, "rule="+string(rule)) && strings.Contains(line, "call="+string(call)) &&
			strings.Contains(line, "item="+item) {
			return
		}
	}
	t.Fatalf("no log line with rule=%s call=%s item=%s:\n%s", rule, call, item, o.log)
}

// callAll makes every call on a binding, and returns each error by call.
func callAll(t *testing.T, s secretstore.Store) map[storeitem.Access]error {
	t.Helper()
	ctx := context.Background()
	item := name(t, "wraithbox/binding/github")
	_, fetchErr := s.Fetch(ctx, item, storeitem.RoleBinding)
	_, listErr := s.List(ctx, storeitem.RoleBinding)
	return map[storeitem.Access]error{
		storeitem.AccessWrite:  s.Write(ctx, item, storeitem.RoleBinding, []byte(secretValue)),
		storeitem.AccessFetch:  fetchErr,
		storeitem.AccessDelete: s.Delete(ctx, item, storeitem.RoleBinding),
		storeitem.AccessList:   listErr,
	}
}

// requireAllRefused checks that every call is refused with rule, logged,
// and that the fake saw no item call.
func requireAllRefused(t *testing.T, o *opened, rule storeitem.Rule) {
	t.Helper()
	for call, err := range callAll(t, o.store) {
		item := "wraithbox/binding/github"
		if call == storeitem.AccessList {
			item = ""
		}
		requireRule(t, o, err, rule, call, item)
	}
	if calls := o.fake.Calls(); len(calls) != 0 {
		t.Errorf("the backend saw %v", calls)
	}
}

// TestC05StoreDialogsOn makes the call that turns dialogs off fail, and
// sees every call refused with store-dialogs-on (C05-credentials-and-store,
// "Store errors").
func TestC05StoreDialogsOn(t *testing.T) {
	fake := storetest.New(team, groupBinding)
	fake.DialogsErr = errors.New("OSStatus -1")
	o := open(t, storeitem.ProgramCredset, fake)
	var e *secretstore.Error
	if !errors.As(o.err, &e) || e.Rule != storeitem.RuleDialogsOn {
		t.Fatalf("New: %v, want %s", o.err, storeitem.RuleDialogsOn)
	}
	requireAllRefused(t, o, storeitem.RuleDialogsOn)
}

// TestC05StoreNeedsTeam opens the store with a signature without a team,
// with one that can't be read, and with one whose team isn't a team ID,
// and sees every call refused with keychain-needs-team before any
// backend call (C05-credentials-and-store, "Builds without a team").
// The first check wins: the fake holds no group either.
func TestC05StoreNeedsTeam(t *testing.T) {
	for _, tt := range []struct {
		desc string
		fake *storetest.Fake
	}{
		{"no team", storetest.New("")},
		{"unreadable", &storetest.Fake{TeamErr: errors.New("OSStatus -67062")}},
		{"not a team ID", storetest.New("abc", groupBinding)},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			o := open(t, storeitem.ProgramCredset, tt.fake)
			var e *secretstore.Error
			if !errors.As(o.err, &e) || e.Rule != storeitem.RuleNeedsTeam {
				t.Fatalf("New: %v, want %s", o.err, storeitem.RuleNeedsTeam)
			}
			if tt.fake.DialogsOffCalls() != 1 {
				t.Errorf("DialogsOff ran %d times, want 1", tt.fake.DialogsOffCalls())
			}
			requireAllRefused(t, o, storeitem.RuleNeedsTeam)
		})
	}
}

// TestC05AccessGroupsMismatch asks for a ca/ write with the binding
// group, and for a group the entitlements don't hold, and sees each
// refused with store-group-mismatch before any backend call, with both
// groups in the log (C05-credentials-and-store, "Access groups").
func TestC05AccessGroupsMismatch(t *testing.T) {
	ctx := context.Background()
	o := open(t, storeitem.ProgramProxyd, storetest.New(team, groupBinding, groupCA))
	ca := "wraithbox/ca/vm-1/ca-1"
	err := o.store.Write(ctx, name(t, ca), storeitem.RoleBinding, []byte(secretValue))
	requireRule(t, o, err, storeitem.RuleGroupMismatch, storeitem.AccessWrite, ca)
	if !strings.Contains(o.log.String(), "group="+groupBinding+" item_group="+groupCA) {
		t.Errorf("the log doesn't name both groups:\n%s", o.log)
	}

	notHeld := open(t, storeitem.ProgramProxyd, storetest.New(team, groupCA))
	binding := "wraithbox/binding/github"
	_, err = notHeld.store.Fetch(ctx, name(t, binding), storeitem.RoleBinding)
	requireRule(t, notHeld, err, storeitem.RuleGroupMismatch, storeitem.AccessFetch, binding)

	// The zero Name is no item, and a role outside the table is no group.
	_, err = o.store.Fetch(ctx, storeitem.Name{}, storeitem.RoleBinding)
	requireRule(t, o, err, storeitem.RuleGroupMismatch, storeitem.AccessFetch, "")
	_, err = o.store.List(ctx, storeitem.Role("other"))
	requireRule(t, o, err, storeitem.RuleGroupMismatch, storeitem.AccessList, "")

	if calls := append(o.fake.Calls(), notHeld.fake.Calls()...); len(calls) != 0 {
		t.Errorf("the backend saw %v", calls)
	}
}

// TestC05AccessGroupsUnreadable opens a store whose entitlements can't be
// read, and sees every call refused with store-group-mismatch
// (C05-credentials-and-store, "Access groups").
func TestC05AccessGroupsUnreadable(t *testing.T) {
	fake := storetest.New(team, groupBinding)
	fake.GroupsErr = errors.New("the entitlement isn't a list of strings")
	o := open(t, storeitem.ProgramCredset, fake)
	if o.err != nil {
		t.Fatalf("New: %v", o.err)
	}
	requireAllRefused(t, o, storeitem.RuleGroupMismatch)
}

// TestC05StoreAccessUndeclared makes accesses outside the declared table
// of S15-03-access-rules and sees each refused with
// store-access-undeclared before any backend call.
func TestC05StoreAccessUndeclared(t *testing.T) {
	ctx := context.Background()
	binding := "wraithbox/binding/github"
	credset := open(t, storeitem.ProgramCredset, storetest.New(team, groupBinding))
	_, err := credset.store.Fetch(ctx, name(t, binding), storeitem.RoleBinding)
	requireRule(t, credset, err, storeitem.RuleAccessUndeclared, storeitem.AccessFetch, binding)

	proxyd := open(t, storeitem.ProgramProxyd, storetest.New(team, groupBinding, groupCA, groupWbadmin))
	_, err = proxyd.store.List(ctx, storeitem.RoleBinding)
	requireRule(t, proxyd, err, storeitem.RuleAccessUndeclared, storeitem.AccessList, "")
	image := "wraithbox/wbadmin/image/base"
	_, err = proxyd.store.Fetch(ctx, name(t, image), storeitem.RoleWbadmin)
	requireRule(t, proxyd, err, storeitem.RuleAccessUndeclared, storeitem.AccessFetch, image)

	rotate := open(t, storeitem.ProgramGuestadminRotate, storetest.New(team, groupWbadmin))
	vm := "wraithbox/wbadmin/vm/vm-1/disk-0"
	err = rotate.store.Delete(ctx, name(t, vm), storeitem.RoleWbadmin)
	requireRule(t, rotate, err, storeitem.RuleAccessUndeclared, storeitem.AccessDelete, vm)

	for _, o := range []*opened{credset, proxyd, rotate} {
		if calls := o.fake.Calls(); len(calls) != 0 {
			t.Errorf("the backend saw %v", calls)
		}
	}
}

// TestC05StoreErrorsNotFoundLocked returns -25300 and -25308 for a
// binding and sees ErrNotFound and ErrLocked (C05-credentials-and-store,
// "Store errors").
func TestC05StoreErrorsNotFoundLocked(t *testing.T) {
	ctx := context.Background()
	binding := "wraithbox/binding/github"
	for _, tt := range []struct {
		status secretstore.Status
		want   error
		rule   storeitem.Rule
	}{
		{secretstore.StatusNotFound, secretstore.ErrNotFound, storeitem.RuleNotFound},
		{secretstore.StatusLocked, secretstore.ErrLocked, storeitem.RuleLocked},
	} {
		fake := storetest.New(team, groupBinding)
		fake.SetStatus(storeitem.AccessFetch, binding, tt.status)
		o := open(t, storeitem.ProgramProxyd, fake)
		value, err := o.store.Fetch(ctx, name(t, binding), storeitem.RoleBinding)
		if !errors.Is(err, tt.want) || value != nil {
			t.Errorf("status %d: %q, %v, want %v", tt.status, value, err, tt.want)
		}
		requireRule(t, o, err, tt.rule, storeitem.AccessFetch, binding)
		if !strings.Contains(o.log.String(), "status="+tt.status.String()) {
			t.Errorf("the log doesn't name the status:\n%s", o.log)
		}
	}
}

// TestC05StoreErrorsReadFailed returns -34018 and another status for a
// binding and a wbadmin/* read, and sees each give store-read-failed
// (C05-credentials-and-store, "Store errors").
func TestC05StoreErrorsReadFailed(t *testing.T) {
	ctx := context.Background()
	for _, status := range []secretstore.Status{secretstore.StatusMissingEntitlement, -50} {
		for _, tt := range []struct {
			program storeitem.Program
			group   string
			item    string
			role    storeitem.Role
		}{
			{storeitem.ProgramProxyd, groupBinding, "wraithbox/binding/github", storeitem.RoleBinding},
			{storeitem.ProgramGuestadminRotate, groupWbadmin, "wraithbox/wbadmin/image/base", storeitem.RoleWbadmin},
		} {
			fake := storetest.New(team, tt.group)
			fake.SetStatus(storeitem.AccessFetch, tt.item, status)
			o := open(t, tt.program, fake)
			_, err := o.store.Fetch(ctx, name(t, tt.item), tt.role)
			requireRule(t, o, err, storeitem.RuleReadFailed, storeitem.AccessFetch, tt.item)
			if errors.Is(err, secretstore.ErrNotFound) || errors.Is(err, secretstore.ErrLocked) {
				t.Errorf("status %d: %v wraps a sentinel", status, err)
			}
		}
	}
}

// TestC05StoreErrorsWriteFailed fails a binding write, a binding delete
// and a wbadmin/* write with -34018, and sees each give
// store-write-failed (C05-credentials-and-store, "Store errors").
func TestC05StoreErrorsWriteFailed(t *testing.T) {
	ctx := context.Background()
	binding := "wraithbox/binding/github"
	build := "wraithbox/wbadmin/build/b1"

	fake := storetest.New(team, groupBinding)
	fake.SetStatus(storeitem.AccessWrite, binding, secretstore.StatusMissingEntitlement)
	fake.SetStatus(storeitem.AccessDelete, binding, secretstore.StatusMissingEntitlement)
	credset := open(t, storeitem.ProgramCredset, fake)
	err := credset.store.Write(ctx, name(t, binding), storeitem.RoleBinding, []byte(secretValue))
	requireRule(t, credset, err, storeitem.RuleWriteFailed, storeitem.AccessWrite, binding)
	err = credset.store.Delete(ctx, name(t, binding), storeitem.RoleBinding)
	requireRule(t, credset, err, storeitem.RuleWriteFailed, storeitem.AccessDelete, binding)

	wbadminFake := storetest.New(team, groupWbadmin)
	wbadminFake.SetStatus(storeitem.AccessWrite, build, secretstore.StatusMissingEntitlement)
	guestadmin := open(t, storeitem.ProgramGuestadminBuild, wbadminFake)
	err = guestadmin.store.Write(ctx, name(t, build), storeitem.RoleWbadmin, []byte(secretValue))
	requireRule(t, guestadmin, err, storeitem.RuleWriteFailed, storeitem.AccessWrite, build)
}

// TestStoreRoundTrip writes a binding with wb-credset's identity, and
// fetches it with wb-proxyd's over the same fake, with each call in the
// group of its role.
func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	fake := storetest.New(team, groupBinding, groupCA)
	credset := open(t, storeitem.ProgramCredset, fake)
	if credset.err != nil {
		t.Fatal(credset.err)
	}
	item := name(t, "wraithbox/binding/github")
	value := []byte(secretValue)
	if err := credset.store.Write(ctx, item, storeitem.RoleBinding, value); err != nil {
		t.Fatal(err)
	}
	names, err := credset.store.List(ctx, storeitem.RoleBinding)
	if err != nil || len(names) != 1 || names[0] != item {
		t.Fatalf("List = %v, %v", names, err)
	}
	proxyd := open(t, storeitem.ProgramProxyd, fake)
	got, err := proxyd.store.Fetch(ctx, item, storeitem.RoleBinding)
	if err != nil || string(got) != secretValue {
		t.Fatalf("Fetch = %q, %v", got, err)
	}
	want := []storetest.Call{
		{Access: storeitem.AccessWrite, Group: groupBinding, Item: item.String()},
		{Access: storeitem.AccessList, Group: groupBinding},
		{Access: storeitem.AccessFetch, Group: groupBinding, Item: item.String()},
	}
	calls := fake.Calls()
	if len(calls) != len(want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %v, want %v", i, calls[i], want[i])
		}
	}
	if err := credset.store.Delete(ctx, item, storeitem.RoleBinding); err != nil {
		t.Fatal(err)
	}
	if _, err := proxyd.store.Fetch(ctx, item, storeitem.RoleBinding); !errors.Is(err, secretstore.ErrNotFound) {
		t.Errorf("Fetch after Delete: %v, want ErrNotFound", err)
	}
}

// TestStoreListRefusesForeignName sees a List fail with store-read-failed
// when the backend gives a name that isn't an item of the group.
func TestStoreListRefusesForeignName(t *testing.T) {
	ctx := context.Background()
	for _, listed := range []string{"not-an-item\nrule=forged", "wraithbox/ca/vm-1/ca-1", strings.Repeat("x", 4096)} {
		fake := storetest.New(team, groupBinding)
		fake.AddListed(listed)
		o := open(t, storeitem.ProgramCredset, fake)
		names, err := o.store.List(ctx, storeitem.RoleBinding)
		if names != nil {
			t.Errorf("List = %v", names)
		}
		requireRule(t, o, err, storeitem.RuleReadFailed, storeitem.AccessList, "")
		if strings.Contains(o.log.String(), strings.Repeat("x", 200)) {
			t.Error("the log holds the whole long name")
		}
	}
}
