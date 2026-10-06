// x27-guestd: the wb-guestd stand-in for X27-vsock-confinement. Throwaway.
//
// A root LaunchDaemon (KeepAlive) in the guest. Binds AF_VSOCK (CID any,
// port from argv, default 1024), listens, and answers every connection with
// one line naming itself, its pid and uid, then closes it. A bind failure
// is logged and the process exits 1, as a real daemon would, so launchd's
// restart behavior shows. Every event is one JSON line on stderr.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

func ev(kv map[string]any) {
	kv["t"] = float64(time.Now().UnixMicro()) / 1e6
	kv["pid"] = os.Getpid()
	b, _ := json.Marshal(kv)
	fmt.Fprintln(os.Stderr, string(b))
}

func main() {
	port := uint32(1024)
	if len(os.Args) > 1 {
		p, _ := strconv.Atoi(os.Args[1])
		port = uint32(p)
	}
	ev(map[string]any{"ev": "start", "uid": os.Getuid(), "port": port})
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		ev(map[string]any{"ev": "socket-failed", "err": err.Error()})
		os.Exit(1)
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		ev(map[string]any{"ev": "bind-failed", "err": err.Error()})
		os.Exit(1)
	}
	if err := unix.Listen(fd, 16); err != nil {
		ev(map[string]any{"ev": "listen-failed", "err": err.Error()})
		os.Exit(1)
	}
	ev(map[string]any{"ev": "listening"})
	for {
		nfd, sa, err := unix.Accept(fd)
		if err != nil {
			ev(map[string]any{"ev": "accept-failed", "err": err.Error()})
			continue
		}
		peer := ""
		if v, ok := sa.(*unix.SockaddrVM); ok {
			peer = fmt.Sprintf("%d:%d", v.CID, v.Port)
		}
		msg := fmt.Sprintf("guestd pid=%d uid=%d\n", os.Getpid(), os.Getuid())
		_, _ = unix.Write(nfd, []byte(msg))
		unix.Close(nfd)
		ev(map[string]any{"ev": "accepted", "peer": peer})
	}
}
