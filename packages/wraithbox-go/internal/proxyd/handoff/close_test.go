package handoff

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// tableFullConn is a Conn whose reads fail with fdchan-fd-table-full:
// the same refusal each time, as fdchan returns after it ended the
// channel, or a new one each time, as for messages it dropped. Its
// second Recv closes again, so a test knows the reader decided on the
// first refusal.
type tableFullConn struct {
	same   *fdchan.Refusal
	closed chan struct{}
	once   sync.Once

	again     chan struct{}
	calls     atomic.Int64
	againOnce sync.Once
}

func (c *tableFullConn) Send([]byte, syscall.Conn) error { return nil }

func (c *tableFullConn) Recv() (*fdchan.Message, error) {
	if c.calls.Add(1) == 2 {
		c.againOnce.Do(func() { close(c.again) })
	}
	if c.same != nil {
		return nil, c.same
	}
	return nil, &fdchan.Refusal{Rule: fdchan.RuleFDTableFull}
}

func (c *tableFullConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

// TestTableFullEndsReads reads a hand-off socket whose reads fail with
// fdchan-fd-table-full. When fdchan ended the channel, the socket ends
// by itself. When it keeps dropping messages, Close still returns.
// Neither is taken as a malformed message from wb-netd.
func TestTableFullEndsReads(t *testing.T) {
	for _, tc := range []struct {
		name string
		conn *tableFullConn
		ends bool
	}{
		{"the channel ended", &tableFullConn{same: &fdchan.Refusal{Rule: fdchan.RuleFDTableFull}}, true},
		{"every message dropped", &tableFullConn{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs syncBuf
			r, err := NewReceiver(Config{
				Handler: nopHandler{}, Wildcard: nopWildcard{},
				Log: slog.New(slog.NewJSONHandler(&logs, nil)), LogWindow: time.Nanosecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			tc.conn.closed = make(chan struct{})
			tc.conn.again = make(chan struct{})
			st := &vmState{slot: "work", copy: newMapping()}
			sock := newSocket(r, st, 1, tc.conn)
			st.sock = sock
			r.vms["work"] = st
			if !sock.run() {
				t.Fatal("run refused an open socket")
			}
			waitClosed(t, tc.conn.again, "the reader didn't read again after the first refusal")
			if tc.ends {
				waitClosed(t, tc.conn.closed, "the socket didn't end")
			}
			done := make(chan struct{})
			go func() { r.Close(); close(done) }()
			waitClosed(t, done, "Close didn't return")
			expectTableFullLogs(t, logs.String())
			if sock.run() {
				t.Fatal("run started a closed socket")
			}
		})
	}
}

// expectTableFullLogs checks that the log has the dropped message and
// no malformed one.
func expectTableFullLogs(t *testing.T, logs string) {
	t.Helper()
	if !strings.Contains(logs, `"rule":"fdchan-fd-table-full"`) {
		t.Fatalf("no dropped message logged:\n%s", logs)
	}
	if strings.Contains(logs, RuleNameReportMalformed) {
		t.Fatalf("a full descriptor table was taken as malformed:\n%s", logs)
	}
}

func waitClosed(t *testing.T, c <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal(msg)
	}
}

// syncBuf is a log buffer that goroutines write to.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// TestCapsDefaults fills each zero cap with its default.
func TestCapsDefaults(t *testing.T) {
	r, err := NewReceiver(Config{Handler: nopHandler{}, Wildcard: nopWildcard{}, Caps: Caps{Streams: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Caps{Streams: 10, Pending: DefaultCaps().Pending}); r.cfg.Caps != want {
		t.Fatalf("caps = %+v, want %+v", r.cfg.Caps, want)
	}
}
