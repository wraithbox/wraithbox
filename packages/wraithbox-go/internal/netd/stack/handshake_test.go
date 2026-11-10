package stack

import (
	"net/netip"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

// TestHandshakeFailed checks the CreateEndpoint failure path: a guest
// that answers the SYN-ACK with an RST fails the handshake, which lets
// go of the place under the established cap and is reset, recorded with
// tcp-handshake-failed before the reset is sent (SEC13-bounded-resources,
// SEC10-audit). The sink never gets the connection.
func TestHandshakeFailed(t *testing.T) {
	conns := make(sink, 1)
	h := start(t, nt.Config(), netd.Hooks{Stream: policy(func(netd.Flow) netd.StreamDecision {
		return netd.StreamDecision{Sink: conns}
	})}, nil)
	tcpNum := uint8(header.TCPProtocolNumber)
	h.send(nt.FromGuest(uint16(header.IPv4ProtocolNumber), nt.IPv4(nt.Leased, synthetic, tcpNum,
		nt.TCPSegment(nt.Leased, synthetic, 40000, 443, header.TCPFlagSyn))))
	synAck := h.receiveTCP()
	if f := synAck.Flags(); f != header.TCPFlagSyn|header.TCPFlagAck {
		t.Fatalf("the stack answered the SYN with %v, want a SYN-ACK", f)
	}
	// The RST's sequence number is the one the SYN-ACK acknowledges, so
	// the handshake takes it.
	h.send(nt.FromGuest(uint16(header.IPv4ProtocolNumber), nt.IPv4(nt.Leased, synthetic, tcpNum,
		tcpRST(nt.Leased, synthetic, 40000, 443, synAck.AckNumber()))))
	// The stack's reset comes after the record, so the count is read
	// without a wait once the reset arrives.
	if f := h.receiveTCP().Flags(); !f.Contains(header.TCPFlagRst) {
		t.Fatalf("the stack sent %v, want an RST", f)
	}
	if n := h.s.Recorder().Count(netd.RuleTCPHandshakeFailed); n != 1 {
		t.Fatalf("tcp-handshake-failed count %d when the RST arrived, want 1", n)
	}
	waitActive(t, h.s, 0)
	select {
	case c := <-conns:
		t.Fatalf("the sink got a connection from %v", c.RemoteAddr())
	default:
	}
}

// receiveTCP waits for the next TCP segment the stack sends.
func (h *harness) receiveTCP() header.TCP {
	h.t.Helper()
	for {
		f := h.receive()
		if header.Ethernet(f).Type() != header.IPv4ProtocolNumber {
			continue
		}
		ip := header.IPv4(f[header.EthernetMinimumSize:])
		if ip.TransportProtocol() == header.TCPProtocolNumber {
			return header.TCP(ip.Payload())
		}
	}
}

// tcpRST is a TCP RST with sequence number seq and a valid checksum.
func tcpRST(src, dst netip.Addr, srcPort, dstPort uint16, seq uint32) []byte {
	b := make([]byte, header.TCPMinimumSize)
	seg := header.TCP(b)
	seg.Encode(&header.TCPFields{
		SrcPort: srcPort, DstPort: dstPort, SeqNum: seq,
		DataOffset: header.TCPMinimumSize, Flags: header.TCPFlagRst,
	})
	sum := header.PseudoHeaderChecksum(header.TCPProtocolNumber,
		tcpip.AddrFrom4(src.As4()), tcpip.AddrFrom4(dst.As4()), uint16(len(b)))
	seg.SetChecksum(^seg.CalculateChecksum(sum))
	return b
}
