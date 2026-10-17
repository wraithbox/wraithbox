package termfilter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// link writes an OSC 8 that opens a link to uri, ended by ST.
func link(uri string) string {
	return "\x1b]8;;" + uri + "\x1b\\"
}

// linkEnd is the OSC 8 that closes a link.
const linkEnd = "\x1b]8;;\x1b\\"

// sgrBold is the SGR that turns bold on, which the filter passes.
const sgrBold = "\x1b[1m"

// filterLinks filters in with one Write and a Close, and returns the
// output, the links, and the overflow counts that aren't zero.
func filterLinks(t *testing.T, in string) (string, []Link, map[Mark]uint64) {
	t.Helper()
	var out bytes.Buffer
	f := New(&out, WithoutBELLimit())
	if _, err := f.Write([]byte(in)); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	over := map[Mark]uint64{}
	for m, n := range f.LinkOverflow() {
		if n > 0 {
			over[m] = n
		}
	}
	return out.String(), f.Links(), over
}

// TestLinks covers S16-02-drops-and-links, "What `wb` keeps of a link".
func TestLinks(t *testing.T) {
	big := strings.Repeat("a", maxString+100)
	tests := []struct {
		name string
		in   string
		out  string
		want []Link
	}{
		{
			name: "text across an sgr change",
			in:   link("https://example.com/") + "he" + sgrBold + "llo" + linkEnd + " after",
			out:  "he" + sgrBold + "llo after",
			want: []Link{{URL: "https://example.com/", Text: "hello"}},
		},
		{
			name: "controls and drops aren't text",
			in:   link("https://example.com/") + "a\r\nb\u009bc\x1b]0;title\x07d" + linkEnd,
			out:  "a\r\nbcd",
			want: []Link{{URL: "https://example.com/", Text: "abcd"}},
		},
		{
			name: "an invalid byte is one U+FFFD",
			in:   link("https://example.com/") + "a\xff" + "b" + linkEnd,
			out:  "a�b",
			want: []Link{{URL: "https://example.com/", Text: "a�b"}},
		},
		{
			name: "text of 300 code points",
			in:   link("https://example.com/") + strings.Repeat("é", 300) + linkEnd,
			out:  strings.Repeat("é", 300),
			want: []Link{{URL: "https://example.com/", Text: strings.Repeat("é", 256), TextCut: true}},
		},
		{
			name: "text of 256 code points isn't cut",
			in:   link("https://example.com/") + strings.Repeat("✓", 256) + linkEnd,
			out:  strings.Repeat("✓", 256),
			want: []Link{{URL: "https://example.com/", Text: strings.Repeat("✓", 256)}},
		},
		{
			name: "url of 3000 bytes",
			in:   link("https://example.com/"+strings.Repeat("p", 2980)) + "x" + linkEnd,
			out:  "x",
			want: []Link{{URL: "https://example.com/" + strings.Repeat("p", 2028), URLCut: true, Text: "x"}},
		},
		{
			name: "url of 2048 bytes isn't cut",
			in:   link("https://example.com/"+strings.Repeat("p", 2028)) + "x" + linkEnd,
			out:  "x",
			want: []Link{{URL: "https://example.com/" + strings.Repeat("p", 2028), Text: "x"}},
		},
		{
			name: "url cut inside its authority",
			in:   link("https://"+strings.Repeat("a.b", 800)+"@127.0.0.1/") + "x" + linkEnd,
			out:  "x",
			want: []Link{{URL: "https://" + strings.Repeat("a.b", 680), URLCut: true, Text: "x", Mark: MarkDotlessName}},
		},
		{
			name: "url with invalid utf-8, as sent",
			in:   link("https://example.com/\xff\xc2") + "x" + linkEnd,
			out:  "x",
			want: []Link{{URL: "https://example.com/\xff\xc2", Text: "x"}},
		},
		{
			name: "osc 8 cut at 16 KB",
			in:   "\x1b]8;;https://127.0.0.1/" + big + "\x07text" + linkEnd,
			out:  "text",
			want: []Link{{URL: "https://127.0.0.1/" + big[:maxLinkURL-18], URLCut: true, Text: "text", Mark: MarkIPLiteral}},
		},
		{
			name: "osc 8 cut at 16 KB in its parameters",
			in:   "\x1b]8;id=" + big + ";https://example.com/\x07text",
			out:  "text",
			want: []Link{{URL: "", URLCut: true, Text: "text", Mark: MarkOtherScheme}},
		},
		{
			name: "osc 8 ended by can",
			in:   "\x1b]8;;https://example.com/\x18text",
			out:  "text",
			want: []Link{{URL: "https://example.com/", URLCut: true, Text: "text"}},
		},
		{
			name: "link still open at the end of the stream",
			in:   link("http://example.com/") + "open",
			out:  "open",
			want: []Link{{URL: "http://example.com/", Text: "open", Mark: MarkHTTP}},
		},
		{
			name: "two opens without a close",
			in:   link("https://a.example/") + "one " + link("file:///etc/passwd") + "two" + linkEnd,
			out:  "one two",
			want: []Link{
				{URL: "https://a.example/", Text: "one "},
				{URL: "file:///etc/passwd", Text: "two", Mark: MarkGuestPath},
			},
		},
		{
			name: "repeats are kept",
			in:   link("https://a.example/") + "x" + linkEnd + link("https://a.example/") + "x" + linkEnd,
			out:  "xx",
			want: []Link{{URL: "https://a.example/", Text: "x"}, {URL: "https://a.example/", Text: "x"}},
		},
		{
			name: "parameters aren't kept",
			in:   "\x1b]8;id=42:x=y;https://a.example/;q\x07x\x1b]8;id=42;\x07",
			out:  "x",
			want: []Link{{URL: "https://a.example/;q", Text: "x"}},
		},
		{
			name: "osc 08 is a link too",
			in:   "\x1b]08;;https://intranet/\x07x",
			out:  "x",
			want: []Link{{URL: "https://intranet/", Text: "x", Mark: MarkDotlessName}},
		},
		{
			name: "empty text",
			in:   link("https://a.example/") + linkEnd,
			out:  "",
			want: []Link{{URL: "https://a.example/"}},
		},
		{
			name: "close alone, and an empty uri with parameters",
			in:   linkEnd + "a\x1b]8;id=1;\x07b\x1b]8\x07c",
			out:  "abc",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, links, over := filterLinks(t, tt.in)
			if out != tt.out {
				t.Errorf("output = %q, want %q", out, tt.out)
			}
			if !slices.Equal(links, tt.want) {
				t.Errorf("links = %+v,\nwant %+v", links, tt.want)
			}
			if len(over) != 0 {
				t.Errorf("overflow = %v, want none", over)
			}
		})
	}
}

