package hostversion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// The rules a Refusal names (S04-architecture, "The version record").
const (
	// RuleTimeout: no whole record before the deadline. On a stream it
	// also covers a connection that ended before the record was whole,
	// and a record of this side's that the peer didn't take.
	RuleTimeout = "peer-version-timeout"
	// RuleMalformed: another magic, a length over 64, a version byte
	// outside 0x20 to 0x7E, or a datagram that isn't exactly one record.
	RuleMalformed = "peer-version-malformed"
	// RuleUnstamped: the peer's version is empty, or this side's own is
	// empty or outside the version form.
	RuleUnstamped = version.RuleUnstamped
	// RuleForm: the peer's version is printable ASCII but not in the
	// version form (S04-08-records-and-bounds, "The guest's version is a
	// label").
	RuleForm = version.RuleForm
	// RuleMismatch: both sides have a version, and they differ as bytes.
	RuleMismatch = version.RuleMismatch
)

// noneLabel is what PeerLabel shows when no version was read.
const noneLabel = "none"

// Refusal is the error of every refusal of the exchange. It holds both
// versions, so wb-hostd can check the installed bundle
// (S04-07-supervision, "Upgrades") and wb can print both
// (S04-08-records-and-bounds, "Between host processes"). The peer's
// version is adversarial input: OwnLabel, PeerLabel and Error show each
// one only through version.Label, escaped and cut to 64 bytes.
type Refusal struct {
	Rule string

	own      string
	peer     string
	peerRead bool
	msg      string
	err      error
}

// OwnLabel is this side's version, as version.Label shows it.
func (r *Refusal) OwnLabel() string { return version.Label(r.own) }

// PeerLabel is the peer's version, as version.Label shows it, or `none`
// when no version was read.
func (r *Refusal) PeerLabel() string {
	if !r.peerRead {
		return noneLabel
	}
	return version.Label(r.peer)
}

func (r *Refusal) Error() string {
	s := fmt.Sprintf("host version refused (%s): %s: own %s, peer %s", r.Rule, r.msg, r.OwnLabel(), r.PeerLabel())
	if r.err != nil {
		s += ": " + r.err.Error()
	}
	return s
}

// Unwrap returns the cause, such as the read's deadline error.
func (r *Refusal) Unwrap() error { return r.err }

// Exchange sends own's version record on conn and reads the peer's, at
// the same time, so two peers that both send first don't wait for each
// other. It returns the peer's version when both are versions in the
// form and equal as bytes. The exchange ends by Timeout. When ctx ends
// first, it stops, closes conn, and returns an error that wraps
// context.Cause(ctx) instead of a Refusal: the caller ended it, not the
// peer. On success the connection's deadline is cleared.
//
// It reads exactly the peer's record, at most MaxRecordLen bytes, so the
// bytes after it are left for the caller. Until it returns, nothing the
// peer sent reaches the caller, and the peer gets nothing but the
// record. Each refusal closes conn and is logged with its rule, both
// versions, and the peer's user ID and process ID when conn is a
// localipc.Conn (S04-architecture, "The version record").
func Exchange(ctx context.Context, log *slog.Logger, conn net.Conn, own string) (string, error) {
	peer, r, err := exchange(ctx, conn, own)
	if err != nil {
		_ = conn.Close()
		logger(log).LogAttrs(ctx, slog.LevelInfo, "host version exchange abandoned",
			append(peerAttrs(conn), slog.String("decision", "abandon"), slog.String("reason", err.Error()))...)
		return "", err
	}
	if r != nil {
		r.own = own
		_ = conn.Close()
		logRefusal(ctx, log, r, peerAttrs(conn)...)
		return "", r
	}
	logger(log).LogAttrs(ctx, slog.LevelDebug, "host peer version accepted",
		append(peerAttrs(conn), slog.String("decision", "accept"), slog.String("peer_version", version.Label(peer)))...)
	return peer, nil
}

// exchangeTimeout is Timeout. The tests shorten it.
var exchangeTimeout = Timeout

