//go:build !darwin

package project

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
)

// TestResolveUnsupportedHost: a host without a canonical form of a path
// refuses every directory and says so (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
func TestResolveUnsupportedHost(t *testing.T) {
	tmp := t.TempDir()
	r := &Resolver{Git: "/usr/bin/git", Own: paths.Paths{
		Data: filepath.Join(tmp, "d"), Config: filepath.Join(tmp, "c"), Logs: filepath.Join(tmp, "l"),
	}}
	_, err := r.Resolve(t.Context(), tmp)
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Rule != RuleHostUnsupported || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Resolve = %v, want %s", err, RuleHostUnsupported)
	}
}
