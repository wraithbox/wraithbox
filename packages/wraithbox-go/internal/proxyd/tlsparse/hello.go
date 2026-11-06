package tlsparse

import (
	"errors"
	"slices"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"golang.org/x/crypto/cryptobyte"
)

// The extension types this package reads or its callers name (IANA "TLS
// ExtensionType Values").
const (
	ExtServerName           uint16 = 0
	ExtALPN                 uint16 = 16
	ExtEarlyData            uint16 = 42
	ExtEncryptedClientHello uint16 = 0xfe0d
	ExtEncryptedServerName  uint16 = 0xffce
	sniHostName             uint8  = 0
	maxSessionID                   = 32
	legacyVersionLowest     uint16 = 0x0301
	legacyVersionHighest    uint16 = 0x0303
	helloRandomLen                 = 32
	serverHelloRandomOffset        = 2
	serverHelloMinBody             = serverHelloRandomOffset + helloRandomLen
	cipherSuiteLen                 = 2
)

// ClientHello is what wb-proxyd reads from a ClientHello. ServerName is
// the host_name of the server_name extension as sent, and HasServerName
// says whether there was one. ALPN is the protocol list in order.
// Extensions holds every extension type in the order sent.
type ClientHello struct {
	Version       uint16
	ServerName    string
	HasServerName bool
	ALPN          []string
	Extensions    []uint16
}

// Has reports whether the ClientHello holds the extension type.
func (ch *ClientHello) Has(ext uint16) bool {
	return slices.Contains(ch.Extensions, ext)
}

// Name returns the server name in the name form of S07-03-dns, folded
// to ASCII lower case, for comparing with a stream's name
// (S07-07-hosts-and-modes, "Name binding"). It returns false when there
// is no server name, or it ends in a dot (RFC 6066, section 3), or it
// fails the name form, an IP literal included. Folding case isn't a
// widening: DNS names compare without case, and a stream's name is in
// lower case.
func (ch *ClientHello) Name() (dnsname.Name, bool) {
	if !ch.HasServerName || strings.HasSuffix(ch.ServerName, ".") {
		return dnsname.Name{}, false
	}
	n, ref := dnsname.FromHost(ch.ServerName)
	if ref != nil {
		return dnsname.Name{}, false
	}
	return n, true
}

// ReadClientHello reads a ClientHello from the start of b: one or more
// handshake records whose fragments, joined, hold exactly one
// ClientHello message. It reads at most maxBytes of b. It returns the
// ClientHello and the length of the records that hold it, ErrShort when
// the bytes so far are a valid start and fewer than maxBytes, or an
// *Error. It refuses a record that isn't a handshake record, a first
// message that isn't a ClientHello, handshake bytes after the
// ClientHello in its last record, and a ClientHello that doesn't fit in
// maxBytes (CheckCap).
func ReadClientHello(b []byte, maxBytes int) (*ClientHello, int, error) {
	if len(b) > maxBytes {
		b = b[:maxBytes]
	}
	var msg []byte
	off := 0
	for {
		r, n, err := ParseRecord(b[off:])
		if errors.Is(err, ErrShort) {
			return nil, 0, short(len(b), maxBytes)
		}
		if err != nil {
			return nil, 0, err
		}
		if r.Type != TypeHandshake {
			return nil, 0, refuse(CheckRecordType, "record of type %d in a ClientHello flight", r.Type)
		}
		off += n
		msg = append(msg, r.Fragment...)
		done, err := clientHelloComplete(msg, maxBytes)
		if err != nil {
			return nil, 0, err
		}
		if done {
			ch, err := parseClientHello(msg[handshakeHeaderLen:])
			if err != nil {
				return nil, 0, err
			}
			return ch, off, nil
		}
	}
}

// short is the result of a flight that ended early: ErrShort while the
// cap leaves room for more, and CheckCap once it doesn't.
func short(have, maxBytes int) error {
	if have >= maxBytes {
		return refuse(CheckCap, "no complete ClientHello in %d bytes", maxBytes)
	}
	return ErrShort
}

// clientHelloComplete reports whether msg, the handshake bytes so far,
// holds the whole ClientHello and nothing after it.
func clientHelloComplete(msg []byte, maxBytes int) (bool, error) {
	if len(msg) < handshakeHeaderLen {
		return false, nil
	}
	typ, n := handshakeHeader(msg)
	if typ != HandshakeClientHello {
		return false, refuse(CheckHandshakeType, "handshake message of type %d, not a ClientHello", typ)
	}
	if handshakeHeaderLen+n > maxBytes {
		return false, refuse(CheckCap, "a ClientHello of %d bytes, over %d", n, maxBytes)
	}
	switch {
	case len(msg) < handshakeHeaderLen+n:
		return false, nil
	case len(msg) > handshakeHeaderLen+n:
		return false, refuse(CheckHandshakeAfterHello, "%d handshake bytes after the ClientHello", len(msg)-handshakeHeaderLen-n)
	}
	return true, nil
}

