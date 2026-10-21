package guestconn

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsock"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
)

// The caps on a VM's connections (S04-02-vm-provider, "Caps and
// closing"; S06-11-reconnect-and-recovery, "Reconnect";
// S04-08-records-and-bounds, "Host work the guest can cause", row
// "Host-guest socket connections").
const (
	// MaxConnsPerVM is the most live connections to one VM's guest.
	MaxConnsPerVM = 16
	// MaxConnsPerProject is the most live connections of one project in
	// a VM. The control link belongs to no project.
	MaxConnsPerProject = 4
	// MaxOpensPerWindow is the most connections opened to one VM in any
	// OpenWindow, reconnects and first connections alike.
	MaxOpensPerWindow = 20
	OpenWindow        = time.Minute
)

// The rules of this package.
const (
	// RulePortUnrouted: a connection to a port that isn't in the route
	// table, which holds only ports below 1024. Refused before the VM
	// manager is asked.
	RulePortUnrouted = "guest-port-unrouted"
	// RuleConnCapVM: the VM has MaxConnsPerVM live connections.
	RuleConnCapVM = "guest-conn-cap-vm"
	// RuleConnCapProject: the project has MaxConnsPerProject live
	// connections in the VM.
	RuleConnCapProject = "guest-conn-cap-project"
	// RuleReconnectRate: the VM had MaxOpensPerWindow connections opened
	// in the last OpenWindow. The control link waits for the window
	// instead, and logs this once per window.
	RuleReconnectRate = "guest-reconnect-rate"
	// RuleConnStale: the run the connection was opened for ended while it
	// was opened. It is closed at once.
	RuleConnStale = "guest-conn-stale"
	// RuleLinkDropped: the control link's connection left READY, and the
	// link connects again.
	RuleLinkDropped = "guest-link-dropped"
)

// route is a guest port wb-hostd connects to, and what for. A connection
// goes to the code that asked for its port, never to one that the
// connection names (S04-02-vm-provider, "The host opens every
// connection").
type route struct {
	name string
}

// routes are the guest ports wb-hostd connects to, every one below 1024,
// which only root in the guest can bind (S04-02-vm-provider, "Guest
// ports below 1024"). M2 has wb-guestd's control link only.
var routes = map[uint32]route{
	guestsock.Port: {name: "wb-guestd"},
}

// Refusal is a refused connection, with its rule.
type Refusal struct {
	Rule    string
	Slot    vm.Slot
	Message string
}

func (r *Refusal) Error() string { return r.Message + " (" + r.Rule + ")" }

// vmConns is one VM slot's connections, over all of its runs.
type vmConns struct {
	slot vm.Slot
	now  func() time.Time
	// log writes the VM's records, a refusal at most once per rule per
	// ratelog.Window. rateLog writes RuleReconnectRate at most once per
	// OpenWindow.
	log     *slog.Logger
	rateLog *slog.Logger

	mu       sync.Mutex
	live     map[*conn]struct{}
	reserved int
	projects map[string]int
	// opened holds the times of the opens in the last OpenWindow, oldest
	// first.
	opened []time.Time
	// endedUpTo is the newest generation whose run has ended. Runs of a
	// slot end in order, so every older one has ended too.
	endedUpTo uint64
}

func newVMConns(slot vm.Slot, base *slog.Logger, now func() time.Time) *vmConns {
	return &vmConns{
		slot: slot, now: now,
		log:      newVMLog(base, slot, now),
		rateLog:  ratelog.NewAt(base.With(slog.String("slot", string(slot))), OpenWindow, now),
		live:     map[*conn]struct{}{},
		projects: map[string]int{},
	}
}

// open opens a connection to port in the guest of generation, for
// project ("" for none), within the caps. Every refusal is logged with
// its rule, and none is queued.
func (ls *Links) open(ctx context.Context, slot vm.Slot, generation uint64, port uint32, project string) (net.Conn, error) {
	vc := ls.vm(slot)
	// SEC08-proj-isolation: only a port of the table, which only guest
	// root can bind, and the connection goes back to this caller only.
	if _, ok := routes[port]; !ok || port >= 1024 {
		return nil, vc.refuse(ctx, RulePortUnrouted,
			fmt.Sprintf("guest port %d of the %s VM isn't one wb-hostd connects to", port, slot))
	}
	// SEC13-bounded-resources: the caps hold before the provider is
	// asked, and a connection over one is refused, never queued.
	if r := vc.reserve(project); r != nil {
		return nil, vc.refuse(ctx, r.Rule, r.Message)
	}
	raw, err := ls.cfg.VMs.ConnectGuest(ctx, slot, generation, port)
	if err != nil {
		vc.release(project)
		return nil, err
	}
	c, ok := vc.track(raw, generation, project)
	if !ok {
		return nil, vc.refuse(ctx, RuleConnStale,
			fmt.Sprintf("generation %d of the %s VM ended while its connection opened", generation, slot))
	}
	return c, nil
}

