package packetlink

import (
	"errors"
	"testing"
)

// TestBufferSizes checks the sizes the transport asks for at the bounds
// of the MTU: SO_SNDBUF holds the largest frame, and SO_RCVBUF is at
// least twice SO_SNDBUF (S07-02-packet-path, "Stack").
func TestBufferSizes(t *testing.T) {
	for _, mtu := range []int{MinMTU, 9000, MaxMTU} {
		snd, rcv := bufferSizes(mtu)
		if err := checkBuffers(snd, rcv, mtu); err != nil {
			t.Errorf("MTU %d: sizes %d and %d fail their own check: %v", mtu, snd, rcv, err)
		}
	}
	if got := ReadBufferSize(MaxMTU); got != 65550 {
		t.Errorf("ReadBufferSize(%d) = %d, want 65550", MaxMTU, got)
	}
}

// TestCheckBuffers feeds the check sizes a kernel could report, and sees
// each one that is too small refused with RuleBuffers.
func TestCheckBuffers(t *testing.T) {
	tests := []struct {
		name     string
		snd, rcv int
		ok       bool
	}{
		{"largest frame, twice", MaxFrame(MaxMTU), 2 * MaxFrame(MaxMTU), true},
		{"send buffer one byte short", MaxFrame(MaxMTU) - 1, 4 * MaxFrame(MaxMTU), false},
		{"receive buffer one byte short of twice", 1 << 20, 2<<20 - 1, false},
		{"receive buffer equal to send", 1 << 20, 1 << 20, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkBuffers(tt.snd, tt.rcv, MaxMTU)
			var r *Refusal
			switch {
			case tt.ok && err != nil:
				t.Errorf("refused: %v", err)
			case !tt.ok && (!errors.As(err, &r) || r.Rule != RuleBuffers):
				t.Errorf("err = %v, want a refusal with %s", err, RuleBuffers)
			}
		})
	}
}

// TestCheckMTU sees an MTU outside the attachment's range refused.
func TestCheckMTU(t *testing.T) {
	for mtu, ok := range map[int]bool{MinMTU - 1: false, MinMTU: true, MaxMTU: true, MaxMTU + 1: false, 0: false} {
		if err := CheckMTU(mtu); (err == nil) != ok {
			t.Errorf("CheckMTU(%d) = %v, want ok %v", mtu, err, ok)
		}
	}
}
