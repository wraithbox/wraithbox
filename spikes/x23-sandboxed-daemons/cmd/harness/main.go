// Command harness plays guest, wb-hostd and wb-proxyd around one netd
// process: it creates the NIC socketpair and the proxy socketpair, starts
// netd with a mechanism, runs a guest gVisor stack on the other NIC end,
// pushes TCP streams through netd to an echoing proxy, and reports
// throughput.
package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"x23/nstack"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness:", err)
		os.Exit(1)
	}
}

func main() {
	mech := flag.String("mech", "pure", "none, cgo, pure, exec")
	bin := flag.String("netd", "bin/netd", "netd binary")
	profile := flag.String("profile", "profiles/netd.sb", "profile")
	streams := flag.Int("streams", 4, "concurrent streams")
	mb := flag.Int("mb", 64, "MiB per stream, each way")
	flag.Parse()

	wd, _ := os.Getwd()
	scratch := filepath.Join(wd, "..", "..", ".scratch")
	must(os.MkdirAll(scratch, 0o700))

	// Listeners that netd must NOT be able to reach.
	tl, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	defer tl.Close()
	go acceptLog("tcp listener", tl)
	up := filepath.Join("../../.scratch", fmt.Sprintf("x23-%d.sock", os.Getpid())) // relative: sun_path is 104 bytes
	ul, err := net.Listen("unix", up)
	must(err)
	defer ul.Close()
	go acceptLog("unix listener", ul)

	nic, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	must(err)
	for _, fd := range nic {
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, 1<<20)
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4<<20)
	}
	px, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	must(err)

	args := []string{"-readfile", filepath.Join(wd, "go.mod"), "-writedir", wd, "-tcp", tl.Addr().String(), "-unix", up}
	absBin, _ := filepath.Abs(*bin)
	absProf, _ := filepath.Abs(*profile)
	var cmd *exec.Cmd
	switch *mech {
	case "exec":
		a := append([]string{"-f", absProf, "-D", "BIN=" + filepath.Dir(absBin), "-D", "EXE=" + absBin, absBin, "-mech", "none"}, args...)
		cmd = exec.Command("/usr/bin/sandbox-exec", a...)
	default:
		a := append([]string{"-mech", *mech, "-profile", absProf, "-D", "BIN=" + filepath.Dir(absBin), "-D", "EXE=" + absBin}, args...)
		cmd = exec.Command(absBin, a...)
	}
	nicChild := os.NewFile(uintptr(nic[1]), "nic-child")
	pxChild := os.NewFile(uintptr(px[1]), "px-child")
	cmd.ExtraFiles = []*os.File{nicChild, pxChild}
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	must(err)
	must(cmd.Start())
	nicChild.Close()
	pxChild.Close()

	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if sc.Text() == "READY" {
				close(ready)
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(30 * time.Second):
		must(fmt.Errorf("netd not ready"))
	}

	// Proxy side: receive fds and echo.
	pxConn, err := net.FileConn(os.NewFile(uintptr(px[0]), "px"))
	must(err)
	go proxy(pxConn.(*net.UnixConn))

	// Guest side.
	gs, err := nstack.New(os.NewFile(uintptr(nic[0]), "nic-guest"), "10.0.0.2", "02:00:00:00:00:02")
	must(err)
	var wg sync.WaitGroup
	start := time.Now()
	errs := make(chan error, *streams)
	for i := range *streams {
		wg.Go(func() {
			errs <- stream(gs, i, *mb)
		})
	}
	wg.Wait()
	el := time.Since(start)
	close(errs)
	ok := true
	for e := range errs {
		if e != nil {
			ok = false
			fmt.Println("stream error:", e)
		}
	}
	total := float64(*streams * *mb * 2)
	fmt.Printf("RESULT mech=%s streams=%d each=%dMiB echoed ok=%v time=%s throughput=%.1f MiB/s (both directions)\n",
		*mech, *streams, *mb, ok, el.Round(time.Millisecond), total/el.Seconds())
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
	_ = os.Remove(up)
}

func acceptLog(name string, l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		fmt.Println("harness: netd reached the", name)
		c.Close()
	}
}

func proxy(u *net.UnixConn) {
	buf := make([]byte, 256)
	oob := make([]byte, syscall.CmsgSpace(4*4))
	for {
		n, oobn, _, _, err := u.ReadMsgUnix(buf, oob)
		if err != nil {
			return
		}
		msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
		if err != nil || len(msgs) != 1 {
			fmt.Println("proxy: bad control message", err)
			continue
		}
		fds, err := syscall.ParseUnixRights(&msgs[0])
		if err != nil || len(fds) != 1 {
			fmt.Println("proxy: bad rights", err)
			continue
		}
		_ = n
		c, err := net.FileConn(os.NewFile(uintptr(fds[0]), "s"))
		if err != nil {
			continue
		}
		go func() {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}()
	}
}

func stream(s *stack.Stack, i, mb int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c, err := gonet.DialContextTCP(ctx, s, nstack.Addr("10.0.0.1", 443), ipv4.ProtocolNumber)
	if err != nil {
		return fmt.Errorf("stream %d dial: %w", i, err)
	}
	defer c.Close()
	chunk := bytes.Repeat([]byte{byte('a' + i)}, 64<<10)
	n := mb << 20
	go func() {
		for sent := 0; sent < n; sent += len(chunk) {
			if _, err := c.Write(chunk); err != nil {
				return
			}
		}
		_ = c.CloseWrite()
	}()
	got, err := io.Copy(io.Discard, c)
	if err != nil {
		return fmt.Errorf("stream %d read: %w", i, err)
	}
	if got != int64(n) {
		return fmt.Errorf("stream %d: echoed %d of %d bytes", i, got, n)
	}
	return nil
}
