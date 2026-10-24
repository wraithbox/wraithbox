// Package vmdspawn starts one wb-vmd with the descriptors of its start
// interface, and connects to it: its VMService client through the
// version exchange, and its hand-off receiver in a vmdlink.Link
// (S04-04-vm-processes, "One `wb-vmd` per VM"; S04-03-self-sandboxing,
// "How `wb-vmd` is started"). wb-hostd's VM provider and wb-dev-vm both
// start wb-vmd through it.
//
// It has no OS code. On a host without the hand-off channel, fdchan
// returns an error that wraps errors.ErrUnsupported, and Start returns
// it (S12-01-platform-interfaces, "Unimplemented platforms fail
// clearly").
package vmdspawn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"google.golang.org/grpc"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/vmhandoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdlink"
)

// SlotVar is the name of wb-vmd's one environment variable
// (S04-04-vm-processes, "The slot").
const SlotVar = "WB_VMD_SLOT"

// ExitTimeout bounds the wait for wb-vmd to exit once its lifeline is
// closed. wb-vmd's own deadline is 10 seconds. After it, wb-vmd is
// killed.
const ExitTimeout = 20 * time.Second

// The gRPC limits of the client to wb-vmd. wb-vmd runs framework code
// on the guest's behalf, so what it sends is bounded as the guest's is
// (S04-08-records-and-bounds, "Host work the guest can cause"). Its
// largest message is a VMState with the framework's error.
const (
	maxMsgSize        = 64 << 10
	maxHeaderListSize = 16 << 10
	windowSize        = 64 << 10
	bufferSize        = 16 << 10
)

// slotPattern is the form of a slot: S04-04-vm-processes names work and
// isolated, and wb-vmd refuses any other.
var slotPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Config is what one wb-vmd is started with.
type Config struct {
	// Program is the absolute path of wb-vmd: the installed bundle's
	// Contents/MacOS/wb-vmd for wb-hostd, never from PATH or <config>.
	Program string
	// Slot is the value of WB_VMD_SLOT, wb-vmd's whole environment.
	Slot string
	// RunDir is the directory of the gRPC socket. It must be mode 0700
	// and the user's: localipc.Dial checks it before each connect.
	RunDir string
	// Endpoint names the gRPC socket, <RunDir>/<Endpoint>.sock, in the
	// form localipc takes. Close removes the socket file.
	Endpoint string
	// Version is this program's build version, which the version
	// exchange compares with wb-vmd's.
	Version string
	// Log reads wb-vmd's log pipe, its fd 2, on its own goroutine. It
	// must read until EOF. The pipe is closed after Log returns.
	Log func(r io.Reader)
	// Logger gets the decisions. nil takes slog.Default().
	Logger *slog.Logger
}

