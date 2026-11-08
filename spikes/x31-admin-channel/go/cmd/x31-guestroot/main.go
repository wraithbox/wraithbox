// x31-guestroot: the wb-guestroot stand-in for X31-admin-channel. Throwaway.
//
// A root LaunchDaemon (KeepAlive) in the guest, apart from wb-guestd. At its
// start it runs the update at start of S06-12-guest-agent for itself only
// (the product's code, copied into ../../gu, with SelfName wb-guestroot),
// unless -no-update. Then it binds AF_VSOCK on -port (default 1022, below
// 1024), and for each connection from the host's CID (2) writes one banner
// line and relays /bin/zsh -l, started as root on a new PTY, until the shell
// exits or the host closes. A connection from any other CID is logged and
// closed. Every event is one JSON line on stderr.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"x31/gu"
)

// version is set with -ldflags -X main.version=...
var version = "0.0.0-x31.0"

func ev(kv map[string]any) {
	kv["t"] = float64(time.Now().UnixMicro()) / 1e6
	kv["pid"] = os.Getpid()
	kv["prog"] = "guestroot"
	kv["version"] = version
	b, _ := json.Marshal(kv)
	fmt.Fprintln(os.Stderr, string(b))
}

func main() {
	port := flag.Uint("port", 1022, "vsock port")
	noUpdate := flag.Bool("no-update", false, "skip the update at start")
	flag.Parse()
	ev(map[string]any{"ev": "start", "uid": os.Getuid(), "port": *port})

	if !*noUpdate {
		gu.SelfName = "wb-guestroot"
		log := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("prog", "guestroot", "pid", os.Getpid())
		if err := gu.AtStart(context.Background(), gu.Config{Own: version, Log: log}); err != nil {
			ev(map[string]any{"ev": "update-exec-failed", "err": err.Error()})
			os.Exit(1)
		}
		ev(map[string]any{"ev": "update-done"})
	}

	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		ev(map[string]any{"ev": "socket-failed", "err": err.Error()})
		os.Exit(1)
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: uint32(*port)}); err != nil {
		ev(map[string]any{"ev": "bind-failed", "err": err.Error()})
		os.Exit(1)
	}
	if err := unix.Listen(fd, 4); err != nil {
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
		v, ok := sa.(*unix.SockaddrVM)
		if !ok || v.CID != unix.VMADDR_CID_HOST {
			peer := fmt.Sprintf("%v", sa)
			if ok {
				peer = fmt.Sprintf("%d:%d", v.CID, v.Port)
			}
			_, _ = unix.Write(nfd, []byte("refused: not the host\n"))
			_ = unix.Close(nfd)
			ev(map[string]any{"ev": "refused", "rule": "admin-peer-not-host", "peer": peer})
			continue
		}
		ev(map[string]any{"ev": "accepted", "peer": fmt.Sprintf("%d:%d", v.CID, v.Port)})
		go serve(nfd)
	}
}

func serve(nfd int) {
	conn := os.NewFile(uintptr(nfd), "vsock")
	defer conn.Close()
	fmt.Fprintf(conn, "guestroot pid=%d uid=%d version=%s\n", os.Getpid(), os.Getuid(), version)
	cmd := exec.Command("/bin/zsh", "-l")
	cmd.Env = []string{"HOME=/var/root", "USER=root", "LOGNAME=root", "SHELL=/bin/zsh",
		"TERM=xterm-256color", "PATH=/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin", "LANG=en_US.UTF-8"}
	cmd.Dir = "/var/root"
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 160})
	if err != nil {
		fmt.Fprintf(conn, "pty start failed: %v\n", err)
		ev(map[string]any{"ev": "pty-failed", "err": err.Error()})
		return
	}
	ev(map[string]any{"ev": "shell-started", "shell_pid": cmd.Process.Pid})
	go func() { _, _ = io.Copy(ptmx, conn); _ = cmd.Process.Signal(unix.SIGHUP) }()
	_, _ = io.Copy(conn, ptmx)
	err = cmd.Wait()
	_ = ptmx.Close()
	ev(map[string]any{"ev": "shell-ended", "err": fmt.Sprint(err)})
}
