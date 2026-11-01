package eventchan

import (
	"strings"
	"unicode/utf8"

	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
)

// The bounds of an event record, as the comments in
// proto/wraithbox/audit/v1/event.proto state them. The sender cuts guest
// strings to them, and wb-hostd ends a sender whose record breaks one
// (S04-08-records-and-bounds, "Accounting and event channels").
const (
	// MaxRecord is the largest record on the channel, 4 KiB. The bounds
	// below keep the largest event under it, which TestLargestRecordFits
	// checks.
	MaxRecord = 4 << 10
	// MaxDecision and MaxRule bound the decision and the rule.
	MaxDecision = 32
	MaxRule     = 64
	// MaxHost bounds the destination host: a DNS name's 253 bytes.
	MaxHost = 253
	// MaxMethod and MaxPath bound an inspected request's method and path.
	MaxMethod = 16
	MaxPath   = 1024
	// MaxLabel bounds each field of the guest's label.
	MaxLabel = 256
	// MaxPort is the largest destination port.
	MaxPort = 65535
	// MaxBytes bounds bytes in and out at 2^53, which a JSON reader
	// holds exactly in a float64.
	MaxBytes = 1 << 53
	// MaxRunCount bounds a run's count.
	MaxRunCount = 1 << 32
	// MaxRunWindow bounds a run's window, in seconds.
	MaxRunWindow = 3600
)

// runWindowSeconds is the window of a run of refusals: one record per minute
// (S09-11-audit, "Events per minute").
const runWindowSeconds = 60

// ValidDecision reports whether s has the form of a decision: 1 to
// MaxDecision bytes of lowercase letters and hyphens.
func ValidDecision(s string) bool {
	return len(s) >= 1 && len(s) <= MaxDecision && onlyBytes(s, false)
}

// ValidRule reports whether s has the form of a rule: 1 to MaxRule bytes
// of lowercase letters, digits and hyphens.
func ValidRule(s string) bool {
	return len(s) >= 1 && len(s) <= MaxRule && onlyBytes(s, true)
}

func onlyBytes(s string, digits bool) bool {
	for i := range len(s) {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c == '-' || digits && c >= '0' && c <= '9'
		if !ok {
			return false
		}
	}
	return true
}

// KnownClass reports whether c is one of the classes a sender writes.
func KnownClass(c auditv1.EventClass) bool {
	switch c {
	case auditv1.EventClass_EVENT_CLASS_DETECTION_FINDING,
		auditv1.EventClass_EVENT_CLASS_NETWORK_ACTIVITY,
		auditv1.EventClass_EVENT_CLASS_HTTP_ACTIVITY:
		return true
	case auditv1.EventClass_EVENT_CLASS_UNSPECIFIED:
	}
	return false
}

// Cut returns s as valid UTF-8 of at most n bytes, cut at a whole rune.
// Invalid bytes become U+FFFD first: a protobuf string must be valid
// UTF-8.
func Cut(s string, n int) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size > 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

// bound cuts each guest string of e to its bound, and caps the byte
// counts, so the record fits and wb-hostd accepts it. The decision and
// rule are the sender's own, and stay as they are.
func bound(e *auditv1.Event) {
	e.DestinationHost = Cut(e.GetDestinationHost(), MaxHost)
	e.HttpMethod = Cut(e.GetHttpMethod(), MaxMethod)
	e.HttpPath = Cut(e.GetHttpPath(), MaxPath)
	e.BytesIn = min(e.GetBytesIn(), MaxBytes)
	e.BytesOut = min(e.GetBytesOut(), MaxBytes)
	if l := e.GetGuestLabel(); l != nil {
		l.Project = Cut(l.GetProject(), MaxLabel)
		l.Session = Cut(l.GetSession(), MaxLabel)
		l.User = Cut(l.GetUser(), MaxLabel)
		l.Program = Cut(l.GetProgram(), MaxLabel)
	}
}
