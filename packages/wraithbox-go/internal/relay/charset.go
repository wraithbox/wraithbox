package relay

import (
	"fmt"
	"log/slog"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
)

// RuleCharset: the host terminal's charset isn't UTF-8, so wb refuses to
// relay a guest terminal stream (S16-01-filter, "Where").
const RuleCharset = "terminal-charset-not-utf8"

// CharsetError is the refusal of a host terminal whose charset isn't
// UTF-8. Its message names the charset wb found and how to fix it
// (NFR06-explained-refusals).
type CharsetError struct {
	Charset tty.Charset
}

func (e *CharsetError) Error() string {
	found, name := "none of LC_ALL, LC_CTYPE and LANG is set", "LANG"
	if e.Charset.Source != "" {
		found, name = fmt.Sprintf("%s is %q", e.Charset.Source, e.Charset.Value), e.Charset.Source
	}
	return fmt.Sprintf("the host terminal's charset isn't UTF-8 (%s): set %s to a UTF-8 locale, such as en_US.UTF-8, and the terminal to UTF-8", found, name)
}

// Line is the last line wb prints on standard error for the refusal,
// `wraith box: … (rule terminal-charset-not-utf8)`
// (S05-02-exit-status-and-signals, "Exit status"). Run prints it, and a
// caller that gets the error from NewSetup prints it too.
func (e *CharsetError) Line() string {
	f := failure{rule: RuleCharset, message: e.Error()}
	return f.line()
}

// ExitStatus is wb's exit status for the refusal: 255, a refusal before
// the session starts (S05-02-exit-status-and-signals, "Exit status").
func (e *CharsetError) ExitStatus() int { return ExitFailure }

// CheckCharset refuses a host terminal whose charset isn't UTF-8 with a
// *CharsetError, and logs the decision with RuleCharset either way. The
// terminal filter's output is safe only on a terminal that reads UTF-8:
// on one that reads 8-bit bytes, a continuation byte such as 0x9B acts
// as a C1 control (S16-01-filter, "Where"). Every way to relay a guest
// terminal stream calls it before the first guest byte, and the zero
// Charset is refused.
func CheckCharset(cs tty.Charset, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if !cs.UTF8 {
		log.Error("relay: refused, the host terminal's charset isn't UTF-8", "source", cs.Source, "value", cs.Value, "rule", RuleCharset)
		return &CharsetError{Charset: cs}
	}
	log.Info("relay: the host terminal's charset is UTF-8", "source", cs.Source, "value", cs.Value, "rule", RuleCharset)
	return nil
}

// refuse ends a relay that didn't start: it closes the stream without
// reading a frame, prints line on standard error, and returns 255. It
// writes nothing else, so no guest byte reaches the terminal.
func refuse(cfg Config, line string) int {
	if cfg.Stream != nil {
		if err := cfg.Stream.CloseSend(); err != nil {
			cfg.Log.Info("relay: close of the stream failed", "err", err)
		}
	}
	if _, err := fmt.Fprintln(cfg.Stderr, line); err != nil {
		cfg.Log.Info("relay: write of the refusal failed", "err", err)
	}
	return ExitFailure
}
