package dns

import (
	"sync"
	"time"
)

// bucket is a token bucket: it holds at most burst tokens, and fills at
// rate tokens a second. Each query takes one.
type bucket struct {
	rate, burst float64

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func newBucket(rate, burst int) *bucket {
	return &bucket{rate: float64(rate), burst: float64(burst), tokens: float64(burst)}
}

// take takes a token at now, and reports whether there was one.
func (b *bucket) take(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.last.IsZero() && now.After(b.last) {
		b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	}
	if b.last.IsZero() || now.After(b.last) {
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
