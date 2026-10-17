//go:build unix

package guestd_test

import (
	"log/slog"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
)

// socketpair returns both ends of a Unix stream socketpair, the kind of
// descriptor wb-vmd passes for a vsock connection on macOS
// (X18-vsock-handoff, answer 2).
func socketpair(t *testing.T) (host, guest net.Conn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	conns := make([]net.Conn, 2)
	for i, fd := range fds {
		f := os.NewFile(uintptr(fd), "socketpair")
		c, err := net.FileConn(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		conns[i] = c
	}
	return conns[0], conns[1]
}

// oneConn is a net.Listener that returns one connection, then waits
// until it is closed.
type oneConn struct {
	c    chan net.Conn
	done chan struct{}
	once sync.Once
	addr net.Addr
}

func newOneConn(c net.Conn) *oneConn {
	l := &oneConn{c: make(chan net.Conn, 1), done: make(chan struct{}), addr: c.LocalAddr()}
	l.c <- c
	return l
}

func (l *oneConn) Accept() (net.Conn, error) {
	select {
	case c := <-l.c:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *oneConn) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *oneConn) Addr() net.Addr { return l.addr }

// TestHelloRoundTrip: wb-guestd's Hello passes the host's checks over a
// socketpair when both sides have the same version, and is refused when
// they differ.
func TestHelloRoundTrip(t *testing.T) {
	cases := []struct {
		name, guest string
		ok          bool
	}{
		{"same version", "1.2.3-dev+abc", true},
		{"other version", "1.2.4", false},
		{"unstamped guest", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, guest := socketpair(t)
			srv := guestd.NewServer(tc.guest, slog.New(slog.DiscardHandler))
			go func() { _ = srv.Serve(newOneConn(guest)) }()
			defer srv.Stop()

			link, err := guestlink.Hello(t.Context(), host, "1.2.3-dev+abc", slog.New(slog.DiscardHandler))
			if tc.ok {
				if err != nil {
					t.Fatalf("Hello: %v", err)
				}
				defer link.Close()
				if link.Version() != tc.guest {
					t.Fatalf("Version = %q, want %q", link.Version(), tc.guest)
				}
				return
			}
			if err == nil {
				_ = link.Close()
				t.Fatal("Hello accepted another version")
			}
		})
	}
}
