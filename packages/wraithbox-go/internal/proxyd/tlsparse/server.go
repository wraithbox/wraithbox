package tlsparse

import "bytes"

// helloRetryRandom is the random of a HelloRetryRequest: the SHA-256 of
// "HelloRetryRequest" (RFC 8446, section 4.1.3).
var helloRetryRandom = []byte{
	0xcf, 0x21, 0xad, 0x74, 0xe5, 0x9a, 0x61, 0x11, 0xbe, 0x1d, 0x8c, 0x02, 0x1e, 0x65, 0xb8, 0x91,
	0xc2, 0xa2, 0x11, 0x16, 0x7a, 0xbb, 0x8c, 0x5e, 0x07, 0x9e, 0x09, 0xe2, 0xc8, 0xa8, 0x33, 0x9c,
}

// ServerEvent is what one server record showed.
type ServerEvent int

// The events.
const (
	// ServerNothing: a record before the ServerHello that holds none,
	// such as an alert, a change cipher spec, or part of a handshake
	// message.
	ServerNothing ServerEvent = iota
	// ServerRetry: a HelloRetryRequest.
	ServerRetry
	// ServerHello: a ServerHello that isn't a HelloRetryRequest. The
	// server's bytes after it aren't read.
	ServerHello
)

// ServerFlight reads a server's records up to its first ServerHello that
// isn't a HelloRetryRequest, for the pass relay (S07-16-tls-and-tunnels,
// "Pass"). It reads only as far as that, within a byte cap
// (SEC13-bounded-resources). The zero ServerFlight isn't usable: use
// NewServerFlight.
type ServerFlight struct {
	maxBytes int
	read     int
	hs       []byte
	retried  bool
}

// NewServerFlight returns a ServerFlight that refuses a flight past
// maxBytes of record fragments before its ServerHello.
func NewServerFlight(maxBytes int) *ServerFlight {
	return &ServerFlight{maxBytes: maxBytes}
}

// Record reads one server record. It refuses, as an *Error, a record
// past the cap, an application data or unknown record, a handshake
// message other than a ServerHello, a second HelloRetryRequest, and
// handshake bytes after a HelloRetryRequest in its record: the server
// has to wait for the second ClientHello.
func (f *ServerFlight) Record(r Record) (ServerEvent, error) {
	f.read += len(r.Fragment)
	if f.read > f.maxBytes {
		return ServerNothing, refuse(CheckCap, "no ServerHello in %d bytes", f.maxBytes)
	}
	switch r.Type {
	case TypeAlert, TypeChangeCipherSpec:
		return ServerNothing, nil
	case TypeHandshake:
	default:
		return ServerNothing, refuse(CheckRecordType, "server record of type %d before the ServerHello", r.Type)
	}
	f.hs = append(f.hs, r.Fragment...)
	if len(f.hs) < handshakeHeaderLen {
		return ServerNothing, nil
	}
	typ, n := handshakeHeader(f.hs)
	if typ != HandshakeServerHello {
		return ServerNothing, refuse(CheckHandshakeType, "server handshake message of type %d before the ServerHello", typ)
	}
	if len(f.hs) < handshakeHeaderLen+n {
		return ServerNothing, nil
	}
	body := f.hs[handshakeHeaderLen : handshakeHeaderLen+n]
	if len(body) < serverHelloMinBody {
		return ServerNothing, refuse(CheckHelloForm, "a ServerHello of %d bytes", len(body))
	}
	if !bytes.Equal(body[serverHelloRandomOffset:serverHelloMinBody], helloRetryRandom) {
		return ServerHello, nil
	}
	if f.retried {
		return ServerNothing, refuse(CheckHandshakeType, "a second HelloRetryRequest")
	}
	if len(f.hs) > handshakeHeaderLen+n {
		return ServerNothing, refuse(CheckHandshakeAfterHello, "handshake bytes after the HelloRetryRequest")
	}
	f.retried = true
	f.hs = f.hs[:0]
	return ServerRetry, nil
}
