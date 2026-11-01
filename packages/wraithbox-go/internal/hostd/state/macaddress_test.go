package state

import (
	"bytes"
	"errors"
	"testing"
)

// TestMACAddress is C11-host-daemons, "Packet transport": a slot's MAC
// address is made once, a locally administered unicast address, kept
// across calls and restarts, and differs between slots.
func TestMACAddress(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, opts(migrations))
	ctx := t.Context()
	work, err := db.MACAddress(ctx, "macos-work")
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != MACLength || work[0]&0x01 != 0 || work[0]&0x02 == 0 {
		t.Fatalf("MAC address %x isn't a locally administered unicast address", work)
	}
	if again, err := db.MACAddress(ctx, "macos-work"); err != nil || !bytes.Equal(again, work) {
		t.Fatalf("second call = %x, %v; want %x", again, err, work)
	}
	isolated, err := db.MACAddress(ctx, "macos-isolated")
	if err != nil || bytes.Equal(isolated, work) {
		t.Fatalf("another slot's = %x, %v; want an address other than %x", isolated, err, work)
	}
	for _, slot := range []string{"", string(bytes.Repeat([]byte("s"), 65))} {
		if _, err := db.MACAddress(ctx, slot); !errors.Is(err, errSlotName) {
			t.Errorf("slot %q: err = %v, want errSlotName", slot, err)
		}
	}
	_ = db.Close()

	db = mustOpen(t, dir, opts(migrations))
	if after, err := db.MACAddress(ctx, "macos-work"); err != nil || !bytes.Equal(after, work) {
		t.Errorf("after a restart = %x, %v; want %x", after, err, work)
	}
}

// TestMACAddressBits: whatever the random bytes, the address is
// locally administered and unicast.
func TestMACAddressBits(t *testing.T) {
	for _, fill := range []byte{0x00, 0xFF, 0x01, 0xFE} {
		withRandom(t, func(b []byte) {
			for i := range b {
				b[i] = fill
			}
		})
		mac := newMAC()
		if checkMAC("s", mac) != nil {
			t.Errorf("random bytes %#x gave %x", fill, mac)
		}
	}
}

// TestMACAddressTaken: a new address that is another slot's is made
// again, and a slot never gets another slot's address.
func TestMACAddressTaken(t *testing.T) {
	db := mustOpen(t, t.TempDir(), opts(migrations))
	ctx := t.Context()
	n := 0
	withRandom(t, func(b []byte) {
		n++
		if n > 2 {
			b[5] = byte(n)
		}
	})
	work, err := db.MACAddress(ctx, "macos-work")
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := db.MACAddress(ctx, "macos-isolated")
	if err != nil || bytes.Equal(isolated, work) {
		t.Fatalf("isolated = %x, %v; work has %x", isolated, err, work)
	}

	withRandom(t, func(b []byte) { clear(b) })
	if _, err := db.MACAddress(ctx, "macos-build"); !errors.Is(err, errMACTaken) {
		t.Fatalf("with every new address taken: err = %v, want errMACTaken", err)
	}
}

// TestMACAddressCheckedOnRead: an address in the database that wb-vmd
// would refuse is an error, not a start with it.
func TestMACAddressCheckedOnRead(t *testing.T) {
	db := mustOpen(t, t.TempDir(), opts(migrations))
	if _, err := db.db.ExecContext(t.Context(),
		`INSERT INTO vm_mac_addresses (slot, mac) VALUES ('macos-work', x'011122334455')`); err != nil {
		t.Fatal(err)
	}
	if mac, err := db.MACAddress(t.Context(), "macos-work"); err == nil {
		t.Fatalf("a multicast address in the database = %x, want an error", mac)
	}
}

func withRandom(t *testing.T, fill func([]byte)) {
	old := randomMAC
	randomMAC = fill
	t.Cleanup(func() { randomMAC = old })
}
