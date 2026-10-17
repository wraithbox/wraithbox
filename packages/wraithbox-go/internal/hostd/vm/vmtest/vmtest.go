// Package vmtest has an in-memory VM provider and a copying cloner for
// tests of wb-hostd's VM manager and of what is built on it. No VM runs.
package vmtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// stateBuffer is how many states an instance holds that the manager
// hasn't read yet. A test that publishes more fails with a panic, rather
// than blocking.
const stateBuffer = 64

// Provider is an in-memory vm.Provider. Each Open makes a new Instance,
// whose first state is generation 0, stopped, as wb-vmd's is. By
// default a Start publishes STARTING and RUNNING for its generation, a
// Stop of either mode publishes STOPPED, and ConnectGuest returns one
// end of a net.Pipe.
//
// Set the fields before the first call. The hooks replace the defaults.
type Provider struct {
	// OpenErr, when set, is Open's error.
	OpenErr error
	// OnStart, OnStop and OnConnect replace the defaults.
	OnStart   func(*Instance, *vmdv1.StartRequest) error
	OnStop    func(*Instance, *vmdv1.StopRequest) error
	OnConnect func(i *Instance, generation uint64, port uint32) (net.Conn, error)

	mu        sync.Mutex
	instances []*Instance
}

// Open implements vm.Provider.
func (p *Provider) Open(_ context.Context, slot vm.Slot) (vm.Instance, error) {
	if p.OpenErr != nil {
		return nil, p.OpenErr
	}
	i := &Instance{slot: slot, p: p, states: make(chan *vmdv1.VMState, stateBuffer)}
	i.Publish(0, vmdv1.State_STATE_STOPPED, nil)
	p.mu.Lock()
	p.instances = append(p.instances, i)
	p.mu.Unlock()
	return i, nil
}

// Instances returns every instance opened, in order.
func (p *Provider) Instances() []*Instance {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*Instance(nil), p.instances...)
}

// Last returns the last instance opened, nil when there is none.
func (p *Provider) Last() *Instance {
	all := p.Instances()
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
}

// Instance is one in-memory VM run.
type Instance struct {
	slot   vm.Slot
	p      *Provider
	states chan *vmdv1.VMState

	mu       sync.Mutex
	closed   bool
	starts   []*vmdv1.StartRequest
	stops    []*vmdv1.StopRequest
	connects []Connect
}

// Connect is one ConnectGuest call.
type Connect struct {
	Generation uint64
	Port       uint32
}

// Slot returns the slot the instance was opened for.
func (i *Instance) Slot() vm.Slot { return i.slot }

// Start implements vm.Instance.
func (i *Instance) Start(_ context.Context, req *vmdv1.StartRequest) error {
	i.mu.Lock()
	i.starts = append(i.starts, req)
	i.mu.Unlock()
	if i.p.OnStart != nil {
		return i.p.OnStart(i, req)
	}
	i.Publish(req.GetGeneration(), vmdv1.State_STATE_STARTING, nil)
	i.Publish(req.GetGeneration(), vmdv1.State_STATE_RUNNING, nil)
	return nil
}

// Stop implements vm.Instance.
func (i *Instance) Stop(_ context.Context, req *vmdv1.StopRequest) error {
	i.mu.Lock()
	i.stops = append(i.stops, req)
	i.mu.Unlock()
	if i.p.OnStop != nil {
		return i.p.OnStop(i, req)
	}
	i.Publish(req.GetGeneration(), vmdv1.State_STATE_STOPPED, nil)
	return nil
}

// ConnectGuest implements vm.Instance.
func (i *Instance) ConnectGuest(_ context.Context, generation uint64, port uint32) (net.Conn, error) {
	i.mu.Lock()
	i.connects = append(i.connects, Connect{Generation: generation, Port: port})
	i.mu.Unlock()
	if i.p.OnConnect != nil {
		return i.p.OnConnect(i, generation, port)
	}
	host, guest := net.Pipe()
	_ = guest.Close()
	return host, nil
}

// States implements vm.Instance.
func (i *Instance) States() <-chan *vmdv1.VMState { return i.states }

// Close implements vm.Instance: the lifeline closes, and wb-vmd exits.
func (i *Instance) Close() error {
	i.end()
	return nil
}

// Exit ends the instance from its side, as a wb-vmd that exits.
func (i *Instance) Exit() { i.end() }

func (i *Instance) end() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.closed {
		i.closed = true
		close(i.states)
	}
}

// Publish sends a state, as wb-vmd's WatchState does. After the
// instance ended it does nothing.
func (i *Instance) Publish(generation uint64, state vmdv1.State, vmErr *vmdv1.VMError) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return
	}
	select {
	case i.states <- &vmdv1.VMState{Generation: generation, State: state, Error: vmErr}:
	default:
		panic(fmt.Sprintf("vmtest: more than %d states not read", stateBuffer))
	}
}

// Closed reports whether the instance ended.
func (i *Instance) Closed() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.closed
}

// Starts returns the start requests, in order.
func (i *Instance) Starts() []*vmdv1.StartRequest {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]*vmdv1.StartRequest(nil), i.starts...)
}

// Stops returns the stop requests, in order.
func (i *Instance) Stops() []*vmdv1.StopRequest {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]*vmdv1.StopRequest(nil), i.stops...)
}

// Connects returns the ConnectGuest calls, in order.
func (i *Instance) Connects() []Connect {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]Connect(nil), i.connects...)
}

// CopyCloner is a vm.Cloner that copies. It keeps the contract of the
// macOS clone: it creates dst exclusively, so it fails on anything at
// dst, a symbolic link included, and doesn't follow a link at src.
type CopyCloner struct {
	mu    sync.Mutex
	calls int
}

// Clone implements vm.Cloner.
func (c *CopyCloner) Clone(src, dst string) error {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("vmtest: %s isn't a regular file", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	// O_CREATE with O_EXCL fails on any name at dst, and doesn't follow
	// a symbolic link there.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

// Calls returns how many clones were asked for.
func (c *CopyCloner) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// ClonerFunc is a vm.Cloner from a function.
type ClonerFunc func(src, dst string) error

// Clone implements vm.Cloner.
func (f ClonerFunc) Clone(src, dst string) error { return f(src, dst) }
