// Package termfilter is the allowlist filter of S16-01-filter. `wb`
// puts it between the guest's output and the user's terminal, or a pipe
// or file (S16-01-filter, "Filter pipes and files too"), which is
// boundary 6 of T00-index. Every byte it reads is the guest's.
//
// The filter splits the stream into text, controls and sequences, and
// passes a token only when a rule of the allowlist allows it. It drops
// any other token whole, so the terminal's parser is back in its ground
// state after every token. It counts each drop by a Rule of
// S16-02-drops-and-links. It logs nothing per token, because the guest
// chooses how many drops there are: the counts are the record. Of the
// OSC 8 links it drops, it keeps the URL, the text and a Mark for the
// record too (links.go, S09-12-terminal-filter).
package termfilter

import (
	"errors"
	"io"
	"maps"
	"time"
)

// ErrClosed is returned by Write after Close.
var ErrClosed = errors.New("termfilter: write after close")

// belInterval is the BEL rate limit: at most one per interval
// (S16-01-filter, "Allowlist").
const belInterval = time.Second

// replacement is U+FFFD, which an invalid byte becomes.
var replacement = []byte("�")

// Filter wraps the writer that guest output goes to. It is not safe
// for concurrent use.
type Filter struct {
	w   io.Writer
	tok tokenizer
	out []byte
	kb  keyboard
	// seen records the reports the exit reset clears (reset.go).
	seen passed

	counts map[Rule]uint64
	links  linkList

	now      func() time.Time
	belLimit bool
	belSeen  bool
	lastBEL  time.Time

	err    error
	closed bool
}

// Option sets up a Filter.
type Option func(*Filter)

// WithClock makes the BEL rate limit read now instead of time.Now.
func WithClock(now func() time.Time) Option {
	return func(f *Filter) { f.now = now }
}

// New returns a Filter that writes what it passes to w.
func New(w io.Writer, opts ...Option) *Filter {
	f := &Filter{
		w:        w,
		counts:   make(map[Rule]uint64, len(rules)),
		links:    newLinkList(),
		now:      time.Now,
		belLimit: true,
	}
	for _, r := range rules {
		f.counts[r] = 0
	}
	for _, o := range opts {
		o(f)
	}
	return f
}

// Write filters p and writes every complete token the allowlist passes
// to the underlying writer, in one write. It holds back only the bytes
// of an unfinished token: a UTF-8 character or a sequence split by the
// end of p. It returns len(p) whether it passed the bytes or dropped
// them. An error of the underlying writer is returned with len(p),
// because the filter has read p, and every later Write returns it again.
func (f *Filter) Write(p []byte) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	if f.closed {
		return 0, ErrClosed
	}
	f.out = f.out[:0]
	f.tok.feed(p, f.handle)
	return len(p), f.flush()
}

// Close ends the stream. The bytes of an unfinished UTF-8 character
// each become U+FFFD, and an unfinished sequence is dropped and counted
// by its class. A link still open is complete. It doesn't close the
// underlying writer. Close after Close does nothing.
func (f *Filter) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	defer f.links.end()
	if f.err != nil {
		return f.err
	}
	f.out = f.out[:0]
	f.tok.end(f.handle)
	return f.flush()
}

// Counts returns a copy of the counts by rule. It holds every rule,
// with zero for a rule that counted nothing. A count never goes down.
func (f *Filter) Counts() map[Rule]uint64 {
	return maps.Clone(f.counts)
}

func (f *Filter) flush() error {
	if len(f.out) == 0 {
		return nil
	}
	n, err := f.w.Write(f.out)
	if err == nil && n < len(f.out) {
		err = io.ErrShortWrite
	}
	f.err = err
	return err
}

func (f *Filter) count(r Rule) {
	f.counts[r]++
}

func (f *Filter) pass(raw []byte) {
	f.out = append(f.out, raw...)
}

// handle decides on one token.
func (f *Filter) handle(t token) {
	switch t.kind {
	case kText:
		f.pass(t.raw)
		f.links.addText(t.raw)
	case kInvalid:
		// Not a drop and not counted: the text around it stays
		// (S16-02-drops-and-links, "Which rule a drop counts under").
		f.pass(replacement)
		f.links.addText(replacement)
	case kC1:
		// A C1 control is a second way to start a sequence, so it never
		// passes (SEC03-no-host-exec).
		f.count(RuleC1)
	case kControl:
		f.control(t.raw[0])
	case kESC:
		f.escape(t)
	case kCSI:
		f.csi(t)
	case kOSC:
		f.osc(t)
	case kDCS:
		// DCS holds tmux pass-through and DECRQSS, which types
		// guest-chosen text into the terminal's input
		// (SEC03-no-host-exec, S16-01-filter "Never on the list").
		f.count(RuleDCS)
	case kAPC:
		// APC holds kitty graphics, which can read a host file by its
		// path (SEC03-no-host-exec).
		f.count(RuleAPC)
	case kPM, kSOS:
		f.count(RulePMOrSOS)
	}
}

// control passes BEL, BS, HT, LF, CR and SI, and drops the other C0
// controls and DEL (S16-01-filter, "Allowlist").
func (f *Filter) control(b byte) {
	switch b {
	case bel:
		f.bel()
	case '\b', '\t', '\n', '\r', 0x0f:
		f.out = append(f.out, b)
	default:
		f.count(RuleC0)
	}
}

// bel passes a BEL, at most one per belInterval. A BEL over the rate
// counts under RuleC0. A BEL that replaces a notification goes through
// the same limit, so a guest can't ring the host's attention signal at
// will (SEC14-no-fake-approvals).
func (f *Filter) bel() {
	now := f.now()
	if f.belLimit && f.belSeen && now.Sub(f.lastBEL) < belInterval {
		f.count(RuleC0)
		return
	}
	f.belSeen = true
	f.lastBEL = now
	f.out = append(f.out, bel)
}

// escapes are the ESC sequences the allowlist passes, without their
// ESC (S16-01-filter, "Allowlist"). `ESC c`, the full reset, isn't one.
var escapes = map[string]bool{
	"7": true, "8": true, "M": true, "D": true, "E": true, "=": true, ">": true,
	"(B": true, "(0": true, ")B": true, ")0": true,
}

func (f *Filter) escape(t token) {
	if !t.cut && escapes[string(t.raw[1:])] {
		f.pass(t.raw)
		return
	}
	f.count(RuleESC)
}

func (f *Filter) csi(t token) {
	// A CSI cut at its cap or ended early counts as RuleCSI, whatever
	// its first bytes (S16-02-drops-and-links).
	if t.cut {
		f.count(RuleCSI)
		return
	}
	if f.allowCSI(t.raw) {
		f.pass(t.raw)
		return
	}
	if t.raw[len(t.raw)-1] == 't' {
		f.count(RuleCSIWindow)
		return
	}
	f.count(RuleCSI)
}
