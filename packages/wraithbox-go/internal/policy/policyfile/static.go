package policyfile

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// GlobalFileName is the name of the global network policy file in
// <config> (S09-01-policy-files, "Files").
const GlobalFileName = "policy.yaml"

// DefaultWildcardBudget is the wildcard budget of a VM whose global
// policy sets none: the new names a VM may resolve under wildcards in
// an active period (S07-03-dns, "Wildcard budget").
const DefaultWildcardBudget = 256

// Static returns a VM's effective policy at its start, until the
// per-VM union of M3.3c replaces it: the built-in profiles and the
// global policy file in configDir, with no project files, modes, limits
// or union. wb-hostd sends it to wb-netd and wb-proxyd. A refused
// global file refuses the start, and the error is a *Refusals.
//
// The built-in profiles have no rules in package builtin yet (M3.7a),
// so the endpoints are the global file's. The checks that guard the
// covered hosts apply all the same, in Load and in policy.Check.
func Static(ctx context.Context, configDir string) (*policy.Policy, error) {
	f, err := LoadFile(ctx, filepath.Join(configDir, GlobalFileName), policy.Source{Kind: policy.SourceGlobal})
	if err != nil {
		return nil, err
	}
	p := f.policy()
	p.Generation = 1
	if f.WildcardBudget == nil {
		p.WildcardBudget = DefaultWildcardBudget
	}
	if r := p.Check(); r != nil {
		return nil, fmt.Errorf("the effective policy from %s is refused: %w", f.Name, r)
	}
	return p, nil
}
