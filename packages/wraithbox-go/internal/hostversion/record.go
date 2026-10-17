// Package hostversion is the build version exchange that opens every
// host connection (S04-architecture, "The version record";
// S04-08-records-and-bounds, "Between host processes"). Each side sends
// its version record, then reads the peer's, and refuses a peer of
// another build version. There are no compatibility rules between host
// processes, so the versions compare as exact bytes, and an unstamped
// build is refused by every peer, an unstamped one included
// (S10-02-packaging-and-signing, "One build version").
//
// Exchange is the form for a stream connection, such as a local IPC
// endpoint (internal/platform/localipc). Record and CheckRecord are the
// form for a datagram channel whose first record is the version
// (internal/platform/fdchan). The gRPC adapter is in grpccreds.
//
// It has no OS code, so it builds and runs on every host OS.
package hostversion

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// Magic opens every version record. Go and Swift write the same bytes
// (S04-architecture, "The version record").
const Magic = "WBV1"

const (
	// headerLen is the magic and the length byte.
	headerLen = len(Magic) + 1
	// MaxRecordLen is the most bytes a record takes, and the most a
	// reader reads before it decides (SEC13-bounded-resources).
	MaxRecordLen = headerLen + version.MaxLen
)

// Timeout bounds the whole exchange on a stream: the peer's record must
// be in before it (S04-architecture, "The version record", which leaves
// the value to code; SEC13-bounded-resources). wb-vmd's Swift side uses
// the same 5 seconds.
const Timeout = 5 * time.Second

// Record returns the version record for own: the magic, one length
// byte, and the version. A version that isn't 1 to 64 bytes of
// printable ASCII is sent as a length of 0, an unstamped build, which no
// peer accepts. wb-vmd's VersionRecord.encode does the same.
func Record(own string) []byte {
	n := len(own)
	if n > version.MaxLen || !printable([]byte(own)) {
		return append([]byte(Magic), 0)
	}
	rec := make([]byte, 0, headerLen+n)
	rec = append(rec, Magic...)
	rec = append(rec, byte(n))
	return append(rec, own...)
}

// printable reports whether every byte of b is from 0x20 to 0x7E.
func printable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7E {
			return false
		}
	}
	return true
}

// readRecord reads one record from r: the header, then exactly as many
// version bytes as the length byte says, so it never reads past the
// record or past MaxRecordLen bytes. A length of 0 gives "", an
// unstamped peer. A read that fails before the record is whole gives
// RuleTimeout when the deadline passed, and the rule short otherwise:
// on a stream the connection ended, in a datagram the record was cut.
func readRecord(r io.Reader, short string) (string, *Refusal) {
	var hdr [headerLen]byte
	if n, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", incomplete(err, short, n)
	}
	if string(hdr[:len(Magic)]) != Magic {
		return "", &Refusal{Rule: RuleMalformed, msg: fmt.Sprintf("the record starts %q, not %q", hdr[:len(Magic)], Magic)}
	}
	size := int(hdr[len(Magic)])
	if size == 0 {
		return "", nil
	}
	if size > version.MaxLen {
		return "", &Refusal{Rule: RuleMalformed, msg: fmt.Sprintf("the record's length %d is over %d", size, version.MaxLen)}
	}
	body := make([]byte, size)
	if n, err := io.ReadFull(r, body); err != nil {
		return "", incomplete(err, short, headerLen+n)
	}
	if !printable(body) {
		return "", &Refusal{Rule: RuleMalformed, msg: "a version byte is outside 0x20 to 0x7E", peer: string(body), peerRead: true}
	}
	return string(body), nil
}

// incomplete is the refusal for a record that didn't arrive whole.
func incomplete(err error, short string, got int) *Refusal {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return &Refusal{Rule: RuleTimeout, msg: fmt.Sprintf("no whole record before the deadline, %d bytes in", got), err: err}
	}
	return &Refusal{Rule: short, msg: fmt.Sprintf("the record ended after %d bytes", got), err: err}
}
