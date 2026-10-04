// Command git-remote-wb is the X07 spike's guest-side git remote helper.
// It speaks the `connect` capability of gitremote-helpers(7): git asks for
// a service, the helper opens a stream to the host and then copies bytes
// both ways. In the product the stream is a vsock connection to wb-hostd.
// In the spike it is an inherited socketpair end, fd $WB_FD.
//
// Throwaway code. Not held to the project gates.
package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "git-remote-wb:", err)
		os.Exit(128)
	}
}

func pkt(s string) []byte { return []byte(fmt.Sprintf("%04x%s", len(s)+4, s)) }

func run() error {
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "capabilities":
			fmt.Print("connect\n\n")
		case strings.HasPrefix(line, "option "):
			fmt.Print("unsupported\n")
		case line == "":
			return nil
		case strings.HasPrefix(line, "connect "):
			return connect(strings.TrimPrefix(line, "connect "), in)
		default:
			return fmt.Errorf("unknown command %q", line)
		}
	}
}

func connect(service string, in *bufio.Reader) error {
	fd, err := strconv.Atoi(os.Getenv("WB_FD"))
	if err != nil {
		return fmt.Errorf("WB_FD: %w", err)
	}
	c, err := net.FileConn(os.NewFile(uintptr(fd), "wb-host"))
	if err != nil {
		return err
	}
	conn := c.(*net.UnixConn)
	// With `connect`, git does not tell the helper which protocol version
	// it wants (only stateless-connect is v2-only), and upload-pack
	// speaks v0 unless asked. So the helper asks for v2 on fetch unless
	// the user configured an older version, as git's own ssh and file
	// transports do. Push has no v2.
	proto := "-"
	if service == "git-upload-pack" {
		proto = "version=2"
		out, _ := exec.Command("git", "config", "--get", "protocol.version").Output()
		switch strings.TrimSpace(string(out)) {
		case "0":
			proto = "-"
		case "1":
			proto = "version=1"
		}
	}
	if _, err := conn.Write(pkt("wb-connect " + service + " " + proto + "\n")); err != nil {
		return err
	}
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return fmt.Errorf("host reply: %w", err)
	}
	n, err := strconv.ParseUint(string(hdr[:]), 16, 16)
	if err != nil || n < 4 || n > 256 {
		return fmt.Errorf("host reply length %q", hdr)
	}
	body := make([]byte, n-4)
	if _, err := io.ReadFull(conn, body); err != nil {
		return err
	}
	if string(body) != "ok\n" {
		return fmt.Errorf("host refused: %s", strings.TrimSpace(string(body)))
	}
	// Tell git the connection is up; from here on stdin/stdout carry the
	// service's own protocol.
	if _, err := os.Stdout.Write([]byte("\n")); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(conn, in) // includes anything bufio already read
		_ = conn.CloseWrite()
		close(done)
	}()
	_, err = io.Copy(os.Stdout, conn)
	return err
}
