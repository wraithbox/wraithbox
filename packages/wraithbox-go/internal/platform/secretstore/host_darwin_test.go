//go:build darwin && cgo && wbstorehost

package secretstore

// The host tests of the Keychain backend (C05-credentials-and-store,
// "Host tests"). They need the data protection keychain, so they run
// only in a test binary that `mise run store:host-test` signs with the
// team's identity into a bundle of its own, with the app's profile, and
// starts with an empty environment but for WB_STORE_HOST_TEST=1,
// WB_STORE_HOST_STEP and WB_STORE_HOST_G2_OUT. They build only with the
// wbstorehost tag, which that task sets, and without the variables each
// one skips.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/selfsandbox"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

const (
	// hostEnv turns the host tests on.
	hostEnv = "WB_STORE_HOST_TEST"
	// stepEnv names the step of the run: "" for the bundle with the
	// binding and ca groups, binding-only for the bundle with binding
	// alone, and confine-after, confine-first, lock and g2.
	stepEnv = "WB_STORE_HOST_STEP"
	// g2OutEnv is the file the maintainer's run of Apple's security tool
	// writes to in the g2 step.
	g2OutEnv = "WB_STORE_HOST_G2_OUT"
	// hostPrefix is the prefix of every item a host test makes. TestMain
	// fails a run that leaves one behind.
	hostPrefix = "wbtest-"
)

// storeOnly is the store-only profile of S04-03-self-sandboxing,
// "Secret store", as X23-sandboxed-daemons ran it. wb-credset gets its
// own copy with I504. This one is the test's, so the measurement of
// decision 7 on I505 doesn't depend on that program.
var storeOnly = selfsandbox.Profile{
	Name: "store-only-host-test",
	Text: `(version 1)
(deny default)
(allow mach-lookup (global-name "com.apple.securityd.xpc" "com.apple.ctkd.token-client"))
`,
}

func TestMain(m *testing.M) {
	// The groups are read before any test confines the process: under the
	// store-only profile the process can't read its own signature
	// (OSStatus 100001), and the Keychain calls of the check still work.
	var groups []string
	var groupsErr error
	if os.Getenv(hostEnv) == "1" {
		groups, groupsErr = (&keychain{}).Groups()
	}
	code := m.Run()
	if os.Getenv(hostEnv) == "1" && leftovers(groups, groupsErr) {
		code = 1
	}
	os.Exit(code)
}

// leftovers lists the items with hostPrefix in groups, the groups the
// process holds or the error of reading them, reports each, and returns
// whether there was one.
func leftovers(groups []string, err error) bool {
	k := &keychain{}
	if err != nil {
		fmt.Fprintf(os.Stderr, "host test: reading the groups: %v\n", err)
		return true
	}
	found := false
	for _, group := range groups {
		names, st := k.List(context.Background(), group)
		if st != StatusOK {
			fmt.Fprintf(os.Stderr, "host test: listing %s: OSStatus %d\n", group, st)
			found = true
			continue
		}
		for _, name := range names {
			if strings.Contains(name, "/"+hostPrefix) {
				fmt.Fprintf(os.Stderr, "host test: %s is left in %s\n", name, group)
				found = true
			}
		}
	}
	return found
}

// hostStep skips the test unless the host tests are on and the run's
// step is step.
func hostStep(t *testing.T, step string) {
	t.Helper()
	if os.Getenv(hostEnv) != "1" {
		t.Skip("a host test: mise run store:host-test runs it in a team-signed bundle")
	}
	if got := os.Getenv(stepEnv); got != step {
		t.Skipf("the run's step is %q, and this test's %q", got, step)
	}
}

// hostBackend is the real Keychain backend with the executable of
// program, so one test bundle can open the store as each program. The
// signing identifier, the team and the groups stay the bundle's.
type hostBackend struct {
	*keychain
	program storeitem.Program
}

