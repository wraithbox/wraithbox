package handoff

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// failConn is a Conn whose sends fail, as a full socket buffer does.
type failConn struct {
	mu     sync.Mutex
	closed bool
	sends  int
}

func (c *failConn) Send([]byte, syscall.Conn) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends++
	return syscall.ENOBUFS
}

func (c *failConn) Recv() (*fdchan.Message, error) { return nil, io.EOF }

func (c *failConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

type nopHandler struct{}

func (nopHandler) HandleStream(*Stream) {}

type nopWildcard struct{}

func (nopWildcard) RecordWildcardName(context.Context, string, dnsname.Name) error { return nil }

// TestResetSendFails resets a stream whose StreamReset can't be sent.
// The hand-off socket closes, with every other stream on it, and the
// failure is logged with stream-reset-failed (S07-05-stream-hand-off,
// "Resets").
func TestResetSendFails(t *testing.T) {
	var logs bytes.Buffer
	r, err := NewReceiver(Config{
		Handler: nopHandler{}, Wildcard: nopWildcard{},
		Log: slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	conn := &failConn{}
	st := &vmState{slot: "work", copy: newMapping()}
	sock := newSocket(r, st, 1, conn)
	st.sock = sock
	streams := make([]*Stream, 2)
	peers := make([]net.Conn, 2)
	for i := range streams {
		var c net.Conn
		c, peers[i] = net.Pipe()
		streams[i] = &Stream{VM: "work", Generation: 1, ID: uint64(i + 1), Conn: c, sock: sock}
		sock.streams[streams[i]] = struct{}{}
	}

	streams[0].Reset()

	if !conn.closed || conn.sends != 1 {
		t.Fatalf("hand-off socket closed %v after %d sends, want closed after 1", conn.closed, conn.sends)
	}
	for i, p := range peers {
		if _, err := p.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Fatalf("stream %d: read = %v, want EOF", i+1, err)
		}
	}
	if st.sock != nil || len(sock.streams) != 0 {
		t.Fatalf("socket still held: %v, %d streams", st.sock != nil, len(sock.streams))
	}
	for _, want := range []string{`"rule":"stream-reset-failed"`, `"stream_id":1`, `"vm":"work"`} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("log lacks %s:\n%s", want, logs.String())
		}
	}
	streams[1].Reset()
	if conn.sends != 1 {
		t.Fatalf("a stream ended with its socket sent a StreamReset")
	}
}

// TestNewReceiverConfig refuses a config without a handler, without a
// wildcard recorder, or with a cap below one.
func TestNewReceiverConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"no handler", Config{Wildcard: nopWildcard{}}},
		{"no wildcard recorder", Config{Handler: nopHandler{}}},
		{"a stream cap of 0", Config{Handler: nopHandler{}, Wildcard: nopWildcard{}, Caps: Caps{Streams: 0, Pending: 1}}},
		{"a pending cap below 0", Config{Handler: nopHandler{}, Wildcard: nopWildcard{}, Caps: Caps{Streams: 1, Pending: -1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewReceiver(tc.cfg); err == nil {
				t.Fatal("NewReceiver accepted the config")
			}
		})
	}
}

// recordSender keeps the records it was sent.
type recordSender struct{ records [][]byte }

func (s *recordSender) Send(b []byte, fd syscall.Conn) error {
	if fd != nil {
		return errors.New("a descriptor")
	}
	s.records = append(s.records, b)
	return nil
}

// TestAccountingChannel sends a wildcard name, which wb-hostd's decoder
// takes from wb-proxyd.
func TestAccountingChannel(t *testing.T) {
	s := &recordSender{}
	if err := (AccountingChannel{Ch: s}).RecordWildcardName(t.Context(), "work", mustName(t, "x.wild.example.com")); err != nil {
		t.Fatal(err)
	}
	if len(s.records) != 1 {
		t.Fatalf("%d records sent", len(s.records))
	}
	rec, ref := egressmsg.DecodeAccountingRecord(s.records[0], false, egressmsg.SenderProxyd)
	if ref != nil || rec.Kind != egressmsg.KindProxydWildcardName || rec.VM != "work" || rec.Name.String() != "x.wild.example.com" {
		t.Fatalf("record = %+v, %v", rec, ref)
	}
}