// Process is one running wb-vmd.
type Process struct {
	// Client is wb-vmd's VMService. The first call connects, and runs
	// the version exchange.
	Client vmdv1.VMServiceClient
	// Link is wb-vmd's ConnectGuest with the hand-off receiver.
	Link *vmdlink.Link

	log    *slog.Logger
	cmd    *exec.Cmd
	sock   string
	creds  *grpccreds.Credentials
	cc     *grpc.ClientConn
	life   *os.File // the lifeline's write end
	exited chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// descriptors are the ends the spawner keeps and the ends it passes to
// wb-vmd as descriptors 0, 2, 3 and 4.
type descriptors struct {
	lifeRead, lifeWrite *os.File
	logRead, logWrite   *os.File
	grpc                *os.File
	handoffOwn, handoff *os.File
}

func (d *descriptors) closeChild() {
	for _, f := range []*os.File{d.lifeRead, d.logWrite, d.grpc, d.handoff} {
		if f != nil {
			_ = f.Close()
		}
	}
}

func (d *descriptors) closeAll() {
	d.closeChild()
	for _, f := range []*os.File{d.lifeWrite, d.logRead, d.handoffOwn} {
		if f != nil {
			_ = f.Close()
		}
	}
}

func (c *Config) check() error {
	var errs []error
	if !filepath.IsAbs(c.Program) {
		errs = append(errs, fmt.Errorf("vmdspawn: the program %q isn't an absolute path", c.Program))
	}
	if !slotPattern.MatchString(c.Slot) {
		errs = append(errs, fmt.Errorf("vmdspawn: the slot %q isn't a slot name", c.Slot))
	}
	if c.Version == "" {
		errs = append(errs, errors.New("vmdspawn: no build version, and wb-vmd refuses an unstamped peer"))
	}
	if c.Log == nil {
		errs = append(errs, errors.New("vmdspawn: no log reader"))
	}
	return errors.Join(errs...)
}

// Start starts wb-vmd and makes its client. It doesn't wait for wb-vmd:
// the first call on Client does. ctx bounds only the start: the
// lifeline, closed by Close, ends wb-vmd.
func Start(ctx context.Context, cfg Config) (*Process, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	p := &Process{log: log, sock: filepath.Join(cfg.RunDir, cfg.Endpoint+".sock"), exited: make(chan struct{})}
	d, err := makeDescriptors(log, p.sock)
	if err != nil {
		return nil, err
	}
	p.life = d.lifeWrite
	if err := p.spawn(ctx, &cfg, d); err != nil {
		d.closeAll()
		_ = os.Remove(p.sock)
		return nil, err
	}
	if err := p.connect(&cfg, d.handoffOwn); err != nil {
		_ = p.Close()
		return nil, err
	}
	return p, nil
}

// makeDescriptors makes the pipes, the listening gRPC socket at sock
// with mode 0600, and the hand-off socketpair.
func makeDescriptors(log *slog.Logger, sock string) (*descriptors, error) {
	d := &descriptors{}
	var err error
	if d.lifeRead, d.lifeWrite, err = os.Pipe(); err != nil {
		return nil, err
	}
	if d.logRead, d.logWrite, err = os.Pipe(); err != nil {
		d.closeAll()
		return nil, err
	}
	if d.grpc, err = listenFile(log, sock); err != nil {
		d.closeAll()
		return nil, err
	}
	if d.handoffOwn, d.handoff, err = fdchan.Socketpair(vmhandoff.MaxRecord); err != nil {
		d.closeAll()
		_ = os.Remove(sock)
		return nil, err
	}
	return d, nil
}

// UnsafeEndpointError is Start's refusal of a file already at the
// socket path, with localipc's rule ipc-endpoint-unsafe.
type UnsafeEndpointError struct {
	Path string
	// Type is the file type found at Path, when lstat read it.
	Type fs.FileMode

	err error
}

func (e *UnsafeEndpointError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("vmdspawn: %s: lstat %q: %v", localipc.RuleEndpointUnsafe, e.Path, e.err)
	}
	return fmt.Sprintf("vmdspawn: %s: %q already holds a file of type %s", localipc.RuleEndpointUnsafe, e.Path, e.Type)
}

// Unwrap returns lstat's error, if that is what failed.
func (e *UnsafeEndpointError) Unwrap() error { return e.err }

// listenFile binds a listening Unix socket at path, gives it mode 0600,
// and returns its descriptor. The directory is mode 0700, so no one else
// reaches the socket before the chmod. Anything already at path, a
// leftover socket or a symbolic link included, is refused with
// ipc-endpoint-unsafe and left in place: bind(2) follows a dangling
// link and makes the socket at its target (S04-architecture,
// "Inter-process contracts"; S04-03-self-sandboxing, "How `wb-vmd` is
// started"). Only the user writes the run directory, so nothing appears
// at path between the lstat and the bind.
func listenFile(log *slog.Logger, path string) (*os.File, error) {
	if fi, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		r := &UnsafeEndpointError{Path: path, err: err}
		if err == nil {
			r.Type = fi.Mode().Type()
		}
		log.LogAttrs(context.Background(), slog.LevelWarn, "wb-vmd socket refused",
			slog.String("decision", "refuse"), slog.String("rule", string(localipc.RuleEndpointUnsafe)),
			slog.String("endpoint", path), slog.String("reason", r.Error()))
		return nil, r
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	// The socket file stays for wb-vmd. Close removes it.
	ln.SetUnlinkOnClose(false)
	defer func() { _ = ln.Close() }()
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	f, err := ln.File()
	if err != nil {
		_ = os.Remove(path)
	}
	return f, err
}

