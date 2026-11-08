// x31-probe: what a guest process can do to wb-guestroot's port, for
// X31-admin-channel. Throwaway. One JSON line per result on stdout.
//
//	x31-probe squat <port> <seconds>   try to bind the port every millisecond;
//	                                   once bound, listen and answer IMPOSTOR
//	x31-probe connect <cid> <port>     connect from inside the guest and read
//	                                   the first line
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

func out(kv map[string]any) {
	kv["t"] = float64(time.Now().UnixMicro()) / 1e6
	kv["uid"] = os.Getuid()
	kv["euid"] = os.Geteuid()
	b, _ := json.Marshal(kv)
	fmt.Println(string(b))
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: x31-probe squat <port> <seconds> | connect <cid> <port>")
		os.Exit(2)
	}
	a, _ := strconv.Atoi(os.Args[2])
	b, _ := strconv.Atoi(os.Args[3])
	switch os.Args[1] {
	case "squat":
		squat(uint32(a), time.Duration(b)*time.Second)
	case "connect":
		connect(uint32(a), uint32(b))
	}
}

func squat(port uint32, d time.Duration) {
	errs := map[string]int{}
	tries := 0
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
		if err != nil {
			out(map[string]any{"ev": "socket-failed", "err": err.Error()})
			return
		}
		tries++
		err = unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port})
		if err == nil {
			out(map[string]any{"ev": "BOUND", "port": port, "tries": tries})
			_ = unix.Listen(fd, 4)
			for time.Now().Before(end) {
				nfd, _, err := unix.Accept(fd)
				if err == nil {
					_, _ = unix.Write(nfd, []byte(fmt.Sprintf("IMPOSTOR uid=%d\n", os.Getuid())))
					_ = unix.Close(nfd)
				}
			}
			return
		}
		errs[err.Error()]++
		_ = unix.Close(fd)
		time.Sleep(time.Millisecond)
	}
	out(map[string]any{"ev": "squat-done", "port": port, "tries": tries, "errors": errs})
}

func connect(cid, port uint32) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		out(map[string]any{"ev": "socket-failed", "err": err.Error()})
		return
	}
	defer unix.Close(fd)
	err = unix.Connect(fd, &unix.SockaddrVM{CID: cid, Port: port})
	if err != nil {
		out(map[string]any{"ev": "connect-failed", "cid": cid, "port": port, "err": err.Error()})
		return
	}
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 2})
	buf := make([]byte, 256)
	n, err := unix.Read(fd, buf)
	r := map[string]any{"ev": "connected", "cid": cid, "port": port}
	if n > 0 {
		r["first"] = string(buf[:n])
	}
	if err != nil {
		r["read_err"] = err.Error()
	}
	out(r)
}
