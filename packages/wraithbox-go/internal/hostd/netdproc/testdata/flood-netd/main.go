// Command flood-netd is a wb-netd for netdproc's tests. It takes the
// start interface and the start message as wb-netd does, counts one
// refused frame in a run, and sends event records until its event
// channel refuses new work. At the stop it ends its VM's runs on the
// channel and closes it as cmd/wb-netd does, with the same timeout, so
// the open run reaches wb-hostd only when wb-hostd reads it. Built with
// -X main.mode=ignore-stop, it ignores the stop instead, and keeps
// sending until it is killed.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/hostlink"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// closeTimeout is cmd/wb-netd's.
const closeTimeout = 3 * time.Second

const network = auditv1.EventClass_EVENT_CLASS_NETWORK_ACTIVITY

// mode is set by the linker: "ignore-stop", or empty.
var mode string

func event() *auditv1.Event {
	return &auditv1.Event{
		Class: network, Decision: "allow", Rule: "egress-allow", DestinationHost: "github.com", DestinationPort: 443,
	}
}

type policies struct{}

func (policies) ApplyPolicy(*policy.Policy) {}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("flood-netd failed", "error", err.Error())
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	control, err := fdchan.New(os.NewFile(3, "control"), egressmsg.MaxRecord)
	if err != nil {
		return err
	}
	events, err := fdchan.New(os.NewFile(6, "events"), eventchan.MaxRecord)
	if err != nil {
		return err
	}
	ctl, err := hostlink.NewControl(hostlink.ControlConfig{
		Conn: control, Own: nt.Own, Log: log,
		Handoff: handoff.New(handoff.Config{Own: nt.Own, Log: log}), Policy: policies{},
	})
	if err != nil {
		return err
	}
	kept, err := ctl.Start()
	if err != nil {
		return err
	}
	vm := eventchan.VM{Slot: eventchan.SlotOf(kept.VM), Generation: kept.Generation}
	sender, err := eventchan.New(eventchan.Config{Conn: events, Logger: log, Own: &vm})
	if err != nil {
		return err
	}
	served := make(chan error, 1)
	go func() { served <- ctl.Serve(context.Background()) }()
	sender.CountRun(vm, network, "drop", "frame-ethertype")
	sent := 0
	for sender.Admit(vm, network) == "" {
		sender.Emit(vm, event())
		sent++
	}
	log.Info("flood-netd blocked", "records", sent, "queued", sender.Queued())
	err = <-served
	log.Info("flood-netd stopping", "reason", err.Error(), "mode", mode)
	if mode == "ignore-stop" {
		ignoreStop(sender, vm)
	}
	sender.EndVM(vm)
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	return sender.Close(ctx)
}

// ignoreStop keeps the sender's queue full until the process is killed,
// so every write the receiver lets through is taken at once.
func ignoreStop(sender *eventchan.Sender, vm eventchan.VM) {
	for {
		if sender.Queued() < eventchan.DefaultQueueCap {
			sender.Emit(vm, event())
			continue
		}
		time.Sleep(time.Millisecond)
	}
}
