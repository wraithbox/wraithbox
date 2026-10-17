package termfilter

import (
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// Caps of S16-02-drops-and-links, "What `wb` keeps of a link". They
// bound what the filter keeps of the links it drops
// (SEC13-bounded-resources), and `wb-hostd` refuses a report past them
// (S09-12-terminal-filter, "Form").
const (
	// maxLinks is the most links the filter keeps. It counts the rest
	// by mark.
	maxLinks = 100
	// maxLinkURL is the most bytes of a URL the filter keeps.
	maxLinkURL = 2048
	// maxLinkText is the most code points of a link's text the filter
	// keeps.
	maxLinkText = 256
)

// Link is what the filter keeps of one OSC 8 link it dropped
// (S16-02-drops-and-links, "What `wb` keeps of a link"). It doesn't
// keep the OSC 8 parameters, such as `id=`.
type Link struct {
	// URL holds the first maxLinkURL bytes of the URL as the guest sent
	// them. It may hold bytes that aren't valid UTF-8.
	URL string
	// URLCut is set when the filter cut the URL at maxLinkURL bytes,
	// or when the OSC 8 itself was cut: at the 16 KB string cap, or
	// ended by CAN, SUB, ESC or the end of the stream. The URL may then
	// be longer than what arrived.
	URLCut bool
	// Text holds the first maxLinkText code points of the link's text:
	// the text the filter passed after the OSC 8 that opened the link,
	// up to the next OSC 8 or the end of the stream. An invalid byte is
	// the U+FFFD that replaced it. Controls and sequences in between
	// aren't part of it. It is valid UTF-8.
	Text string
	// TextCut is set when the text had more than maxLinkText code
	// points.
	TextCut bool
	// Mark is the mark of the URL, or the zero Mark.
	Mark Mark
}

// linkList keeps the links the filter drops, in order, and counts by
// mark those past maxLinks.
type linkList struct {
	kept []Link
	// open is set while cur is a kept link whose text is still
	// arriving.
	open bool
	cur  Link
	text []byte
	// textLen is the number of code points in text.
	textLen  int
	overflow map[Mark]uint64
	onLink   func(Link)
}

func newLinkList() linkList {
	overflow := make(map[Mark]uint64, len(marks)+1)
	for _, m := range marks {
		overflow[m] = 0
	}
	overflow[MarkUnmarked] = 0
	return linkList{overflow: overflow}
}

// osc8 reads a dropped OSC 8. Its payload is what follows the OSC
// number and its `;`: the parameters, a `;`, and the URI. It ends the
// open link, if any. It opens a link when the URI isn't empty, or when
// the OSC 8 was cut, because the URI of a cut OSC 8 may not have
// arrived. Past maxLinks it counts the link by mark instead.
func (l *linkList) osc8(payload string, cut bool) {
	l.end()
	_, uri, _ := strings.Cut(payload, ";")
	if uri == "" && !cut {
		return
	}
	urlCut := cut
	if len(uri) > maxLinkURL {
		uri, urlCut = uri[:maxLinkURL], true
	}
	mark := markURL(uri, urlCut)
	if len(l.kept) >= maxLinks {
		if mark == "" {
			mark = MarkUnmarked
		}
		l.overflow[mark]++
		return
	}
	// Clone, so the link doesn't hold on to the whole sequence.
	l.cur = Link{URL: strings.Clone(uri), URLCut: urlCut, Mark: mark}
	l.text = l.text[:0]
	l.textLen = 0
	l.open = true
}

// addText adds text the filter passed to the open link, up to
// maxLinkText code points. p is valid UTF-8.
func (l *linkList) addText(p []byte) {
	if !l.open || l.cur.TextCut {
		return
	}
	for len(p) > 0 {
		if l.textLen == maxLinkText {
			l.cur.TextCut = true
			return
		}
		_, n := utf8.DecodeRune(p)
		l.text = append(l.text, p[:n]...)
		l.textLen++
		p = p[n:]
	}
}

// end completes the open link, if any: it keeps it, and calls onLink
// with it.
func (l *linkList) end() {
	if !l.open {
		return
	}
	l.open = false
	l.cur.Text = string(l.text)
	l.kept = append(l.kept, l.cur)
	if l.onLink != nil {
		l.onLink(l.cur)
	}
}

// WithLinkFunc makes the filter call fn with each link it keeps, when
// the link is complete: at the next OSC 8, or at Close. `wb` sends each
// kept link to `wb-hostd` then (S09-12-terminal-filter, "Reports"). fn
// runs inside Write or Close.
func WithLinkFunc(fn func(Link)) Option {
	return func(f *Filter) { f.links.onLink = fn }
}

// Links returns a copy of the complete links the filter kept, in the
// order the guest sent them, repeats included. A link still open, whose
// text may grow, isn't among them until the next OSC 8 or Close. There
// are at most maxLinks.
func (f *Filter) Links() []Link {
	return slices.Clone(f.links.kept)
}

// LinkOverflow returns a copy of the counts by mark of the links past
// the kept ones, with those without a mark under MarkUnmarked. It holds
// every mark, with zero for a mark that counted nothing. A count never
// goes down.
func (f *Filter) LinkOverflow() map[Mark]uint64 {
	return maps.Clone(f.links.overflow)
}
