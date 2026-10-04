// Package probes runs checks inside a (possibly) sandboxed process and
// reports which operations work and which are denied.
package probes

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
)

// Result is one probe outcome.
type Result struct {
	Name string
	OK   bool
	Err  string
}

func (r Result) String() string {
	s := "DENIED"
	if r.OK {
		s = "ok"
	}
	if r.Err != "" {
		return fmt.Sprintf("%-34s %-6s %s", r.Name, s, r.Err)
	}
	return fmt.Sprintf("%-34s %s", r.Name, s)
}

func res(name string, err error) Result {
	if err != nil {
		return Result{Name: name, Err: err.Error()}
	}
	return Result{Name: name, OK: true}
}

// Env is what the harness tells the probe about the outside world.
type Env struct {
	TCPAddr    string // a host TCP listener, loopback
	UnixPath   string // a Unix socket listener path
	ReadFile   string // a readable file outside any allowed dir
	WriteDir   string // a writable dir outside any allowed dir
	StateDir   string // the daemon's own state dir (allowed for hostd)
	RemoteAddr string // an internet address, e.g. 1.1.1.1:443
	RemoteName string // a DNS name, e.g. example.com
}

// Runtime checks the Go runtime keeps working.
func Runtime() []Result {
	var out []Result
	// goroutines, channels, timers, GC, preemption of a tight loop.
	var wg sync.WaitGroup
	sum := make([]int, 64)
	for i := range 64 {
		wg.Go(func() {
			b := make([][]byte, 0)
			for j := range 2000 {
				b = append(b, make([]byte, 1024+j))
			}
			sum[i] = len(b)
		})
	}
	wg.Wait()
	runtime.GC()
	debug.FreeOSMemory()
	out = append(out, res("runtime: goroutines+GC+FreeOSMemory", nil))

	done := make(chan struct{})
	go func() {
		x := 0
		for i := 0; i < 2_000_000_000; i++ {
			x += i
		}
		_ = x
		close(done)
	}()
	t := time.NewTimer(5 * time.Second)
	select {
	case <-done:
		out = append(out, res("runtime: async preemption+timer", nil))
	case <-t.C:
		out = append(out, res("runtime: async preemption+timer", fmt.Errorf("timeout")))
	}
	out = append(out, res(fmt.Sprintf("runtime: NumCPU=%d GOMAXPROCS=%d", runtime.NumCPU(), runtime.GOMAXPROCS(0)), nil))

	b := make([]byte, 32)
	_, err := rand.Read(b)
	out = append(out, res("crypto/rand", err))
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err == nil {
		h := sha256.Sum256([]byte("x"))
		_, err = ecdsa.SignASN1(rand.Reader, k, h[:])
	}
	out = append(out, res("crypto ecdsa sign", err))

	_, err = time.LoadLocation("Europe/Amsterdam")
	out = append(out, res("time.LoadLocation (zoneinfo read)", err))
	out = append(out, res("time.Local="+time.Local.String(), nil))

	_, err = os.Hostname()
	out = append(out, res("os.Hostname", err))
	_, err = os.Getwd()
	out = append(out, res("os.Getwd", err))
	return out
}

func readFile(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 16)
	_, err = f.Read(buf)
	return err
}

func writeFile(dir string) error {
	p := filepath.Join(dir, fmt.Sprintf("x23-probe-%d", os.Getpid()))
	err := os.WriteFile(p, []byte("x"), 0o600)
	if err == nil {
		_ = os.Remove(p)
	}
	return err
}

// Filesystem checks reads and writes.
func Filesystem(e Env) []Result {
	var out []Result
	out = append(out, res("fs: read /etc/passwd", readFile("/etc/passwd")))
	// The sandbox applies to the whole process: every OS thread, including
	// the ones the runtime started before sandbox_init, is confined.
	var mu sync.Mutex
	var wg sync.WaitGroup
	allowed := 0
	for range 32 {
		wg.Go(func() {
			runtime.LockOSThread() // a thread of its own; not unlocked, so it exits after
			if readFile("/etc/passwd") == nil {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	var terr error
	if allowed > 0 {
		terr = nil
	} else {
		terr = fmt.Errorf("denied on all 32 threads")
	}
	out = append(out, res(fmt.Sprintf("fs: read /etc/passwd on 32 locked threads (%d allowed)", allowed), terr))
	out = append(out, res("fs: read "+e.ReadFile, readFile(e.ReadFile)))
	out = append(out, res("fs: write "+e.WriteDir, writeFile(e.WriteDir)))
	out = append(out, res("fs: write /tmp", writeFile("/tmp")))
	_, err := os.ReadDir(os.Getenv("HOME"))
	out = append(out, res("fs: readdir $HOME", err))
	_, err = os.Stat("/etc/passwd")
	out = append(out, res("fs: stat /etc/passwd", err))
	if e.StateDir != "" {
		out = append(out, res("fs: read state "+e.StateDir+"/state.txt", readFile(filepath.Join(e.StateDir, "state.txt"))))
		out = append(out, res("fs: write state "+e.StateDir, writeFile(e.StateDir)))
	}
	return out
}

// Network checks outbound and inbound network.
func Network(e Env) []Result {
	var out []Result
	dial := func(name, netw, addr string) {
		c, err := net.DialTimeout(netw, addr, 3*time.Second)
		if err == nil {
			c.Close()
		}
		out = append(out, res(name, err))
	}
	dial("net: tcp connect loopback "+e.TCPAddr, "tcp", e.TCPAddr)
	if e.RemoteAddr != "" {
		dial("net: tcp connect "+e.RemoteAddr, "tcp", e.RemoteAddr)
	}
	dial("net: udp connect 1.1.1.1:53", "udp", "1.1.1.1:53")
	if e.UnixPath != "" {
		dial("net: unix connect "+filepath.Base(e.UnixPath), "unix", e.UnixPath)
	}
	if e.RemoteName != "" {
		_, err := net.LookupHost(e.RemoteName)
		out = append(out, res("net: DNS lookup "+e.RemoteName, err))
		if err == nil {
			c, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", e.RemoteName+":443", nil)
			if err == nil {
				c.Close()
			}
			out = append(out, res("net: TLS to "+e.RemoteName+" (system roots)", err))
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		l.Close()
	}
	out = append(out, res("net: tcp listen loopback", err))
	// socketpair is needed to hand streams to wb-proxyd.
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.Close(fds[0])
		syscall.Close(fds[1])
	}
	out = append(out, res("net: socketpair", err))
	return out
}

// Process checks exec and signals.
func Process() []Result {
	var out []Result
	err := exec.Command("/bin/echo", "hi").Run()
	out = append(out, res("proc: exec /bin/echo", err))
	err = syscall.Kill(1, 0)
	out = append(out, res("proc: signal 0 to launchd (pid 1)", err))
	err = syscall.Kill(os.Getppid(), 0)
	out = append(out, res("proc: signal 0 to parent", err))
	return out
}
