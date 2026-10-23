// Package vmdprovider is wb-hostd's VM provider on macOS: each Open
// starts a new wb-vmd through internal/vmdspawn, and each Instance
// speaks wraithbox.vmd.v1 to it on its own socket, with guest
// connections through its hand-off receiver (S04-02-vm-provider;
// S04-04-vm-processes, "One `wb-vmd` per VM").
//
// Everything wb-vmd sends is data a possibly compromised wb-vmd chose:
// its rules and texts go to the manager as a *vm.ProviderRefusal, which
// checks and bounds them, and its states go to the manager, which reads
// only the generation's own. The client's receive limits are set in
// vmdspawn.
//
// It has no OS code. On a host without wb-vmd's hand-off channel, Open
// fails with vmdspawn's error, which wraps errors.ErrUnsupported.
package vmdprovider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/logpipe"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdspawn"
)

// errorDomain is the ErrorInfo domain of wb-vmd's refusals
// (proto/wraithbox/vmd/v1).
const errorDomain = "wb-vmd"

// OpenTimeout bounds Open's wait for wb-vmd's first state, which is
// also the wait for its version exchange.
const OpenTimeout = 30 * time.Second

// stateBuffer is how many states an instance holds that the manager
// hasn't read yet. A full buffer holds up the reading of more.
const stateBuffer = 16

// Config is what the provider runs with.
type Config struct {
	// Program is the installed bundle's Contents/MacOS/wb-vmd, never from
	// PATH or <config> (S17-01-bundle).
	Program string
	// RunDir is <data>/run, mode 0700. Each instance's gRPC socket is
	// <RunDir>/vmd-<slot>-<generation>.sock, removed when it ends.
	RunDir string
	// Version is wb-hostd's build version, which wb-vmd must have too
	// (S04-08-records-and-bounds, "Between host processes").
	Version string
	// Logs returns the writer of a process's log lines. wb-vmd's log
	// pipe goes to logpipe as wb-vmd-<slot> (S04-03-self-sandboxing,
	// "Daemon logs").
	Logs func(process string) io.Writer
	// Logger gets the decisions. nil takes slog.Default().
	Logger *slog.Logger
}

// Provider implements vm.Provider. Its methods are safe for concurrent
// use.
type Provider struct {
	cfg Config
	log *slog.Logger

	mu     sync.Mutex
	open   map[*instance]struct{}
	closed bool
}

var _ vm.Provider = (*Provider)(nil)

// New returns a provider. It starts nothing.
func New(cfg Config) (*Provider, error) {
	if !filepath.IsAbs(cfg.Program) || !filepath.IsAbs(cfg.RunDir) {
		return nil, fmt.Errorf("vmdprovider: the program %q and the run directory %q must be absolute paths", cfg.Program, cfg.RunDir)
	}
	if cfg.Logs == nil {
		return nil, errors.New("vmdprovider: no log files")
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Provider{cfg: cfg, log: log, open: make(map[*instance]struct{})}, nil
}

// Open implements vm.Provider: it starts a wb-vmd for slot, with the
// slot as its only environment, and returns once wb-vmd has passed the
// version exchange and sent its first state (SEC08-proj-isolation: each
// wb-vmd gets only its slot and its own channels).
func (p *Provider) Open(ctx context.Context, slot vm.Slot, generation uint64) (vm.Instance, error) {
	process := "wb-vmd-" + string(slot)
	w := p.cfg.Logs(process)
	proc, err := vmdspawn.Start(ctx, vmdspawn.Config{
		Program: p.cfg.Program, Slot: string(slot), RunDir: p.cfg.RunDir,
		Endpoint: fmt.Sprintf("vmd-%s-%d", slot, generation), Version: p.cfg.Version,
		Log:    func(r io.Reader) { _ = logpipe.Copy(r, w, process) },
		Logger: p.log,
	})
	if err != nil {
		return nil, err
	}
	watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	i := &instance{
		slot: slot, proc: proc, log: p.log, cancel: cancel, p: p,
		states: make(chan *vmdv1.VMState, stateBuffer), followed: make(chan struct{}),
	}
	stream, first, err := i.watch(watchCtx)
	if err != nil {
		cancel()
		_ = proc.Close()
		if r := proc.VersionRefusal(); r != nil {
			return nil, fmt.Errorf("wb-vmd was refused: %w", r)
		}
		return nil, fmt.Errorf("wb-vmd sent no state: %w", err)
	}
	if !p.add(i) {
		cancel()
		_ = proc.Close()
		return nil, errors.New("vmdprovider: the provider is closed")
	}
	i.states <- first
	go i.follow(watchCtx, stream)
	return i, nil
}

func (p *Provider) add(i *instance) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.open[i] = struct{}{}
	return true
}

func (p *Provider) remove(i *instance) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.open, i)
}

