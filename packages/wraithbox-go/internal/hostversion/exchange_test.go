package hostversion

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a log sink two goroutines share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testLog() (*slog.Logger, *syncBuffer) {
	var buf syncBuffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

type result struct {
	peer string
	err  error
}

// side is one end's outcome and log.
type side struct {
	result
	log string
}

// both runs Exchange on the two ends of a net.Pipe at once, each with
// its own version.
func both(t *testing.T, a, b string) (sa, sb side) {
	t.Helper()
	ca, cb := net.Pipe()
	defer ca.Close()
	defer cb.Close()
	la, ba := testLog()
	lb, bb := testLog()
	done := make(chan result, 1)
	go func() {
		p, err := Exchange(context.Background(), lb, cb, b)
		done <- result{p, err}
	}()
	p, err := Exchange(context.Background(), la, ca, a)
	return side{result{p, err}, ba.String()}, side{<-done, bb.String()}
}

func wantRule(t *testing.T, err error, rule string) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Rule != rule {
		t.Fatalf("err = %v, want %s", err, rule)
	}
	return r
}

func TestExchangeSameVersion(t *testing.T) {
	ra, rb := both(t, "1.2.3+abc", "1.2.3+abc")
	if ra.err != nil || rb.err != nil {
		t.Fatalf("Exchange: %v, %v", ra.err, rb.err)
	}
	if ra.peer != "1.2.3+abc" || rb.peer != "1.2.3+abc" {
		t.Errorf("peers = %q, %q", ra.peer, rb.peer)
	}
}

// TestExchangeMismatch: another version is refused on both sides, with
// both versions in the error and the log (S04-08-records-and-bounds,
// "Between host processes").
func TestExchangeMismatch(t *testing.T) {
	ra, rb := both(t, "1.2.3", "1.2.4")
	a := wantRule(t, ra.err, RuleMismatch)
	b := wantRule(t, rb.err, RuleMismatch)
	if a.OwnLabel() != `"1.2.3"` || a.PeerLabel() != `"1.2.4"` {
		t.Errorf("side a: own %s, peer %s", a.OwnLabel(), a.PeerLabel())
	}
	if b.OwnLabel() != `"1.2.4"` || b.PeerLabel() != `"1.2.3"` {
		t.Errorf("side b: own %s, peer %s", b.OwnLabel(), b.PeerLabel())
	}
	for _, log := range []string{ra.log, rb.log} {
		for _, want := range []string{`"rule":"peer-version-mismatch"`, `1.2.3`, `1.2.4`} {
			if !strings.Contains(log, want) {
				t.Errorf("log = %s, want %s", log, want)
			}
		}
	}
}

// TestExchangeUnstamped: two unstamped builds refuse each other
// (C11-host-daemons, "Build versions"; S10-02-packaging-and-signing),
// and so does one unstamped side against a stamped one.
func TestExchangeUnstamped(t *testing.T) {
	for _, c := range []struct{ a, b string }{{"", ""}, {"", "1.2.3"}, {"1.2.3", ""}} {
		ra, rb := both(t, c.a, c.b)
		wantRule(t, ra.err, RuleUnstamped)
		wantRule(t, rb.err, RuleUnstamped)
	}
}

// TestExchangeOwnOutsideForm: an own version outside the record's bytes
// goes out as unstamped, and both sides refuse.
func TestExchangeOwnOutsideForm(t *testing.T) {
	ra, rb := both(t, "1.2.3\n", "1.2.3")
	wantRule(t, ra.err, RuleUnstamped)
	wantRule(t, rb.err, RuleUnstamped)
}

// What a fake peer does after it sent its bytes.
type then int

const (
	hold        then = iota // keep the connection open
	keepSending             // send "A" without end
	hangUp                  // close the connection
)

// peer runs a fake peer on the far end of a pipe: it reads whatever it
// is sent, writes send, and then does next.
func peer(t *testing.T, send []byte, next then) net.Conn {
	t.Helper()
	ours, theirs := net.Pipe()
	t.Cleanup(func() { _ = ours.Close(); _ = theirs.Close() })
	go func() { _, _ = io.Copy(io.Discard, theirs) }()
	go func() {
		_, err := theirs.Write(send)
		switch {
		case err != nil || next == hold:
			return
		case next == hangUp:
			_ = theirs.Close()
			return
		}
		for {
			if _, err := theirs.Write(bytes.Repeat([]byte("A"), 1024)); err != nil {
				return
			}
		}
	}()
	return ours
}

