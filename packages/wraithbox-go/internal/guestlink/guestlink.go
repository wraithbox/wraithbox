// Package guestlink is wb-hostd's end of a connection to wb-guestd: it
// runs gRPC over a host-guest socket connection and checks wb-guestd's
// Hello before anything else uses the connection
// (S04-08-records-and-bounds, "With `wb-guestd`").
//
// wb-guestd runs as root in the guest, which the threat model takes as
// the adversary, so every byte it sends is guest input
// (SEC11-root-gains-nothing). The receive caps are set on the client
// before anything is decoded, the call has a deadline, and the answer is
// checked against fixed bounds. A refusal closes the connection and is
// logged with its rule.
package guestlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

const (
	// HelloTimeout is how long wb-guestd has to answer Hello.
	HelloTimeout = 5 * time.Second

	// MaxRecvMsgSize is the largest message the host decodes from
	// wb-guestd. A Hello answer is well under it. The cap is a client
	// option, so gRPC refuses a larger message before it decodes it
	// (S04-08-records-and-bounds, "Host work the guest can cause").
	MaxRecvMsgSize = 4 << 10

	// maxSendMsgSize is the largest message the host sends, wb-guestd's
	// own receive cap.
	maxSendMsgSize = 64 << 10

	// maxHeaderListSize caps the response headers and trailers, which
	// hold the guest's status message.
	maxHeaderListSize = 4 << 10

	// maxPackages and maxPackageLen bound the package list
	// (S04-08-records-and-bounds, "With `wb-guestd`").
	maxPackages   = 16
	maxPackageLen = 64

	// maxTextLen is how much of a text from the guest, such as a gRPC
	// status message, goes into a log record or an error.
	maxTextLen = 128
)

// The rules of this package. A version refusal names the rule of
// version.Check instead (RuleUnstamped, RuleForm or RuleMismatch there).
const (
	// RuleInvalid: the call failed (an error status, an answer over the
	// receive cap, a broken connection) or its answer didn't decode.
	RuleInvalid = "guest-hello-invalid"
	// RuleTimeout: no answer within HelloTimeout.
	RuleTimeout = "guest-hello-timeout"
	// RulePackagesForm: the package list has more than 16 names, or a
	// name over 64 bytes or not of the form `wraithbox.<lowercase
	// letters>.v<number>`.
	RulePackagesForm = "guest-packages-form"
	// RulePackagesMismatch: the package list isn't the host's own.
	RulePackagesMismatch = "guest-packages-mismatch"
)

// packageForm is the form of a served package name.
var packageForm = regexp.MustCompile(`^wraithbox\.[a-z]+\.v[0-9]+$`)

// Refusal is the error of every refused Hello. Its message holds only
// text the host made, and guest text escaped and cut.
type Refusal struct {
	Rule string
	msg  string
	err  error
}

func (r *Refusal) Error() string { return "guest hello refused (" + r.Rule + "): " + r.msg }

// Unwrap returns the cause, such as a *version.Refusal.
func (r *Refusal) Unwrap() error { return r.err }

// Link is a checked connection to wb-guestd.
type Link struct {
	cc      *grpc.ClientConn
	version string
}

// Version is wb-guestd's build version, which equals the host's own.
func (l *Link) Version() string { return l.version }

// Close closes the connection.
func (l *Link) Close() error { return l.cc.Close() }

// Hello runs gRPC over conn, calls GuestService.Hello, and checks the
// answer against own, the host's build version (normally
// version.Build()). On success the returned Link owns conn. On a refusal
// conn is closed, the refusal is logged with its rule, and the error is
// a *Refusal. When the host cancels ctx first, conn is closed and the
// error wraps context.Canceled: the host gave up, the guest wasn't refused.
func Hello(ctx context.Context, conn net.Conn, own string, log *slog.Logger) (*Link, error) {
	cc, err := newClient(conn)
	if err != nil {
		_ = conn.Close()
		return nil, refuse(ctx, log, own, &Refusal{Rule: RuleInvalid, msg: "client: " + err.Error(), err: err})
	}
	callCtx, cancel := context.WithTimeout(ctx, HelloTimeout)
	defer cancel()
	resp, err := guestv1.NewGuestServiceClient(cc).Hello(callCtx, &guestv1.HelloRequest{})
	if err != nil {
		closeBoth(cc, conn)
		if errors.Is(callCtx.Err(), context.Canceled) {
			log.LogAttrs(ctx, slog.LevelInfo, "guest hello abandoned", slog.String("decision", "close"))
			return nil, fmt.Errorf("guestlink: hello abandoned by the host: %w", callCtx.Err())
		}
		return nil, refuse(ctx, log, own, callRefusal(callCtx, err))
	}
	v, r := check(own, resp)
	if r != nil {
		closeBoth(cc, conn)
		return nil, refuse(ctx, log, own, r)
	}
	log.LogAttrs(ctx, slog.LevelInfo, "guest hello accepted",
		slog.String("decision", "accept"), slog.String("version", version.Label(v)))
	return &Link{cc: cc, version: v}, nil
}