func (b hostBackend) Code() (Code, error) {
	code, err := b.keychain.Code()
	code.Executable = b.program.Executable()
	return code, err
}

// openAs opens the real Keychain backend as program, logging to the
// test's output.
func openAs(t *testing.T, program storeitem.Program) Store {
	t.Helper()
	log := slog.New(slog.NewTextHandler(t.Output(), nil))
	store, err := New(program, hostBackend{&keychain{}, program}, log)
	if err != nil {
		t.Fatalf("open as %s: %v", program, err)
	}
	return store
}

// team returns the team of the bundle's signature.
func team(t *testing.T) string {
	t.Helper()
	team, err := (&keychain{}).Team()
	if err != nil || team == "" {
		t.Fatalf("the bundle's team: %q, %v", team, err)
	}
	return team
}

// testItem returns a new item name of form, with hostPrefix and random
// parts, and removes the item when the test ends, whatever the test did.
func testItem(t *testing.T, form storeitem.Form) storeitem.Name {
	t.Helper()
	random := func() string {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		return hostPrefix + hex.EncodeToString(b)
	}
	var s string
	switch form {
	case storeitem.FormBinding:
		s = storeitem.Prefix + "binding/" + random()
	case storeitem.FormCA:
		s = storeitem.Prefix + "ca/" + random() + "/" + random()
	case storeitem.FormWbadminBuild, storeitem.FormWbadminImage, storeitem.FormWbadminVM:
		// The test bundles don't hold the wbadmin group.
		t.Fatalf("no test item of form %s", form)
	}
	item, err := storeitem.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	group := storeitem.Group(team(t), item.Role())
	t.Cleanup(func() {
		st := (&keychain{}).Delete(t.Context(), group, item.String())
		if st != StatusOK && st != StatusNotFound {
			t.Errorf("removing %s: OSStatus %d", item, st)
		}
	})
	return item
}

// wantRule fails the test unless err is an *Error with rule.
func wantRule(t *testing.T, call string, err error, rule storeitem.Rule) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Rule != rule {
		t.Errorf("%s: %v, want %s", call, err, rule)
	}
}

// TestC05HostEnvironment sees the bundle run with nothing in its
// environment but the host test's own variables (decision 8 on I505),
// signed by a team, with the groups its step signs it with, with
// Keychain dialogs off after an open.
func TestC05HostEnvironment(t *testing.T) {
	hostStep(t, "")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "WB_STORE_HOST_") {
			name, _, _ := strings.Cut(kv, "=")
			t.Errorf("the environment holds %s", name)
		}
	}
	tm := team(t)
	groups, err := (&keychain{}).Groups()
	want := []string{storeitem.Group(tm, storeitem.RoleBinding), storeitem.Group(tm, storeitem.RoleCA)}
	if err != nil || !slices.Equal(groups, want) {
		t.Errorf("groups: %q, %v; want %q", groups, err, want)
	}
	openAs(t, storeitem.ProgramProxyd)
	if allowed, err := dialogsAllowed(); err != nil || allowed {
		t.Errorf("after open, dialogs allowed = %v, %v; want false", allowed, err)
	}
}

// TestC05HostProgramMismatch opens the store with the real backend, whose
// signed Info.plist names the test bundle's executable, and sees it
// refused with store-program-mismatch (S15-02-secret-store, "The group
// of each call").
func TestC05HostProgramMismatch(t *testing.T) {
	hostStep(t, "")
	code, err := (&keychain{}).Code()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("signing identifier %q, executable %q", code.Identifier, code.Executable)
	if code.Executable != filepath.Base(exe) || code.Identifier == "" {
		t.Errorf("code %+v, want the executable %q and an identifier", code, filepath.Base(exe))
	}
	_, err = Open(storeitem.ProgramCredset)
	wantRule(t, "open as credset", err, storeitem.RuleProgramMismatch)
}

