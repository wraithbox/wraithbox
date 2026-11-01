//go:build darwin && !cgo

package secretstore

import (
	"context"
	"errors"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// TestNoShim opens the store of a darwin build without cgo and sees
// every call refused with store-shim-missing (S15-02-secret-store,
// "Failed store calls"). Run it with CGO_ENABLED=0 go test.
func TestNoShim(t *testing.T) {
	store, err := Open(storeitem.ProgramCredset)
	var e *Error
	if !errors.As(err, &e) || e.Rule != storeitem.RuleShimMissing {
		t.Fatalf("Open: %v, want %s", err, storeitem.RuleShimMissing)
	}
	item, err := storeitem.Parse("wraithbox/binding/github")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, fetchErr := store.Fetch(ctx, item, storeitem.RoleBinding)
	_, listErr := store.List(ctx, storeitem.RoleBinding)
	for _, err := range []error{
		store.Write(ctx, item, storeitem.RoleBinding, []byte("value")),
		fetchErr,
		store.Delete(ctx, item, storeitem.RoleBinding),
		listErr,
	} {
		if !errors.As(err, &e) || e.Rule != storeitem.RuleShimMissing {
			t.Errorf("call: %v, want %s", err, storeitem.RuleShimMissing)
		}
	}
}
