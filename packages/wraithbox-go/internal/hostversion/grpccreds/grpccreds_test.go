package grpccreds

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
)

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

// TestRefusesUncheckedConn: a connection that didn't come through
// localipc is refused with RulePeerUnchecked and closed.
func TestRefusesUncheckedConn(t *testing.T) {
	var buf syncBuffer
	creds := New(slog.New(slog.NewJSONHandler(&buf, nil)), "1.2.3")
	a, b := net.Pipe()
	defer b.Close()
	if _, _, err := creds.ServerHandshake(a); err == nil || !strings.Contains(err.Error(), RulePeerUnchecked) {
		t.Errorf("ServerHandshake on a pipe: %v, want %s", err, RulePeerUnchecked)
	}
	if _, err := a.Write([]byte("x")); err == nil {
		t.Error("the refused connection is still open")
	}
	if !strings.Contains(buf.String(), `"rule":"ipc-peer-unchecked"`) {
		t.Errorf("log = %s, want the rule", buf.String())
	}
}
