package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/packetlink"
)

// The descriptors of wb-netd's start interface (S04-09-netd-start,
// "Start interface").
const (
	logFD        = 2
	controlFD    = 3
	transportFD  = 4
	accountingFD = 5
	eventsFD     = 6
)

// The rules of wb-netd's start.
const (
	// ruleArguments: wb-netd takes no argument.
	ruleArguments = "netd-arguments"
	// ruleDescriptorInvalid: a descriptor of the start interface is
	// missing or of the wrong kind.
	ruleDescriptorInvalid = "netd-descriptor-invalid"
)

// channels are the inherited descriptors, each checked.
type channels struct {
	control    *fdchan.Channel
	transport  *packetlink.Transport
	accounting *fdchan.Channel
	events     *fdchan.Channel
}

// close closes each channel that is open.
func (c *channels) close() {
	for _, ch := range []*fdchan.Channel{c.control, c.accounting, c.events} {
		if ch != nil {
			_ = ch.Close()
		}
	}
	if c.transport != nil {
		_ = c.transport.Close()
	}
}

// openDescriptors checks each descriptor of the start interface and
// takes it. fdchan.New and packetlink.New refuse a descriptor that isn't
// a connected AF_UNIX SOCK_DGRAM socket, a closed one included. On an
// error every descriptor it took is closed.
//
// First it refuses any open descriptor above eventsFD: wb-netd inherits
// the start interface's descriptors and no other (S04-09-netd-start,
// "Start interface"; SEC12-least-privilege). The walk comes before
// anything in wb-netd opens a descriptor of its own.
func openDescriptors() (*channels, error) {
	extra, err := fdchan.FirstOpenFrom(eventsFD + 1)
	switch {
	case err != nil:
		return nil, fmt.Errorf("the descriptors above %d: %w", eventsFD, err)
	case extra >= 0:
		return nil, fmt.Errorf("descriptor %d is open, and the start interface ends at %d", extra, eventsFD)
	}
	if _, err := os.NewFile(logFD, "log").Stat(); err != nil {
		return nil, fmt.Errorf("descriptor %d, the log pipe: %w", logFD, err)
	}
	c := &channels{}
	var errs []error
	open := func(fd uintptr, name string, maxRecord int) *fdchan.Channel {
		ch, err := fdchan.New(os.NewFile(fd, name), maxRecord)
		if err != nil {
			errs = append(errs, fmt.Errorf("descriptor %d, the %s: %w", fd, name, err))
		}
		return ch
	}
	c.control = open(controlFD, "control channel", egressmsg.MaxRecord)
	t, err := packetlink.New(os.NewFile(transportFD, "packet transport"), netd.DefaultMTU)
	if err != nil {
		errs = append(errs, fmt.Errorf("descriptor %d, the packet transport: %w", transportFD, err))
	}
	c.transport = t
	c.accounting = open(accountingFD, "accounting channel", egressmsg.MaxRecord)
	c.events = open(eventsFD, "event channel", eventchan.MaxRecord)
	if err := errors.Join(errs...); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}