func record(version string) []byte {
	return append(append([]byte(Magic), byte(len(version))), version...)
}

// TestExchangeRefusesRecords: each bad record gets its rule.
func TestExchangeRefusesRecords(t *testing.T) {
	for _, c := range []struct {
		name string
		send []byte
		rule string
		next then
	}{
		{"empty version", record(""), RuleUnstamped, hold},
		{"65 bytes", append(append([]byte(Magic), 65), strings.Repeat("1", 65)...), RuleMalformed, hold},
		{"control byte", record("1.2.3\x01"), RuleMalformed, hold},
		{"non-ASCII byte", record("1.2.3\xc3\xa9"), RuleMalformed, hold},
		{"DEL byte", record("1.2.3\x7f"), RuleMalformed, hold},
		{"wrong magic", append([]byte("WBV2\x05"), "1.2.3"...), RuleMalformed, hold},
		{"not in the version form", record("one.two"), RuleForm, hold},
		{"other version", record("1.2.4"), RuleMismatch, hold},
		{"ends early", []byte("WBV1\x05" + "1.2"), RuleTimeout, hangUp},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn := peer(t, c.send, c.next)
			_, err := Exchange(context.Background(), nil, conn, "1.2.3")
			wantRule(t, err, c.rule)
		})
	}
}

// TestExchangeSilentPeer: a peer that never sends gets RuleTimeout, at
// the exchange's deadline, shortened here.
func TestExchangeSilentPeer(t *testing.T) {
	old := exchangeTimeout
	exchangeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { exchangeTimeout = old })
	conn := peer(t, nil, hold)
	log, buf := testLog()
	start := time.Now()
	_, err := Exchange(context.Background(), log, conn, "1.2.3")
	r := wantRule(t, err, RuleTimeout)
	if time.Since(start) > Timeout {
		t.Errorf("Exchange took %v, past its deadline", time.Since(start))
	}
	if r.PeerLabel() != noneLabel {
		t.Errorf("PeerLabel = %s, want %s", r.PeerLabel(), noneLabel)
	}
	if !strings.Contains(buf.String(), `"rule":"peer-version-timeout"`) {
		t.Errorf("log = %s, want the rule", buf.String())
	}
}

// TestExchangeCancel: a cancelled context ends the exchange at once,
// with the context's cause and no Refusal: the peer didn't time out.
func TestExchangeCancel(t *testing.T) {
	conn := peer(t, nil, hold)
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("the caller gave up")
	go func() { time.Sleep(50 * time.Millisecond); cancel(cause) }()
	_, err := Exchange(ctx, nil, conn, "1.2.3")
	if !errors.Is(err, cause) {
		t.Errorf("Exchange = %v, want the context's cause", err)
	}
	if _, ok := errors.AsType[*Refusal](err); ok {
		t.Errorf("Exchange = %v, want no Refusal", err)
	}
}

// TestExchangeContextDeadline: a context deadline sooner than Timeout
// ends the exchange with context.DeadlineExceeded, not RuleTimeout.
func TestExchangeContextDeadline(t *testing.T) {
	conn := peer(t, nil, hold)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Exchange(ctx, nil, conn, "1.2.3")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Exchange = %v, want context.DeadlineExceeded", err)
	}
}

// countingConn counts the bytes read from a connection.
type countingConn struct {
	net.Conn
	mu sync.Mutex
	n  int
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	c.n += n
	c.mu.Unlock()
	return n, err
}

// TestExchangeStopsAtBound: a peer that keeps sending is read only up to
// its record, never past MaxRecordLen bytes (SEC13-bounded-resources).
func TestExchangeStopsAtBound(t *testing.T) {
	for _, c := range []struct {
		name string
		send []byte
		rule string
		max  int
	}{
		{"garbage", nil, RuleMalformed, headerLen},
		{"64 bytes, then more", append([]byte(Magic), 64), RuleForm, MaxRecordLen},
		{"record then more", record("1.2.3"), "", len(record("1.2.3"))},
	} {
		t.Run(c.name, func(t *testing.T) {
			cc := &countingConn{Conn: peer(t, c.send, keepSending)}
			_, err := Exchange(context.Background(), nil, cc, "1.2.3")
			if c.rule == "" && err != nil {
				t.Fatalf("Exchange: %v", err)
			}
			if c.rule != "" {
				wantRule(t, err, c.rule)
			}
			cc.mu.Lock()
			defer cc.mu.Unlock()
			if cc.n > c.max {
				t.Errorf("read %d bytes, want at most %d", cc.n, c.max)
			}
		})
	}
}

