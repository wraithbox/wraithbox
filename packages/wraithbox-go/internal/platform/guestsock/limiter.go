package guestsock

import (
	"sync"
	"time"
)

// logLimiter lets one log record of a kind through per window and counts
// the ones it holds back. The record that opens the next window carries
// that count, and Close logs what is left (#115 item 1,
// SEC13-bounded-resources).
type logLimiter struct {
	window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	next time.Time // when the next record may be written
	held int       // records held back since the last one written
}

// allow reports whether a record may be written now. When it may, held is
// the number held back since the last one, and the count starts again.
func (l *logLimiter) allow() (ok bool, held int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Before(l.next) {
		l.held++
		return false, 0
	}
	held, l.held = l.held, 0
	l.next = now.Add(l.window)
	return true, held
}

// flush returns the number held back and starts the count again.
func (l *logLimiter) flush() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	held := l.held
	l.held = 0
	return held
}
