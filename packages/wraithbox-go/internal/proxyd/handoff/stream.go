package handoff

import (
	"errors"
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

	sock *socket
	// endMu makes the end and a SendWhileOpen call one step each: end
	// sets ended and closes done under it, and SendWhileOpen checks
	// ended and sends under it.
	endMu sync.Mutex
	ended atomic.Bool
	// done is closed when the stream ends. doneOnce makes it, so a
	// Stream built without it works.
	doneOnce sync.Once
	done     chan struct{}

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
// AddCheck also runs the check at once against Policy, so a recompute
// applied between the hand-off and the call isn't missed: the policy is
// read under the same lock that registers the check, and a recompute
// sets the policy and copies the checks under that lock too. When the
// check fails, AddCheck itself resets the stream with policy-recomputed
// and logs it, as a recompute does, so no caller can let the stream go
// on (SEC05-default-deny). It returns the check, or "" when it passed.
func (s *Stream) AddCheck(check func(*policy.Policy) egressmsg.Check) egressmsg.Check {
	s.mu.Lock()
	s.checks = append(s.checks, check)
	p := s.policy
	s.mu.Unlock()
	failed := check(p)
	if failed != "" && s.end(true) {
		s.sock.r.refused(Event{VM: s.VM, Generation: s.Generation, Rule: RulePolicyRecomputed, Check: failed},
			streamAttrs(s)...)
	}
	return failed
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
// (S07-05-stream-hand-off, "Resets"). It reports whether this call
// ended the stream. A later Reset or Close does nothing and returns
// false, so only the caller that ended a stream logs why.
func (s *Stream) Reset() bool { return s.end(true) }

// Close ends the stream: it closes Conn, which wb-netd answers with a
// FIN. It reports whether this call ended the stream, as Reset does.
func (s *Stream) Close() bool { return s.end(false) }

// Done returns a channel that is closed when the stream ends: by its
// handler, a recompute, an address conflict or the socket's end. The
// handler stops its work on the stream then.
func (s *Stream) Done() <-chan struct{} { return s.doneChan() }

func (s *Stream) doneChan() chan struct{} {
	s.doneOnce.Do(func() { s.done = make(chan struct{}) })
	return s.done
}

// ErrEnded is SendWhileOpen's error when the stream had ended.
var ErrEnded = errors.New("handoff: the stream has ended")

// SendWhileOpen runs send unless the stream has ended, and returns
// ErrEnded without running it otherwise. The check and send are one
// step under the lock that ends the stream: a Reset or Close that comes
// while send runs waits for it to return, and once Done is closed no
// send starts. So a byte that goes upstream through send went before
// the stream ended, and none after (S07-15-non-http-streams, "Logging a
// reset": a connection that opens after a reset sends nothing
// upstream). send must be bounded, by a write deadline, as every end
// of the stream waits for it.
func (s *Stream) SendWhileOpen(send func() error) error {
	s.endMu.Lock()
	defer s.endMu.Unlock()
	if s.ended.Load() {
		return ErrEnded
	}
	return send()
}

// end ends the stream once, and reports whether this call did. The
// StreamReset goes before Conn closes. Done is closed under endMu, so
// it waits for a SendWhileOpen that runs.
func (s *Stream) end(reset bool) bool {
	s.endMu.Lock()
	did := s.ended.CompareAndSwap(false, true)
	if did {
		close(s.doneChan())
	}
	s.endMu.Unlock()
	if !did {
		return false
	}
	s.sock.remove(s)
	if reset {
		s.sock.sendReset(s.ID)
	}
	_ = s.Conn.Close()
	return true
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
