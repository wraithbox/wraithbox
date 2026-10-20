package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"time"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsock"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdlink"
)

// helloRetry is how long hello waits between tries while wb-guestd isn't
// listening yet.
const helloRetry = time.Second

// portValue is a vsock port flag: a number from 0 to 2^32-1. wb-vmd
// decides which ports it connects to, so a test can ask for 1024 and see
// the refusal.
type portValue uint32

func (p *portValue) String() string { return strconv.FormatUint(uint64(*p), 10) }

func (p *portValue) Set(s string) error {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return fmt.Errorf("not a vsock port: %w", err)
	}
	*p = portValue(n)
	return nil
}

// parse parses a command's flags and checks the common ones.
func parse(fs *flag.FlagSet, f *vmFlags, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		// The flag package has printed the error and the flags.
		return fmt.Errorf("%w: %w", err, errUsage)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q: %w", fs.Arg(0), errUsage)
	}
	if f.cpuCount == 0 || f.cpuCount > math.MaxUint32 {
		return fmt.Errorf("-cpu-count %d is outside 1 to %d: %w", f.cpuCount, uint32(math.MaxUint32), errUsage)
	}
	return nil
}

// withVM starts wb-vmd, boots the VM, runs body, and stops both.
func withVM(ctx context.Context, f *vmFlags, stdout, stderr io.Writer, body func(*session) error) error {
	s, err := startVMD(ctx, stdout, stderr)
	if err != nil {
		return err
	}
	err = s.boot(ctx, f)
	if err == nil {
		err = body(s)
	}
	return errors.Join(err, s.close())
}

// bootCommand boots the VM and prints its states until Ctrl-C, or until
// it stops on its own.
func bootCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("boot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := addVMFlags(fs)
	if err := parse(fs, f, args); err != nil {
		return err
	}
	return withVM(ctx, f, stdout, stderr, func(s *session) error {
		err := s.watch(ctx, func(*vmdv1.VMState) bool { return false })
		if errors.Is(err, context.Canceled) {
			err = nil // Ctrl-C
		}
		return err
	})
}

// helloCommand boots the VM, connects to port in the guest, and runs
// wb-guestd's hello on the connection. With -hold it waits for a line on
// standard input between the hand-off and the hello, so wb-vmd can be
// stopped with SIGSTOP in between (X18-vsock-handoff): a passed
// descriptor works without wb-vmd.
func helloCommand(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hello", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := addVMFlags(fs)
	port := portValue(guestsock.Port)
	fs.Var(&port, "port", "the guest port wb-guestd listens on")
	hold := fs.Bool("hold", false, "wait for a line on standard input between the hand-off and the hello")
	guestVersion := fs.String("guest-version", "", "the build version wb-guestd must report (default: wb-dev-vm's own)")
	if err := parse(fs, f, args); err != nil {
		return err
	}
	return withVM(ctx, f, stdout, stderr, func(s *session) error {
		h := hello{s: s, port: uint32(port), wait: f.wait, stdout: stdout, guestVersion: s.own}
		if *guestVersion != "" {
			h.guestVersion = *guestVersion
		}
		if *hold {
			h.hold = bufio.NewReader(stdin)
		}
		return h.run(ctx)
	})
}

// hello is one run of the hello command.
type hello struct {
	s      *session
	port   uint32
	wait   time.Duration
	stdout io.Writer
	hold   *bufio.Reader // nil without -hold
	// guestVersion is the version Hello checks wb-guestd's against.
	guestVersion string
}

func (h *hello) run(ctx context.Context) error {
	if h.port == 0 || h.port >= vmdlink.FirstUnreservedPort {
		return h.probe(ctx)
	}
	waitCtx, cancel := context.WithTimeout(ctx, h.wait)
	defer cancel()
	link, err := h.retry(waitCtx)
	if err != nil {
		return err
	}
	if h.hold == nil {
		fmt.Fprintf(h.stdout, "wb-guestd %s\n", link.Version())
		return link.Close()
	}
	_ = link.Close()
	// wb-guestd answers, so this connection is the one the hold is for.
	conn, err := h.s.link.ConnectGuest(ctx, generation, h.port)
	if err != nil {
		return err
	}
	fmt.Fprintf(h.stdout, "handed off; wb-vmd is pid %d. Stop it (kill -STOP %d), then press Enter for the hello.\n",
		h.s.cmd.Process.Pid, h.s.cmd.Process.Pid)
	if _, err := h.hold.ReadString('\n'); err != nil && !errors.Is(err, io.EOF) {
		_ = conn.Close()
		return err
	}
	link, err = guestlink.Hello(ctx, conn, h.guestVersion, h.s.log)
	if err != nil {
		return err
	}
	fmt.Fprintf(h.stdout, "wb-guestd %s, on a connection wb-vmd passed before it was stopped\n", link.Version())
	return link.Close()
}

// probeRequestID is the request ID of a probe. The link counts its own
// from 1, so the two never meet, and the receiver refuses a record for a
// request it didn't open.
const probeRequestID = math.MaxUint64

// probe asks wb-vmd itself for a port the host never connects to, which
// vmdlink refuses before it asks, and prints wb-vmd's refusal. It fails
// when wb-vmd doesn't refuse.
func (h *hello) probe(ctx context.Context) error {
	_, err := h.s.client.ConnectGuest(ctx, &vmdv1.ConnectGuestRequest{
		Generation: generation, RequestId: probeRequestID, GuestPort: h.port,
	})
	if err == nil {
		return fmt.Errorf("wb-vmd accepted a connect to guest port %d", h.port)
	}
	fmt.Fprintf(h.stdout, "wb-vmd refused guest port %d: %v\n", h.port, err)
	return nil
}

// retry connects and runs the hello until it works or ctx ends. A
// refusal of wb-vmd other than a failed connect, which means wb-guestd
// isn't listening yet, ends it at once.
func (h *hello) retry(ctx context.Context) (*guestlink.Link, error) {
	for {
		conn, err := h.s.link.ConnectGuest(ctx, generation, h.port)
		if err == nil {
			return guestlink.Hello(ctx, conn, h.guestVersion, h.s.log)
		}
		if !retryable(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("no answer from guest port %d: %w", h.port, err)
		case <-time.After(helloRetry):
		}
	}
}

// connectCommand boots the VM, listens at -listen, and relays each
// client to a new connection to -port in the guest.
func connectCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := addVMFlags(fs)
	var port portValue
	fs.Var(&port, "port", "the guest port, below 1024")
	listen := fs.String("listen", "", "the Unix socket to create, <directory>/<name>.sock")
	if err := parse(fs, f, args); err != nil {
		return err
	}
	if port == 0 || *listen == "" {
		return fmt.Errorf("connect needs -port and -listen: %w", errUsage)
	}
	dir, name, err := endpointOf(*listen)
	if err != nil {
		return err
	}
	return withVM(ctx, f, stdout, stderr, func(s *session) error {
		dial := func(ctx context.Context) (net.Conn, error) {
			return s.link.ConnectGuest(ctx, generation, uint32(port))
		}
		return serveRelay(ctx, s.log, dir, name, dial, stdout)
	})
}
