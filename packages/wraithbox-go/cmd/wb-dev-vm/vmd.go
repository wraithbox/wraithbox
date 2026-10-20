package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/vmhandoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdlink"
)

const (
	// generation is the VM generation of the one start of each run.
	// Generations start at 1 (proto/wraithbox/vmd/v1).
	generation = 1

	// slotEnv is wb-vmd's only environment: the work slot
	// (S04-04-vm-processes, "The slot").
	slotEnv = "WB_VMD_SLOT=work"

	// endpoint is the name of wb-vmd's gRPC socket in the run directory.
	endpoint = "vmd"

	// startedTimeout bounds the wait for wb-vmd's started record.
	startedTimeout = 10 * time.Second

	// exitTimeout bounds the wait for wb-vmd to exit once its lifeline
	// is closed. wb-vmd's own deadline is 10 seconds.
	exitTimeout = 20 * time.Second
)

// vmdPath is the signed wb-vmd that `mise run swift:sign` writes in the
// checkout that `mise run go:build` built this program in, as
// packages/wraithbox-go/bin/wb-dev-vm. A `go run` build is unstamped,
// which wb-vmd refuses, so the stamped program in bin/ is the one that
// runs.
func vmdPath() string {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		// The start then fails, and names the path.
		return "wb-vmd"
	}
	return filepath.Join(filepath.Dir(exe), "..", "..", "wraithbox-swift", ".build", "signed", "wb-vmd")
}

// vmFlags are the flags of every command.
type vmFlags struct {
	cpuCount  uint
	memoryMiB uint64
	wait      time.Duration
}

func addVMFlags(fs *flag.FlagSet) *vmFlags {
	f := &vmFlags{}
	fs.UintVar(&f.cpuCount, "cpu-count", 4, "virtual CPUs")
	fs.Uint64Var(&f.memoryMiB, "memory-mib", 4096, "guest memory in MiB")
	fs.DurationVar(&f.wait, "wait", 3*time.Minute, "how long to wait for the VM, and for wb-guestd to answer")
	return f
}

// session is one wb-vmd, its VM, and the link to it.
type session struct {
	log    *slog.Logger
	out    io.Writer
	own    string
	dir    string
	cmd    *exec.Cmd
	exited chan struct{}
	life   *os.File // the lifeline's write end
	cc     *grpc.ClientConn
	client vmdv1.VMServiceClient
	link   *vmdlink.Link
}

// descriptors are the ends wb-dev-vm keeps and the ends it passes to
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

