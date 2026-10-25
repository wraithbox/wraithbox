package project

import "testing"

const (
	rootA = "1111111111111111111111111111111111111111"
	rootB = "2222222222222222222222222222222222222222"
	urlA  = "https://a.example/a.git"
	urlB  = "https://b.example/b.git"
)

// TestCheckIdentity: each refusal of the identity check with its rule,
// and what passes (S05-04-naming, "Identity check").
func TestCheckIdentity(t *testing.T) {
	tests := []struct {
		name              string
		recorded, current Identity
		want              Rule
	}{
		{"same URLs", Identity{URLs: []string{urlA, urlB}}, Identity{URLs: []string{urlB, urlA, urlA}}, RuleIdentityMatched},
		{"URL added", Identity{URLs: []string{urlA}}, Identity{URLs: []string{urlA, urlB}}, RuleURLsChanged},
		{"URL removed", Identity{URLs: []string{urlA, urlB}}, Identity{URLs: []string{urlA}}, RuleURLsChanged},
		{"URL replaced", Identity{URLs: []string{urlA}}, Identity{URLs: []string{urlB}}, RuleURLsChanged},
		{"all URLs gone", Identity{URLs: []string{urlA}}, Identity{RootCommits: []string{rootA}}, RuleURLsChanged},
		{"remote added to a recorded root", Identity{RootCommits: []string{rootA}}, Identity{URLs: []string{urlA}}, RuleURLsChanged},
		{"same root", Identity{RootCommits: []string{rootA}}, Identity{RootCommits: []string{rootA}}, RuleIdentityMatched},
		{"root added", Identity{RootCommits: []string{rootA}}, Identity{RootCommits: []string{rootB, rootA}}, RuleIdentityMatched},
		{"root gone", Identity{RootCommits: []string{rootA, rootB}}, Identity{RootCommits: []string{rootB}}, RuleRootCommitGone},
		{"root replaced", Identity{RootCommits: []string{rootA}}, Identity{RootCommits: []string{rootB}}, RuleRootCommitGone},
		{"commits gone", Identity{RootCommits: []string{rootA}}, Identity{}, RuleRootCommitGone},
		{"empty recorded, URL now", Identity{}, Identity{URLs: []string{urlA}}, RuleIdentityMatched},
		{"empty recorded, root now", Identity{}, Identity{RootCommits: []string{rootA}}, RuleIdentityMatched},
		{"both empty", Identity{}, Identity{}, RuleIdentityMatched},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckIdentity(tt.recorded, tt.current); got != tt.want {
				t.Errorf("CheckIdentity(%+v, %+v) = %s, want %s", tt.recorded, tt.current, got, tt.want)
			}
		})
	}
}

// TestMatches: the matching of registration (S05-04-naming,
// "Registration").
func TestMatches(t *testing.T) {
	tests := []struct {
		name string
		a, b Identity
		want bool
	}{
		{"same URLs", Identity{URLs: []string{urlA, urlB}}, Identity{URLs: []string{urlB, urlA}}, true},
		{"one URL more", Identity{URLs: []string{urlA}}, Identity{URLs: []string{urlA, urlB}}, false},
		{"other URL", Identity{URLs: []string{urlA}}, Identity{URLs: []string{urlB}}, false},
		{"URL and root", Identity{URLs: []string{urlA}}, Identity{RootCommits: []string{rootA}}, false},
		{"shared root", Identity{RootCommits: []string{rootA, rootB}}, Identity{RootCommits: []string{rootB}}, true},
		{"no shared root", Identity{RootCommits: []string{rootA}}, Identity{RootCommits: []string{rootB}}, false},
		{"empty and empty", Identity{}, Identity{}, false},
		{"empty and root", Identity{}, Identity{RootCommits: []string{rootA}}, false},
		{"empty and URL", Identity{URLs: []string{urlA}}, Identity{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Matches(tt.a, tt.b); got != tt.want {
				t.Errorf("Matches(%+v, %+v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			if got := Matches(tt.b, tt.a); got != tt.want {
				t.Errorf("Matches(%+v, %+v) = %v, want %v", tt.b, tt.a, got, tt.want)
			}
		})
	}
}
