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

	mu sync.Mutex
	// policy is the effective policy the stream was last checked
	// against: the one at hand-off, then each one a recompute applied.
	policy *policy.Policy
	checks []func(*policy.Policy) egressmsg.Check
}

// Policy returns the effective policy the stream was last checked
// against: the VM's policy at hand-off, or the latest one a recompute
// applied since. The handler reads the stream's protocol and mode from
// it (S07-15-non-http-streams, "One protocol per host and port").
func (s *Stream) Policy() *policy.Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy
}

// AddCheck registers a check that runs on the stream at each policy
// change, after the name and port check. The check returns "" when the
// new policy still allows the stream, and the check that fails
// otherwise; the stream is then reset with policy-recomputed
// (S07-01-per-vm-policy, "Open streams"). The handler adds one for what
// it learned after hand-off, such as the TLS mode of the host.
//
// AddCheck also runs the check at once against Policy and returns its
// result, so a recompute applied between the hand-off and the call
// isn't missed: the policy is read under the same lock that registers
// the check, and a recompute sets the policy and copies the checks
// under that lock too. On a non-empty result the caller resets the
// stream.
func (s *Stream) AddCheck(check func(*policy.Policy) egressmsg.Check) egressmsg.Check {
	s.mu.Lock()
	s.checks = append(s.checks, check)
	p := s.policy
	s.mu.Unlock()
	return check(p)
}

// recheck checks the stream against a new effective policy: its name
// and port, then each registered check. It returns the first check that
// fails, or "".
func (s *Stream) recheck(p *policy.Policy) egressmsg.Check {
	s.mu.Lock()
	s.policy = p
	checks := append([]func(*policy.Policy) egressmsg.Check(nil), s.checks...)
	s.mu.Unlock()
	if check := streamAllowed(p, s.Name, s.Port); check != "" {
		return check
	}
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