// parseClientHello parses a ClientHello body (RFC 8446, section 4.1.2):
// version, random, session id, cipher suites, compression methods and
// the extensions, with nothing after them.
func parseClientHello(body []byte) (*ClientHello, error) {
	s := cryptobyte.String(body)
	ch := &ClientHello{}
	if err := ch.readFixed(&s); err != nil {
		return nil, err
	}
	if s.Empty() {
		return ch, nil
	}
	var exts cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&exts) || !s.Empty() {
		return nil, refuse(CheckHelloForm, "the extensions don't parse, or bytes follow them")
	}
	if err := ch.readExtensions(exts); err != nil {
		return nil, err
	}
	return ch, nil
}

// readFixed reads the ClientHello's fields before the extensions:
// version, random, session id, cipher suites and compression methods.
func (ch *ClientHello) readFixed(s *cryptobyte.String) error {
	var sessionID, suites, compression cryptobyte.String
	if !s.ReadUint16(&ch.Version) || !s.Skip(helloRandomLen) ||
		!s.ReadUint8LengthPrefixed(&sessionID) || len(sessionID) > maxSessionID ||
		!s.ReadUint16LengthPrefixed(&suites) || len(suites) == 0 || len(suites)%cipherSuiteLen != 0 ||
		!s.ReadUint8LengthPrefixed(&compression) || len(compression) == 0 {
		return refuse(CheckHelloForm, "the ClientHello's fixed fields don't parse")
	}
	if ch.Version < legacyVersionLowest || ch.Version > legacyVersionHighest {
		return refuse(CheckHelloForm, "ClientHello version %#04x", ch.Version)
	}
	return nil
}

// readExtensions reads the extension list, refusing a type sent twice,
// and parses the server_name and ALPN extensions.
func (ch *ClientHello) readExtensions(exts cryptobyte.String) error {
	seen := map[uint16]struct{}{}
	for !exts.Empty() {
		var typ uint16
		var data cryptobyte.String
		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&data) {
			return refuse(CheckHelloForm, "extension %d doesn't parse", len(ch.Extensions)+1)
		}
		if _, dup := seen[typ]; dup {
			return refuse(CheckExtensionDuplicate, "extension type %d twice", typ)
		}
		seen[typ] = struct{}{}
		ch.Extensions = append(ch.Extensions, typ)
		var err error
		switch typ {
		case ExtServerName:
			err = ch.readServerName(data)
		case ExtALPN:
			err = ch.readALPN(data)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// readServerName reads the server_name extension (RFC 6066, section
// 3): a list of exactly one host_name entry, which isn't empty. An
// entry of another name type, or a second host_name, is refused (fail
// closed).
func (ch *ClientHello) readServerName(data cryptobyte.String) error {
	var list cryptobyte.String
	if !data.ReadUint16LengthPrefixed(&list) || !data.Empty() || list.Empty() {
		return refuse(CheckServerName, "the server_name list doesn't parse")
	}
	for !list.Empty() {
		var typ uint8
		var host cryptobyte.String
		if !list.ReadUint8(&typ) || !list.ReadUint16LengthPrefixed(&host) || len(host) == 0 {
			return refuse(CheckServerName, "a server_name entry doesn't parse")
		}
		if typ != sniHostName {
			return refuse(CheckServerName, "a server_name entry of name type %d", typ)
		}
		if ch.HasServerName {
			return refuse(CheckServerName, "more than one host_name entry")
		}
		ch.ServerName, ch.HasServerName = string(host), true
	}
	return nil
}

// readALPN reads the ALPN extension (RFC 7301, section 3.1): a list of
// at least one protocol name, each at least one byte.
func (ch *ClientHello) readALPN(data cryptobyte.String) error {
	var list cryptobyte.String
	if !data.ReadUint16LengthPrefixed(&list) || !data.Empty() || list.Empty() {
		return refuse(CheckALPN, "the ALPN list doesn't parse")
	}
	for !list.Empty() {
		var proto cryptobyte.String
		if !list.ReadUint8LengthPrefixed(&proto) || len(proto) == 0 {
			return refuse(CheckALPN, "an ALPN protocol name doesn't parse")
		}
		ch.ALPN = append(ch.ALPN, string(proto))
	}
	return nil
}
