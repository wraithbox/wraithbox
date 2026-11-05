package netdtest

import (
	"encoding/binary"
	"net"
	"net/netip"
)

// The offsets of a BOOTP message that the DHCP tests read (RFC 2131,
// section 2).
const (
	DHCPOffXID    = 4
	DHCPOffCIAddr = 12
	DHCPOffYIAddr = 16
	DHCPOffCHAddr = 28
	DHCPOffCookie = 236
	DHCPOffOpts   = 240
)

// DHCPOpt is a DHCP option: its code, length and value.
func DHCPOpt(code byte, v ...byte) []byte {
	return append([]byte{code, u8(len(v))}, v...)
}

// DHCP is a DHCP request from mac with xid and ciaddr, holding the
// options as given and then END. Each option is a DHCPOpt, or raw bytes.
func DHCP(xid uint32, ciaddr netip.Addr, mac net.HardwareAddr, opts ...[]byte) []byte {
	b := make([]byte, DHCPOffOpts)
	b[0], b[1], b[2] = 1, 1, 6
	binary.BigEndian.PutUint32(b[DHCPOffXID:], xid)
	if ciaddr.IsValid() {
		a := ciaddr.As4()
		copy(b[DHCPOffCIAddr:], a[:])
	}
	copy(b[DHCPOffCHAddr:], mac)
	copy(b[DHCPOffCookie:], []byte{99, 130, 83, 99})
	for _, o := range opts {
		b = append(b, o...)
	}
	return append(b, 255)
}

// DHCPOptions reads the options of a DHCP message up to END: the codes
// in their order, and the values by code. It returns false when the
// options don't parse, repeat, or have no END.
func DHCPOptions(msg []byte) ([]byte, map[byte][]byte, bool) {
	if len(msg) < DHCPOffOpts {
		return nil, nil, false
	}
	var codes []byte
	values := map[byte][]byte{}
	b := msg[DHCPOffOpts:]
	for i := 0; i < len(b); {
		code := b[i]
		switch {
		case code == 0:
			i++
			continue
		case code == 255:
			return codes, values, true
		case i+1 >= len(b) || i+2+int(b[i+1]) > len(b):
			return nil, nil, false
		}
		if _, dup := values[code]; dup {
			return nil, nil, false
		}
		codes = append(codes, code)
		values[code] = b[i+2 : i+2+int(b[i+1])]
		i += 2 + int(b[i+1])
	}
	return nil, nil, false
}

// DHCPAddr is the IPv4 address at an offset of a DHCP message.
func DHCPAddr(msg []byte, off int) netip.Addr {
	return netip.AddrFrom4([4]byte(msg[off : off+4]))
}