// TestLinkPast100 sees the 101st link and later counted by mark, and
// not kept (S16-02-drops-and-links, "What `wb` keeps of a link").
func TestLinkPast100(t *testing.T) {
	var in strings.Builder
	for i := range maxLinks {
		fmt.Fprintf(&in, "%s%d%s", link(fmt.Sprintf("https://example.com/%d", i)), i, linkEnd)
	}
	in.WriteString(link("file:///etc/hosts") + "101" + linkEnd)
	in.WriteString(link("https://example.com/") + "102")
	in.WriteString(link("http://example.com/") + "103")
	in.WriteString(link("file:///etc/passwd") + "104" + linkEnd)
	_, links, over := filterLinks(t, in.String())
	if len(links) != maxLinks {
		t.Fatalf("kept %d links, want %d", len(links), maxLinks)
	}
	if last := links[maxLinks-1]; last.Text != "99" {
		t.Errorf("last kept link = %+v, want the 100th", last)
	}
	want := map[Mark]uint64{MarkGuestPath: 2, MarkUnmarked: 1, MarkHTTP: 1}
	if !maps.Equal(over, want) {
		t.Errorf("overflow = %v, want %v", over, want)
	}
}

// TestLinkOverflowNeverGoesDown writes links past 100 one by one, and
// sees every count stay or rise.
func TestLinkOverflowNeverGoesDown(t *testing.T) {
	f := New(io.Discard)
	prev := f.LinkOverflow()
	if len(prev) != len(Marks())+1 {
		t.Fatalf("LinkOverflow() has %d keys, want every mark and %s", len(prev), MarkUnmarked)
	}
	urls := []string{"https://example.com/", "file:///x", "http://example.com/", "https://[::1]/"}
	for i := range 3 * maxLinks {
		if _, err := f.Write([]byte(link(urls[i%len(urls)]) + "x")); err != nil {
			t.Fatal(err)
		}
		now := f.LinkOverflow()
		for m, n := range now {
			if n < prev[m] {
				t.Fatalf("overflow %s went down from %d to %d", m, prev[m], n)
			}
		}
		prev = now
	}
	if got := prev[MarkUnmarked] + prev[MarkGuestPath] + prev[MarkHTTP] + prev[MarkIPLiteral]; got != 2*maxLinks {
		t.Errorf("overflow counts sum to %d, want %d", got, 2*maxLinks)
	}
}

