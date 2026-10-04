// Package host is the X07 spike's stand-in for the git gateway in
// wb-hostd: it takes one connection from the guest (a socketpair end in
// place of vsock), reads which service the guest wants, and runs
// `git upload-pack` against a read-only repository or `git receive-pack`
// behind the pktfilter ref filter into the landing repository.
//
// Throwaway code. Not held to the project gates.
package host

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"x07/pktfilter"
)

// Config is fixed by the host per session. Nothing in it comes from the
// guest.
type Config struct {
	Git       string   // host git binary
	ServeRepo string   // repository upload-pack reads (a git dir)
	Landing   string   // bare landing repository receive-pack writes
	SessionID string   // the session's id; refs land in refs/heads/wb/<id>/
	Advertise []string // full ref names upload-pack advertises; all else is hidden
	MaxInput  int64    // receive.maxInputSize, bytes
	Home      string   // empty directory used as HOME for host git
	ForceV0   bool     // ignore the guest's protocol request for upload-pack
	NoFilter  bool     // run receive-pack without the pktfilter in front
	Log       *log.Logger
}

// Conn is a stream with half-close, as a vsock or unix connection has.
type Conn interface {
	io.ReadWriteCloser
	CloseWrite() error
}

// Header is the first pkt-line the guest sends:
// "wb-connect <service> <protocol>\n". The protocol field is "-" or a
// GIT_PROTOCOL value.
const maxHeader = 256

func (c *Config) env(protocol string) []string {
	// SEC03-no-host-exec: scrubbed environment, no system or global git
	// configuration, so nothing from the host user's git setup applies.
	env := []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + c.Home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	}
	if protocol != "" {
		env = append(env, "GIT_PROTOCOL="+protocol)
	}
	return env
}

// Serve handles one guest connection and closes it.
func Serve(conn Conn, c *Config) error {
	defer conn.Close()
	br := bufio.NewReader(conn)
	p, flush, _, err := pktfilter.ReadPkt(br)
	if err != nil || flush || len(p) > maxHeader {
		c.Log.Printf("decision=deny rule=header-framing err=%v", err)
		return errors.New("bad header")
	}
	f := strings.Fields(strings.TrimSuffix(string(p), "\n"))
	if len(f) != 3 || f[0] != "wb-connect" {
		c.Log.Printf("decision=deny rule=header-format header=%q", p)
		_, _ = conn.Write(pktfilter.Pkt("ERR bad header\n"))
		return errors.New("bad header")
	}
	service, proto := f[1], f[2]
	if proto == "-" {
		proto = ""
	}
	switch service {
	case "git-upload-pack":
		// Fetch speaks v0, v1, or v2.
		if proto != "" && proto != "version=0" && proto != "version=1" && proto != "version=2" {
			c.Log.Printf("decision=deny rule=protocol-allowlist service=%s protocol=%q", service, proto)
			_, _ = conn.Write(pktfilter.Pkt("ERR protocol not allowed\n"))
			return errors.New("protocol")
		}
		if c.ForceV0 {
			proto = ""
		}
		c.Log.Printf("decision=allow rule=upload-pack-readonly protocol=%q", proto)
		_, _ = conn.Write(pktfilter.Pkt("ok\n"))
		return c.uploadPack(conn, br, proto)
	case "git-receive-pack":
		// Push has no v2; the filter parses v0/v1 only.
		if proto != "" && proto != "version=0" && proto != "version=1" {
			proto = ""
		}
		c.Log.Printf("decision=allow rule=receive-pack-landing protocol=%q", proto)
		_, _ = conn.Write(pktfilter.Pkt("ok\n"))
		return c.receivePack(conn, br, proto)
	default:
		c.Log.Printf("decision=deny rule=service-allowlist service=%q", service)
		_, _ = conn.Write(pktfilter.Pkt("ERR service not allowed\n"))
		return errors.New("service")
	}
}

