// x27-probe: what a guest process can do with AF_VSOCK. X27-vsock-confinement,
// throwaway. Every result is one JSON line on stdout.
//
//	x27-probe matrix [label]   socket/bind/listen on a set of ports, bind
//	                           variants on the wb-guestd port, connects to
//	                           the host, the local CID ioctl
//	x27-probe squat <port> <seconds> [label]
//	                           try bind(CID any, port) every millisecond until
//	                           it works or <seconds> pass; on success listen
//	                           and answer every connection as an impostor
//	                           until <seconds> pass
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

var label string

func out(kv map[string]any) {
	kv["uid"] = os.Getuid()
	kv["euid"] = os.Geteuid()
	kv["t"] = float64(time.Now().UnixMicro()) / 1e6
	if label != "" {
		kv["label"] = label
	}
	b, _ := json.Marshal(kv)
	fmt.Println(string(b))
}

func es(err error) string {
	if err == nil {
		return "ok"
	}
	if e, ok := err.(unix.Errno); ok {
		return fmt.Sprintf("%s (errno %d)", unix.ErrnoName(e), int(e))
	}
	return err.Error()
}

// try runs socket, bind and listen on (cid, port) and reports each step.
func try(name string, cid, port uint32, opts []int) {
	r := map[string]any{"test": name, "cid": cid, "port": port}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	r["socket"] = es(err)
	if err != nil {
		out(r)
		return
	}
	defer unix.Close(fd)
	for _, o := range opts {
		r[fmt.Sprintf("setsockopt_%#x", o)] = es(unix.SetsockoptInt(fd, unix.SOL_SOCKET, o, 1))
	}
	err = unix.Bind(fd, &unix.SockaddrVM{CID: cid, Port: port})
	r["bind"] = es(err)
	if err == nil {
		if sa, e := unix.Getsockname(fd); e == nil {
			if v, ok := sa.(*unix.SockaddrVM); ok {
				r["boundAs"] = fmt.Sprintf("%d:%d", v.CID, v.Port)
			}
		}
		r["listen"] = es(unix.Listen(fd, 16))
	}
	out(r)
}

func dial(name string, cid, port uint32) {
	r := map[string]any{"test": name, "cid": cid, "port": port}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	r["socket"] = es(err)
	if err == nil {
		t0 := time.Now()
		err = unix.Connect(fd, &unix.SockaddrVM{CID: cid, Port: port})
		r["connect"] = es(err)
		r["ms"] = float64(time.Since(t0).Microseconds()) / 1000
		if err == nil {
			if sa, e := unix.Getsockname(fd); e == nil {
				if v, ok := sa.(*unix.SockaddrVM); ok {
					r["localAddr"] = fmt.Sprintf("%d:%d", v.CID, v.Port)
				}
			}
		}
		unix.Close(fd)
	}
	out(r)
}

func matrix() {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	r := map[string]any{"test": "local-cid", "socket": es(err)}
	cid := uint32(3)
	if err == nil {
		c, e := unix.IoctlGetInt(fd, unix.IOCTL_VM_SOCKETS_GET_LOCAL_CID)
		r["ioctl"] = es(e)
		if e == nil {
			r["cid"] = c
			cid = uint32(c)
		}
		unix.Close(fd)
	}
	out(r)
	fd, err = unix.Socket(unix.AF_VSOCK, unix.SOCK_DGRAM, 0)
	out(map[string]any{"test": "socket-dgram", "socket": es(err)})
	if err == nil {
		unix.Close(fd)
	}
	for _, p := range []uint32{1, 80, 1023, 1024, 1025, 4096, 50000} {
		try("bind-any", unix.VMADDR_CID_ANY, p, nil)
	}
	// Variants on the wb-guestd port, which matter while it is held.
	try("bind-localcid", cid, 1024, nil)
	try("bind-any-reuse", unix.VMADDR_CID_ANY, 1024, []int{unix.SO_REUSEADDR, unix.SO_REUSEPORT})
	try("bind-localcid-reuse", cid, 1024, []int{unix.SO_REUSEADDR, unix.SO_REUSEPORT})
	try("bind-host-cid", unix.VMADDR_CID_HOST, 1024, nil)
	try("bind-port-any", unix.VMADDR_CID_ANY, unix.VMADDR_PORT_ANY, nil)
	dial("connect-host-listener", unix.VMADDR_CID_HOST, 2048)
	dial("connect-host-nolistener", unix.VMADDR_CID_HOST, 2049)
	dial("connect-self-guestd", cid, 1024)
}

func squat(port uint32, secs float64) {
	t0 := time.Now()
	deadline := t0.Add(time.Duration(secs * float64(time.Second)))
	tries := 0
	lastErr := ""
	fd := -1
	for time.Now().Before(deadline) {
		tries++
		s, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
		if err != nil {
			out(map[string]any{"test": "squat", "socket": es(err)})
			return
		}
		err = unix.Bind(s, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port})
		if err == nil {
			err = unix.Listen(s, 16)
		}
		if err == nil {
			fd = s
			break
		}
		lastErr = es(err)
		unix.Close(s)
		time.Sleep(time.Millisecond)
	}
	if fd < 0 {
		out(map[string]any{"test": "squat", "port": port, "won": false, "tries": tries, "lastErr": lastErr})
		return
	}
	out(map[string]any{"test": "squat", "port": port, "won": true, "tries": tries, "afterMs": float64(time.Since(t0).Microseconds()) / 1000, "lastErr": lastErr})
	go func() {
		time.Sleep(time.Until(deadline))
		out(map[string]any{"test": "squat", "event": "giving up the port"})
		os.Exit(0)
	}()
	for {
		nfd, sa, err := unix.Accept(fd)
		if err != nil {
			continue
		}
		peer := ""
		if v, ok := sa.(*unix.SockaddrVM); ok {
			peer = fmt.Sprintf("%d:%d", v.CID, v.Port)
		}
		_, _ = unix.Write(nfd, []byte(fmt.Sprintf("IMPOSTOR uid=%d pid=%d\n", os.Getuid(), os.Getpid())))
		unix.Close(nfd)
		out(map[string]any{"test": "squat", "event": "answered host as wb-guestd", "peer": peer})
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: x27-probe matrix [label] | squat <port> <seconds> [label]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "matrix":
		if len(os.Args) > 2 {
			label = os.Args[2]
		}
		matrix()
	case "squat":
		p, _ := strconv.Atoi(os.Args[2])
		s, _ := strconv.ParseFloat(os.Args[3], 64)
		if len(os.Args) > 4 {
			label = os.Args[4]
		}
		squat(uint32(p), s)
	default:
		os.Exit(2)
	}
}