// reserve takes a place under every cap for one new connection, and
// counts it as opened. It returns the refusal of the first cap that is
// full.
func (vc *vmConns) reserve(project string) *Refusal {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	now := vc.now()
	vc.dropOldLocked(now)
	switch {
	case len(vc.opened) >= MaxOpensPerWindow:
		return &Refusal{Rule: RuleReconnectRate, Slot: vc.slot, Message: fmt.Sprintf(
			"the %s VM had %d connections opened in the last %s", vc.slot, len(vc.opened), OpenWindow)}
	case len(vc.live)+vc.reserved >= MaxConnsPerVM:
		return &Refusal{Rule: RuleConnCapVM, Slot: vc.slot, Message: fmt.Sprintf(
			"the %s VM has %d connections open", vc.slot, MaxConnsPerVM)}
	case project != "" && vc.projects[project] >= MaxConnsPerProject:
		return &Refusal{Rule: RuleConnCapProject, Slot: vc.slot, Message: fmt.Sprintf(
			"the project has %d connections open in the %s VM", MaxConnsPerProject, vc.slot)}
	}
	vc.opened = append(vc.opened, now)
	vc.reserved++
	if project != "" {
		vc.projects[project]++
	}
	return nil
}

// release gives back a reservation whose connection didn't open.
func (vc *vmConns) release(project string) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.reserved--
	vc.releaseProjectLocked(project)
}

func (vc *vmConns) releaseProjectLocked(project string) {
	if project == "" {
		return
	}
	vc.projects[project]--
	if vc.projects[project] <= 0 {
		delete(vc.projects, project)
	}
}

// track turns a reservation into a live connection of generation. When
// that run has ended, it closes raw instead and reports false.
func (vc *vmConns) track(raw net.Conn, generation uint64, project string) (*conn, bool) {
	vc.mu.Lock()
	vc.reserved--
	if generation <= vc.endedUpTo {
		vc.releaseProjectLocked(project)
		vc.mu.Unlock()
		_ = raw.Close()
		return nil, false
	}
	c := &conn{Conn: raw, owner: vc, generation: generation, project: project}
	vc.live[c] = struct{}{}
	vc.mu.Unlock()
	return c, true
}

// dropOldLocked forgets the opens older than OpenWindow.
func (vc *vmConns) dropOldLocked(now time.Time) {
	cut := 0
	for cut < len(vc.opened) && !now.Before(vc.opened[cut].Add(OpenWindow)) {
		cut++
	}
	vc.opened = vc.opened[cut:]
}

// untilRoom is how long until the window has room for one more open: 0
// when it has room now.
func (vc *vmConns) untilRoom() time.Duration {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	now := vc.now()
	vc.dropOldLocked(now)
	if len(vc.opened) < MaxOpensPerWindow {
		return 0
	}
	return vc.opened[len(vc.opened)-MaxOpensPerWindow].Add(OpenWindow).Sub(now)
}

// closeGeneration closes every connection of generation and older, and
// marks those runs ended, so a connection that opens for one later is
// closed at once.
func (vc *vmConns) closeGeneration(generation uint64) {
	vc.mu.Lock()
	vc.endedUpTo = max(vc.endedUpTo, generation)
	var doomed []*conn
	for c := range vc.live {
		if c.generation <= generation {
			doomed = append(doomed, c)
		}
	}
	vc.mu.Unlock()
	for _, c := range doomed {
		_ = c.Close()
	}
}

// closeAll closes every live connection.
func (vc *vmConns) closeAll() {
	vc.mu.Lock()
	doomed := make([]*conn, 0, len(vc.live))
	for c := range vc.live {
		doomed = append(doomed, c)
	}
	vc.mu.Unlock()
	for _, c := range doomed {
		_ = c.Close()
	}
}

// refuse logs a refusal with its rule, and returns it.
func (vc *vmConns) refuse(ctx context.Context, rule, msg string) *Refusal {
	log := vc.log
	if rule == RuleReconnectRate {
		log = vc.rateLog
	}
	log.LogAttrs(ctx, slog.LevelWarn, "guest connection refused",
		slog.String("rule", rule), slog.String("decision", "refuse"), slog.String("reason", msg))
	return &Refusal{Rule: rule, Slot: vc.slot, Message: msg}
}

// conn is a live connection. Its Close gives its place under the caps
// back, once.
type conn struct {
	net.Conn

	owner      *vmConns
	generation uint64
	project    string
	once       sync.Once
}

func (c *conn) Close() error {
	c.once.Do(func() {
		vc := c.owner
		vc.mu.Lock()
		delete(vc.live, c)
		vc.releaseProjectLocked(c.project)
		vc.mu.Unlock()
	})
	return c.Conn.Close()
}
