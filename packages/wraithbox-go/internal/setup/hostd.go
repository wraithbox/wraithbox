package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"syscall"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
)

// HostdKind is the answer of a running wb-hostd, or of none.
type HostdKind int

const (
	// HostdNotListening: nothing listens at wb-hostd's endpoint, so no VM
	// runs, because VMs stop with wb-hostd (S04-07-supervision, "When
	// `wb-hostd` stops").
	HostdNotListening HostdKind = iota
	// HostdOtherVersion: a wb-hostd of another build version answered.
	// It refuses wb (S04-08-records-and-bounds, "Version skew"), so wb
	// can't read its VMs.
	HostdOtherVersion
	// HostdSameVersion: a wb-hostd of wb's build version answered.
	HostdSameVersion
)

// VM is a running VM and its sessions, as wb-hostd names them.
type VM struct {
	Name     string
	Sessions []string
}

// HostdState is what wb setup learned from wb-hostd.
type HostdState struct {
	Kind HostdKind
	// Peer is the other build's version, escaped for printing
	// (HostdOtherVersion).
	Peer string
	// Listed is whether wb-hostd gave its VM list, and VMs is that list
	// (HostdSameVersion). wb-hostd serves no VM list until M2.7, so the
	// client leaves Listed false.
	Listed bool
	VMs    []VM
}

// Hostd asks the running wb-hostd, if any, which VMs run. An error
// means wb setup can't tell, and it refuses (S17-02-setup, "The agent").
type Hostd interface {
	State(ctx context.Context) (HostdState, error)
}

// hostdTimeout bounds the whole query: the connect and the version
// exchange.
const hostdTimeout = 10 * time.Second

// hostdClient is the Hostd that dials wb-hostd's local IPC endpoint and
// runs the build version exchange (S04-08-records-and-bounds, "Between
// host processes").
type hostdClient struct {
	log *slog.Logger
	run string
	own string
}

// NewHostd returns the Hostd that asks the wb-hostd listening in run,
// the run directory of the paths package, with the build version own.
func NewHostd(log *slog.Logger, run, own string) Hostd {
	return &hostdClient{log: log, run: run, own: own}
}

// State dials wb-hostd and runs the version exchange:
//
//   - No socket, or a refused connect while no listener holds the
//     endpoint's lock: HostdNotListening.
//   - A version refusal that read the peer's version: HostdOtherVersion.
//   - The same version: HostdSameVersion, not Listed.
//
// Everything else is an error: a refused connect while a listener holds
// the lock (a full backlog), a failed peer check, an unsafe run
// directory, a timeout, a malformed record. wb setup refuses on an
// error, because a wrong "nothing runs" stops running VMs and loses
// their sessions.
func (c *hostdClient) State(ctx context.Context) (HostdState, error) {
	ctx, cancel := context.WithTimeout(ctx, hostdTimeout)
	defer cancel()
	conn, err := localipc.Dial(ctx, c.log, c.run, localipc.HostdEndpoint)
	if err != nil {
		return c.notListening(err)
	}
	peer, err := hostversion.Exchange(ctx, c.log, conn, c.own)
	if r, ok := errors.AsType[*hostversion.Refusal](err); ok && otherBuild(r.Rule) {
		return HostdState{Kind: HostdOtherVersion, Peer: r.PeerLabel()}, nil
	}
	if err != nil {
		return HostdState{}, fmt.Errorf("the version exchange with wb-hostd: %w", err)
	}
	_ = conn.Close()
	c.log.LogAttrs(ctx, slog.LevelInfo, "wb-hostd answered with this build's version", slog.String("peer_version", peer))
	return HostdState{Kind: HostdSameVersion}, nil
}

// notListening tells a dial error that means no wb-hostd listens from
// any other. A missing socket or run directory is not listening. So is
// a refused connect, but only when no listener holds the endpoint's
// lock: a live listener with a full backlog refuses a connect too.
func (c *hostdClient) notListening(dialErr error) (HostdState, error) {
	if !errors.Is(dialErr, fs.ErrNotExist) && !errors.Is(dialErr, syscall.ECONNREFUSED) {
		return HostdState{}, fmt.Errorf("dial wb-hostd: %w", dialErr)
	}
	held, err := localipc.LockHeld(c.log, c.run, localipc.HostdEndpoint)
	if err != nil {
		return HostdState{}, fmt.Errorf("dial wb-hostd: %w; and its endpoint's lock can't be checked: %w", dialErr, err)
	}
	if held {
		return HostdState{}, fmt.Errorf("dial wb-hostd: %w, while a wb-hostd holds its endpoint", dialErr)
	}
	return HostdState{Kind: HostdNotListening}, nil
}

// otherBuild reports whether a version refusal read a peer of another
// build: another stamped version, an unstamped one, or one outside the
// version form.
func otherBuild(rule string) bool {
	switch rule {
	case hostversion.RuleMismatch, hostversion.RuleUnstamped, hostversion.RuleForm:
		return true
	}
	return false
}
