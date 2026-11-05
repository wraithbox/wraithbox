// Package dhcp is wb-netd's DHCP server (S07-02-packet-path,
// "Addressing"). It leases the guest its one address and offers only
// the subnet mask, router, DNS server, lease time, server identifier and
// interface MTU, never a proxy configuration (FR08-no-proxy-config).
//
// Parse and Reply are pure functions. Parse reads a few fixed BOOTP
// fields and the options the server needs, and checks the form of the
// rest. Every byte it reads comes from the guest, so it rejects anything
// it doesn't expect (S04-01-design-principles). Serve is the loop on the
// stack's UDP port 67.
package dhcp

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
)

// The UDP ports of DHCP.
const (
	ServerPort = 67
	ClientPort = 68
)

// MaxMessage bounds the DHCP messages the server reads, in bytes. A
// client sends a few hundred, and RFC 2131 only requires 576 to be
// taken. A larger datagram is dropped with dhcp-form
// (SEC13-bounded-resources).
const MaxMessage = 1500

// The fixed part of a BOOTP message (RFC 2131, section 2), and the magic
// cookie the options start with.
const (
	offOp     = 0
	offHType  = 1
	offHLen   = 2
	offXID    = 4
	offFlags  = 10
	offCIAddr = 12
	offYIAddr = 16
	offCHAddr = 28
	offCookie = 236
	offOpts   = 240

	opRequest = 1
	opReply   = 2
	htypeEth  = 1
	hlenEth   = 6
)

var magicCookie = [4]byte{99, 130, 83, 99}

// MessageType is the DHCP message type, option 53 (RFC 2132, 9.6).
type MessageType uint8

// The message types.
const (
	MsgDiscover MessageType = 1
	MsgOffer    MessageType = 2
	MsgRequest  MessageType = 3
	MsgDecline  MessageType = 4
	MsgAck      MessageType = 5
	MsgNak      MessageType = 6
	MsgRelease  MessageType = 7
	MsgInform   MessageType = 8
)

var typeNames = map[MessageType]string{
	MsgDiscover: "DISCOVER", MsgOffer: "OFFER", MsgRequest: "REQUEST", MsgDecline: "DECLINE",
	MsgAck: "ACK", MsgNak: "NAK", MsgRelease: "RELEASE", MsgInform: "INFORM",
}

func (t MessageType) String() string {
	if n, ok := typeNames[t]; ok {
		return n
	}
	return fmt.Sprintf("type-%d", uint8(t))
}

// The option codes the server reads or writes (RFC 2132).
const (
	optPad         = 0
	optSubnetMask  = 1
	optRouter      = 3
	optDNS         = 6
	optHostName    = 12
	optMTU         = 26
	optRequestedIP = 50
	optLeaseTime   = 51
	optOverload    = 52
	optMessageType = 53
	optServerID    = 54
	optParams      = 55
	optMaxSize     = 57
	optVendorClass = 60
	optClientID    = 61
	optFQDN        = 81
	optEnd         = 255
)

// minMaxSize is the least maximum message size a client may give in
// option 57 (RFC 2132, 9.10).
const minMaxSize = 576

// size is the least and the most bytes an option holds.
type size struct{ least, most int }

func (s size) fits(n int) bool { return n >= s.least && n <= s.most }

// optionSizes are the sizes of the options the server reads and checks
// (RFC 2132). The strings it only logs may have any size.
var optionSizes = map[byte]size{
	optMessageType: {1, 1},
	optRequestedIP: {4, 4},
	optServerID:    {4, 4},
	optParams:      {1, 255},
	optMaxSize:     {2, 2},
	optClientID:    {2, 255},
}

// Request is a DHCP message from the guest, as far as the server reads
// it. The strings are the raw option bytes, which are logged only
// through LogAttrs.
type Request struct {
	Type   MessageType
	XID    uint32
	Flags  uint16
	CIAddr netip.Addr
	CHAddr net.HardwareAddr
	// Requested is option 50, and the zero Addr when it is absent.
	Requested netip.Addr
	// ServerID is option 54, and the zero Addr when it is absent.
	ServerID netip.Addr
	// Params is the parameter request list, option 55. The server
	// offers the same options whatever it holds.
	Params []byte
	// MaxSize is option 57, and 0 when it is absent.
	MaxSize uint16

	// The strings logged through guesttext.LogField: options 12, 60, 61
	// and 81.
	HostName    string
	VendorClass string
	ClientID    string
	FQDN        string
}

