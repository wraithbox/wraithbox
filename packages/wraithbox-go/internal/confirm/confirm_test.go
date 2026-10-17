package confirm_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/confirm"
)

// fakeTerm is a terminal: input is what the person types, and output
// what wb printed.
type fakeTerm struct {
	in     *strings.Reader
	out    bytes.Buffer
	closed bool
}

func (f *fakeTerm) Read(p []byte) (int, error)  { return f.in.Read(p) }
func (f *fakeTerm) Write(p []byte) (int, error) { return f.out.Write(p) }
func (f *fakeTerm) Close() error                { f.closed = true; return nil }

func opener(t *fakeTerm) func() (io.ReadWriteCloser, error) {
	return func() (io.ReadWriteCloser, error) { return t, nil }
}

var prompt = confirm.Prompt{Command: "wb vm reset-data", Lines: []string{"This deletes the work VM's data disk."}}

func TestConfirmed(t *testing.T) {
	for _, typed := range []string{"yes\n", "  yes \r\n"} {
		term := &fakeTerm{in: strings.NewReader(typed)}
		if err := confirm.Ask(opener(term), prompt); err != nil {
			t.Errorf("%q: %v", typed, err)
		}
		if !strings.Contains(term.out.String(), "This deletes the work VM's data disk.\r\nType yes to go on: ") || !term.closed {
			t.Errorf("printed %q, closed %t", term.out.String(), term.closed)
		}
	}
}

func TestDeclined(t *testing.T) {
	for _, typed := range []string{"", "no\n", "y\n", "YES\n", "yes", "yes yes\n", strings.Repeat("x", 300) + "\nyes\n"} {
		term := &fakeTerm{in: strings.NewReader(typed)}
		err := confirm.Ask(opener(term), prompt)
		if r, ok := errors.AsType[*confirm.Refusal](err); !ok || r.Rule != confirm.RuleDeclined {
			t.Errorf("%q: err = %v, want %s", typed, err, confirm.RuleDeclined)
		}
	}
}

// TestNoTerminal: without a terminal the question isn't asked, and the
// command is refused.
func TestNoTerminal(t *testing.T) {
	err := confirm.Ask(func() (io.ReadWriteCloser, error) { return nil, errors.New("no controlling terminal") }, prompt)
	r, ok := errors.AsType[*confirm.Refusal](err)
	if !ok || r.Rule != confirm.RuleNoTerminal {
		t.Fatalf("err = %v, want %s", err, confirm.RuleNoTerminal)
	}
	if !strings.Contains(r.Message, "wb vm reset-data") {
		t.Errorf("message %q", r.Message)
	}
}
