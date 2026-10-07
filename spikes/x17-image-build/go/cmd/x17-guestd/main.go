// x17-guestd is the wb-guestd stand-in for X17-image-build. Throwaway.
//
// A root LaunchDaemon in the guest. It listens on vsock port 1000 (below
// 1024, so only root can bind it) and answers one JSON request per line:
//
//	{"op":"hello"}                         pid, uid, boot time, daemon start
//	{"op":"run","script":"...","timeout":s} /bin/sh -c script as root
//	{"op":"put","path":"...","mode":"0644","b64":"..."}
//	{"op":"shutdown"}                      answer, then shutdown -h now
//
// The host is the only peer that can reach the port, and in the spike the
// host is the build tool, so there is no authentication.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

const port = 1000

var started = time.Now()

type req struct {
	Op      string `json:"op"`
	Script  string `json:"script,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
	Path    string `json:"path,omitempty"`
	Mode    string `json:"mode,omitempty"`
	B64     string `json:"b64,omitempty"`
}

func bootTime() (time.Time, error) {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(tv.Sec, int64(tv.Usec)*1000), nil
}

func handle(r req) map[string]any {
	t0 := time.Now()
	out := map[string]any{"op": r.Op}
	switch r.Op {
	case "hello":
		bt, err := bootTime()
		out["pid"] = os.Getpid()
		out["uid"] = os.Getuid()
		out["now"] = time.Now().Format(time.RFC3339Nano)
		if err == nil {
			out["bootTime"] = bt.Format(time.RFC3339Nano)
			out["daemonStartAfterBootSec"] = started.Sub(bt).Seconds()
			out["uptimeSec"] = time.Since(bt).Seconds()
		} else {
			out["bootTimeErr"] = err.Error()
		}
	case "run":
		to := r.Timeout
		if to <= 0 {
			to = 300
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(to)*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", r.Script)
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin", "HOME=/var/root", "LANG=en_US.UTF-8"}
		var so, se bytes.Buffer
		cmd.Stdout, cmd.Stderr = &so, &se
		err := cmd.Run()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			out["error"] = err.Error()
			code = -1
		}
		out["exit"] = code
		out["out"] = so.String()
		out["err"] = se.String()
	case "put":
		data, err := base64.StdEncoding.DecodeString(r.B64)
		if err != nil {
			out["error"] = err.Error()
			break
		}
		mode, err := strconv.ParseUint(r.Mode, 8, 32)
		if err != nil {
			out["error"] = err.Error()
			break
		}
		if err := os.WriteFile(r.Path, data, os.FileMode(mode)); err != nil {
			out["error"] = err.Error()
			break
		}
		if err := os.Chmod(r.Path, os.FileMode(mode)); err != nil {
			out["error"] = err.Error()
		}
		out["bytes"] = len(data)
	case "shutdown":
		go func() {
			time.Sleep(200 * time.Millisecond)
			if err := exec.Command("/sbin/shutdown", "-h", "now").Run(); err != nil {
				log.Printf("shutdown: %v", err)
			}
		}()
	default:
		out["error"] = "unknown op"
	}
	out["ms"] = float64(time.Since(t0).Microseconds()) / 1000
	return out
}

func serve(c net.Conn) {
	defer c.Close()
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 1<<20), 256<<20)
	enc := json.NewEncoder(c)
	for sc.Scan() {
		var r req
		var resp map[string]any
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			resp = map[string]any{"error": "bad request: " + err.Error()}
		} else {
			log.Printf("request op=%s", r.Op)
			resp = handle(r)
		}
		if err := enc.Encode(resp); err != nil {
			log.Printf("write: %v", err)
			return
		}
	}
}

type conn struct{ *os.File }

func (conn) LocalAddr() net.Addr  { return nil }
func (conn) RemoteAddr() net.Addr { return nil }

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	bt, _ := bootTime()
	log.Printf("start pid %d uid %d, %.2fs after boot", os.Getpid(), os.Getuid(), started.Sub(bt).Seconds())
	var fd int
	var err error
	// The vsock driver may not be ready the moment launchd starts us.
	for i := 0; ; i++ {
		fd, err = unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
		if err == nil {
			if err = unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err == nil {
				err = unix.Listen(fd, 16)
			}
			if err == nil {
				break
			}
			unix.Close(fd)
		}
		log.Printf("listen try %d: %v", i, err)
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("listening on vsock port %d, %.2fs after boot", port, time.Since(bt).Seconds())
	for {
		nfd, _, err := unix.Accept(fd)
		if err != nil {
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.ECONNABORTED) {
				continue
			}
			log.Fatalf("accept: %v", err)
		}
		f := os.NewFile(uintptr(nfd), fmt.Sprintf("vsock-%d", nfd))
		go serve(conn{f})
	}
}
