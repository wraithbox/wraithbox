package project

import (
	"slices"
)

// The rules of the identity check (S05-04-naming, "Identity check").
const (
	// RuleIdentityMatched: the current identity is the recorded one, or
	// the recorded one is empty.
	RuleIdentityMatched Rule = "project-identity-matched"
	// RuleURLsChanged: the current URL set isn't the recorded one.
	RuleURLsChanged Rule = "project-urls-changed"
	// RuleRootCommitGone: a recorded root commit isn't among the current
	// root commits.
	RuleRootCommitGone Rule = "project-root-commit-gone"
)

// Empty reports whether the identity has neither a URL nor a root
// commit.
func (id Identity) Empty() bool {
	return len(id.URLs) == 0 && len(id.RootCommits) == 0
}

// Normalized returns a copy with both lists sorted and without
// duplicates, the form Identity returns.
func (id Identity) Normalized() Identity {
	return Identity{URLs: sortedSet(id.URLs), RootCommits: sortedSet(id.RootCommits)}
}

func sortedSet(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := slices.Clone(s)
	slices.Sort(out)
	return slices.Compact(out)
}

// CheckIdentity compares a project's recorded identity with the
// repository's current one (S05-04-naming, "Identity check"). It
// returns RuleURLsChanged when the URL sets differ, RuleRootCommitGone
// when a recorded root commit is missing from the current ones, and
// RuleIdentityMatched otherwise. An empty recorded identity matches any
// current one: a repository that had neither a remote nor a commit
// when it was recorded can be replaced at its path before its first
// session start (S05-04-naming, "Grow-only"). The order of each list
// and duplicates in it don't count.
//
// Without this check a different repository cloned into the same
// directory would inherit the old project's approvals and policy
// (SEC08-proj-isolation).
func CheckIdentity(recorded, current Identity) Rule {
	if recorded.Empty() {
		return RuleIdentityMatched
	}
	r, c := recorded.Normalized(), current.Normalized()
	if !slices.Equal(r.URLs, c.URLs) {
		return RuleURLsChanged
	}
	for _, root := range r.RootCommits {
		if _, found := slices.BinarySearch(c.RootCommits, root); !found {
			return RuleRootCommitGone
		}
	}
	return RuleIdentityMatched
}

// Matches reports whether two identities name the same repository for
// registration (S05-04-naming, "Registration"): the same non-empty URL
// set, or, when neither has a URL, a shared root commit. An empty
// identity matches nothing.
func Matches(a, b Identity) bool {
	a, b = a.Normalized(), b.Normalized()
	switch {
	case len(a.URLs) > 0 || len(b.URLs) > 0:
		return slices.Equal(a.URLs, b.URLs)
	case len(a.RootCommits) == 0 || len(b.RootCommits) == 0:
		return false
	}
	for _, root := range a.RootCommits {
		if _, found := slices.BinarySearch(b.RootCommits, root); found {
			return true
		}
	}
	return false
}