// exchange returns the peer's version, or the refusal, or the error of
// a ctx that ended first.
func exchange(ctx context.Context, conn net.Conn, own string) (string, *Refusal, error) {
	if err := conn.SetDeadline(time.Now().Add(exchangeTimeout)); err != nil {
		return "", &Refusal{Rule: RuleTimeout, msg: "set the exchange deadline", err: err}, nil
	}
	// ctx ends the exchange through this function, never through the
	// connection's deadline, so ctx.Err() is set whenever ctx is why a
	// read failed. A deadline in the past ends a blocked read or write
	// at once.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	sent := make(chan error, 1)
	go func() {
		_, err := conn.Write(Record(own))
		sent <- err
	}()
	peer, r := readRecord(conn, RuleTimeout)
	if r != nil {
		stop()
		_ = conn.Close() // ends the write, if it is still blocked
		<-sent
		if ctx.Err() != nil {
			return "", nil, ctxEnded(ctx)
		}
		return "", r, nil
	}
	writeErr := <-sent
	if !stop() {
		return "", nil, ctxEnded(ctx)
	}
	if writeErr != nil {
		r := incomplete(writeErr, RuleTimeout, 0)
		r.msg, r.peer, r.peerRead = "the peer didn't take this side's record", peer, true
		return "", r, nil
	}
	if r := check(own, peer); r != nil {
		return "", r, nil
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return "", &Refusal{Rule: RuleTimeout, msg: "clear the exchange deadline", err: err, peer: peer, peerRead: true}, nil
	}
	return peer, nil, nil
}

// ctxEnded is the error of an exchange the caller's context ended.
func ctxEnded(ctx context.Context) error {
	return fmt.Errorf("hostversion: the caller's context ended the exchange: %w", context.Cause(ctx))
}

// CheckRecord checks a datagram that must be exactly one version record,
// the first record of a datagram channel, and returns the peer's version
// when it passes as Exchange's would. It looks at no more than
// MaxRecordLen bytes. A refusal is logged with its rule and both
// versions. The caller closes the channel.
func CheckRecord(log *slog.Logger, own string, rec []byte) (string, error) {
	peer, r := checkRecord(own, rec)
	if r != nil {
		r.own = own
		logRefusal(context.Background(), log, r)
		return "", r
	}
	return peer, nil
}

func checkRecord(own string, rec []byte) (string, *Refusal) {
	if len(rec) > MaxRecordLen {
		return "", &Refusal{Rule: RuleMalformed, msg: fmt.Sprintf("the datagram is %d bytes, over %d", len(rec), MaxRecordLen)}
	}
	rd := bytes.NewReader(rec)
	peer, r := readRecord(rd, RuleMalformed)
	if r != nil {
		return "", r
	}
	if rd.Len() != 0 {
		return "", &Refusal{Rule: RuleMalformed, msg: fmt.Sprintf("%d bytes follow the record", rd.Len()), peer: peer, peerRead: true}
	}
	return peer, check(own, peer)
}

// check compares the versions with version.Check: both in the form and
// equal as bytes (S04-08-records-and-bounds, "Version skew").
func check(own, peer string) *Refusal {
	err := version.Check(own, peer)
	if err == nil {
		return nil
	}
	rule := RuleUnstamped
	if vr, ok := errors.AsType[*version.Refusal](err); ok {
		rule = vr.Rule
	}
	return &Refusal{Rule: rule, msg: "the version check refused the peer", peer: peer, peerRead: true}
}

// peerAttrs gives the peer's user ID and process ID when conn is a
// localipc connection.
func peerAttrs(conn net.Conn) []slog.Attr {
	pc, ok := conn.(interface{ Peer() localipc.Peer })
	if !ok {
		return nil
	}
	p := pc.Peer()
	return []slog.Attr{slog.Int("peer_uid", p.UID), slog.Int("peer_pid", p.PID)}
}

func logger(log *slog.Logger) *slog.Logger {
	if log == nil {
		return slog.Default()
	}
	return log
}

// logRefusal logs a refusal with its rule and both versions, each
// escaped and cut to 64 bytes.
func logRefusal(ctx context.Context, log *slog.Logger, r *Refusal, attrs ...slog.Attr) {
	attrs = append([]slog.Attr{
		slog.String("rule", r.Rule),
		slog.String("decision", "refuse"),
		slog.String("own_version", r.OwnLabel()),
		slog.String("peer_version", r.PeerLabel()),
		slog.String("reason", r.msg),
	}, attrs...)
	logger(log).LogAttrs(ctx, slog.LevelWarn, "host peer version refused", attrs...)
}