// newClient makes a gRPC client whose only connection is conn. TLS would
// add nothing: the host-guest socket is a channel to one VM that only
// wb-vmd opens, and its peer is guest root either way.
func newClient(conn net.Conn) (*grpc.ClientConn, error) {
	var used atomic.Bool
	dial := func(context.Context, string) (net.Conn, error) {
		if used.Swap(true) {
			return nil, errors.New("guestlink: the connection was already used")
		}
		return conn, nil
	}
	return grpc.NewClient("passthrough:///wb-guestd",
		grpc.WithContextDialer(dial),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDisableRetry(),
		// A Link stays open for later calls. Going idle would close conn,
		// and the dialer can't open it again.
		grpc.WithIdleTimeout(0),
		grpc.WithMaxHeaderListSize(maxHeaderListSize),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(MaxRecvMsgSize),
			grpc.MaxCallSendMsgSize(maxSendMsgSize)),
	)
}

func closeBoth(cc *grpc.ClientConn, conn net.Conn) {
	_ = cc.Close()
	_ = conn.Close()
}

// callRefusal names the rule of a failed call. Only a deadline of the
// host's, the call's own or the caller's, makes it a timeout: a status
// code the guest sent is guest input, whichever code it is.
func callRefusal(ctx context.Context, err error) *Refusal {
	st := status.Convert(err)
	msg := st.Code().String() + ": " + guestText(st.Message())
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Refusal{Rule: RuleTimeout, msg: msg, err: ctx.Err()}
	}
	return &Refusal{Rule: RuleInvalid, msg: msg}
}

// check applies the checks of S04-08-records-and-bounds to a decoded
// Hello answer, the version first. It returns the version only when the
// answer passes, and the refusal otherwise.
func check(own string, resp *guestv1.HelloResponse) (string, *Refusal) {
	v := resp.GetGuestdVersion()
	if err := version.Check(own, v); err != nil {
		rule := RuleInvalid
		if vr, ok := errors.AsType[*version.Refusal](err); ok {
			rule = vr.Rule
		}
		return "", &Refusal{Rule: rule, msg: err.Error(), err: err}
	}
	if r := checkPackages(resp.GetServedPackages()); r != nil {
		return "", r
	}
	return v, nil
}

// checkPackages checks the form of the package list, then that it is the
// host's own. The host uses the list only to refuse.
func checkPackages(got []string) *Refusal {
	if len(got) > maxPackages {
		return &Refusal{Rule: RulePackagesForm, msg: strconv.Itoa(len(got)) + " packages, over " + strconv.Itoa(maxPackages)}
	}
	for i, name := range got {
		if len(name) > maxPackageLen || !packageForm.MatchString(name) {
			return &Refusal{Rule: RulePackagesForm, msg: "package " + strconv.Itoa(i) + " " + guestText(name)}
		}
	}
	if own := ownPackages(); !slices.Equal(got, own) {
		return &Refusal{Rule: RulePackagesMismatch, msg: "packages differ from the host's " + strconv.Quote(own[0])}
	}
	return nil
}

// ownPackages is the host's own package list, from the generated
// descriptor of the contract it speaks.
func ownPackages() []string {
	return []string{string(guestv1.File_wraithbox_guest_v1_guest_proto.Package())}
}

// refuse logs a refusal with its rule and returns it.
func refuse(ctx context.Context, log *slog.Logger, own string, r *Refusal) *Refusal {
	log.LogAttrs(ctx, slog.LevelWarn, "guest hello refused",
		slog.String("rule", r.Rule), slog.String("decision", "refuse"),
		slog.String("own_version", version.Label(own)), slog.String("reason", r.msg))
	return r
}

// guestText shows a text from the guest: cut to maxTextLen bytes, then
// quoted with every byte outside printable ASCII escaped.
func guestText(s string) string {
	if len(s) > maxTextLen {
		s = s[:maxTextLen]
	}
	return strconv.QuoteToASCII(s)
}
