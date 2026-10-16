package logpipe

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeClock is a clock a test moves. It is safe for the reader and the
// writer goroutine.
type fakeClock struct{ ns atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ns.Store(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC).UnixNano())
	return c
}

func (c *fakeClock) now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *fakeClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

// record is a decoded output record.
type record struct {
	Time      string `json:"time"`
	Process   string `json:"process"`
	Line      string `json:"line"`
	Truncated bool   `json:"truncated"`
	Rule      string `json:"rule"`
	Dropped   int64  `json:"dropped"`
}

// lockedBuffer collects records; each Write is one record.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// decode checks that out is a sequence of well-formed JSON records, one
// per line, of printable ASCII, and returns them.
func decode(t testing.TB, out []byte) []record {
	t.Helper()
	var recs []record
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		for _, c := range line {
			if c < 0x20 || c >= 0x7f {
				t.Fatalf("record has byte %#x: %q", c, line)
			}
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		var r record
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("record %q: %v", line, err)
		}
		if dec.More() {
			t.Fatalf("record %q holds more than one value", line)
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) > 0 && out[len(out)-1] != '\n' {
		t.Fatalf("output doesn't end in a newline: %q", out[max(0, len(out)-40):])
	}
	return recs
}

func run(t *testing.T, input string, clk *fakeClock) []record {
	t.Helper()
	var out lockedBuffer
	if err := copyWith(strings.NewReader(input), &out, "wb-netd", clk.now); err != nil {
		t.Fatal(err)
	}
	return decode(t, out.Bytes())
}

func lines(recs []record) []record {
	var out []record
	for _, r := range recs {
		if r.Rule == "" {
			out = append(out, r)
		}
	}
	return out
}

func drops(recs []record, rule string) []record {
	var out []record
	for _, r := range recs {
		if r.Rule == rule {
			out = append(out, r)
		}
	}
	return out
}

func TestRecordFields(t *testing.T) {
	recs := run(t, "hello\nworld", newFakeClock())
	if len(recs) != 2 {
		t.Fatalf("records = %+v, want 2", recs)
	}
	want := record{Time: "2026-10-17T12:00:00Z", Process: "wb-netd", Line: "hello"}
	if recs[0] != want {
		t.Errorf("record = %+v, want %+v", recs[0], want)
	}
	if recs[1].Line != "world" {
		t.Errorf("a last line without a newline = %q, want world", recs[1].Line)
	}
}

// 101 lines in one second write 100 and one drop record with the count
// 1 (S04-08-records-and-bounds, "Logs and health").
func TestRateLimit(t *testing.T) {
	var in strings.Builder
	for i := range 101 {
		fmt.Fprintf(&in, "line %d\n", i)
	}
	recs := run(t, in.String(), newFakeClock())
	if n := len(lines(recs)); n != 100 {
		t.Errorf("lines written = %d, want 100", n)
	}
	d := drops(recs, RuleRate)
	if len(d) != 1 || d[0].Dropped != 1 {
		t.Errorf("rate drop records = %+v, want one with count 1", d)
	}
}

// The count goes out when the window turns, and a new window writes
// lines again.
func TestRateWindowTurns(t *testing.T) {
	clk := newFakeClock()
	r, w := io.Pipe()
	var out lockedBuffer
	done := make(chan error, 1)
	go func() { done <- copyWith(r, &out, "wb-vmd-work", clk.now) }()
	for range 150 {
		fmt.Fprintln(w, "first window")
	}
	// An empty write returns once the reader asks for more, so after it
	// has handled the last line.
	_, _ = w.Write(nil)
	clk.advance(time.Second)
	for range 10 {
		fmt.Fprintln(w, "second window")
	}
	_ = w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	recs := decode(t, out.Bytes())
	if n := len(lines(recs)); n != 110 {
		t.Errorf("lines written = %d, want 110", n)
	}
	if d := drops(recs, RuleRate); len(d) != 1 || d[0].Dropped != 50 {
		t.Errorf("rate drop records = %+v, want one with count 50", d)
	}
}

// A 5 KiB line is cut at 4 KiB with the marker, and the next line is
// intact.
func TestLongLineCut(t *testing.T) {
	long := strings.Repeat("a", MaxLine) + strings.Repeat("b", 1024)
	recs := run(t, long+"\nnext\n", newFakeClock())
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	if recs[0].Line != strings.Repeat("a", MaxLine) || !recs[0].Truncated {
		t.Errorf("cut line = %d bytes, truncated %v; want %d bytes of a and truncated",
			len(recs[0].Line), recs[0].Truncated, MaxLine)
	}
	if recs[1].Line != "next" || recs[1].Truncated {
		t.Errorf("next line = %+v, want next intact", recs[1])
	}
}

func TestLineOfExactlyMaxNotCut(t *testing.T) {
	recs := run(t, strings.Repeat("x", MaxLine)+"\n", newFakeClock())
	if len(recs) != 1 || recs[0].Truncated || len(recs[0].Line) != MaxLine {
		t.Errorf("records = %d, first truncated %v; want one whole line", len(recs), recs[0].Truncated)
	}
}

// A line with a quote, a control byte and invalid UTF-8 becomes one
// well-formed record, and can't forge a second one or another process.
func TestHostileLine(t *testing.T) {
	in := "say \"hi\"\x1b[31m\xff\xfe\"},{\"process\":\"wb-hostd\",\"line\":\"forged\r\n"
	recs := run(t, in, newFakeClock())
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	bad := string(utf8.RuneError)
	want := "say \"hi\"\x1b[31m" + bad + bad + "\"},{\"process\":\"wb-hostd\",\"line\":\"forged\r"
	if r.Process != "wb-netd" || r.Line != want {
		t.Errorf("record = %+v, want process wb-netd and line %q", r, want)
	}
}

func TestUnicodeEscaped(t *testing.T) {
	in := "\u00e9 \u2028 \U0001F600 \x7f\n"
	var out lockedBuffer
	if err := copyWith(strings.NewReader(in), &out, "wb-netd", newFakeClock().now); err != nil {
		t.Fatal(err)
	}
	raw := string(out.Bytes())
	for _, esc := range []string{`\u00e9`, `\u2028`, `\ud83d\ude00`, `\u007f`} {
		if !strings.Contains(raw, esc) {
			t.Errorf("record %s lacks %s", raw, esc)
		}
	}
	if recs := decode(t, out.Bytes()); recs[0].Line != strings.TrimSuffix(in, "\n") {
		t.Errorf("line = %q, want %q", recs[0].Line, in)
	}
}

// blockingWriter blocks every Write until release is closed.
type blockingWriter struct {
	release chan struct{}
	out     lockedBuffer
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	<-b.release
	return b.out.Write(p)
}

