// Package tlstest builds TLS records and handshake messages for the
// tests of tlsparse and the stream checks of wb-proxyd. It writes only
// what the parsers read, and nothing a real handshake needs.
package tlstest

import (
	"golang.org/x/crypto/cryptobyte"
)

// Record content types and handshake types, as in tlsparse.
const (
	TypeChangeCipherSpec = 20
	TypeAlert            = 21
	TypeHandshake        = 22
	TypeApplicationData  = 23
)

// Extension is one ClientHello extension with its raw data.
type Extension struct {
	Type uint16
	Data []byte
}

// Hello describes a ClientHello.
type Hello struct {
	// ServerName is the one host_name entry; empty sends no server_name
	// extension.
	ServerName string
	// ALPN is the protocol list; nil sends no ALPN extension.
	ALPN []string
	// Extensions follow server_name and ALPN, in order.
	Extensions []Extension
	// Version is the legacy version; 0 means 0x0303.
	Version uint16
}

// Message returns the ClientHello as a handshake message, its header
// included.
func (h Hello) Message() []byte {
	version := h.Version
	if version == 0 {
		version = 0x0303
	}
	var b cryptobyte.Builder
	b.AddUint8(1)
	b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint16(version)
		b.AddBytes(make([]byte, 32))
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(make([]byte, 32)) })
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddUint16(0x1301) })
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddUint8(0) })
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { h.addExtensions(b) })
	})
	return b.BytesOrPanic()
}

func (h Hello) addExtensions(b *cryptobyte.Builder) {
	if h.ServerName != "" {
		b.AddUint16(0)
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddUint8(0)
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes([]byte(h.ServerName)) })
			})
		})
	}
	if h.ALPN != nil {
		b.AddUint16(16)
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				for _, p := range h.ALPN {
					b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes([]byte(p)) })
				}
			})
		})
	}
	for _, e := range h.Extensions {
		b.AddUint16(e.Type)
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(e.Data) })
	}
}

// Records returns the ClientHello in one handshake record.
func (h Hello) Records() []byte {
	return Split(TypeHandshake, h.Message())
}

// Split puts msg into records of type typ: one record per size given,
// and one more for what is left.
func Split(typ uint8, msg []byte, sizes ...int) []byte {
	var out []byte
	for _, n := range sizes {
		out = append(out, Record(typ, msg[:n])...)
		msg = msg[n:]
	}
	if len(msg) > 0 {
		out = append(out, Record(typ, msg)...)
	}
	return out
}

// Record returns one record of type typ with version 0x0303.
func Record(typ uint8, frag []byte) []byte {
	var b cryptobyte.Builder
	b.AddUint8(typ)
	b.AddUint16(0x0303)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(frag) })
	return b.BytesOrPanic()
}

// helloRetryRandom is the random of a HelloRetryRequest (RFC 8446,
// section 4.1.3).
var helloRetryRandom = []byte{
	0xcf, 0x21, 0xad, 0x74, 0xe5, 0x9a, 0x61, 0x11, 0xbe, 0x1d, 0x8c, 0x02, 0x1e, 0x65, 0xb8, 0x91,
	0xc2, 0xa2, 0x11, 0x16, 0x7a, 0xbb, 0x8c, 0x5e, 0x07, 0x9e, 0x09, 0xe2, 0xc8, 0xa8, 0x33, 0x9c,
}

// ServerHello returns a ServerHello handshake message, or a
// HelloRetryRequest when retry is set.
func ServerHello(retry bool) []byte {
	random := make([]byte, 32)
	if retry {
		random = helloRetryRandom
	}
	var b cryptobyte.Builder
	b.AddUint8(2)
	b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint16(0x0303)
		b.AddBytes(random)
		b.AddUint8(0)
		b.AddUint16(0x1301)
		b.AddUint8(0)
		b.AddUint16(0)
	})
	return b.BytesOrPanic()
}
