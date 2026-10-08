// x06-guestd is the wb-guestd stand-in for X06-guest-xcode. Throwaway.
//
// Derived from x17-guestd (tag spike-x17-image-build). A root LaunchDaemon
// in the guest, on vsock port 1000, one JSON request per line:
//
//	{"op":"hello"}
//	{"op":"run","script":"...","timeout":s}       /bin/sh -c script as root
//	{"op":"put","path":"...","mode":"0644","b64":"..."}
//	{"op":"runas","user":"u","how":"setuid|asuser","pty":true,
//	 "sandbox":"/path/profile.sb","script":"...","timeout":s}
//	{"op":"shutdown"}
//
// runas starts /bin/sh -c script as a non-admin user the way wb-guestd
// would: "setuid" sets the child's user, group and groups directly
// (syscall.Credential) and starts a new POSIX session; "asuser" goes through
// `launchctl asuser <uid>` first, which puts the child in the user's
// per-user launchd domain, and then drops to the user with "dropto". With
// "pty" the child gets a new pseudo-terminal as its controlling terminal.
// With "sandbox" the script runs under sandbox-exec with that profile and
// the parameters HOME and WORK.
//
// The binary is also its own helper: `x06-guestd dropto <uid> <gid>
// <groups,> -- argv...` sets the groups, group and user, then execs argv.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const port = 1000

const maxOut = 256 << 10

var started = time.Now()

