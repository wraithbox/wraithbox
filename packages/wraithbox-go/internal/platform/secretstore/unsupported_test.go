//go:build linux || windows

package secretstore

import (
	"context"
	"errors"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// TestUnsupported is the platform contract test for an OS without a
// store backend: Open and every call fail with an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
func TestUnsupported(t *testing.T) {
	store, err := Open(storeitem.ProgramCredset)
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Open: %v, want errors.ErrUnsupported", err)
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
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("call: %v, want errors.ErrUnsupported", err)
		}
	}
}