// FormError is why a message isn't a well-formed DHCP request. The
// server drops it with dhcp-form.
type FormError struct{ Reason string }

func (e *FormError) Error() string { return "dhcp: " + e.Reason }

func formErr(format string, a ...any) error { return &FormError{Reason: fmt.Sprintf(format, a...)} }

// Parse reads a DHCP request. It returns a *FormError unless the message
// is a BOOTREQUEST for a 6-byte Ethernet address, of at most MaxMessage
// bytes, with the magic cookie and a message type, whose options are
// well-formed, each at most once, end in END within the message, and
// don't use option overload (S07-02-packet-path, "Addressing"). Parse
// doesn't check the MAC or the type: Decide does.
func Parse(b []byte) (Request, error) {
	switch {
	case len(b) > MaxMessage:
		return Request{}, formErr("%d bytes, over %d", len(b), MaxMessage)
	case len(b) < offOpts:
		return Request{}, formErr("%d bytes, under the %d of the fixed fields", len(b), offOpts)
	case b[offOp] != opRequest:
		return Request{}, formErr("op %d isn't BOOTREQUEST", b[offOp])
	case b[offHType] != htypeEth || b[offHLen] != hlenEth:
		return Request{}, formErr("hardware type %d, length %d isn't Ethernet", b[offHType], b[offHLen])
	case [4]byte(b[offCookie:offOpts]) != magicCookie:
		return Request{}, formErr("no magic cookie")
	}
	req := Request{
		XID:    binary.BigEndian.Uint32(b[offXID:]),
		Flags:  binary.BigEndian.Uint16(b[offFlags:]),
		CIAddr: netip.AddrFrom4([4]byte(b[offCIAddr:])),
		CHAddr: net.HardwareAddr(append([]byte(nil), b[offCHAddr:offCHAddr+hlenEth]...)),
	}
	if err := req.parseOptions(b[offOpts:]); err != nil {
		return Request{}, err
	}
	switch {
	case req.Type == 0:
		return Request{}, formErr("no message type")
	case req.MaxSize != 0 && req.MaxSize < minMaxSize:
		return Request{}, formErr("maximum message size %d under %d", req.MaxSize, minMaxSize)
	}
	return req, nil
}

// parseOptions reads the options up to END. The bytes after END are
// padding and aren't read.
func (r *Request) parseOptions(b []byte) error {
	var seen [256]bool
	for i := 0; i < len(b); {
		code := b[i]
		i++
		switch code {
		case optPad:
			continue
		case optEnd:
			return nil
		}
		if i >= len(b) {
			return formErr("option %d has no length", code)
		}
		n := int(b[i])
		i++
		if i+n > len(b) {
			return formErr("option %d of %d bytes runs past the message", code, n)
		}
		if seen[code] {
			return formErr("option %d repeats", code)
		}
		seen[code] = true
		if err := r.setOption(code, b[i:i+n]); err != nil {
			return err
		}
		i += n
	}
	return formErr("no END option")
}

// setOption keeps an option the server reads, after checking its size.
// The server reads no other option, and refuses option overload, which
// would put options in the sname and file fields.
func (r *Request) setOption(code byte, v []byte) error {
	if code == optOverload {
		return formErr("option overload")
	}
	if s, ok := optionSizes[code]; ok && !s.fits(len(v)) {
		return formErr("option %d of %d bytes", code, len(v))
	}
	switch code {
	case optMessageType:
		r.Type = MessageType(v[0])
	case optRequestedIP:
		r.Requested = netip.AddrFrom4([4]byte(v))
	case optServerID:
		r.ServerID = netip.AddrFrom4([4]byte(v))
	case optParams:
		r.Params = append([]byte(nil), v...)
	case optMaxSize:
		r.MaxSize = binary.BigEndian.Uint16(v)
	case optClientID:
		r.ClientID = string(v)
	case optHostName:
		r.HostName = string(v)
	case optVendorClass:
		r.VendorClass = string(v)
	case optFQDN:
		r.FQDN = string(v)
	}
	return nil
}
