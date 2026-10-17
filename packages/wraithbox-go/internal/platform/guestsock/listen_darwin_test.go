package guestsock

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestWrapAcceptedNonVsockPeer: a descriptor whose peer isn't a vsock
// address gets CID 0, which isn't the host's, so Accept closes it. A
// vsock peer keeps its CID and port.
func TestWrapAcceptedNonVsockPeer(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[1])
	sa, err := unix.Getpeername(fds[0])
	if err != nil {
		t.Fatal(err)
	}
	conn, peer, err := wrapAccepted(fds[0], sa, Port)
	if err != nil {
		t.Fatalf("wrapAccepted: %v", err)
	}
	defer conn.Close()
	if peer.CID != 0 {
		t.Fatalf("peer %+v, want CID 0 for an AF_UNIX peer", peer)
	}
	if got := conn.RemoteAddr().String(); got != "0:0" {
		t.Fatalf("RemoteAddr = %q, want 0:0", got)
	}

	fds2, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds2[1])
	conn2, peer2, err := wrapAccepted(fds2[0], &unix.SockaddrVM{CID: HostCID, Port: 50000}, Port)
	if err != nil {
		t.Fatalf("wrapAccepted: %v", err)
	}
	defer conn2.Close()
	if peer2 != (Peer{CID: HostCID, Port: 50000}) {
		t.Fatalf("peer %+v, want the host's CID and port", peer2)
	}
}
