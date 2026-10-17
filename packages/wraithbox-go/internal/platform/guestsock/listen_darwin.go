package guestsock

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// listenBacklog is the listen queue. A self-CID connect takes a slot
// until wb-guestd's accept fails on it (#115 item 1), so the queue holds
// more than the host's own few connections.
const listenBacklog = 128

// bindPort makes an AF_VSOCK stream socket bound to port on every CID,
// listening, non-blocking and close-on-exec (X18-vsock-handoff, answer 1).
func bindPort(port uint32) (socket, error) {
	// darwin has no SOCK_CLOEXEC. Holding ForkLock until the socket is
	// close-on-exec keeps a fork in another goroutine from inheriting it.
	syscall.ForkLock.RLock()
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err == nil {
		unix.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, os.NewSyscallError("socket", err)
	}
	if err := setUp(fd, port); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock-listener:%d", port))
	rc, err := f.SyscallConn()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &vsockListener{f: f, rc: rc, port: port}, nil
}

// setUp binds, listens, and makes fd non-blocking, so that os.NewFile
// puts it in the runtime poller and Close wakes a waiting accept.
func setUp(fd int, port uint32) error {
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		return os.NewSyscallError("bind", err)
	}
	if err := unix.Listen(fd, listenBacklog); err != nil {
		return os.NewSyscallError("listen", err)
	}
	return os.NewSyscallError("setnonblock", unix.SetNonblock(fd, true))
}

type vsockListener struct {
	f    *os.File
	rc   syscall.RawConn
	port uint32
}

func (v *vsockListener) accept() (net.Conn, Peer, error) {
	var (
		nfd  int
		sa   unix.Sockaddr
		aerr error
	)
	err := v.rc.Read(func(fd uintptr) bool {
		syscall.ForkLock.RLock()
		nfd, sa, aerr = unix.Accept(int(fd))
		if aerr == nil {
			unix.CloseOnExec(nfd)
		}
		syscall.ForkLock.RUnlock()
		return !errors.Is(aerr, unix.EAGAIN)
	})
	if err != nil {
		return nil, Peer{}, err
	}
	if aerr != nil {
		return nil, Peer{}, os.NewSyscallError("accept", aerr)
	}
	return wrapAccepted(nfd, sa, v.port)
}

// wrapAccepted turns an accepted descriptor into a net.Conn. A peer that
// isn't a vsock address gets CID 0 (VMADDR_CID_HYPERVISOR), which is not
// the host's, so Accept closes it.
func wrapAccepted(nfd int, sa unix.Sockaddr, port uint32) (net.Conn, Peer, error) {
	var peer Peer
	if vm, ok := sa.(*unix.SockaddrVM); ok {
		peer = Peer{CID: vm.CID, Port: vm.Port}
	}
	if err := unix.SetNonblock(nfd, true); err != nil {
		_ = unix.Close(nfd)
		return nil, Peer{}, os.NewSyscallError("setnonblock", err)
	}
	f := os.NewFile(uintptr(nfd), fmt.Sprintf("vsock:%d:%d", peer.CID, peer.Port))
	return &vsockConn{
		File:   f,
		local:  vsockAddr{cid: unix.VMADDR_CID_ANY, port: port},
		remote: vsockAddr{cid: peer.CID, port: peer.Port},
	}, peer, nil
}

func (v *vsockListener) close() error { return v.f.Close() }

func (v *vsockListener) addr() net.Addr {
	return vsockAddr{cid: unix.VMADDR_CID_ANY, port: v.port}
}

// vsockConn is a net.Conn on an accepted vsock socket. The socket is
// non-blocking in the runtime poller, so deadlines work.
type vsockConn struct {
	*os.File
	local, remote vsockAddr
}

func (c *vsockConn) LocalAddr() net.Addr  { return c.local }
func (c *vsockConn) RemoteAddr() net.Addr { return c.remote }

type vsockAddr struct{ cid, port uint32 }

func (vsockAddr) Network() string { return "vsock" }

func (a vsockAddr) String() string { return fmt.Sprintf("%d:%d", a.cid, a.port) }