// TestRefusalLogEscapes: the log holds the peer's version escaped and cut
// to 64 bytes.
func TestRefusalLogEscapes(t *testing.T) {
	bad := strings.Repeat("x", 60) + "\x01\x02yz"
	conn := peer(t, record(bad), hold)
	log, buf := testLog()
	_, err := Exchange(context.Background(), log, conn, "1.2.3")
	r := wantRule(t, err, RuleMalformed)
	want := `"` + strings.Repeat("x", 60) + `\x01\x02yz"`
	if r.PeerLabel() != want {
		t.Errorf("PeerLabel = %s, want %s", r.PeerLabel(), want)
	}
	if strings.Contains(buf.String(), "\x01") || !strings.Contains(buf.String(), `\\x01\\x02yz`) {
		t.Errorf("log = %s, want the version escaped", buf.String())
	}
	if !strings.Contains(buf.String(), `"own_version":"\"1.2.3\""`) {
		t.Errorf("log = %s, want the own version", buf.String())
	}
}

func TestRecord(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []byte
	}{
		{"1.2.3+abc", record("1.2.3+abc")},
		{"", record("")},
		{strings.Repeat("a", 65), record("")},
		{"1.2.3\x00", record("")},
		{strings.Repeat("a", 64), record(strings.Repeat("a", 64))},
	} {
		if got := Record(c.in); !bytes.Equal(got, c.want) {
			t.Errorf("Record(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCheckRecord(t *testing.T) {
	if peer, err := CheckRecord(nil, "1.2.3", Record("1.2.3")); err != nil || peer != "1.2.3" {
		t.Fatalf("CheckRecord of its own record = %q, %v", peer, err)
	}
	for _, c := range []struct {
		name string
		rec  []byte
		rule string
	}{
		{"empty datagram", nil, RuleMalformed},
		{"cut short", record("1.2.3")[:6], RuleMalformed},
		{"bytes after", append(record("1.2.3"), 0), RuleMalformed},
		{"over the bound", append(record(strings.Repeat("1", 64)), 'x'), RuleMalformed},
		{"unstamped", record(""), RuleUnstamped},
		{"mismatch", record("1.2.4"), RuleMismatch},
	} {
		t.Run(c.name, func(t *testing.T) {
			log, buf := testLog()
			_, err := CheckRecord(log, "1.2.3", c.rec)
			wantRule(t, err, c.rule)
			if !strings.Contains(buf.String(), `"rule":"`+c.rule+`"`) {
				t.Errorf("log = %s, want the rule", buf.String())
			}
		})
	}
}

// boundedReader counts what is read from it.
type boundedReader struct {
	r io.Reader
	n int
}

func (b *boundedReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.n += n
	return n, err
}

// FuzzVersionRecord feeds the record check arbitrary bytes, as a
// compromised wb-netd could send on its channels (C11-host-daemons,
// "Fuzzing"). Properties: it never panics, it reads no more than
// MaxRecordLen bytes, and an accepted record holds a version of 1 to 64
// printable ASCII bytes equal to its own.
func FuzzVersionRecord(f *testing.F) {
	f.Add("1.2.3", Record("1.2.3"))
	f.Add("1.2.3", Record(""))
	f.Add("1.2.3", []byte("WBV1\xff"))
	f.Add("", Record(""))
	f.Add("1.2.3-rc.1+abc", append(Record("1.2.3-rc.1+abc"), "trailing"...))
	f.Fuzz(func(t *testing.T, own string, data []byte) {
		br := &boundedReader{r: bytes.NewReader(data)}
		streamPeer, streamRefusal := readRecord(br, RuleTimeout)
		if br.n > MaxRecordLen {
			t.Fatalf("read %d bytes, over %d", br.n, MaxRecordLen)
		}
		if streamRefusal == nil && (len(streamPeer) > 64 || !printable([]byte(streamPeer))) {
			t.Fatalf("stream read gave version %q", streamPeer)
		}
		peer, err := CheckRecord(slog.New(slog.DiscardHandler), own, data)
		if err != nil {
			var r *Refusal
			if !errors.As(err, &r) || r.Rule == "" {
				t.Fatalf("refusal without a rule: %v", err)
			}
			return
		}
		if peer != own || len(peer) < 1 || len(peer) > 64 || !printable([]byte(peer)) {
			t.Fatalf("accepted %q with own %q", peer, own)
		}
	})
}
