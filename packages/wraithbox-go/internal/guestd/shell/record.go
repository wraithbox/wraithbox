package shell

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// The records between wb-guestd and the session helper
// (S06-09-session-lifecycle, "Session helper"). Each is RecordSize
// bytes: a kind byte, three zero bytes, and a value as a big-endian
// uint32. The helper writes reports on its descriptor 3 and reads
// commands on its descriptor 4.
//
// The helper runs as the project user, so wb-guestd parses its reports
// as untrusted input (SEC11-root-gains-nothing): strictly, in order, and
// never taking a PID from them to choose what to signal.

// RecordSize is the size of every report and command.
const RecordSize = 8

// The helper's descriptors.
const (
	reportFD  = 3
	commandFD = 4
)

// ReportKind is the kind of a report.
type ReportKind byte

// The reports, helper to wb-guestd.
const (
	// ReportStarted is the first report, and only the first. Its value is
	// the program's PID, which wb-guestd only logs.
	ReportStarted ReportKind = iota + 1
	// ReportStopped: the program stopped. The value is 0.
	ReportStopped
	// ReportContinued: the program continued. The value is 0.
	ReportContinued
	// ReportExited: the program exited with the value as its code, 0 to
	// 255. The last report.
	ReportExited
	// ReportSignaled: a signal ended the program, the value, 1 to 64. The
	// last report.
	ReportSignaled
)

// The bounds of a report's value.
const (
	maxExitCode = 255
	maxSignal   = 64
)

// Report is one report.
type Report struct {
	Kind  ReportKind
	Value uint32
}

// Encode returns r as a record.
func (r Report) Encode() [RecordSize]byte { return record(byte(r.Kind), r.Value) }

// Final reports whether r is an exit or a signal, the last report.
func (r Report) Final() bool { return r.Kind == ReportExited || r.Kind == ReportSignaled }

func record(kind byte, value uint32) [RecordSize]byte {
	var b [RecordSize]byte
	b[0] = kind
	binary.BigEndian.PutUint32(b[4:], value)
	return b
}

// u32 returns n as a record value, and math.MaxUint32 when n is
// outside 0..MaxUint32. No report, command, user or group has that
// value, so the other end refuses it.
func u32(n int) uint32 {
	if n < 0 || n > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(n)
}

// The errors of ReportReader.Next.
var (
	// ErrReport is a report out of form or out of order: an unknown kind,
	// a short record, padding that isn't zero, a value out of bounds, a
	// report before the start or a second start, or a record after the
	// exit. The rule is RuleHelperReport.
	ErrReport = errors.New("shell: a session helper report out of form")
	// ErrGone is the end of the reports without an exit or a signal. The
	// rule is RuleHelperGone.
	ErrGone = errors.New("shell: the session helper went without an exit report")
)

// ReportReader reads a helper's reports in order. After an error, every
// Next returns that error again.
type ReportReader struct {
	r       io.Reader
	started bool
	ended   bool
	err     error
}

// NewReportReader reads reports from r.
func NewReportReader(r io.Reader) *ReportReader { return &ReportReader{r: r} }

// Next returns the next report. After the final report it returns
// io.EOF at the end of r, and ErrReport for any further byte.
func (rr *ReportReader) Next() (Report, error) {
	if rr.err != nil {
		return Report{}, rr.err
	}
	rep, err := rr.next()
	if err != nil {
		rr.err = err
	}
	return rep, err
}

func (rr *ReportReader) next() (Report, error) {
	var b [RecordSize]byte
	n, err := io.ReadFull(rr.r, b[:])
	switch {
	case rr.ended && n > 0:
		return Report{}, ErrReport
	case rr.ended && err == io.EOF:
		return Report{}, io.EOF
	case errors.Is(err, io.ErrUnexpectedEOF):
		return Report{}, ErrReport
	case err == io.EOF:
		return Report{}, ErrGone
	case err != nil:
		return Report{}, fmt.Errorf("%w: %w", ErrGone, err)
	}
	rep := Report{Kind: ReportKind(b[0]), Value: binary.BigEndian.Uint32(b[4:])}
	if b[1] != 0 || b[2] != 0 || b[3] != 0 || !rr.inOrder(rep) {
		return Report{}, ErrReport
	}
	rr.started = true
	rr.ended = rep.Final()
	return rep, nil
}

// inOrder reports whether rep is in bounds, and may come now.
func (rr *ReportReader) inOrder(rep Report) bool {
	if rep.Kind != ReportStarted && !rr.started {
		return false
	}
	switch rep.Kind {
	case ReportStarted:
		return !rr.started && rep.Value >= 2 && rep.Value <= math.MaxInt32
	case ReportStopped, ReportContinued:
		return rep.Value == 0
	case ReportExited:
		return rep.Value <= maxExitCode
	case ReportSignaled:
		return rep.Value >= 1 && rep.Value <= maxSignal
	}
	return false
}

// Command is the kind of a command.
type Command byte

// The commands, wb-guestd to helper. The helper applies each to its
// program's process group only.
const (
	// CommandHangup sends SIGHUP, then SIGCONT, so a stopped group gets
	// the hangup (S06-09-session-lifecycle, "Closing the terminal ends
	// the session"). The end of the commands counts as one.
	CommandHangup Command = iota + 1
	// CommandStop sends SIGSTOP, because a shell ignores SIGTSTP.
	CommandStop
	// CommandResume sends SIGCONT.
	CommandResume
	// CommandSignal sends the signal of its value, one of
	// commandSignals.
	CommandSignal
)

// commandSignals are the signals a CommandSignal may carry: those the
// terminal stream forwards (S05-02-exit-status-and-signals).
var commandSignals = []sessionproc.Signal{
	sessionproc.SigInt, sessionproc.SigQuit, sessionproc.SigTerm, sessionproc.SigTstp, sessionproc.SigCont,
}

// EncodeCommand returns a command as a record. sig is for
// CommandSignal, and 0 otherwise.
func EncodeCommand(c Command, sig sessionproc.Signal) [RecordSize]byte {
	return record(byte(c), u32(int(sig)))
}

// parseCommand reads a command record. ok is false for one out of form.
func parseCommand(b [RecordSize]byte) (c Command, sig sessionproc.Signal, ok bool) {
	if b[1] != 0 || b[2] != 0 || b[3] != 0 {
		return 0, 0, false
	}
	c, v := Command(b[0]), binary.BigEndian.Uint32(b[4:])
	switch c {
	case CommandHangup, CommandStop, CommandResume:
		return c, 0, v == 0
	case CommandSignal:
		for _, s := range commandSignals {
			if u32(int(s)) == v {
				return c, s, true
			}
		}
	}
	return 0, 0, false
}
