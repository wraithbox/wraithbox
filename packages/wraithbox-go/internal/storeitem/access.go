package storeitem

import "slices"

// Program is the compiled-in identity of a program with store access. A
// program opens the store with its own constant, never with one from a
// flag, the environment or a request (S15-03-access-rules, rule 1).
// wb-guestadmin has one identity per run kind, the fixed entries of
// wb-launcher (S04-05-helper-programs, "wb-guestadmin's profile").
type Program string

// The programs of S15-03-access-rules, "Declared access".
const (
	ProgramCredset           Program = "wb-credset" //nolint:gosec // G101: the name of a program, not a credential
	ProgramProxyd            Program = "wb-proxyd"
	ProgramGuestadminBuild   Program = "wb-guestadmin-build"
	ProgramGuestadminRotate  Program = "wb-guestadmin-rotate"
	ProgramGuestadminCleanup Program = "wb-guestadmin-cleanup"
)

// Access is an access kind of S15-03-access-rules, rule 1.
type Access string

// The access kinds.
const (
	AccessWrite  Access = "write"
	AccessDelete Access = "delete"
	// AccessFetch brings the value into the process.
	AccessFetch Access = "fetch"
	// AccessList gives item names only, never a value.
	AccessList Access = "list"
	// AccessUse signs or decrypts in place, and the key stays in the
	// store or the hardware key store.
	AccessUse Access = "use"
)

// Declaration is one declared access: a program may make one access of
// one kind to the items of one form.
type Declaration struct {
	Program Program
	Form    Form
	Access  Access
}

// declared is the table of S15-03-access-rules, "Declared access", one
// row per program, item form and access kind. Nothing outside it is
// allowed (rule 1). The CA signing key's use is through the wrapped key
// item ca/<vm>/<ca>, and on Windows the named key has the same name.
var declared = []Declaration{
	{ProgramCredset, FormBinding, AccessWrite},
	{ProgramCredset, FormBinding, AccessDelete},
	{ProgramCredset, FormBinding, AccessList},

	{ProgramProxyd, FormBinding, AccessFetch},
	// The OAuth refresh token of a binding.
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

	// Rule 8: the items of builds, images and disks that are gone.
	{ProgramGuestadminCleanup, FormWbadminBuild, AccessDelete},
	{ProgramGuestadminCleanup, FormWbadminImage, AccessDelete},
	{ProgramGuestadminCleanup, FormWbadminVM, AccessDelete},
}

// Declarations returns a copy of the declared access table.
func Declarations() []Declaration { return slices.Clone(declared) }

// Declared reports whether program p may make access a to the items of
// form f.
func Declared(p Program, f Form, a Access) bool {
	return slices.Contains(declared, Declaration{p, f, a})
}

// Rule names the rule behind a refusal or a failed store call, for the
// log and the caller's error (NFR06-explained-refusals).
type Rule string

// The rules of the secret store's checks and failures.
const (
	// RuleDialogsOn: the call that turns Keychain dialogs off failed, so
	// the process doesn't touch the store (S15-03-access-rules, "No
	// Keychain dialogs").
	RuleDialogsOn Rule = "store-dialogs-on"
	// RuleNeedsTeam: the process's own signature has no team, so it has no
	// Keychain backend (S15-02-secret-store, "Ad-hoc signed builds").
	RuleNeedsTeam Rule = "keychain-needs-team"
	// RuleGroupMismatch: the group the call names isn't the group of the
	// item, or the process's entitlements don't hold it or can't be read
	// (S15-02-secret-store, "The group of each call").
	RuleGroupMismatch Rule = "store-group-mismatch"
	// RuleAccessUndeclared: the access isn't in the declared access table
	// for the program (S15-03-access-rules, rule 1).
	RuleAccessUndeclared Rule = "store-access-undeclared"
	// RuleShimMissing: a darwin build without cgo, which has no Keychain
	// shim (S15-02-secret-store, "Failed store calls").
	RuleShimMissing Rule = "store-shim-missing"
	// RuleNotFound: the item isn't in the store, -25300 on the Keychain.
	// The caller refuses its work with its own rule.
	RuleNotFound Rule = "store-item-not-found"
	// RuleLocked: the store is locked, -25308 on the Keychain. The caller
	// waits for the unlock (S15-03-access-rules, rule 7).
	RuleLocked Rule = "store-locked"
	// RuleReadFailed: any other failure of a fetch or a list
	// (S15-02-secret-store, "Failed store calls").
	RuleReadFailed Rule = "store-read-failed"
	// RuleWriteFailed: any other failure of a write or a delete
	// (S15-02-secret-store, "Failed store calls").
	RuleWriteFailed Rule = "store-write-failed"
)
