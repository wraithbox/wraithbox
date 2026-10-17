// Package confirm asks a person to confirm a command that destroys
// state, in wb's own terminal (SEC14-no-fake-approvals). It prints what
// the command does, and the command goes on only when the person types
// the answer word. It is refused without a terminal, and no flag skips
// the question. `wb vm reset-data` uses it, and `wb vm reset-ca` and `wb
// gate reset go-clock` are to (S05-03-management-commands).
//
// The terminal comes from an opener, internal/platform/tty.Open in wb,
// never from standard input or output: a script that pipes an answer
// there doesn't answer, and a session's stream never reaches it.
package confirm

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Rule names the rule behind a refusal.
type Rule string

// The rules of this package.
const (
	// RuleNoTerminal: wb has no terminal of its own to ask in.
	RuleNoTerminal Rule = "confirm-no-terminal"
	// RuleDeclined: the person typed something other than the answer
	// word, or nothing.
	RuleDeclined Rule = "confirm-declined"
)

// Refusal is the error of a refusal.
type Refusal struct {
	Rule    Rule
	Message string

	err error
}

func (r *Refusal) Error() string { return r.Message }

// Unwrap returns the cause, such as the terminal's open error.
func (r *Refusal) Unwrap() error { return r.err }

// Answer is the word that confirms. Anything else declines.
const Answer = "yes"

// maxAnswer bounds what is read of the answer line.
const maxAnswer = 256

// Prompt is what the person reads before the question.
type Prompt struct {
	// Command names the command, for the refusals.
	Command string
	// Lines say what the command does and what is lost. Each is printed
	// on its own line. The caller strips control and escape sequences
	// from any value it didn't choose itself.
	Lines []string
}

// Ask opens the terminal with open, prints p's lines and the question,
// and reads one line. It returns nil when the line is Answer, and a
// *Refusal otherwise.
func Ask(open func() (io.ReadWriteCloser, error), p Prompt) error {
	term, err := open()
	if err != nil {
		return &Refusal{
			Rule: RuleNoTerminal, err: err,
			Message: fmt.Sprintf("%s asks for a confirmation in its own terminal, and there is none: run it in a terminal (%v)", p.Command, err),
		}
	}
	defer func() { _ = term.Close() }()
	var b strings.Builder
	for _, line := range p.Lines {
		b.WriteString(line)
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "Type %s to go on: ", Answer)
	if _, err := io.WriteString(term, b.String()); err != nil {
		return &Refusal{Rule: RuleNoTerminal, err: err, Message: fmt.Sprintf("%s can't write to its terminal: %v", p.Command, err)}
	}
	line, err := readLine(term)
	if err != nil || line != Answer {
		return &Refusal{Rule: RuleDeclined, err: err, Message: fmt.Sprintf("%s wasn't confirmed, and nothing changed", p.Command)}
	}
	return nil
}

// readLine reads up to a newline, at most maxAnswer bytes, and trims the
// line's spaces and carriage return.
func readLine(r io.Reader) (string, error) {
	br := bufio.NewReaderSize(io.LimitReader(r, maxAnswer), maxAnswer)
	line, err := br.ReadString('\n')
	if !strings.HasSuffix(line, "\n") {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return "", fmt.Errorf("no complete answer line: %w", err)
	}
	return strings.TrimSpace(line), nil
}