type req struct {
	Op      string            `json:"op"`
	Script  string            `json:"script,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
	Path    string            `json:"path,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	B64     string            `json:"b64,omitempty"`
	User    string            `json:"user,omitempty"`
	How     string            `json:"how,omitempty"`
	PTY     bool              `json:"pty,omitempty"`
	Sandbox string            `json:"sandbox,omitempty"`
	Work    string            `json:"work,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func bootTime() (time.Time, error) {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(tv.Sec, int64(tv.Usec)*1000), nil
}

// tailBuf keeps the last maxOut bytes written to it.
type tailBuf struct {
	mu    sync.Mutex
	b     []byte
	total int
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.total += len(p)
	t.b = append(t.b, p...)
	if len(t.b) > maxOut {
		t.b = t.b[len(t.b)-maxOut:]
	}
	return len(p), nil
}

func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.total > len(t.b) {
		return fmt.Sprintf("[%d bytes cut]\n", t.total-len(t.b)) + string(t.b)
	}
	return string(t.b)
}

// openPTY opens a new pseudo-terminal pair the way posix_openpt,
// grantpt, unlockpt and ptsname do on macOS.
func openPTY() (*os.File, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := int(m.Fd())
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		m.Close()
		return nil, nil, fmt.Errorf("grant: %w", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		m.Close()
		return nil, nil, fmt.Errorf("unlock: %w", err)
	}
	name := make([]byte, 128)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		m.Close()
		return nil, nil, fmt.Errorf("name: %w", e)
	}
	n := bytes.IndexByte(name, 0)
	s, err := os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}

type ids struct {
	uid, gid int
	groups   []int
	home     string
}

func lookup(name string) (ids, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return ids{}, err
	}
	var r ids
	r.uid, _ = strconv.Atoi(u.Uid)
	r.gid, _ = strconv.Atoi(u.Gid)
	r.home = u.HomeDir
	gs, err := u.GroupIds()
	if err != nil {
		return ids{}, err
	}
	for _, g := range gs {
		n, _ := strconv.Atoi(g)
		r.groups = append(r.groups, n)
	}
	return r, nil
}

func intsCSV(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}

func runAs(r req, out map[string]any) {
	id, err := lookup(r.User)
	if err != nil {
		out["error"] = err.Error()
		return
	}
	argv := []string{"/bin/sh", "-c", r.Script}
	if r.Sandbox != "" {
		work := r.Work
		if work == "" {
			work = id.home
		}
		argv = append([]string{"/usr/bin/sandbox-exec", "-f", r.Sandbox, "-D", "HOME=" + id.home, "-D", "WORK=" + work}, argv...)
	}
	attr := &syscall.SysProcAttr{Setsid: true}
	switch r.How {
	case "", "setuid":
		g := make([]uint32, len(id.groups))
		for i, n := range id.groups {
			g[i] = uint32(n)
		}
		attr.Credential = &syscall.Credential{Uid: uint32(id.uid), Gid: uint32(id.gid), Groups: g}
	case "asuser":
		self, _ := os.Executable()
		argv = append([]string{"/bin/launchctl", "asuser", strconv.Itoa(id.uid), self, "dropto",
			strconv.Itoa(id.uid), strconv.Itoa(id.gid), intsCSV(id.groups), "--"}, argv...)
	default:
		out["error"] = "unknown how"
		return
	}
	env := []string{
		"HOME=" + id.home, "USER=" + r.User, "LOGNAME=" + r.User, "SHELL=/bin/zsh",
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin", "LANG=en_US.UTF-8",
	}
	if r.PTY {
		env = append(env, "TERM=xterm-256color")
	}
	for k, v := range r.Env {
		env = append(env, k+"="+v)
	}
	to := r.Timeout
	if to <= 0 {
		to = 600
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = id.home
	cmd.SysProcAttr = attr
	var so, se tailBuf
	var master *os.File
	if r.PTY {
		m, s, err := openPTY()
		if err != nil {
			out["error"] = "pty: " + err.Error()
			return
		}
		master = m
		defer m.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = s, s, s
		attr.Setctty = true
		attr.Ctty = 0
		defer s.Close()
	} else {
		cmd.Stdout, cmd.Stderr = &so, &se
	}
	if err := cmd.Start(); err != nil {
		out["error"] = err.Error()
		return
	}
	var copied sync.WaitGroup
	if master != nil {
		copied.Add(1)
		go func() {
			defer copied.Done()
			_, _ = io.Copy(&so, master)
		}()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var werr error
	select {
	case werr = <-done:
	case <-time.After(time.Duration(to) * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		werr = <-done
		out["timedOut"] = true
	}
	if master != nil {
		// The slave closes with the last process that holds it; the copy
		// then ends with EIO. Give it a moment, then stop waiting.
		cmd.Stdin.(*os.File).Close()
		w := make(chan struct{})
		go func() { copied.Wait(); close(w) }()
		select {
		case <-w:
		case <-time.After(2 * time.Second):
		}
	}
	code := 0
	var ee *exec.ExitError
	if errors.As(werr, &ee) {
		code = ee.ExitCode()
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			out["signal"] = ws.Signal().String()
		}
	} else if werr != nil {
		out["error"] = werr.Error()
		code = -1
	}
	out["exit"] = code
	out["out"] = so.String()
	out["err"] = se.String()
	out["argv0"] = argv[0]
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
			out["uptimeSec"] = time.Since(bt).Seconds()
		}
	case "run":
		to := r.Timeout
		if to <= 0 {
			to = 600
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(to)*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", r.Script)
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin", "HOME=/var/root", "LANG=en_US.UTF-8"}
		var so, se tailBuf
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
	case "runas":
		runAs(r, out)
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
			log.Printf("request op=%s user=%s how=%s pty=%v sandbox=%s", r.Op, r.User, r.How, r.PTY, r.Sandbox)
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

// dropto: x06-guestd dropto <uid> <gid> <g1,g2,...> -- argv...
func dropto(args []string) {
	if len(args) < 5 || args[3] != "--" {
		fmt.Fprintln(os.Stderr, "usage: dropto uid gid groups -- argv...")
		os.Exit(2)
	}
	uid, _ := strconv.Atoi(args[0])
	gid, _ := strconv.Atoi(args[1])
	var gs []int
	for _, g := range strings.Split(args[2], ",") {
		if n, err := strconv.Atoi(g); err == nil {
			gs = append(gs, n)
		}
	}
	if err := unix.Setgroups(gs); err != nil {
		fmt.Fprintln(os.Stderr, "setgroups:", err)
		os.Exit(1)
	}
	if err := unix.Setgid(gid); err != nil {
		fmt.Fprintln(os.Stderr, "setgid:", err)
		os.Exit(1)
	}
	if err := unix.Setuid(uid); err != nil {
		fmt.Fprintln(os.Stderr, "setuid:", err)
		os.Exit(1)
	}
	argv := args[4:]
	err := syscall.Exec(argv[0], argv, os.Environ())
	fmt.Fprintln(os.Stderr, "exec:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "dropto" {
		dropto(os.Args[2:])
		return
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	bt, _ := bootTime()
	log.Printf("start pid %d uid %d, %.2fs after boot", os.Getpid(), os.Getuid(), started.Sub(bt).Seconds())
	var fd int
	var err error
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
	log.Printf("listening on vsock port %d", port)
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
