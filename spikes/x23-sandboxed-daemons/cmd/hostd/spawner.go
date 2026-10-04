package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"x23/probes"
)

// A sandboxed process cannot apply a second sandbox (the kernel reports
// "forbidden-sandbox-reinit"), and children inherit their parent's
// sandbox. So hostd starts a spawner before it confines itself. The
// spawner stays unconfined, takes requests only over a private
// socketpair, starts only programs from a fixed table, and each child
// confines itself.

// startSpawner re-executes this binary in spawner mode and returns the
// hostd end of the request socket. Called before sandboxing.
func startSpawner(programs map[string]string) (*net.UnixConn, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{"-spawner-mode"}
	for name, path := range programs {
		args = append(args, "-program", name+"="+path)
	}
	child := os.NewFile(uintptr(fds[1]), "spawner")
	cmd := exec.Command(self, args...)
	cmd.ExtraFiles = []*os.File{child}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	child.Close()
	c, err := net.FileConn(os.NewFile(uintptr(fds[0]), "spawner-req"))
	if err != nil {
		return nil, err
	}
	return c.(*net.UnixConn), nil
}

// spawnerMain serves requests: "<program> <arg>..." plus descriptors,
// which become the child's fds 0, 1, 2, 3, ... in order.
func spawnerMain(programs map[string]string) {
	c, err := net.FileConn(os.NewFile(3, "req"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "spawner:", err)
		os.Exit(1)
	}
	u := c.(*net.UnixConn)
	buf := make([]byte, 4096)
	oob := make([]byte, syscall.CmsgSpace(16*4))
	for {
		n, oobn, _, _, err := u.ReadMsgUnix(buf, oob)
		if err != nil || n == 0 {
			return
		}
		var fds []int
		if msgs, err := syscall.ParseSocketControlMessage(oob[:oobn]); err == nil {
			for i := range msgs {
				if f, err := syscall.ParseUnixRights(&msgs[i]); err == nil {
					fds = append(fds, f...)
				}
			}
		}
		fields := strings.Fields(string(buf[:n]))
		reply := spawn(programs, fields, fds)
		for _, fd := range fds {
			syscall.Close(fd)
		}
		_, _ = u.Write([]byte(reply))
	}
}

func spawn(programs map[string]string, fields []string, fds []int) string {
	if len(fields) == 0 {
		return "error empty request"
	}
	path, ok := programs[fields[0]]
	if !ok {
		// Fail closed: only programs from the table, decided at start.
		fmt.Fprintf(os.Stderr, "spawner: refused %q: not in the program table\n", fields[0])
		return "error refused " + fields[0]
	}
	if len(fds) < 3 {
		return "error need stdin, stdout, stderr"
	}
	files := make([]*os.File, len(fds))
	for i, fd := range fds {
		nfd, err := syscall.Dup(fd)
		if err != nil {
			return "error dup"
		}
		files[i] = os.NewFile(uintptr(nfd), fmt.Sprintf("fd%d", i))
	}
	cmd := exec.Command(path, fields[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = files[0], files[1], files[2]
	cmd.ExtraFiles = files[3:]
	err := cmd.Start()
	for _, f := range files {
		f.Close()
	}
	if err != nil {
		return "error " + err.Error()
	}
	go func() { _ = cmd.Wait() }()
	return fmt.Sprintf("pid %d", cmd.Process.Pid)
}

// netdViaSpawner starts netd through the spawner with its profile on a
// pipe (fd 3) and its output on a pipe, and prints what it reports.
func netdViaSpawner(u *net.UnixConn, mech string, profile []byte, e probes.Env) {
	pr, pw, err := os.Pipe()
	if err != nil {
		fmt.Println("hostd: pipe:", err)
		return
	}
	or, ow, err := os.Pipe()
	if err != nil {
		fmt.Println("hostd: pipe:", err)
		return
	}
	null, err := os.Open("/dev/null")
	if err != nil {
		fmt.Println("hostd: /dev/null:", err)
		return
	}
	req := fmt.Sprintf("netd -mech %s -profile fd:3 -probe-only -tcp %s -readfile %s -writedir %s",
		mech, e.TCPAddr, e.ReadFile, e.WriteDir)
	reply, err := spawnVia(u, req, null, ow, ow, pr)
	null.Close()
	ow.Close()
	pr.Close()
	fmt.Printf("hostd: spawner reply %q %v\n", reply, err)
	_, _ = pw.Write(profile)
	pw.Close()
	out, _ := io.ReadAll(or)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fmt.Println("  child|", line)
	}
}

// spawnVia asks the spawner to start a program; fds are passed along.
func spawnVia(u *net.UnixConn, req string, files ...*os.File) (string, error) {
	fds := make([]int, len(files))
	for i, f := range files {
		fds[i] = int(f.Fd())
	}
	if _, _, err := u.WriteMsgUnix([]byte(req), syscall.UnixRights(fds...), nil); err != nil {
		return "", err
	}
	buf := make([]byte, 256)
	n, err := u.Read(buf)
	return string(buf[:n]), err
}
