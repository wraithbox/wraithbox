package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/dns"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/hostlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/stack"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/synth"
)

// closeTimeout bounds the flush of the event channel at the end. It
// outlasts eventchan.BlockAfter, so a record whose first write found the
// channel full is still written.
const closeTimeout = 3 * time.Second

// The rules of wb-netd's end (S04-09-netd-start, "Start interface").
const (
	// ruleStopped: wb-hostd closed the control channel, and wb-netd
	// exits 0.
	ruleStopped = "netd-stopped"
	// ruleFailed: the start, the control channel or the packet transport
	// failed or ended, and wb-netd exits 1.
	ruleFailed = "netd-failed"
)

// daemon is one VM's wb-netd once its start message is read.
type daemon struct {
	log     *slog.Logger
	ch      *channels
	link    *gate
	sender  *handoff.Sender
	control *hostlink.Control
	acct    *hostlink.Accounting
	events  *eventchan.Sender
	vm      eventchan.VM
	pool    *synth.Pool
	dns     *dns.Resolver
	stack   *stack.Stack
}

// serve starts the drain, reads the start message, builds the stack and
// serves until the control channel or the transport ends. It returns
// the exit status.
func serve(ctx context.Context, log *slog.Logger, ch *channels, own string) int {
	d := &daemon{log: log, ch: ch}
	defer d.close()
	d.link = newGate(ch.transport, netd.DefaultMTU, netd.NewRecorder(log, nil))
	pol := &policies{}
	kept, err := d.start(own, pol)
	if err != nil {
		d.fail(ctx, "start", err)
		return 1
	}
	if err := d.build(own, kept, pol); err != nil {
		d.fail(ctx, "start", err)
		return 1
	}
	return d.run(ctx)
}

// start reads the start message. The hand-off sockets and the policies
// that come later on the control channel go to the Sender and to pol.
func (d *daemon) start(own string, pol *policies) (hostlink.Kept, error) {
	d.sender = handoff.New(handoff.Config{Own: own, Log: d.log})
	var err error
	d.control, err = hostlink.NewControl(hostlink.ControlConfig{
		Conn: d.ch.control, Own: own, Log: d.log, Handoff: d.sender, Policy: pol,
	})
	if err != nil {
		return hostlink.Kept{}, err
	}
	return d.control.Start()
}

// build makes the accounting writer, the event channel, the pool, the
// DNS server and the stack of the VM the start message named.
func (d *daemon) build(own string, kept hostlink.Kept, pol *policies) error {
	var err error
	d.acct, err = hostlink.NewAccounting(hostlink.AccountingConfig{Conn: d.ch.accounting, Own: own, Log: d.log})
	if err != nil {
		return err
	}
	d.vm = eventchan.VM{Slot: eventchan.SlotOf(kept.VM), Generation: kept.Generation}
	d.events, err = eventchan.New(eventchan.Config{Conn: d.ch.events, Logger: d.log, Own: &d.vm})
	if err != nil {
		return err
	}
	ev := runEvents{s: d.events, vm: d.vm}
	host := hostState{s: d.events, vm: d.vm}
	d.pool, err = synth.New(netd.DefaultSynthetic, kept.Entries, synth.Deps{
		Accounting: d.acct, Reports: d.sender, Ports: pol, HandOff: d.sender, Log: d.log, Events: ev,
	})
	if err != nil {
		return err
	}
	d.dns, err = dns.New(dns.Config{
		Addresses: d.pool, Accounting: d.acct, Names: pol, Host: host,
		Debited: kept.Debited, DebitCount: kept.DebitCount, Log: d.log, Events: ev,
	})
	if err != nil {
		return err
	}
	cfg := netd.Config{
		GuestMAC: kept.Link.GuestMAC, GatewayMAC: kept.Link.GatewayMAC,
		Gateway: kept.Link.Gateway, Leased: kept.Link.Leased, Subnet: kept.Link.Subnet,
		Synthetic: netd.DefaultSynthetic, MTU: netd.DefaultMTU, Caps: netd.DefaultCaps(),
	}
	// No DNS over TCP (S07-03-dns): a nil hook resets it with
	// gateway-port-refused.
	d.stack, err = stack.New(cfg, d.link, netd.Hooks{
		Stream: netd.HostStateStreams{Policy: d.pool, Host: host}, DNSOverUDP: d.dns, Events: ev, Log: d.log,
	})
	return err
}

// run opens the link to the stack and serves until wb-hostd closes the
// control channel, the channel fails, or the transport does. It returns
// 0 only when wb-hostd closed the control channel.
func (d *daemon) run(ctx context.Context) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.link.open()
	stackDone := make(chan error, 1)
	controlDone := make(chan error, 1)
	poolDone := make(chan struct{})
	go func() {
		defer close(poolDone)
		d.pool.Run(ctx)
	}()
	go func() { stackDone <- d.stack.Run(ctx) }()
	go func() { controlDone <- d.control.Serve(ctx) }()
	var end, after error
	stopped := false
	select {
	case end = <-controlDone:
		// Only wb-hostd closing the control channel is a stop
		// (S04-09-netd-start, "Start interface").
		stopped = errors.Is(end, io.EOF)
		cancel()
		after = <-stackDone
	case err := <-stackDone:
		// The transport's end, an EOF included, is a failure: the VM
		// lost its network while wb-hostd still runs it.
		end = fmt.Errorf("the packet transport ended: %w", err)
		cancel()
		after = <-controlDone
	}
	<-poolDone
	if after != nil {
		// The other half's end after the cancel, kept for the log only.
		d.log.LogAttrs(ctx, slog.LevelDebug, "wb-netd half stopped", slog.String("reason", after.Error()))
	}
	if stopped {
		d.log.LogAttrs(ctx, slog.LevelInfo, "wb-netd stopped",
			slog.String("rule", ruleStopped), slog.String("decision", "stop"),
			slog.String("reason", end.Error()))
		return 0
	}
	d.fail(ctx, "serve", end)
	return 1
}

// fail logs why wb-netd ends early.
func (d *daemon) fail(ctx context.Context, step string, err error) {
	d.log.LogAttrs(ctx, slog.LevelError, "wb-netd failed",
		slog.String("rule", ruleFailed), slog.String("decision", "exit"),
		slog.String("step", step), slog.String("reason", err.Error()))
}

// close ends the VM's runs on the event channel and flushes it, then
// closes every channel. Each part may be missing after an early failure.
func (d *daemon) close() {
	if d.dns != nil {
		d.dns.Close()
	}
	if d.events != nil {
		// The open runs go to wb-hostd for the VM's stop record
		// (S09-11-audit, "Runs with a count").
		d.events.EndVM(d.vm)
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		_ = d.events.Close(ctx)
		cancel()
	}
	if d.acct != nil {
		_ = d.acct.Close()
	}
	if d.sender != nil {
		d.sender.Close()
	}
	// The stack closes the transport when its Run returns. A stack that
	// never ran left it open, and so did a start that failed.
	_ = d.link.Close()
	d.ch.close()
}