// A blocked file write doesn't stop the reader from draining the pipe.
// The lines that find the queue full are dropped and counted.
func TestBlockedWriteKeepsDraining(t *testing.T) {
	const total = 1000
	clk := newFakeClock()
	r, w := io.Pipe()
	bw := &blockingWriter{release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- copyWith(r, bw, "wb-vmd-work", clk.now) }()

	// io.Pipe hands each write to the reader, so the writes finish only
	// if the reader drains. 50 lines per second keep under the rate.
	written := make(chan struct{})
	go func() {
		defer close(written)
		for i := range total {
			if i%50 == 0 {
				clk.advance(time.Second)
			}
			fmt.Fprintf(w, "line %d\n", i)
		}
		_ = w.Close()
	}()
	select {
	case <-written:
	case <-time.After(10 * time.Second):
		t.Fatal("the reader stopped draining while the write was blocked")
	}
	close(bw.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	recs := decode(t, bw.out.Bytes())
	n := int64(len(lines(recs)))
	var dropped int64
	for _, d := range drops(recs, RuleBacklog) {
		dropped += d.Dropped
	}
	if n+dropped != total || dropped == 0 {
		t.Errorf("written %d and dropped %d, want a total of %d with some dropped", n, dropped, total)
	}
	if len(drops(recs, RuleRate)) != 0 {
		t.Error("lines under the rate were counted over it")
	}
}

type failingWriter struct{ n atomic.Int64 }

func (f *failingWriter) Write([]byte) (int, error) {
	f.n.Add(1)
	return 0, io.ErrShortWrite
}

func TestFailedWritesDoNotStopTheReader(t *testing.T) {
	fw := &failingWriter{}
	if err := copyWith(strings.NewReader("a\nb\nc\n"), fw, "wb-netd", newFakeClock().now); err != nil {
		t.Fatal(err)
	}
	if fw.n.Load() < 3 {
		t.Errorf("writes = %d, want at least 3", fw.n.Load())
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestReadError(t *testing.T) {
	var out lockedBuffer
	if err := copyWith(errReader{}, &out, "wb-netd", time.Now); err == nil {
		t.Error("a read error was not returned")
	}
}

func TestProcessName(t *testing.T) {
	for _, name := range []string{"wb-netd", "wb-vmd-work", "wb-hostd", "a", strings.Repeat("a", 64)} {
		if err := checkProcess(name); err != nil {
			t.Errorf("%q refused: %v", name, err)
		}
	}
	for _, name := range []string{"", "-wb", "1wb", "wb_netd", "WB", "wb/../x", "wb\"", "wb netd", strings.Repeat("a", 65)} {
		if err := checkProcess(name); err == nil {
			t.Errorf("%q accepted", name)
		}
		if err := Copy(strings.NewReader("x\n"), io.Discard, name); err == nil {
			t.Errorf("Copy took process %q", name)
		}
		if _, err := OpenFile(t.TempDir(), name); err == nil {
			t.Errorf("OpenFile took process %q", name)
		}
	}
}

// FuzzLogPipe feeds arbitrary bytes in arbitrary chunks. Every output
// record is one well-formed JSON line of printable ASCII, no line field
// holds more than MaxLine characters, and the splitter's buffer never
// grows past MaxLine.
func FuzzLogPipe(f *testing.F) {
	f.Add([]byte("hello\nworld\n"), []byte{3, 7})
	f.Add([]byte(strings.Repeat("a", MaxLine+10)+"\nb"), []byte{255, 1})
	f.Add([]byte("\"\\\x00\x1b\xff\xc3\n\xe2\x80\xa8\n"), []byte{1})
	f.Add(bytes.Repeat([]byte("x\n"), 300), []byte{0})
	f.Fuzz(func(t *testing.T, data, sizes []byte) {
		checkSplitterBounded(t, chunks(data, sizes))
		checkRecordsWellFormed(t, chunks(data, sizes))
	})
}

// checkSplitterBounded: the splitter's line buffer never grows, and no
// line it emits is longer than MaxLine.
func checkSplitterBounded(t *testing.T, in [][]byte) {
	s := newSplitter()
	limit := cap(s.line)
	emit := func(line []byte, _ bool) {
		if len(line) > MaxLine {
			t.Fatalf("splitter emitted %d bytes", len(line))
		}
	}
	for _, chunk := range in {
		s.feed(chunk, emit)
		if cap(s.line) != limit || len(s.line) > MaxLine {
			t.Fatalf("splitter buffer grew to len %d cap %d", len(s.line), cap(s.line))
		}
	}
}

// checkRecordsWellFormed: every record is one JSON line of printable
// ASCII (decode), of the spawn's process, with at most MaxLine
// characters in its line.
func checkRecordsWellFormed(t *testing.T, in [][]byte) {
	clk := newFakeClock()
	var out lockedBuffer
	if err := copyWith(&chunkReader{chunks: in, clk: clk}, &out, "wb-vmd-work", clk.now); err != nil {
		t.Fatal(err)
	}
	for _, rec := range decode(t, out.Bytes()) {
		if rec.Process != "wb-vmd-work" {
			t.Fatalf("record of process %q", rec.Process)
		}
		if n := utf8.RuneCountInString(rec.Line); n > MaxLine {
			t.Fatalf("line field of %d characters", n)
		}
	}
}

// chunks cuts data into pieces of the sizes given, cycling, 1 to 256
// bytes each.
func chunks(data, sizes []byte) [][]byte {
	var out [][]byte
	for i := 0; len(data) > 0; i++ {
		n := 64
		if len(sizes) > 0 {
			n = int(sizes[i%len(sizes)]) + 1
		}
		n = min(n, len(data))
		out = append(out, data[:n])
		data = data[n:]
	}
	return out
}

// chunkReader returns one chunk per Read, and moves the clock a little,
// so the fuzzer crosses rate windows too.
type chunkReader struct {
	chunks [][]byte
	clk    *fakeClock
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[0])
	c.chunks[0] = c.chunks[0][n:]
	if len(c.chunks[0]) == 0 {
		c.chunks = c.chunks[1:]
	}
	c.clk.advance(37 * time.Millisecond)
	return n, nil
}