// TestC05HostBindingRoundTrip writes, fetches, updates by a second
// write, lists and deletes a binding item, as wb-credset and wb-proxyd
// do (C05-credentials-and-store).
func TestC05HostBindingRoundTrip(t *testing.T) {
	hostStep(t, "")
	ctx := t.Context()
	credset := openAs(t, storeitem.ProgramCredset)
	proxyd := openAs(t, storeitem.ProgramProxyd)
	item := testItem(t, storeitem.FormBinding)

	if err := credset.Write(ctx, item, storeitem.RoleBinding, []byte("first")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, err := proxyd.Fetch(ctx, item, storeitem.RoleBinding); err != nil || string(got) != "first" {
		t.Fatalf("fetch: %q, %v; want first", got, err)
	}
	// The second write is the update of S15-02-secret-store, "Keychain".
	if err := proxyd.Write(ctx, item, storeitem.RoleBinding, []byte("second")); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got, err := proxyd.Fetch(ctx, item, storeitem.RoleBinding); err != nil || string(got) != "second" {
		t.Fatalf("fetch after the update: %q, %v; want second", got, err)
	}
	checkAttributes(t, item, storeitem.RoleBinding)
	names, err := credset.List(ctx, storeitem.RoleBinding)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !slices.Contains(names, item) {
		t.Errorf("list %v doesn't hold %s", names, item)
	}
	if err := credset.Delete(ctx, item, storeitem.RoleBinding); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = proxyd.Fetch(ctx, item, storeitem.RoleBinding)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("fetch after the delete: %v, want ErrNotFound", err)
	}
}

// TestC05HostCARoundTrip writes, fetches and deletes a ca item as
// wb-proxyd, and reads back its attributes.
func TestC05HostCARoundTrip(t *testing.T) {
	hostStep(t, "")
	ctx := t.Context()
	proxyd := openAs(t, storeitem.ProgramProxyd)
	item := testItem(t, storeitem.FormCA)
	value := []byte{0, 1, 2, 0xff}
	if err := proxyd.Write(ctx, item, storeitem.RoleCA, value); err != nil {
		t.Fatalf("write: %v", err)
	}
	checkAttributes(t, item, storeitem.RoleCA)
	if got, err := proxyd.Fetch(ctx, item, storeitem.RoleCA); err != nil || !bytes.Equal(got, value) {
		t.Fatalf("fetch: %x, %v; want %x", got, err, value)
	}
	if err := proxyd.Delete(ctx, item, storeitem.RoleCA); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// checkAttributes reads back the attributes of item and sees one item, in
// the group of role, with the fixed account, of the protection class of
// every write, that doesn't synchronize (G4, S15-02-secret-store, "Backend guarantees").
func checkAttributes(t *testing.T, item storeitem.Name, role storeitem.Role) {
	t.Helper()
	a, st := readItemAttributes(item.String())
	if st != StatusOK {
		t.Fatalf("attributes of %s: OSStatus %d", item, st)
	}
	t.Logf("attributes of %s: %+v", item, a)
	if want := storeitem.Group(team(t), role); a.Count != 1 || a.Group != want {
		t.Errorf("%d items, the first in %q; want 1 in %q", a.Count, a.Group, want)
	}
	// WB_ACCOUNT of items_darwin.go.
	if a.Account != "wraithbox" {
		t.Errorf("account %q, want wraithbox", a.Account)
	}
	if !a.WhenUnlockedThisDeviceOnly {
		t.Errorf("protection class %q, want kSecAttrAccessibleWhenUnlockedThisDeviceOnly", a.Accessible)
	}
	// The Keychain may leave a false attribute out of what it returns.
	if a.Synchronizable == 1 {
		t.Error("the item synchronizes")
	}
}

// TestC05HostNotFound fetches an item that isn't there and sees
// ErrNotFound with store-item-not-found.
func TestC05HostNotFound(t *testing.T) {
	hostStep(t, "")
	item := testItem(t, storeitem.FormBinding)
	_, err := openAs(t, storeitem.ProgramProxyd).Fetch(t.Context(), item, storeitem.RoleBinding)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("fetch: %v, want ErrNotFound", err)
	}
	wantRule(t, "fetch", err, storeitem.RuleNotFound)
}

