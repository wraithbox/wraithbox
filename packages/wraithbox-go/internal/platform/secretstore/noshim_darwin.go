//go:build darwin && !cgo

package secretstore

import (
	"log/slog"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// Open is the store of a darwin build without cgo, such as go:cross
// from Linux. Such a build has no Keychain shim (S10-01-libraries), so
// it fails closed: every call is refused with store-shim-missing, logged
// at open (S15-02-secret-store, "Failed store calls"). The bundle's
// build sets CGO_ENABLED=1, so a bundle never ships this.
func Open(program storeitem.Program) (Store, error) {
	s := &checked{program: program, log: slog.Default()}
	return s, s.refuseOpen(storeitem.RuleShimMissing, "this darwin build has no cgo, so it has no Keychain shim")
}
