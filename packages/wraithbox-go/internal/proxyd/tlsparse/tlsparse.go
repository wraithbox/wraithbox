// Package tlsparse reads the TLS records and handshake messages a guest
// sends before wb-proxyd decides its stream: the record layer, the
// ClientHello and the type of a server's first handshake message
// (S07-15-non-http-streams, "First bytes"; S07-07-hosts-and-modes,
// "Name binding"; S07-16-tls-and-tunnels, "Pass").
//
// Every byte it reads comes from the guest, or from an upstream server
// the guest chose, so every parser here fails closed: a form it doesn't
// know is an *Error naming the check that failed, never a guess. The
// ClientHello is read with golang.org/x/crypto/cryptobyte, the
// length-prefixed reader of Go's own TLS stack, so no length is trusted
// before it is checked against the bytes that hold it. The package has
// no OS code and doesn't log: its caller logs each refusal with its rule.
package tlsparse

import (
	"errors"
	"fmt"
)

// The record content types (RFC 8446, section 5.1).
const (
	TypeChangeCipherSpec uint8 = 20
	TypeAlert            uint8 = 21
	TypeHandshake        uint8 = 22
	TypeApplicationData  uint8 = 23
)

// The handshake message types this package names (RFC 8446, section 4).
const (
	HandshakeClientHello uint8 = 1
	HandshakeServerHello uint8 = 2
)

// Record layer bounds (RFC 8446, section 5.1 and 5.2).
const (
	// HeaderLen is the length of a record header: type, version, length.
	HeaderLen = 5
	// MaxPlaintext is the longest fragment of a record in the clear,
	// such as a handshake record.
	MaxPlaintext = 1 << 14
	// MaxCiphertext is the longest fragment of any record, an encrypted
	// one included.
	MaxCiphertext = MaxPlaintext + 256
	// handshakeHeaderLen is a handshake message's type and 24-bit length.
	handshakeHeaderLen = 4
)

// ErrShort means the bytes so far are a valid start, and more are
// needed to decide.
var ErrShort = errors.New("tlsparse: more bytes needed")

// The checks an *Error names.
const (
	// CheckRecordType: a record of a content type the caller doesn't
	// take there, such as a ClientHello flight that holds a record that
	// isn't a handshake record.
	CheckRecordType = "record-type"
	// CheckRecordVersion: a record whose version isn't 0x0301 to 0x0303.
	CheckRecordVersion = "record-version"
	// CheckRecordLength: an empty handshake fragment, or a fragment
	// longer than the record layer allows.
	CheckRecordLength = "record-length"
	// CheckHandshakeType: a handshake message other than the one
	// expected, such as a flight that doesn't start with a ClientHello.
	CheckHandshakeType = "handshake-type"
	// CheckHelloForm: a ClientHello or ServerHello that doesn't parse,
	// or holds bytes after its last field.
	CheckHelloForm = "hello-form"
	// CheckHandshakeAfterHello: handshake bytes after the ClientHello
	// in the records that carry it (fail closed).
	CheckHandshakeAfterHello = "handshake-after-hello"
	// CheckServerName: a server_name extension that doesn't parse, has
	// more than one host_name entry, an empty one, or an entry of
	// another name type.
	CheckServerName = "server-name"
	// CheckALPN: an ALPN extension that doesn't parse, or names an
	// empty protocol.
	CheckALPN = "alpn"
	// CheckExtensionDuplicate: one extension type twice (RFC 8446,
	// section 4.2).
	CheckExtensionDuplicate = "extension-duplicate"
	// CheckCap: the ClientHello doesn't fit in the caller's byte cap
	// (SEC13-bounded-resources).
	CheckCap = "byte-cap"
)

// Error is a refusal of this package: the check that failed and what it
// saw. Its message holds no guest bytes, only lengths and numbers.
type Error struct {
	Check string
	msg   string
}

func (e *Error) Error() string { return "tlsparse: " + e.Check + ": " + e.msg }

func refuse(check, format string, args ...any) *Error {
	return &Error{Check: check, msg: fmt.Sprintf(format, args...)}
}

// CheckOf returns the check of an *Error in err's chain, or "".
func CheckOf(err error) string {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Check
	}
	return ""
}

// Record is one TLS record. Fragment is a slice of the bytes given to
// ParseRecord.
type Record struct {
	Type     uint8
	Version  uint16
	Fragment []byte
}

// ParseRecord reads one record from the start of b, and returns it and
// its length on the wire. It returns ErrShort when b holds less than
// the whole record. It refuses a version other than 0x0301 to 0x0303 (a
// ClientHello's record may say TLS 1.0, and every later record says
// TLS 1.2, RFC 8446 section 5.1), a fragment past MaxCiphertext, and a
// handshake, alert or change cipher spec fragment past MaxPlaintext or
// of length 0. It doesn't check the content type: the caller decides
// which types it takes.
func ParseRecord(b []byte) (Record, int, error) {
	if len(b) < HeaderLen {
		return Record{}, 0, ErrShort
	}
	r := Record{Type: b[0], Version: uint16(b[1])<<8 | uint16(b[2])}
	n := int(b[3])<<8 | int(b[4])
	if r.Version < 0x0301 || r.Version > 0x0303 {
		return Record{}, 0, refuse(CheckRecordVersion, "record version %#04x", r.Version)
	}
	if n > MaxCiphertext {
		return Record{}, 0, refuse(CheckRecordLength, "record of %d bytes, over %d", n, MaxCiphertext)
	}
	if r.Type != TypeApplicationData && (n == 0 || n > MaxPlaintext) {
		return Record{}, 0, refuse(CheckRecordLength, "record of type %d with %d bytes", r.Type, n)
	}
	if len(b) < HeaderLen+n {
		return Record{}, 0, ErrShort
	}
	r.Fragment = b[HeaderLen : HeaderLen+n]
	return r, HeaderLen + n, nil
}

// handshakeHeader reads a handshake message's type and body length from
// the first four bytes of b, which the caller checked are there.
func handshakeHeader(b []byte) (typ uint8, n int) {
	return b[0], int(b[1])<<16 | int(b[2])<<8 | int(b[3])
}