func (c *Config) uploadPack(conn Conn, in io.Reader, proto string) error {
	args := []string{
		// SEC02-no-host-fs-share, SEC04-no-guest-secrets: advertise only
		// the configured refs. Later hideRefs entries win, so these
		// command-line values override anything in the repository config.
		"-c", "uploadpack.hideRefs=refs/",
		"-c", "uploadpack.allowTipSHA1InWant=false",
		"-c", "uploadpack.allowReachableSHA1InWant=false",
		"-c", "uploadpack.allowAnySHA1InWant=false",
		"-c", "uploadpack.allowFilter=false",
		"-c", "uploadpack.advertiseBundleURIs=false",
		"-c", "core.hooksPath=/dev/null",
	}
	for _, r := range c.Advertise {
		args = append(args, "-c", "uploadpack.hideRefs=!"+r)
	}
	args = append(args, "upload-pack", "--strict", c.ServeRepo)
	cmd := exec.Command(c.Git, args...)
	cmd.Env = c.env(proto)
	cmd.Dir = c.Home
	cmd.Stdin = in
	cmd.Stdout = conn
	cmd.Stderr = prefixWriter{c.Log, "upload-pack: "}
	err := cmd.Run()
	_ = conn.CloseWrite()
	c.Log.Printf("upload-pack exit err=%v", err)
	return err
}

func (c *Config) receivePack(conn Conn, in io.Reader, proto string) error {
	prefix := "refs/heads/wb/" + c.SessionID + "/"
	args := []string{
		"-c", "core.hooksPath=/dev/null", // SEC03-no-host-exec: no hooks run
		"-c", "receive.fsckObjects=true", // reject malformed objects
		// fsckObjects turns fsck warnings into errors but leaves the
		// INFO-level object checks alone; make those errors too.
		"-c", "receive.fsck.badFilemode=error",
		"-c", "receive.fsck.badTagName=error",
		"-c", "receive.fsck.missingTaggerEntry=error",
		"-c", "receive.fsck.gitmodulesParse=error",
		"-c", "receive.fsck.gitattributesSymlink=error",
		"-c", "receive.fsck.gitignoreSymlink=error",
		"-c", "receive.fsck.mailmapSymlink=error",
		"-c", fmt.Sprintf("receive.maxInputSize=%d", c.MaxInput),
		"-c", "receive.denyDeletes=true",
		"-c", "receive.autogc=false",
		"-c", "receive.advertisePushOptions=false",
		"-c", "receive.shallowUpdate=false",
		// Advertise only this session's refs (plus alternates as .have).
		"-c", "receive.hideRefs=refs/",
		"-c", "receive.hideRefs=!" + strings.TrimSuffix(prefix, "/"),
		"receive-pack", c.Landing,
	}
	cmd := exec.Command(c.Git, args...)
	cmd.Env = c.env(proto)
	cmd.Dir = c.Home
	cmd.Stderr = prefixWriter{c.Log, "receive-pack: "}
	if c.NoFilter {
		// receive.hideRefs alone enforces the prefix.
		cmd.Stdin = in
		cmd.Stdout = conn
		err := cmd.Run()
		_ = conn.CloseWrite()
		c.Log.Printf("receive-pack (no filter) exit err=%v", err)
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(conn, stdout)
	}()

	// SEC03-no-host-exec, FR03-work-as-branch: the ref restriction is
	// this filter. It reads the whole command list before receive-pack
	// sees a byte of it.
	req, ferr := pktfilter.ReadRequest(in, prefix)
	if ferr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		wg.Wait()
		var ref *pktfilter.Refusal
		if errors.As(ferr, &ref) {
			c.Log.Printf("decision=deny rule=ref-prefix prefix=%s err=%v", prefix, ferr)
			_, _ = conn.Write(pktfilter.RefusalResponse(req, req.Commands, ref.Reason))
			// Drain what the client still sends (its pack), bounded, so it
			// can finish writing and read the report.
			_, _ = io.CopyN(io.Discard, in, c.MaxInput)
		} else {
			c.Log.Printf("decision=deny rule=push-framing err=%v", ferr)
		}
		_ = conn.CloseWrite()
		return ferr
	}
	for _, cm := range req.Commands {
		c.Log.Printf("decision=allow rule=ref-prefix ref=%s old=%s new=%s", cm.Ref, cm.Old, cm.New)
	}
	if _, err := stdin.Write(req.Raw); err != nil {
		return err
	}
	_, _ = io.Copy(stdin, in)
	_ = stdin.Close()
	wg.Wait()
	err = cmd.Wait()
	_ = conn.CloseWrite()
	c.Log.Printf("receive-pack exit err=%v", err)
	return err
}

type prefixWriter struct {
	l *log.Logger
	p string
}

func (w prefixWriter) Write(b []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		w.l.Print(w.p + line)
	}
	return len(b), nil
}

// Discard is a logger that drops everything.
var Discard = log.New(io.Discard, "", 0)

// Stderr logger.
func StderrLogger(prefix string) *log.Logger { return log.New(os.Stderr, prefix, log.Lmicroseconds) }