// TestLinkNeverPasses writes a link with each mark, and one without,
// and sees every OSC 8 dropped and its text kept: a mark never makes a
// link pass (SEC03-no-host-exec).
func TestLinkNeverPasses(t *testing.T) {
	urls := []string{
		"https://example.com/", "file:///etc/hosts", "https://127.0.0.1/", "https://localhost/",
		"https://intranet/", "https://u@example.com/", "http://example.com/", "ftp://example.com/",
	}
	for _, u := range urls {
		got, d := filterString(t, link(u)+"text"+linkEnd)
		if got != "text" {
			t.Errorf("link to %q: output = %q, want only its text", u, got)
		}
		if !maps.Equal(d, drops{RuleOSCLink: 2}) {
			t.Errorf("link to %q: counts = %v, want two %s", u, d, RuleOSCLink)
		}
	}
}

// TestLinkFunc sees the callback run once per kept link, when the link
// is complete, and Links hold only complete links.
func TestLinkFunc(t *testing.T) {
	var got []Link
	f := New(io.Discard, WithLinkFunc(func(l Link) { got = append(got, l) }))
	write := func(s string) {
		t.Helper()
		if _, err := f.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	write(link("https://a.example/") + "one")
	if len(got) != 0 || len(f.Links()) != 0 {
		t.Fatalf("an open link was reported: %+v, %+v", got, f.Links())
	}
	write(" more" + link("https://b.example/") + "two")
	want := []Link{{URL: "https://a.example/", Text: "one more"}}
	if !slices.Equal(got, want) || !slices.Equal(f.Links(), want) {
		t.Fatalf("after the second open: callback %+v, Links %+v, want %+v", got, f.Links(), want)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	want = append(want, Link{URL: "https://b.example/", Text: "two"})
	if !slices.Equal(got, want) || !slices.Equal(f.Links(), want) {
		t.Fatalf("after Close: callback %+v, Links %+v, want %+v", got, f.Links(), want)
	}
	if err := f.Close(); err != nil || len(got) != 2 {
		t.Errorf("second Close() = %v, and %d links reported, want nil and 2", err, len(got))
	}
}

// TestLinkAtCloseAfterWriterError sees Close complete the open link
// even when the writer failed.
func TestLinkAtCloseAfterWriterError(t *testing.T) {
	boom := errors.New("boom")
	f := New(failWriter{err: boom})
	if _, err := f.Write([]byte(link("https://a.example/") + "x")); !errors.Is(err, boom) {
		t.Fatalf("Write() = %v, want boom", err)
	}
	if err := f.Close(); !errors.Is(err, boom) {
		t.Fatalf("Close() = %v, want boom", err)
	}
	want := []Link{{URL: "https://a.example/", Text: "x"}}
	if got := f.Links(); !slices.Equal(got, want) {
		t.Errorf("Links() = %+v, want %+v", got, want)
	}
}

func TestLinksIsACopy(t *testing.T) {
	f := New(io.Discard)
	if _, err := f.Write([]byte(link("https://a.example/") + "x" + linkEnd)); err != nil {
		t.Fatal(err)
	}
	l := f.Links()
	l[0].URL = "changed"
	if f.Links()[0].URL != "https://a.example/" {
		t.Error("Links() returns its own list")
	}
	o := f.LinkOverflow()
	o[MarkHTTP] = 7
	if f.LinkOverflow()[MarkHTTP] != 0 {
		t.Error("LinkOverflow() returns its own map")
	}
}

// checkLinks checks the link list of C10-terminal, "Fuzzing", for
// FuzzFilter, against S16-02-drops-and-links, "What `wb` keeps of a
// link": at most 100 links, each URL of at most 2048 bytes, each text
// valid UTF-8 of at most 256 code points, each mark the marker's mark
// for its URL, a mark on each cut URL whose authority runs to the cut,
// no overflow before the list is full, and no more links than OSC 8
// drops. It also checks that the link function got the same
// links.
func checkLinks(r filtered) error {
	if len(r.links) > 100 {
		return fmt.Errorf("%d links kept, want at most 100", len(r.links))
	}
	var over uint64
	for _, n := range r.overflow {
		over += n
	}
	if over > 0 && len(r.links) < 100 {
		return fmt.Errorf("%d links counted past the kept %d", over, len(r.links))
	}
	if total := uint64(len(r.links)) + over; total > r.counts[RuleOSCLink] {
		return fmt.Errorf("%d links from %d OSC 8 drops", total, r.counts[RuleOSCLink])
	}
	for _, l := range r.links {
		if err := checkLink(l); err != nil {
			return err
		}
	}
	if !slices.Equal(r.reported, r.links) {
		return fmt.Errorf("the link function got %d links, Links has %d", len(r.reported), len(r.links))
	}
	return nil
}

func checkLink(l Link) error {
	switch {
	case len(l.URL) > 2048:
		return fmt.Errorf("URL of %d bytes, want at most 2048", len(l.URL))
	case !utf8.ValidString(l.Text):
		return fmt.Errorf("text %q isn't valid UTF-8", l.Text)
	case utf8.RuneCountInString(l.Text) > 256:
		return fmt.Errorf("text of %d code points, want at most 256", utf8.RuneCountInString(l.Text))
	case l.Mark != markURL(l.URL, l.URLCut):
		return fmt.Errorf("link to %q marked %q, the marker says %q", l.URL, l.Mark, markURL(l.URL, l.URLCut))
	case l.URLCut && l.Mark == "" && !authorityEnds(l.URL):
		return fmt.Errorf("cut link to %q has no mark, but its authority runs to the cut", l.URL)
	}
	return nil
}

// TestCheckLinksRefuses feeds checkLinks link lists that break a
// property, and sees it refuse each. A checker that passes everything
// would make FuzzFilter pass too.
func TestCheckLinksRefuses(t *testing.T) {
	ok := Link{URL: "https://example.com/", Text: "x"}
	many := slices.Repeat([]Link{ok}, 101)
	full := slices.Repeat([]Link{ok}, 100)
	osc8 := map[Rule]uint64{RuleOSCLink: 1000}
	one := func(l Link) []Link { return []Link{l} }
	bad := map[string]filtered{
		"101 links":           {counts: osc8, links: many, reported: many},
		"overflow before 100": {counts: osc8, links: one(ok), reported: one(ok), overflow: map[Mark]uint64{MarkHTTP: 1}},
		"more than osc 8":     {counts: map[Rule]uint64{RuleOSCLink: 100}, links: full, reported: full, overflow: map[Mark]uint64{MarkUnmarked: 1}},
		"long url":            {counts: osc8, links: one(Link{URL: "https://" + strings.Repeat("a", 2041)})},
		"invalid text":        {counts: osc8, links: one(Link{URL: "https://example.com/", Text: "\xff"})},
		"long text":           {counts: osc8, links: one(Link{URL: "https://example.com/", Text: strings.Repeat("é", 257)})},
		"wrong mark":          {counts: osc8, links: one(Link{URL: "file:///etc/hosts"})},
		"cut without a mark":  {counts: osc8, links: one(Link{URL: "https://example.com", URLCut: true})},
		"not reported":        {counts: osc8, links: one(ok), reported: []Link{}},
	}
	for name, r := range bad {
		if r.reported == nil {
			r.reported = r.links
		}
		if checkLinks(r) == nil {
			t.Errorf("%s: checkLinks = nil, want an error", name)
		}
	}
	good := filtered{counts: osc8, links: full, reported: full, overflow: map[Mark]uint64{MarkUnmarked: 5}}
	if err := checkLinks(good); err != nil {
		t.Errorf("checkLinks of a full list = %v, want nil", err)
	}
}