// startVMD starts wb-vmd and connects to it. own is wb-dev-vm's build
// version, which wb-vmd's version exchange compares with its own.
func startVMD(ctx context.Context, out, stderr io.Writer) (*session, error) {
	own := version.Build()
	if own == "" {
		return nil, errors.New("wb-dev-vm is unstamped, and wb-vmd refuses an unstamped peer: build it with mise run go:build ::: swift:sign")
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	dir, err := os.MkdirTemp("", "wb-dev-vm")
	if err != nil {
		return nil, err
	}
	d, err := makeDescriptors(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	s := &session{log: log, out: out, own: own, dir: dir, exited: make(chan struct{}), life: d.lifeWrite}
	started := make(chan string, 1)
	if err := s.spawn(ctx, vmdPath(), d, stderr, started); err != nil {
		d.closeAll()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := s.connect(d.handoffOwn); err != nil {
		_ = s.close()
		return nil, err
	}
	if err := s.waitStarted(ctx, started); err != nil {
		_ = s.close()
		return nil, err
	}
	return s, nil
}

// makeDescriptors makes the pipes, the listening gRPC socket in dir with
// mode 0600, and the hand-off socketpair.
func makeDescriptors(dir string) (*descriptors, error) {
	d := &descriptors{}
	var err error
	if d.lifeRead, d.lifeWrite, err = os.Pipe(); err != nil {
		return nil, err
	}
	if d.logRead, d.logWrite, err = os.Pipe(); err != nil {
		d.closeAll()
		return nil, err
	}
	if d.grpc, err = listenFile(filepath.Join(dir, endpoint+".sock")); err != nil {
		d.closeAll()
		return nil, err
	}
	if d.handoffOwn, d.handoff, err = fdchan.Socketpair(vmhandoff.MaxRecord); err != nil {
		d.closeAll()
		return nil, err
	}
	return d, nil
}

// listenFile binds a listening Unix socket at path, gives it mode 0600,
// and returns its descriptor. The directory is new and mode 0700, so no
// one else reaches the socket before the chmod.
func listenFile(path string) (*os.File, error) {
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	// The socket file stays for wb-vmd. The directory is removed at the
	// end.
	ln.SetUnlinkOnClose(false)
	defer func() { _ = ln.Close() }()
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	return ln.File()
}

// spawn starts wb-vmd with no arguments, the slot as its whole
// environment, and the descriptors of its start interface. Go marks
// every other descriptor close-on-exec, so wb-vmd inherits nothing else.
func (s *session) spawn(ctx context.Context, path string, d *descriptors, stderr io.Writer, started chan<- string) error {
	// The lifeline, not ctx, ends wb-vmd, so that it stops its VM first.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), path)
	cmd.Args = []string{path}
	cmd.Env = []string{slotEnv}
	cmd.Stdin = d.lifeRead
	cmd.Stderr = d.logWrite
	cmd.ExtraFiles = []*os.File{d.grpc, d.handoff}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", path, err)
	}
	d.closeChild()
	s.cmd = cmd
	go s.readLog(d.logRead, stderr, started)
	go func() {
		_ = cmd.Wait()
		close(s.exited)
	}()
	s.log.Info("wb-vmd started", "pid", cmd.Process.Pid, "path", path)
	return nil
}

// readLog prints wb-vmd's log records, and sends the version of its
// started record.
func (s *session) readLog(r *os.File, stderr io.Writer, started chan<- string) {
	defer func() { _ = r.Close() }()
	var once sync.Once
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Bytes()
		fmt.Fprintf(stderr, "wb-vmd: %s\n", line)
		var rec struct {
			Event   string `json:"event"`
			Version string `json:"version"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Event == "started" {
			once.Do(func() { started <- rec.Version })
		}
	}
}

// connect makes the gRPC client, through localipc and the version
// exchange, and the link with the hand-off receiver.
func (s *session) connect(handoff *os.File) error {
	rx, err := vmhandoff.NewReceiver(handoff, s.log)
	if err != nil {
		return err
	}
	creds := grpccreds.New(s.log, s.own)
	cc, err := grpc.NewClient("passthrough:///"+endpoint, grpccreds.ClientOptions(s.log, creds, s.dir, endpoint)...)
	if err != nil {
		_ = rx.Close()
		return err
	}
	s.cc = cc
	s.client = vmdv1.NewVMServiceClient(cc)
	s.link = vmdlink.New(s.client, rx, s.log)
	return nil
}

// waitStarted waits for wb-vmd's started record, and checks that it runs
// the same build version.
func (s *session) waitStarted(ctx context.Context, started <-chan string) error {
	select {
	case v := <-started:
		if v != s.own {
			return fmt.Errorf("wb-vmd is version %s and wb-dev-vm %s: build both with mise run go:build ::: swift:sign", version.Label(v), version.Label(s.own))
		}
		return nil
	case <-s.exited:
		return errors.New("wb-vmd exited before it started: its log above says why")
	case <-time.After(startedTimeout):
		return fmt.Errorf("wb-vmd logged no started record within %s", startedTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// boot starts the VM and waits until it runs.
func (s *session) boot(ctx context.Context, f *vmFlags) error {
	_, err := s.client.Start(ctx, &vmdv1.StartRequest{Generation: generation, CpuCount: uint32(min(f.cpuCount, math.MaxUint32)), MemoryMib: f.memoryMiB})
	if err != nil {
		return fmt.Errorf("start the VM: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, f.wait)
	defer cancel()
	return s.watch(waitCtx, func(st *vmdv1.VMState) bool { return st.GetState() == vmdv1.State_STATE_RUNNING })
}

// watch prints each state until done returns true, the VM stops or
// fails, or ctx ends, which returns ctx's error.
func (s *session) watch(ctx context.Context, done func(*vmdv1.VMState) bool) error {
	stream, err := s.client.WatchState(ctx, &vmdv1.WatchStateRequest{})
	if err != nil {
		return err
	}
	for {
		resp, err := stream.Recv()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("watch the VM: %w", err)
		}
		st := resp.GetState()
		fmt.Fprintf(s.out, "state: %s (generation %d)\n", st.GetState(), st.GetGeneration())
		if done(st) {
			return nil
		}
		if st.GetState() == vmdv1.State_STATE_ERROR || st.GetState() == vmdv1.State_STATE_STOPPED && st.GetGeneration() == generation {
			return fmt.Errorf("the VM is %s: %s", st.GetState(), st.GetError().GetDescription())
		}
	}
}

// close closes the lifeline, which makes wb-vmd stop its VM at once and
// exit (S04-07-supervision, "When `wb-hostd` stops"), waits for it, and
// removes the run directory.
func (s *session) close() error {
	_ = s.life.Close()
	var err error
	select {
	case <-s.exited:
	case <-time.After(exitTimeout):
		_ = s.cmd.Process.Kill()
		<-s.exited
		err = fmt.Errorf("wb-vmd didn't exit within %s of its lifeline closing, and was killed", exitTimeout)
	}
	if s.link != nil {
		_ = s.link.Close()
	}
	if s.cc != nil {
		_ = s.cc.Close()
	}
	_ = os.RemoveAll(s.dir)
	s.log.Info("wb-vmd ended", "state", s.cmd.ProcessState.String())
	return err
}
