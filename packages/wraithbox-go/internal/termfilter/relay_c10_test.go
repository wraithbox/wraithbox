//go:build darwin || linux

package termfilter_test

import (
	"bytes"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/relay"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/termfilter"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// dropped holds a sequence of each kind the filter drops
// (S16-01-filter, "Allowlist"), in forms wb itself never writes.
var dropped = []string{
	"\x05", "\x7f", "\xc2\x9b31m",
	"\x1bc", "\x1b#8",
	"\x1b[3J", "\x1b[21t", "\x1b[8;100;100t", "\x1b[22;2t", "\x1b[!p", "\x1b[=1u", "\x1b[>1;2m", "\x1b[?9h",
	"\x1b]0;guest title\x07", "\x1b]2;Approve? y/n\x1b\\",
	"\x1b]8;;https://evil.example\x07", "\x1b]52;c;ZXZpbA==\x07", "\x1b]1337;File=name=eA==:eA==\x07",
	"\x1b]7;file://host/tmp\x07", "\x1b]10;rgb:0/0/0\x07",
	"\x1bP$qm\x1b\\", "\x1b_Gf=24;AAAA\x1b\\", "\x1b^pm\x1b\\", "\x1bX sos\x1b\\",
}

// fakeSummary is a session summary the guest prints in the background
// color and leaves hidden, to hide the real one after it.
const fakeSummary = "\x1b[30;40m\x1b[8m wraith box: session x returned 0 commits\r\n"

// script is a stream that serves its frames and ignores what wb sends.
type script struct {
	frames chan *terminalv1.ServerFrame
}

func (s *script) Send(*terminalv1.ClientFrame) error { return nil }

func (s *script) Recv() (*terminalv1.ServerFrame, error) { return <-s.frames, nil }

func (s *script) CloseSend() error { return nil }

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return bytes.Clone(l.b.Bytes())
}

// TestC10Conformance is the second case of C10-terminal, "Conformance",
// at unit level: the guest prints every kind of sequence the filter
// drops, a window title and a fake summary in the background color,
// then exits. The PTY wb writes to holds none of the dropped sequences.
// Replayed into the terminal state model, it shows the title wb set
// until wb restores the user's, and default colors and modes after the
// reset, before the real summary.
func TestC10Conformance(t *testing.T) {
	ptm, pts, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptm.Close() }()
	out := &lockedBuffer{}
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(out, ptm); close(copied) }()

	term, err := tty.NewTerminal(int(pts.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	// Standard input is a pipe nobody writes to, so the drain ends on
	// its timeout, and closing the PTY doesn't wait for a read.
	stdin, typed := io.Pipe()
	defer func() { _ = typed.Close() }()
	s := &script{frames: make(chan *terminalv1.ServerFrame, len(dropped)+3)}
	for _, d := range dropped {
		s.frames <- outFrame("x" + d + "y")
	}
	s.frames <- outFrame(fakeSummary)
	s.frames <- &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Code{Code: 0}}}}
	code := relay.Run(relay.Config{
		Stream:       s,
		Terminal:     term,
		Charset:      tty.Charset{Source: "LANG", Value: "en_US.UTF-8", UTF8: true},
		Signals:      make(chan tty.Signal),
		Stdin:        stdin,
		Stdout:       pts,
		Stderr:       pts,
		TTY:          relay.TTYs{Stdin: true, Stdout: true, Stderr: true},
		ID:           "c10-20261021-1412-k3f9",
		DrainTimeout: 20 * time.Millisecond,
	})
	_ = pts.Close()
	select {
	case <-copied:
	case <-time.After(5 * time.Second):
		t.Fatal("the PTY didn't close")
	}
	if code != 0 {
		t.Errorf("exit status %d, want 0", code)
	}
	got := out.Bytes()
	for _, d := range dropped {
		if bytes.Contains(got, []byte(d)) {
			t.Errorf("the terminal got the dropped %q", d)
		}
	}

	restore := bytes.LastIndex(got, []byte("\x1b[23t"))
	summary := bytes.Index(got, []byte("terminal: dropped"))
	if restore == -1 || summary < restore {
		t.Fatalf("no title restore before the summary in %q", got)
	}
	m := termfilter.NewModel()
	m.Feed(got[:restore])
	if title := m.Title(); title != "wb: c10-20261021-1412-k3f9" {
		t.Errorf("title before the restore %q", title)
	}
	m.Feed(got[restore:])
	if c := m.Changed(); !slices.Equal(c, nil) {
		t.Errorf("after the relay the terminal differs in %v", c)
	}
}

func outFrame(data string) *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Output{Output: &terminalv1.Output{Stream: terminalv1.Stream_STREAM_STDOUT, Data: []byte(data)}}}
}
