package relay

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// envNames are the host variables the guest gets, and no others
// (S16-03-around-the-stream, "Environment"). TERM crosses unchanged and
// is never mapped to another name ("Terminfo").
var envNames = []string{
	"TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM",
	"LANG", "LC_ALL", "LC_CTYPE",
}

// maxEnvValue is the most bytes of an environment value.
const maxEnvValue = 256

// defaultSize is the window size sent when the terminal reports none in
// range.
var defaultSize = terminalv1.WindowSize{Rows: 24, Cols: 80}

// LeftOut is a variable of envNames that is set but doesn't cross,
// with the reason.
type LeftOut struct {
	Name, Reason string
}

// Environment returns the variables of envNames that lookup finds, in
// that order. A value over 256 bytes, or with a byte outside printable
// ASCII (0x20 to 0x7E), is left out and returned in the second list.
func Environment(lookup func(string) (string, bool)) ([]*terminalv1.EnvVar, []LeftOut) {
	var vars []*terminalv1.EnvVar
	var left []LeftOut
	for _, name := range envNames {
		v, ok := lookup(name)
		if !ok {
			continue
		}
		if reason := envValueProblem(v); reason != "" {
			left = append(left, LeftOut{Name: name, Reason: reason})
			continue
		}
		vars = append(vars, &terminalv1.EnvVar{Name: name, Value: v})
	}
	return vars, left
}

func envValueProblem(v string) string {
	if len(v) > maxEnvValue {
		return fmt.Sprintf("over %d bytes", maxEnvValue)
	}
	for _, c := range []byte(v) {
		if c < 0x20 || c > 0x7e {
			return "a byte outside printable ASCII"
		}
	}
	return ""
}

// NewSetup returns the terminal setup of the first message of a Shell
// call, and the lines wb prints on standard error before the relay
// starts: one that names the variables left out of the environment,
// when there are any. term is used in interactive use only. It refuses
// a host terminal whose charset isn't UTF-8 with a *CharsetError
// (CheckCharset), so a session is refused before wb-hostd opens it.
func NewSetup(ttys TTYs, term Terminal, cs tty.Charset, lookup func(string) (string, bool), log *slog.Logger) (*terminalv1.Setup, []string, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := CheckCharset(cs, log); err != nil {
		return nil, nil, err
	}
	vars, left := Environment(lookup)
	s := &terminalv1.Setup{
		Interactive:    ttys.Interactive(),
		SeparateStderr: ttys.SeparateStderr(),
		Env:            vars,
	}
	if s.Interactive {
		s.Size = setupSize(term, log)
	}
	var lines []string
	if len(left) > 0 {
		parts := make([]string, 0, len(left))
		for _, l := range left {
			log.Info("relay: environment variable left out", "name", l.Name, "reason", l.Reason, "rule", RuleEnvValue)
			parts = append(parts, l.Name+" ("+l.Reason+")")
		}
		lines = append(lines, "wb: left out of the guest's environment: "+strings.Join(parts, ", "))
	}
	return s, lines, nil
}

func setupSize(term Terminal, log *slog.Logger) *terminalv1.WindowSize {
	if term == nil {
		return &terminalv1.WindowSize{Rows: defaultSize.Rows, Cols: defaultSize.Cols}
	}
	rows, cols, err := term.Size()
	if err != nil || rows < 1 || rows > maxWindow || cols < 1 || cols > maxWindow {
		log.Info("relay: window size out of range, sending the default", "rows", rows, "cols", cols, "err", err, "rule", RuleWindowSize)
		return &terminalv1.WindowSize{Rows: defaultSize.Rows, Cols: defaultSize.Cols}
	}
	return &terminalv1.WindowSize{Rows: uint32(rows), Cols: uint32(cols)}
}
