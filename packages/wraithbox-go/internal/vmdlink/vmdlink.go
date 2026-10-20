// Package vmdlink is the host's end of one wb-vmd on macOS: its
// VMService client and its hand-off receiver together. ConnectGuest asks
// wb-vmd for a connection to a guest port and waits for the descriptor
// on the hand-off channel (S04-02-vm-provider, "Hand-off on macOS").
// Hello runs wb-guestd's hello on such a connection.
//
// wb-hostd's VM manager and wb-dev-vm use it. A refusal of wb-vmd keeps
// wb-vmd's rule, which is data a possibly compromised wb-vmd chose: it is
// escaped and cut before it is logged or shown.
package vmdlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsock"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/vmhandoff"
)

// HandoffTimeout bounds the wait for the hand-off record once wb-vmd has
// answered ConnectGuest. wb-vmd sends the record before it answers, so
// it is normally there already.
const HandoffTimeout = 5 * time.Second

// RPCTimeout bounds the ConnectGuest call itself: wb-vmd's own connect
// timeout of 5 seconds, and slack for the call and the send
// (S04-02-vm-provider, "Caps and closing"). A wb-vmd that doesn't answer
// can't hold the caller longer.
const RPCTimeout = 10 * time.Second

// FirstUnreservedPort is the lowest guest port the host never connects
// to: every guest port it connects to is below 1024, where only guest
// root can listen (S04-02-vm-provider, "Guest ports below 1024").
const FirstUnreservedPort = 1024

// errorDomain is the ErrorInfo domain of wb-vmd's refusals.
const errorDomain = "wb-vmd"

// maxRuleLen is how much of a rule from wb-vmd goes into a log record or
// an error.
const maxRuleLen = 64

// The rules of this package.
const (
	// RuleConnectRefused: wb-vmd refused ConnectGuest, or the call
	// failed. Refusal.VMDRule holds wb-vmd's own rule, when it gave one.
	RuleConnectRefused = "vmd-connect-refused"
	// RuleHandoffMissing: wb-vmd answered ConnectGuest, and no
	// connection came for the request within HandoffTimeout.
	RuleHandoffMissing = "handoff-missing"
	// RuleGuestPort: a connection to guest port 0, or to one of 1024 or
	// above, refused before wb-vmd is asked. wb-vmd refuses the same
	// ports itself.
	RuleGuestPort = "guest-port-invalid"
)

// Refusal is the error of a refused ConnectGuest.
type Refusal struct {
	Rule string
	// VMDRule is wb-vmd's rule, from the ErrorInfo reason of its error,
	// escaped and cut. It is empty when wb-vmd gave none.
	VMDRule string

	msg string
	err error
}

func (r *Refusal) Error() string {
	s := "vmdlink: " + r.Rule
	if r.VMDRule != "" {
		s += " (" + r.VMDRule + ")"
	}
	return s + ": " + r.msg
}

// Unwrap returns the cause: the gRPC error, or the hand-off's refusal.
func (r *Refusal) Unwrap() error { return r.err }

// Link is the host's end of one wb-vmd.
type Link struct {
	client vmdv1.VMServiceClient
	rx     *vmhandoff.Receiver
	log    *slog.Logger
	ids    atomic.Uint64
	ran    chan error
}

// New makes a link of a VMService client and the hand-off receiver of
// the same wb-vmd, and starts receiving. The link owns rx.
func New(client vmdv1.VMServiceClient, rx *vmhandoff.Receiver, log *slog.Logger) *Link {
	if log == nil {
		log = slog.Default()
	}
	l := &Link{client: client, rx: rx, log: log, ran: make(chan error, 1)}
	go func() { l.ran <- rx.Run() }()
	return l
}

// ConnectGuest opens a connection to port in the guest of generation.
// It opens the request on the receiver first, because wb-vmd sends the
// record before it answers. On a refusal no connection is left open: a
// record that comes for a closed request is refused and its descriptor
// closed.
func (l *Link) ConnectGuest(ctx context.Context, generation uint64, port uint32) (net.Conn, error) {
	id := l.ids.Add(1)
	if port == 0 || port >= FirstUnreservedPort {
		return nil, l.refuse(ctx, id, generation, port, &Refusal{
			Rule: RuleGuestPort,
			msg:  fmt.Sprintf("guest port %d is not 1 to %d", port, FirstUnreservedPort-1),
		})
	}
	pending, err := l.rx.Expect(id, generation, port)
	if err != nil {
		return nil, fmt.Errorf("vmdlink: %w", err)
	}
	rpcCtx, cancelRPC := context.WithTimeout(ctx, RPCTimeout)
	_, err = l.client.ConnectGuest(rpcCtx, &vmdv1.ConnectGuestRequest{Generation: generation, RequestId: id, GuestPort: port})
	cancelRPC()
	if err != nil {
		pending.Cancel()
		return nil, l.refuse(ctx, id, generation, port, rpcRefusal(err))
	}
	waitCtx, cancel := context.WithTimeout(ctx, HandoffTimeout)
	defer cancel()
	conn, err := pending.Wait(waitCtx)
	if err != nil {
		r := &Refusal{Rule: RuleHandoffMissing, msg: err.Error(), err: err}
		if hr, ok := errors.AsType[*vmhandoff.Refusal](err); ok {
			r.Rule = string(hr.Rule)
		}
		return nil, l.refuse(ctx, id, generation, port, r)
	}
	l.log.LogAttrs(ctx, slog.LevelInfo, "guest connection opened",
		slog.String("decision", "accept"), slog.Uint64("request_id", id),
		slog.Uint64("generation", generation), slog.Uint64("guest_port", uint64(port)))
	return conn, nil
}

// Hello connects to wb-guestd's port and runs the hello with own, the
// host's build version. On success the returned link owns the
// connection.
func (l *Link) Hello(ctx context.Context, generation uint64, own string) (*guestlink.Link, error) {
	conn, err := l.ConnectGuest(ctx, generation, guestsock.Port)
	if err != nil {
		return nil, err
	}
	return guestlink.Hello(ctx, conn, own, l.log)
}

// Close closes the receiver, which fails every open request, and waits
// for the receive loop to end. It returns the loop's error.
func (l *Link) Close() error {
	_ = l.rx.Close()
	return <-l.ran
}

// rpcRefusal reads wb-vmd's rule from the ErrorInfo of a failed call.
func rpcRefusal(err error) *Refusal {
	st := status.Convert(err)
	r := &Refusal{Rule: RuleConnectRefused, msg: st.Code().String(), err: err}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == errorDomain {
			r.VMDRule = shown(info.GetReason())
			break
		}
	}
	return r
}

// shown is a rule from wb-vmd as it goes into a log or an error: as it
// is when it has the form of a rule name, and otherwise cut, then quoted
// with every byte outside printable ASCII escaped.
func shown(s string) string {
	if ruleName.MatchString(s) {
		return s
	}
	if len(s) > maxRuleLen {
		s = s[:maxRuleLen]
	}
	return strconv.QuoteToASCII(s)
}

// ruleName is the form of a rule name: lowercase words joined by
// hyphens, at most maxRuleLen bytes.
var ruleName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func (l *Link) refuse(ctx context.Context, id, generation uint64, port uint32, r *Refusal) *Refusal {
	l.log.LogAttrs(ctx, slog.LevelWarn, "guest connection refused",
		slog.String("rule", r.Rule), slog.String("vmd_rule", r.VMDRule),
		slog.String("decision", "refuse"), slog.String("reason", r.msg),
		slog.Uint64("request_id", id), slog.Uint64("generation", generation),
		slog.Uint64("guest_port", uint64(port)))
	return r
}
