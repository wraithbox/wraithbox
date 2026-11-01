package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/vmhandoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdspawn"
)

const (
	// generation is the VM generation of the one start of each run.
	// Generations start at 1 (proto/wraithbox/vmd/v1).
	generation = 1

	// slot is wb-vmd's slot, its only environment (S04-04-vm-processes,
	// "The slot").
	slot = "work"

	// endpoint is the name of wb-vmd's gRPC socket in the run directory.
	endpoint = "vmd"

	// startedTimeout bounds the wait for wb-vmd's started record.
	startedTimeout = 10 * time.Second
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
	proc   *vmdspawn.Process
	client vmdv1.VMServiceClient
	link   *vmdlink.Link
	// transport is the host end of the VM's packet transport. Nothing
	// reads it: the VM has a network device and no network.
	transport *vmhandoff.Transport
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
	started := make(chan string, 1)
	proc, err := vmdspawn.Start(ctx, vmdspawn.Config{
		Program: vmdPath(), Slot: slot, RunDir: dir, Endpoint: endpoint, Version: own,
		Log:    func(r io.Reader) { readLog(r, stderr, started) },
		Logger: log,
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	s := &session{log: log, out: out, own: own, dir: dir, proc: proc, client: proc.Client, link: proc.Link}
	if err := s.waitStarted(ctx, started); err != nil {
		_ = s.close()
		return nil, err
	}
	return s, nil
}

// readLog prints wb-vmd's log records, and sends the version of its
// started record.
func readLog(r io.Reader, stderr io.Writer, started chan<- string) {
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

// waitStarted waits for wb-vmd's started record, and checks that it runs
// the same build version.
func (s *session) waitStarted(ctx context.Context, started <-chan string) error {
	select {
	case v := <-started:
		if v != s.own {
			return fmt.Errorf("wb-vmd is version %s and wb-dev-vm %s: build both with mise run go:build ::: swift:sign", version.Label(v), version.Label(s.own))
		}
		return nil
	case <-s.proc.Exited():
		return errors.New("wb-vmd exited before it started: its log above says why")
	case <-time.After(startedTimeout):
		return fmt.Errorf("wb-vmd logged no started record within %s", startedTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// boot starts the VM with a new MAC address and waits until it runs.
func (s *session) boot(ctx context.Context, f *vmFlags) error {
	t, err := s.link.Start(ctx, &vmdv1.StartRequest{
		Generation: generation, CpuCount: uint32(min(f.cpuCount, math.MaxUint32)), MemoryMib: f.memoryMiB,
		MacAddress: newMAC(),
	})
	if err != nil {
		return fmt.Errorf("start the VM: %w", err)
	}
	s.transport = t
	waitCtx, cancel := context.WithTimeout(ctx, f.wait)
	defer cancel()
	return s.watch(waitCtx, func(st *vmdv1.VMState) bool { return st.GetState() == vmdv1.State_STATE_RUNNING })
}

// newMAC is a random, locally administered unicast MAC address, the
// form wb-vmd accepts (S06-06-devices-and-warm-start, "Devices"). Each
// run of wb-dev-vm gets a new one: it keeps no state.
func newMAC() []byte {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	b[0] = b[0]&^0x01 | 0x02
	return b
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
	err := s.proc.Close()
	if s.transport != nil {
		_ = s.transport.Close()
	}
	_ = os.RemoveAll(s.dir)
	s.log.Info("wb-vmd ended", "state", s.proc.ProcessState().String())
	return err
}
