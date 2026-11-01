// Package packetlink is wb-netd's packet transport: the host end of a
// VM's virtual NIC, which carries one Ethernet frame per datagram
// (S07-02-packet-path, "Packet transport", S12-01-platform-interfaces,
// "Packet transport on macOS").
//
// A Transport reads and writes whole frames. It doesn't look inside
// them: the link endpoint in internal/netd/link does, and drops a read
// that fills the buffer of ReadBufferSize bytes with frame-truncated.
//
// It is in internal/platform because the mechanism is per OS
// (S12-01-platform-interfaces, "Rules"). On darwin the transport is an
// AF_UNIX SOCK_DGRAM socketpair end that wb-hostd passes to wb-netd.
// Linux and Windows return an error that wraps errors.ErrUnsupported.
// The buffer sizes and their check are in this file, which builds on
// every OS, so the tests on every OS cover them.
package packetlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// MTU bounds: the macOS file-handle attachment takes an MTU of 1500 to
// 65535 (S07-02-packet-path, "MTU").
const (
	MinMTU = 1500
	MaxMTU = 65535
)

// EthernetHeader is the size of an Ethernet header without a VLAN tag.
const EthernetHeader = 14

// ErrBusy is wrapped by a WriteFrame that failed because the peer's
// receive buffer was full (ENOBUFS or EAGAIN on darwin). The frame
// wasn't sent, and the caller may try again (S07-02-packet-path,
// "Per-VM caps").
var ErrBusy = errors.New("packetlink: peer buffer full")

// Rule names the rule behind a refused descriptor, for the log.
type Rule string

// The rules of New. Each refusal has its own.
const (
	// RuleSocketType: the descriptor isn't an AF_UNIX SOCK_DGRAM socket.
	RuleSocketType Rule = "packetlink-socket-type"
	// RuleSocketUnconnected: the descriptor has no peer.
	RuleSocketUnconnected Rule = "packetlink-socket-unconnected"
	// RuleBuffers: the socket buffers the kernel reports after setup
	// don't hold the largest frame, or SO_RCVBUF is below twice
	// SO_SNDBUF.
	RuleBuffers Rule = "packetlink-buffers"
)

// Refusal is the error of a descriptor New refused.
type Refusal struct {
	Rule Rule

	msg string
	err error
}

func (r *Refusal) Error() string { return "packetlink: " + string(r.Rule) + ": " + r.msg }

// Unwrap returns the cause, such as the error of a failed getsockopt.
func (r *Refusal) Unwrap() error { return r.err }

// refuse logs a refusal with its rule and returns it.
func refuse(rule Rule, msg string, err error, attrs ...slog.Attr) *Refusal {
	attrs = append([]slog.Attr{
		slog.String("rule", string(rule)),
		slog.String("decision", "refuse"),
	}, attrs...)
	slog.Default().LogAttrs(context.Background(), slog.LevelWarn, "packet transport refused: "+msg, attrs...)
	return &Refusal{Rule: rule, msg: msg, err: err}
}

// MaxFrame is the largest frame at an MTU: the MTU plus the Ethernet
// header (S07-02-packet-path, "MTU").
func MaxFrame(mtu int) int { return mtu + EthernetHeader }

// ReadBufferSize is the size of the buffer a caller reads a frame into:
// one byte more than the largest frame. A read that fills it was
// truncated (S07-02-packet-path, "Stack").
func ReadBufferSize(mtu int) int { return MaxFrame(mtu) + 1 }

const (
	// minSendBuffer is the least SO_SNDBUF the transport sets. It is the
	// value of X03-network-path, well above the largest frame.
	minSendBuffer = 1 << 20

	// receiveFactor is SO_RCVBUF as a multiple of SO_SNDBUF. Apple's
	// header for the file-handle attachment asks for at least two and
	// recommends four (S07-02-packet-path, "Stack"). X03-network-path
	// used four.
	receiveFactor = 4
)

// bufferSizes returns the SO_SNDBUF and SO_RCVBUF to set for an MTU:
// SO_SNDBUF at least the largest frame, and SO_RCVBUF four times
// SO_SNDBUF (S07-02-packet-path, "Packet transport" and "Stack").
func bufferSizes(mtu int) (snd, rcv int) {
	snd = max(minSendBuffer, MaxFrame(mtu))
	return snd, receiveFactor * snd
}

// CheckMTU refuses an MTU outside MinMTU to MaxMTU.
func CheckMTU(mtu int) error {
	if mtu < MinMTU || mtu > MaxMTU {
		return fmt.Errorf("packetlink: MTU %d is outside %d to %d", mtu, MinMTU, MaxMTU)
	}
	return nil
}

// checkBuffers checks the socket buffer sizes the kernel reports after
// setup: SO_SNDBUF holds the largest frame, and SO_RCVBUF is at least
// twice SO_SNDBUF (S07-02-packet-path, "Stack"). A kernel that capped a
// size lower than the transport asked for fails the check with
// RuleBuffers, and the transport isn't used.
func checkBuffers(snd, rcv, mtu int) error {
	attrs := []slog.Attr{slog.Int("send_buffer", snd), slog.Int("receive_buffer", rcv), slog.Int("mtu", mtu)}
	if snd < MaxFrame(mtu) {
		msg := fmt.Sprintf("SO_SNDBUF is %d, below the largest frame of %d bytes", snd, MaxFrame(mtu))
		return refuse(RuleBuffers, msg, nil, attrs...)
	}
	if rcv < 2*snd {
		msg := fmt.Sprintf("SO_RCVBUF is %d, below twice SO_SNDBUF (%d)", rcv, snd)
		return refuse(RuleBuffers, msg, nil, attrs...)
	}
	return nil
}
