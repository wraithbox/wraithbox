//go:build unix

package guestlink

import (
	"net"
	"os"
	"sync"
	"syscall"
	"testing"

	"google.golang.org/grpc"
)

// socketpair returns both ends of a Unix stream socketpair, the kind of
// descriptor wb-vmd passes for a vsock connection on macOS
// (X18-vsock-handoff, answer 2). Both ends are closed at the end of the
// test.
func socketpair(t testing.TB) (host, guest net.Conn) {
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
		t.Cleanup(func() { _ = c.Close() })
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

// serveOn serves srv on conn until the end of the test.
func serveOn(t testing.TB, srv *grpc.Server, conn net.Conn) {
	t.Helper()
	go func() { _ = srv.Serve(newOneConn(conn)) }()
	t.Cleanup(srv.Stop)
}
