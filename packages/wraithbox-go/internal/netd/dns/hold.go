package dns

import (
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
)

// HeldQuery is one held query (S04-08-records-and-bounds, S07-04-dns-refusals,
// "Hold"): what a re-run after a widening needs to run it again without
// a second debit, and the refusal bytes its timer sends.
type HeldQuery struct {
	Query   Query
	Name    dnsname.Name
	Arrival time.Time
	Rule    netd.Rule
	// Response is the refusal's bytes, built at arrival.
	Response []byte
	// Debited reports that the name's debit was made.
	Debited bool

	send  func([]byte)
	timer *time.Timer
}

// holdTable holds the refused queries until Hold after their arrival,
// at most limit at once (S07-04-dns-refusals, "Hold limits",
// SEC13-bounded-resources).
type holdTable struct {
	limit int

	mu      sync.Mutex
	entries map[*HeldQuery]struct{}
	closed  bool
}

func newHoldTable(limit int) *holdTable {
	return &holdTable{limit: limit, entries: map[*HeldQuery]struct{}{}}
}

// add holds h, and sends h.Response with send Hold after h.Arrival. Its
// timer only sends the bytes built at arrival. It returns false, and
// holds nothing, when the table is full or closed.
func (t *holdTable) add(h *HeldQuery, send func([]byte)) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || len(t.entries) >= t.limit {
		return false
	}
	h.send = send
	t.entries[h] = struct{}{}
	h.timer = time.AfterFunc(time.Until(h.Arrival.Add(Hold)), func() { t.fire(h) })
	return true
}

// fire sends a held refusal, unless the table let it go first.
func (t *holdTable) fire(h *HeldQuery) {
	t.mu.Lock()
	_, ok := t.entries[h]
	delete(t.entries, h)
	t.mu.Unlock()
	if ok {
		h.send(h.Response)
	}
}

// len returns how many queries are held.
func (t *holdTable) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// close stops every timer, so no held refusal is sent, and refuses
// later adds.
func (t *holdTable) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	for h := range t.entries {
		h.timer.Stop()
	}
	clear(t.entries)
}