// spawn starts wb-vmd with no arguments, the slot as its whole
// environment, and the descriptors of its start interface
// (SEC12-least-privilege, SEC08-proj-isolation). Go marks every other
// descriptor close-on-exec, so wb-vmd inherits nothing else.
func (p *Process) spawn(ctx context.Context, cfg *Config, d *descriptors) error {
	// The lifeline, not ctx, ends wb-vmd, so that it stops its VM first.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), cfg.Program) //nolint:gosec // G204: the program is the installed bundle's wb-vmd, and it gets no arguments.
	cmd.Args = []string{cfg.Program}
	cmd.Env = []string{SlotVar + "=" + cfg.Slot}
	cmd.Stdin = d.lifeRead
	cmd.Stderr = d.logWrite
	cmd.ExtraFiles = []*os.File{d.grpc, d.handoff}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cfg.Program, err)
	}
	d.closeChild()
	p.cmd = cmd
	go func() {
		defer func() { _ = d.logRead.Close() }()
		cfg.Log(d.logRead)
	}()
	go func() {
		_ = cmd.Wait()
		p.log.LogAttrs(context.WithoutCancel(ctx), slog.LevelInfo, "wb-vmd exited",
			slog.Int("pid", cmd.Process.Pid), slog.String("slot", cfg.Slot),
			slog.String("state", cmd.ProcessState.String()))
		close(p.exited)
	}()
	p.log.LogAttrs(ctx, slog.LevelInfo, "wb-vmd started",
		slog.Int("pid", cmd.Process.Pid), slog.String("slot", cfg.Slot),
		slog.String("program", cfg.Program), slog.String("socket", p.sock))
	return nil
}

// connect makes the gRPC client, through localipc and the version
// exchange, with explicit limits, and the link with the hand-off
// receiver.
func (p *Process) connect(cfg *Config, handoff *os.File) error {
	rx, err := vmhandoff.NewReceiver(handoff, p.log)
	if err != nil {
		return err
	}
	p.creds = grpccreds.New(p.log, cfg.Version)
	opts := append(grpccreds.ClientOptions(p.log, p.creds, cfg.RunDir, cfg.Endpoint),
		grpc.WithDisableRetry(),
		// One connection for the instance's life: the socket's listener
		// is wb-vmd's, and there is no other.
		grpc.WithIdleTimeout(0),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgSize), grpc.MaxCallSendMsgSize(maxMsgSize)),
		grpc.WithMaxHeaderListSize(maxHeaderListSize),
		grpc.WithStaticStreamWindowSize(windowSize),
		grpc.WithStaticConnWindowSize(windowSize),
		grpc.WithReadBufferSize(bufferSize),
		grpc.WithWriteBufferSize(bufferSize),
	)
	cc, err := grpc.NewClient("passthrough:///"+cfg.Endpoint, opts...)
	if err != nil {
		_ = rx.Close()
		return err
	}
	p.cc = cc
	p.Client = vmdv1.NewVMServiceClient(cc)
	p.Link = vmdlink.New(p.Client, rx, p.log)
	return nil
}

// Pid is wb-vmd's process ID.
func (p *Process) Pid() int { return p.cmd.Process.Pid }

// Exited is closed once wb-vmd has exited.
func (p *Process) Exited() <-chan struct{} { return p.exited }

// ProcessState is wb-vmd's exit status, nil until Exited is closed.
func (p *Process) ProcessState() *os.ProcessState {
	select {
	case <-p.exited:
		return p.cmd.ProcessState
	default:
		return nil
	}
}

// VersionRefusal is the version exchange's refusal of wb-vmd, nil when
// there was none. A call that failed because of one says only that the
// connection is unavailable.
func (p *Process) VersionRefusal() *hostversion.Refusal { return p.creds.LastRefusal() }

// Close closes the lifeline, which makes wb-vmd stop its VM at once and
// exit (S04-07-supervision, "When `wb-hostd` stops"). It waits for wb-vmd
// to exit, kills it after ExitTimeout, then closes the client and the
// link and removes the socket file. It is safe to call more than once.
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		_ = p.life.Close()
		select {
		case <-p.exited:
		case <-time.After(ExitTimeout):
			_ = p.cmd.Process.Kill()
			<-p.exited
			p.closeErr = fmt.Errorf("wb-vmd didn't exit within %s of its lifeline closing, and was killed", ExitTimeout)
			p.log.LogAttrs(context.Background(), slog.LevelWarn, "wb-vmd killed",
				slog.String("decision", "kill"), slog.String("rule", "vmd-exit-timeout"),
				slog.Int("pid", p.cmd.Process.Pid))
		}
		if p.Link != nil {
			_ = p.Link.Close()
		}
		if p.cc != nil {
			_ = p.cc.Close()
		}
		_ = os.Remove(p.sock)
	})
	return p.closeErr
}
