package handoff

import (
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// Stream is a stream that passed the checks of S07-05-stream-hand-off,
// "Streams". Its VM and generation are its hand-off socket's, which
// wb-hostd labeled, and its name is the one the copy held for its
// address at hand-off (SEC08-proj-isolation). The fields don't change.
type Stream struct {
	VM         string
	Generation uint64
	// ID is wb-netd's id for the stream, which a StreamReset names.
	ID   uint64
	Name dnsname.Name
	Addr netip.Addr
	Port uint16
	// Conn is the stream's socket, from wb-netd.
	Conn net.Conn

	sock  *socket
	ended atomic.Bool

	mu     sync.Mutex
	checks []func(*policy.Policy) egressmsg.Check
}

// AddCheck registers a check that runs on the stream at each policy
// change, after the name and port check. It returns "" when the new
// policy still allows the stream, and the check that fails otherwise;
// the stream is then reset with policy-recomputed
// (S07-01-per-vm-policy, "Open streams"). The handler adds one for what
// it learned after hand-off, such as the TLS mode of the host.
func (s *Stream) AddCheck(check func(*policy.Policy) egressmsg.Check) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks = append(s.checks, check)
}

// recheck checks the stream against a new effective policy: its name
// and port, then each registered check. It returns the first check that
// fails, or "".
func (s *Stream) recheck(p *policy.Policy) egressmsg.Check {
	if check := streamAllowed(p, s.Name, s.Port); check != "" {
		return check
	}
	s.mu.Lock()
	checks := append([]func(*policy.Policy) egressmsg.Check(nil), s.checks...)
	s.mu.Unlock()
	for _, c := range checks {
		if check := c(p); check != "" {
			return check
		}
	}
	return ""
}

// Reset ends the stream with a reset: it sends a StreamReset, so
// wb-netd resets the guest's connection, and then closes Conn
// (S07-05-stream-hand-off, "Resets"). A later Reset or Close does
// nothing.
func (s *Stream) Reset() { s.end(true) }

// Close ends the stream: it closes Conn, which wb-netd answers with a
// FIN. A later Reset or Close does nothing.
func (s *Stream) Close() { s.end(false) }

// end ends the stream once. The StreamReset goes before Conn closes.
func (s *Stream) end(reset bool) {
	if !s.ended.CompareAndSwap(false, true) {
		return
	}
	s.sock.remove(s)
	if reset {
		s.sock.sendReset(s.ID)
	}
	_ = s.Conn.Close()
}

// streamAttrs are a stream's log attributes. The name is escaped and
// cut, as a guest string.
func streamAttrs(s *Stream) []slog.Attr {
	return []slog.Attr{
		slog.Uint64("stream_id", s.ID),
		slog.String("name", guesttext.LogField(s.Name.String(), logCut)),
		slog.String("address", s.Addr.String()),
		slog.Int("port", int(s.Port)),
	}
}