// TestC05HostGroupMismatch runs in the bundle signed with the binding
// group alone. The checks refuse a ca fetch with store-group-mismatch,
// and the same fetch made straight on the backend, past the checks, gets
// -34018 from the Keychain itself (S15-02-secret-store, "The group of
// each call").
func TestC05HostGroupMismatch(t *testing.T) {
	hostStep(t, "binding-only")
	tm := team(t)
	groups, err := (&keychain{}).Groups()
	if want := []string{storeitem.Group(tm, storeitem.RoleBinding)}; err != nil || !slices.Equal(groups, want) {
		t.Fatalf("groups: %q, %v; want %q", groups, err, want)
	}
	item, err := storeitem.Parse(storeitem.Prefix + "ca/" + hostPrefix + "vm/" + hostPrefix + "ca")
	if err != nil {
		t.Fatal(err)
	}
	_, err = openAs(t, storeitem.ProgramProxyd).Fetch(t.Context(), item, storeitem.RoleCA)
	wantRule(t, "fetch through the checks", err, storeitem.RuleGroupMismatch)

	caGroup := storeitem.Group(tm, storeitem.RoleCA)
	if _, st := (&keychain{}).Fetch(t.Context(), caGroup, item.String()); st != StatusMissingEntitlement {
		t.Errorf("fetch past the checks: OSStatus %d, want %d", st, StatusMissingEntitlement)
	}
	if _, st := (&keychain{}).List(t.Context(), caGroup); st != StatusMissingEntitlement {
		t.Errorf("list past the checks: OSStatus %d, want %d", st, StatusMissingEntitlement)
	}
}

// TestC05HostLocked waits for the maintainer to lock the screen, sees a
// fetch return ErrLocked, then waits for the unlock and sees the same
// fetch succeed (S15-03-access-rules, rule 7). No dialog may appear.
func TestC05HostLocked(t *testing.T) {
	hostStep(t, "lock")
	ctx := t.Context()
	proxyd := openAs(t, storeitem.ProgramProxyd)
	item := testItem(t, storeitem.FormBinding)
	if err := proxyd.Write(ctx, item, storeitem.RoleBinding, []byte("locked")); err != nil {
		t.Fatalf("write: %v", err)
	}
	fetch := func() error {
		_, err := proxyd.Fetch(ctx, item, storeitem.RoleBinding)
		return err
	}
	t.Log(">>> LOCK THE SCREEN NOW (Control-Command-Q), and unlock it when this test says so")
	err := poll(t, func() bool { return errors.Is(fetch(), ErrLocked) })
	if err != nil {
		t.Fatalf("no ErrLocked while waiting for the lock: %v", err)
	}
	wantRule(t, "fetch while locked", fetch(), storeitem.RuleLocked)
	t.Log(">>> LOCKED: a fetch gave ErrLocked. UNLOCK THE SCREEN NOW")
	if err := poll(t, func() bool { return fetch() == nil }); err != nil {
		t.Fatalf("the fetch didn't succeed after the unlock: %v", err)
	}
	t.Log(">>> UNLOCKED: the same fetch succeeded")
}

