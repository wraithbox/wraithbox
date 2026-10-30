package shell

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

func reports(rs ...Report) []byte {
	var b []byte
	for _, r := range rs {
		rec := r.Encode()
		b = append(b, rec[:]...)
	}
	return b
}

var started = Report{Kind: ReportStarted, Value: 4242}

func TestReportReader(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []ReportKind
		err  error
	}{
		{"exit", reports(started, Report{Kind: ReportExited, Value: 3}), []ReportKind{ReportStarted, ReportExited}, io.EOF},
		{"stop and continue", reports(started, Report{Kind: ReportStopped}, Report{Kind: ReportContinued},
			Report{Kind: ReportSignaled, Value: 9}), []ReportKind{ReportStarted, ReportStopped, ReportContinued, ReportSignaled}, io.EOF},
		{"no start", reports(Report{Kind: ReportExited}), nil, ErrReport},
		{"second start", reports(started, started), []ReportKind{ReportStarted}, ErrReport},
		{"start pid 1", reports(Report{Kind: ReportStarted, Value: 1}), nil, ErrReport},
		{"start pid over int32", reports(Report{Kind: ReportStarted, Value: 1 << 31}), nil, ErrReport},
		{"code 256", reports(started, Report{Kind: ReportExited, Value: 256}), []ReportKind{ReportStarted}, ErrReport},
		{"signal 0", reports(started, Report{Kind: ReportSignaled}), []ReportKind{ReportStarted}, ErrReport},
		{"signal 65", reports(started, Report{Kind: ReportSignaled, Value: 65}), []ReportKind{ReportStarted}, ErrReport},
		{"stop with a value", reports(started, Report{Kind: ReportStopped, Value: 1}), []ReportKind{ReportStarted}, ErrReport},
		{"unknown kind", reports(started, Report{Kind: 9}), []ReportKind{ReportStarted}, ErrReport},
		{"padding", append(reports(started), 2, 1, 0, 0, 0, 0, 0, 0), []ReportKind{ReportStarted}, ErrReport},
		{"short", append(reports(started), 4, 0, 0), []ReportKind{ReportStarted}, ErrReport},
		{
			"record after the exit", reports(started, Report{Kind: ReportExited}, Report{Kind: ReportStopped}),
			[]ReportKind{ReportStarted, ReportExited},
			ErrReport,
		},
		{
			"byte after the exit", append(reports(started, Report{Kind: ReportExited}), 0),
			[]ReportKind{ReportStarted, ReportExited},
			ErrReport,
		},
		{"gone", reports(started, Report{Kind: ReportStopped}), []ReportKind{ReportStarted, ReportStopped}, ErrGone},
		{"empty", nil, nil, ErrGone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := NewReportReader(bytes.NewReader(tt.in))
			got, err := readAll(rr)
			if !errors.Is(err, tt.err) {
				t.Errorf("error %v, want %v", err, tt.err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("reports %v, want %v", got, tt.want)
			}
			if _, again := rr.Next(); !errors.Is(again, tt.err) {
				t.Errorf("Next after the end: %v, want %v", again, tt.err)
			}
		})
	}
}

// readAll returns the kinds of the reports rr reads, and the error that
// ends them.
func readAll(rr *ReportReader) ([]ReportKind, error) {
	var kinds []ReportKind
	for {
		r, err := rr.Next()
		if err != nil {
			return kinds, err
		}
		kinds = append(kinds, r.Kind)
	}
}

func TestParseCommand(t *testing.T) {
	for _, c := range []Command{CommandHangup, CommandStop, CommandResume} {
		if got, _, ok := parseCommand(EncodeCommand(c, 0)); !ok || got != c {
			t.Errorf("command %d: %d %v", c, got, ok)
		}
		if _, _, ok := parseCommand(EncodeCommand(c, sessionproc.SigInt)); ok {
			t.Errorf("command %d with a value parsed", c)
		}
	}
	for _, s := range commandSignals {
		if c, sig, ok := parseCommand(EncodeCommand(CommandSignal, s)); !ok || c != CommandSignal || sig != s {
			t.Errorf("signal %s: %d %s %v", s, c, sig, ok)
		}
	}
	bad := [][RecordSize]byte{
		EncodeCommand(CommandSignal, sessionproc.SigHup),
		EncodeCommand(CommandSignal, sessionproc.SigStop),
		EncodeCommand(CommandSignal, 0),
		EncodeCommand(9, 0),
		{byte(CommandHangup), 1},
	}
	for _, b := range bad {
		if _, _, ok := parseCommand(b); ok {
			t.Errorf("%v parsed", b)
		}
	}
}

// FuzzHelperReport: arbitrary bytes from the helper never yield an exit
// code outside 0 to 255, a signal outside 1 to 64, a start PID below 2,
// a report before the start, or a report after the exit, and never
// panic (SEC11-root-gains-nothing).
func FuzzHelperReport(f *testing.F) {
	for _, r := range []Report{
		{Kind: ReportStopped}, {Kind: ReportContinued}, {Kind: ReportExited, Value: 255}, {Kind: ReportSignaled, Value: 64},
	} {
		f.Add(reports(started, r))
	}
	f.Add(reports(started))
	f.Add(append(reports(started), 4, 0, 0))
	f.Add(reports(started, Report{Kind: 77}))
	f.Fuzz(func(t *testing.T, in []byte) {
		rr := NewReportReader(bytes.NewReader(in))
		seenStart, seenEnd := false, false
		for range len(in)/RecordSize + 2 {
			r, err := rr.Next()
			if err != nil {
				checkFuzzEnd(t, err, seenEnd)
				return
			}
			checkFuzzed(t, r, seenStart, seenEnd)
			seenStart = true
			seenEnd = r.Final()
		}
		t.Fatal("the reader never ended")
	})
}

// checkFuzzEnd fails t when the reader ended with an error of its own,
// or with io.EOF before an exit.
func checkFuzzEnd(t *testing.T, err error, seenEnd bool) {
	t.Helper()
	switch {
	case errors.Is(err, io.EOF):
		if !seenEnd {
			t.Fatal("EOF without an exit")
		}
	case !errors.Is(err, ErrReport) && !errors.Is(err, ErrGone):
		t.Fatalf("error %v", err)
	}
}

func checkFuzzed(t *testing.T, r Report, seenStart, seenEnd bool) {
	t.Helper()
	switch {
	case seenEnd:
		t.Fatalf("report %+v after the exit", r)
	case (r.Kind == ReportStarted) == seenStart:
		t.Fatalf("report %+v out of order", r)
	case r.Kind == ReportStarted && r.Value < 2:
		t.Fatalf("start PID %d", r.Value)
	case r.Kind == ReportExited && r.Value > maxExitCode:
		t.Fatalf("exit code %d", r.Value)
	case r.Kind == ReportSignaled && (r.Value < 1 || r.Value > maxSignal):
		t.Fatalf("signal %d", r.Value)
	}
}

// FuzzHelperCommand: the helper parses arbitrary command records
// without a panic, and only into a known command.
func FuzzHelperCommand(f *testing.F) {
	sig := EncodeCommand(CommandSignal, sessionproc.SigInt)
	f.Add(sig[:])
	f.Add([]byte{byte(CommandHangup), 0, 0, 0, 0, 0, 0, 1})
	f.Fuzz(func(t *testing.T, in []byte) {
		var b [RecordSize]byte
		copy(b[:], in)
		c, sig, ok := parseCommand(b)
		if ok && (c < CommandHangup || c > CommandSignal || (c == CommandSignal) != (sig != 0)) {
			t.Fatalf("parsed %v as %d %d", b, c, sig)
		}
	})
}
