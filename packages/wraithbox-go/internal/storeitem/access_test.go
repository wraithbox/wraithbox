package storeitem

import (
	"slices"
	"testing"
)

// TestDeclaredAccess sees each row of S15-03-access-rules, "Declared
// access", in the table, and nothing else.
func TestDeclaredAccess(t *testing.T) {
	want := []Declaration{
		{ProgramCredset, FormBinding, AccessWrite},
		{ProgramCredset, FormBinding, AccessDelete},
		{ProgramCredset, FormBinding, AccessList},
		{ProgramProxyd, FormBinding, AccessFetch},
		{ProgramProxyd, FormBinding, AccessWrite},
		{ProgramProxyd, FormCA, AccessWrite},
		{ProgramProxyd, FormCA, AccessFetch},
		{ProgramProxyd, FormCA, AccessDelete},
		{ProgramProxyd, FormCA, AccessUse},
		{ProgramGuestadminBuild, FormWbadminBuild, AccessWrite},
		{ProgramGuestadminBuild, FormWbadminBuild, AccessFetch},
		{ProgramGuestadminRotate, FormWbadminBuild, AccessFetch},
		{ProgramGuestadminRotate, FormWbadminImage, AccessWrite},
		{ProgramGuestadminRotate, FormWbadminImage, AccessFetch},
		{ProgramGuestadminRotate, FormWbadminVM, AccessWrite},
		{ProgramGuestadminCleanup, FormWbadminBuild, AccessDelete},
		{ProgramGuestadminCleanup, FormWbadminImage, AccessDelete},
		{ProgramGuestadminCleanup, FormWbadminVM, AccessDelete},
	}
	got := Declarations()
	for _, d := range want {
		if !slices.Contains(got, d) {
			t.Errorf("missing %v", d)
		}
	}
	for _, d := range got {
		if !slices.Contains(want, d) {
			t.Errorf("not in S15-03-access-rules: %v", d)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d rows, want %d", len(got), len(want))
	}
}

// TestDeclaredRefusals checks the refusals the issue names: wb-credset
// may not fetch a binding, wb-proxyd may not touch wbadmin/*, and
// wb-guestadmin may not touch binding/* or ca/*.
func TestDeclaredRefusals(t *testing.T) {
	if Declared(ProgramCredset, FormBinding, AccessFetch) {
		t.Error("wb-credset may fetch a binding")
	}
	if Declared("wb", FormBinding, AccessFetch) {
		t.Error("a program outside the table may fetch a binding")
	}
	wbadmin := []Form{FormWbadminBuild, FormWbadminImage, FormWbadminVM}
	bindingAndCA := []Form{FormBinding, FormCA}
	untouchable := map[Program][]Form{
		ProgramProxyd:            wbadmin,
		ProgramGuestadminBuild:   bindingAndCA,
		ProgramGuestadminRotate:  bindingAndCA,
		ProgramGuestadminCleanup: bindingAndCA,
	}
	for _, d := range everyAccess() {
		if slices.Contains(untouchable[d.Program], d.Form) && Declared(d.Program, d.Form, d.Access) {
			t.Errorf("%s may %s %s", d.Program, d.Access, d.Form)
		}
	}
}

// everyAccess returns every program, form and access kind.
func everyAccess() []Declaration {
	programs := []Program{
		ProgramCredset, ProgramProxyd,
		ProgramGuestadminBuild, ProgramGuestadminRotate, ProgramGuestadminCleanup,
	}
	var out []Declaration
	for _, p := range programs {
		for _, f := range Forms() {
			for _, a := range []Access{AccessWrite, AccessDelete, AccessFetch, AccessList, AccessUse} {
				out = append(out, Declaration{p, f, a})
			}
		}
	}
	return out
}

// TestFormRoles sees every form have a valid role, and every role a form.
func TestFormRoles(t *testing.T) {
	for _, f := range Forms() {
		if !f.Role().Valid() {
			t.Errorf("form %q has role %q", f, f.Role())
		}
	}
	for _, r := range Roles() {
		if !slices.ContainsFunc(Forms(), func(f Form) bool { return f.Role() == r }) {
			t.Errorf("role %q has no form", r)
		}
	}
	if Form("other/<x>").Role() != "" || Role("other").Valid() {
		t.Error("an unknown form or role is valid")
	}
}