// TestC05HostG2 holds a binding item while the maintainer looks for it
// with Apple's security tool, which names no group, from another
// terminal. The tool must not find it (G2, S15-02-secret-store,
// "Backend guarantees"). The test writes the item, prints the command,
// waits for the command's output in the file of g2OutEnv, reads it, and
// sees the item still there afterwards. Agents never run security
// (docs/agents/host-work.md), so the maintainer does.
func TestC05HostG2(t *testing.T) {
	hostStep(t, "g2")
	out := os.Getenv(g2OutEnv)
	if out == "" {
		t.Fatalf("%s names no file", g2OutEnv)
	}
	ctx := t.Context()
	proxyd := openAs(t, storeitem.ProgramProxyd)
	item := testItem(t, storeitem.FormBinding)
	if err := proxyd.Write(ctx, item, storeitem.RoleBinding, []byte("g2")); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf(">>> IN ANOTHER TERMINAL, RUN:\n\n/usr/bin/security find-generic-password -s %s 2>&1 | tee %s\n", item, out)
	var text string
	err := poll(t, func() bool {
		b, err := os.ReadFile(out)
		text = string(b)
		return err == nil && strings.Contains(text, "\n")
	})
	if err != nil {
		t.Fatalf("no output in %s: %v", out, err)
	}
	t.Logf("security's output:\n%s", text)
	if !strings.Contains(text, "could not be found") || strings.Contains(text, item.String()) {
		t.Errorf("security found %s, or said something else", item)
	}
	if got, err := proxyd.Fetch(ctx, item, storeitem.RoleBinding); err != nil || string(got) != "g2" {
		t.Errorf("the item after the search: %q, %v; want g2", got, err)
	}
}

// poll calls ok every two seconds until it returns true, for at most
// three minutes.
func poll(t *testing.T, ok func() bool) error {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if ok() {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return errors.New("timed out after three minutes")
}

// TestC05HostConfineAfterOpen opens the store, confines the process with
// the store-only profile, and sees a binding round trip work, the order
// of wb-credset (decision 7 on I505).
func TestC05HostConfineAfterOpen(t *testing.T) {
	hostStep(t, "confine-after")
	credset := openAs(t, storeitem.ProgramCredset)
	proxyd := openAs(t, storeitem.ProgramProxyd)
	item := testItem(t, storeitem.FormBinding)
	if err := selfsandbox.Confine(storeOnly, nil); err != nil {
		t.Fatalf("confine: %v", err)
	}
	if err := roundTrip(t, credset, proxyd, item); err != nil {
		t.Error(err)
	}
}

// TestC05HostConfineFirst confines the process with the store-only
// profile, then opens the store, and sees the open refused with
// keychain-needs-team: the profile blocks the read of the process's own
// signature (OSStatus 100001), so a program opens the store before it
// confines itself (decision 7 on I505; S15-06-binding-commands,
// "`wb-credset`"). If this starts to pass, the order can change.
func TestC05HostConfineFirst(t *testing.T) {
	hostStep(t, "confine-first")
	if err := selfsandbox.Confine(storeOnly, nil); err != nil {
		t.Fatalf("confine: %v", err)
	}
	log := slog.New(slog.NewTextHandler(t.Output(), nil))
	_, err := New(storeitem.ProgramCredset, hostBackend{&keychain{}, storeitem.ProgramCredset}, log)
	wantRule(t, "open after confine", err, storeitem.RuleNeedsTeam)
	// 100001 was measured before the shim checked the signature's
	// validity, which may now be the call that fails, so any status is
	// the refusal, and the log names it.
	_, err = (&keychain{}).Team()
	t.Logf("reading the signature after confine: %v", err)
	if err == nil || !strings.Contains(err.Error(), "OSStatus") {
		t.Errorf("reading the signature after confine: %v, want an OSStatus", err)
	}
}

// roundTrip writes item as credset, fetches it as proxyd, and deletes it
// as credset.
func roundTrip(t *testing.T, credset, proxyd Store, item storeitem.Name) error {
	t.Helper()
	ctx := t.Context()
	if err := credset.Write(ctx, item, storeitem.RoleBinding, []byte("confined")); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	got, err := proxyd.Fetch(ctx, item, storeitem.RoleBinding)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if string(got) != "confined" {
		return fmt.Errorf("fetch: %q, want confined", got)
	}
	if err := credset.Delete(ctx, item, storeitem.RoleBinding); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}