// Close closes every open instance, which closes each wb-vmd's
// lifeline (S04-07-supervision, "When `wb-hostd` stops"), and makes
// every later Open fail.
func (p *Provider) Close() error {
	p.mu.Lock()
	p.closed = true
	all := make([]*instance, 0, len(p.open))
	for i := range p.open {
		all = append(all, i)
	}
	p.mu.Unlock()
	var wg sync.WaitGroup
	errs := make([]error, len(all))
	for n, i := range all {
		wg.Go(func() { errs[n] = i.Close() })
	}
	wg.Wait()
	return errors.Join(errs...)
}

// instance is one wb-vmd.
type instance struct {
	slot   vm.Slot
	proc   *vmdspawn.Process
	log    *slog.Logger
	p      *Provider
	cancel context.CancelFunc
	states chan *vmdv1.VMState
	// followed is closed once follow has closed states.
	followed  chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// watch opens WatchState and reads the first state within OpenTimeout.
func (i *instance) watch(ctx context.Context) (vmdv1.VMService_WatchStateClient, *vmdv1.VMState, error) {
	timer := time.AfterFunc(OpenTimeout, i.cancel)
	defer timer.Stop()
	stream, err := i.proc.Client.WatchState(ctx, &vmdv1.WatchStateRequest{})
	if err != nil {
		return nil, nil, err
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, nil, err
	}
	return stream, resp.GetState(), nil
}

// follow passes on each state until the stream ends, then ends wb-vmd
// and closes States. A stream that ends while wb-vmd may still run
// leaves its VM's state unknown, so wb-vmd is ended too: an instance
// without its states is never kept (S04-07-supervision, "`wb-vmd` and
// `wb-vmd-install`"). ctx is cancelled by Close, whose end is no
// decision of follow's.
func (i *instance) follow(ctx context.Context, stream vmdv1.VMService_WatchStateClient) {
	defer close(i.followed)
	defer close(i.states)
	var err error
	for {
		var resp *vmdv1.WatchStateResponse
		if resp, err = stream.Recv(); err != nil {
			break
		}
		select {
		case i.states <- resp.GetState():
		case <-i.proc.Exited():
		}
	}
	select {
	case <-i.proc.Exited():
	case <-ctx.Done():
	default:
		i.log.LogAttrs(ctx, slog.LevelWarn, "wb-vmd ended: its state stream ended",
			slog.String("decision", "end"), slog.String("rule", "vmd-state-stream-ended"),
			slog.String("slot", string(i.slot)), slog.Int("pid", i.proc.Pid()), slog.String("error", err.Error()))
	}
	_ = i.proc.Close()
}

// Start implements vm.Instance. A refused start keeps wb-vmd's rule.
// After a failed start, wb-vmd has published the framework's error with
// the generation's ERROR state, and GetState returns it.
func (i *instance) Start(ctx context.Context, req *vmdv1.StartRequest) error {
	_, err := i.proc.Client.Start(ctx, req)
	if err == nil {
		return nil
	}
	pr := refusal(err)
	if pr == nil {
		return err
	}
	resp, gerr := i.proc.Client.GetState(ctx, &vmdv1.GetStateRequest{})
	if st := resp.GetState(); gerr == nil && st.GetGeneration() == req.GetGeneration() {
		pr.VMError = st.GetError()
	}
	return pr
}

// Stop implements vm.Instance.
func (i *instance) Stop(ctx context.Context, req *vmdv1.StopRequest) error {
	_, err := i.proc.Client.Stop(ctx, req)
	if pr := refusal(err); pr != nil {
		return pr
	}
	return err
}

// States implements vm.Instance.
func (i *instance) States() <-chan *vmdv1.VMState { return i.states }

// ConnectGuest implements vm.Instance through vmdlink. A refusal keeps
// vmdlink's rule.
func (i *instance) ConnectGuest(ctx context.Context, generation uint64, port uint32) (net.Conn, error) {
	conn, err := i.proc.Link.ConnectGuest(ctx, generation, port)
	if r, ok := errors.AsType[*vmdlink.Refusal](err); ok {
		return nil, &vm.ProviderRefusal{Rule: r.Rule, Detail: r.Error()}
	}
	return conn, err
}

// Close implements vm.Instance: it closes wb-vmd's lifeline, waits for
// it to exit, and waits until States is closed.
func (i *instance) Close() error {
	i.closeOnce.Do(func() {
		i.cancel()
		i.closeErr = i.proc.Close()
		<-i.followed
		i.p.remove(i)
	})
	return i.closeErr
}

// refusal reads wb-vmd's rule from the ErrorInfo of a failed call, nil
// when err is nil or carries none.
func refusal(err error) *vm.ProviderRefusal {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return nil
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == errorDomain {
			return &vm.ProviderRefusal{Rule: info.GetReason(), Detail: st.Message()}
		}
	}
	return nil
}
