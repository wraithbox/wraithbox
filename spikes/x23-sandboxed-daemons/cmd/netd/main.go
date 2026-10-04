// Command netd stands in for wb-netd: it confines itself, then runs a
// gVisor stack on an inherited datagram socket (fd 3) and hands each
// accepted guest TCP stream to a proxy over an inherited Unix socket
// (fd 4) by passing one end of a fresh socketpair with SCM_RIGHTS.
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"syscall"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"

	"x23/nstack"
	"x23/probes"
	"x23/sbxapply"
)

func main() {
	mech := flag.String("mech", "none", "none, cgo, pure")
	probeOnly := flag.Bool("probe-only", false, "run the probes and exit")
	setProcs := flag.Bool("setprocs", false, "call runtime.GOMAXPROCS(runtime.NumCPU()) before confining")
	profile := flag.String("profile", "", "SBPL profile file")
	var params sbxapply.Params
	flag.Var(&params, "D", "profile parameter key=value")
	var e probes.Env
	flag.StringVar(&e.TCPAddr, "tcp", "127.0.0.1:1", "loopback TCP listener")
	flag.StringVar(&e.UnixPath, "unix", "", "Unix socket listener")
	flag.StringVar(&e.ReadFile, "readfile", "", "file outside allowed dirs")
	flag.StringVar(&e.WriteDir, "writedir", "", "dir outside allowed dirs")
	flag.StringVar(&e.RemoteAddr, "remote", "1.1.1.1:443", "internet TCP address")
	flag.StringVar(&e.RemoteName, "name", "example.com", "DNS name")
	flag.Parse()

	if *setProcs {
		runtime.GOMAXPROCS(runtime.NumCPU()) // turns off the periodic hw.ncpu re-read
	}
	d, err := sbxapply.Apply(*mech, *profile, params)
	if err != nil {
		fmt.Fprintln(os.Stderr, "netd: apply:", err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "netd: mechanism=%s apply=%s\n", *mech, d)
	if *probeOnly {
		all := probes.Runtime()
		all = append(all, probes.Filesystem(e)...)
		all = append(all, probes.Network(e)...)
		all = append(all, probes.Process()...)
		for _, r := range all {
			fmt.Fprintln(os.Stderr, "netd probe:", r)
		}
		return
	}

	nic := os.NewFile(3, "nic")
	proxy, err := net.FileConn(os.NewFile(4, "proxy"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "netd: proxy fd:", err)
		os.Exit(1)
	}
	pu := proxy.(*net.UnixConn)

	s, err := nstack.New(nic, "10.0.0.1", "02:00:00:00:00:01")
	if err != nil {
		fmt.Fprintln(os.Stderr, "netd: stack:", err)
		os.Exit(1)
	}
	l, err := gonet.ListenTCP(s, nstack.Addr("10.0.0.1", 443), ipv4.ProtocolNumber)
	if err != nil {
		fmt.Fprintln(os.Stderr, "netd: listen:", err)
		os.Exit(1)
	}

	all := probes.Runtime()
	all = append(all, probes.Filesystem(e)...)
	all = append(all, probes.Network(e)...)
	all = append(all, probes.Process()...)
	for _, r := range all {
		fmt.Fprintln(os.Stderr, "netd probe:", r)
	}
	fmt.Println("READY")

	for {
		c, err := l.Accept()
		if err != nil {
			fmt.Fprintln(os.Stderr, "netd: accept:", err)
			return
		}
		go handoff(c, pu)
	}
}

// handoff gives the proxy a socketpair end and splices the guest stream
// to the other end.
func handoff(c net.Conn, pu *net.UnixConn) {
	defer c.Close()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "netd: socketpair:", err)
		return
	}
	hdr := []byte(c.LocalAddr().String())
	if _, _, err := pu.WriteMsgUnix(hdr, syscall.UnixRights(fds[1]), nil); err != nil {
		fmt.Fprintln(os.Stderr, "netd: sendmsg:", err)
		syscall.Close(fds[0])
		syscall.Close(fds[1])
		return
	}
	syscall.Close(fds[1])
	pc, err := net.FileConn(os.NewFile(uintptr(fds[0]), "stream"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "netd: fileconn:", err)
		return
	}
	defer pc.Close()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(pc, c)
		pc.(*net.UnixConn).CloseWrite()
		close(done)
	}()
	_, _ = io.Copy(c, pc)
	<-done
}
