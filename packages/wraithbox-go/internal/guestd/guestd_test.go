package guestd_test

import (
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
)

// TestServedPackages: wb-guestd serves the package of its contract.
func TestServedPackages(t *testing.T) {
	got := guestd.ServedPackages()
	if len(got) != 1 || got[0] != "wraithbox.guest.v1" {
		t.Fatalf("ServedPackages = %q, want [wraithbox.guest.v1]", got)
	}
}

// TestMaxRecvMsgSize: the host sends nothing large, so wb-guestd takes at
// most 64 KiB in one message.
func TestMaxRecvMsgSize(t *testing.T) {
	if guestd.MaxRecvMsgSize != 64<<10 {
		t.Fatalf("MaxRecvMsgSize = %d, want 64 KiB", guestd.MaxRecvMsgSize)
	}
}
