// Package vs wraps darwin AF_VSOCK sockets and passed descriptors as
// net.Conn, using golang.org/x/sys/unix only (no cgo, no raw syscall shim).
// X18-vsock-handoff spike, throwaway.
package vs

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

// Addr is a vsock or "passed descriptor" address.
type Addr struct{ S string }

func (a Addr) Network() string { return "vsock" }
func (a Addr) String() string  { return a.S }

// Conn is a net.Conn on an *os.File. os.NewFile registers a non-blocking
// socket with the runtime poller, so deadlines work.
type Conn struct {
	*os.File
	local, remote Addr
}

func (c *Conn) LocalAddr() net.Addr  { return c.local }
func (c *Conn) RemoteAddr() net.Addr { return c.remote }

func sockName(sa unix.Sockaddr) string {
	switch a := sa.(type) {
	case *unix.SockaddrVM:
		return fmt.Sprintf("vsock:%d:%d", a.CID, a.Port)
	case *unix.SockaddrUnix:
		return "unix:" + a.Name
	case nil:
		return "none"
	default:
		return fmt.Sprintf("%T", sa)
	}
}

// FromFD wraps a socket descriptor (accepted, dialed, or received over
// SCM_RIGHTS) as a net.Conn. It takes ownership of fd.
func FromFD(fd int, name string) (*Conn, error) {
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, fmt.Errorf("setnonblock: %w", err)
	}
	c := &Conn{File: os.NewFile(uintptr(fd), name)}
	if sa, err := unix.Getsockname(fd); err == nil {
		c.local = Addr{sockName(sa)}
	} else {
		c.local = Addr{"getsockname: " + err.Error()}
	}
	if sa, err := unix.Getpeername(fd); err == nil {
		c.remote = Addr{sockName(sa)}
	} else {
		c.remote = Addr{"getpeername: " + err.Error()}
	}
	return c, nil
}

// Describe reports what kind of socket fd is.
func Describe(fd int) map[string]any {
	r := map[string]any{}
	if ty, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE); err == nil {
		r["sockType"] = ty
	}
	if sa, err := unix.Getsockname(fd); err == nil {
		r["local"] = sockName(sa)
	} else {
		r["local"] = "getsockname: " + err.Error()
	}
	if sa, err := unix.Getpeername(fd); err == nil {
		r["peer"] = sockName(sa)
	} else {
		r["peer"] = "getpeername: " + err.Error()
	}
	return r
}

// Listener accepts AF_VSOCK connections.
type Listener struct {
	fd   int
	port uint32
	once sync.Once
}

// Listen binds AF_VSOCK to (VMADDR_CID_ANY, port).
func Listen(port uint32) (*Listener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_VSOCK): %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("bind: %w", err)
	}
	if err := unix.Listen(fd, 128); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("listen: %w", err)
	}
	return &Listener{fd: fd, port: port}, nil
}

// Accept blocks in accept(2) on its own thread.
func (l *Listener) Accept() (net.Conn, error) {
	for {
		nfd, _, err := unix.Accept(l.fd)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.ECONNABORTED) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return FromFD(nfd, "vsock-accepted")
	}
}

func (l *Listener) Close() error {
	var err error
	l.once.Do(func() { err = unix.Close(l.fd) })
	return err
}

func (l *Listener) Addr() net.Addr { return Addr{fmt.Sprintf("vsock:any:%d", l.port)} }

// Dial connects AF_VSOCK to (cid, port).
func Dial(cid, port uint32) (*Conn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_VSOCK): %w", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: cid, Port: port}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("connect: %w", err)
	}
	return FromFD(fd, "vsock-dialed")
}

// LocalCID asks the vsock driver for this machine's CID.
func LocalCID() (int, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return 0, err
	}
	defer unix.Close(fd)
	return unix.IoctlGetInt(fd, unix.IOCTL_VM_SOCKETS_GET_LOCAL_CID)
}

// OneConn is a net.Listener that yields one connection, then blocks until closed.
type OneConn struct {
	c    net.Conn
	mu   sync.Mutex
	done chan struct{}
	used bool
}

func NewOneConn(c net.Conn) *OneConn { return &OneConn{c: c, done: make(chan struct{})} }

func (o *OneConn) Accept() (net.Conn, error) {
	o.mu.Lock()
	if !o.used {
		o.used = true
		o.mu.Unlock()
		return o.c, nil
	}
	o.mu.Unlock()
	<-o.done
	return nil, net.ErrClosed
}

func (o *OneConn) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	select {
	case <-o.done:
	default:
		close(o.done)
	}
	return nil
}

func (o *OneConn) Addr() net.Addr { return o.c.LocalAddr() }

// Codec handles both protobuf messages (the health service) and raw JSON
// bytes (the spike's own x18.Guest service, which has no .proto).
type Codec struct{}

func (Codec) Name() string { return "proto" }

func (Codec) Marshal(v any) ([]byte, error) {
	switch m := v.(type) {
	case proto.Message:
		return proto.Marshal(m)
	case *[]byte:
		return *m, nil
	default:
		return json.Marshal(v)
	}
}

func (Codec) Unmarshal(data []byte, v any) error {
	switch m := v.(type) {
	case proto.Message:
		return proto.Unmarshal(data, m)
	case *[]byte:
		*m = append((*m)[:0], data...)
		return nil
	default:
		return json.Unmarshal(data, v)
	}
}

// Ms is milliseconds since t.
func Ms(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }
