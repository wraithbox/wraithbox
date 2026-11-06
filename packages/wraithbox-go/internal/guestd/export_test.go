package guestd

import (
	"context"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/trust"
)

// TrustInstallFunc is a fake trust installer for the tests of
// InstallTrust.
type TrustInstallFunc func(ctx context.Context, ders [][]byte) *trust.Refusal

// Install calls f.
func (f TrustInstallFunc) Install(ctx context.Context, ders [][]byte) *trust.Refusal {
	return f(ctx, ders)
}

// WithTrustInstaller makes InstallTrust install with f instead of the
// guest's trust directory, which only these tests change.
func WithTrustInstaller(f TrustInstallFunc) Option { return func(s *service) { s.trust = f } }
