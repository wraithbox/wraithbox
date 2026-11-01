package relay

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/termfilter"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// TestEnvironment is S16-03-around-the-stream, "Environment" and
// "Terminfo": of a host environment that holds every name it includes
// and excludes, only the seven cross, TERM unchanged. A value over 256
// bytes or with a control byte is left out and named.
func TestEnvironment(t *testing.T) {
	host := map[string]string{
		"TERM":                 "xterm-ghostty",
		"TERM_PROGRAM":         "ghostty",
		"TERM_PROGRAM_VERSION": "1.2.3",
		"COLORTERM":            "truecolor",
		"LANG":                 "en_US.UTF-8",
		"LC_ALL":               strings.Repeat("a", 257),
		"LC_CTYPE":             "UTF-8\x1b[2J",
		"TMUX":                 "/tmp/tmux-501/default,1,0",
		"STY":                  "123.pts-0",
		"SSH_AUTH_SOCK":        "/tmp/agent.sock",
		"SSH_CONNECTION":       "a b c d",
		"TERMINFO":             "/Users/me/.terminfo",
		"LC_TERMINAL":          "iTerm2",
		"ITERM_SESSION_ID":     "w0t0p0",
		"LC_TIME":              "C",
	}
	lookup := func(k string) (string, bool) { v, ok := host[k]; return v, ok }
	vars, left := Environment(lookup)
	var got []string
	for _, v := range vars {
		got = append(got, v.GetName()+"="+v.GetValue())
	}
	want := []string{"TERM=xterm-ghostty", "TERM_PROGRAM=ghostty", "TERM_PROGRAM_VERSION=1.2.3", "COLORTERM=truecolor", "LANG=en_US.UTF-8"}
	if !slices.Equal(got, want) {
		t.Errorf("environment %q, want %q", got, want)
	}
	wantLeft := []LeftOut{{"LC_ALL", "over 256 bytes"}, {"LC_CTYPE", "a byte outside printable ASCII"}}
	if !slices.Equal(left, wantLeft) {
		t.Errorf("left out %v, want %v", left, wantLeft)
	}

	h := newHarness(t, allTTYs)
	setup, lines, err := NewSetup(allTTYs, h.term, utf8Charset, lookup, h.cfg.Log)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "wb: left out of the guest's environment: LC_ALL (over 256 bytes), LC_CTYPE (a byte outside printable ASCII)" {
		t.Errorf("lines %q", lines)
	}
	if !strings.Contains(h.logs.String(), "rule="+RuleEnvValue) {
		t.Errorf("log %q doesn't name the rule", h.logs.String())
	}
	if !setup.GetInteractive() || setup.GetSeparateStderr() || setup.GetSize().GetRows() != 24 || len(setup.GetEnv()) != 5 {
		t.Errorf("setup %v", setup)
	}
}

func TestEnvironmentAt256(t *testing.T) {
	v := strings.Repeat("~", 256)
	vars, left := Environment(func(k string) (string, bool) { return v, k == "LANG" })
	if len(vars) != 1 || len(left) != 0 {
		t.Errorf("a value of 256 printable bytes: %v, left out %v", vars, left)
	}
}

// TestSetupUses: the setup says which use it is and whether standard
// error is separate (S05-01-agent-commands, "Interactive and
// non-interactive use").
func TestSetupUses(t *testing.T) {
	tests := []struct {
		ttys                  TTYs
		interactive, separate bool
	}{
		{allTTYs, true, false},
		{TTYs{Stdin: true, Stdout: true}, true, true},
		{TTYs{Stdout: true, Stderr: true}, false, true},
		{TTYs{}, false, true},
	}
	for _, tt := range tests {
		s, _, err := NewSetup(tt.ttys, &fakeTerminal{rows: 0, cols: 0}, utf8Charset, func(string) (string, bool) { return "", false }, nil)
		if err != nil {
			t.Fatal(err)
		}
		if s.GetInteractive() != tt.interactive || s.GetSeparateStderr() != tt.separate {
			t.Errorf("%+v: setup %v", tt.ttys, s)
		}
		if tt.interactive && (s.GetSize().GetRows() != 24 || s.GetSize().GetCols() != 80) {
			t.Errorf("%+v: a size out of range gives %v, want the default", tt.ttys, s.GetSize())
		}
		if !tt.interactive && s.GetSize() != nil {
			t.Errorf("%+v: a size in non-interactive use", tt.ttys)
		}
	}
}

func TestCheckOpened(t *testing.T) {
	ok := &hostdv1.ShellOpened{ShellId: "myrepo-20261021-1412-k3f9", ProjectName: "myrepo", GuestUser: "wbp1"}
	s, err := CheckOpened(ok)
	if err != nil || s.Label() != "wb: debug shell myrepo-20261021-1412-k3f9 in project myrepo, as guest user wbp1. A debug shell returns no work." {
		t.Errorf("CheckOpened = %+v, %v; label %q", s, err, s.Label())
	}
	s, err = CheckOpened(&hostdv1.ShellOpened{ShellId: "p-20261021-1412-k3f9", ProjectName: "p\u202e\x1b]2;b\x07", GuestUser: "u"})
	if err != nil || s.Project != "p]2;b" {
		t.Errorf("CheckOpened stripped = %+v, %v", s, err)
	}
	for _, bad := range []*hostdv1.ShellOpened{
		{ShellId: strings.Repeat("a", 129), ProjectName: "p", GuestUser: "u"},
		{ShellId: "s", ProjectName: strings.Repeat("a", 129), GuestUser: "u"},
		{ShellId: "s", ProjectName: "p", GuestUser: strings.Repeat("a", 65)},
		{ShellId: "", ProjectName: "p", GuestUser: "u"},
		{ShellId: "\x1b", ProjectName: "p", GuestUser: "u"},
		{ShellId: "s", ProjectName: "p", GuestUser: "u"},
		{ShellId: "a\x1b]2;b\x07-20261021-1412-k3f9", ProjectName: "p", GuestUser: "u"},
		{ShellId: "a..b-20261021-1412-k3f9", ProjectName: "p", GuestUser: "u"},
	} {
		if _, err := CheckOpened(bad); err == nil {
			t.Errorf("CheckOpened(%v) accepted", bad)
		}
	}
}

// fakeShellClient is a ShellService.Shell call that answers with
// responses.
type fakeShellClient struct {
	sent      []*hostdv1.ShellRequest
	responses []*hostdv1.ShellResponse
	// sendErr is what Send returns, and end what Recv returns once the
	// responses run out (io.EOF when nil).
	sendErr error
	end     error
}

func (c *fakeShellClient) Send(r *hostdv1.ShellRequest) error {
	if c.sendErr != nil {
		return c.sendErr
	}
	c.sent = append(c.sent, r)
	return nil
}

func (c *fakeShellClient) Recv() (*hostdv1.ShellResponse, error) {
	if len(c.responses) == 0 {
		if c.end != nil {
			return nil, c.end
		}
		return nil, io.EOF
	}
	r := c.responses[0]
	c.responses = c.responses[1:]
	return r, nil
}

func (c *fakeShellClient) CloseSend() error { return nil }

func TestOpenShell(t *testing.T) {
	opened := &hostdv1.ShellResponse{Msg: &hostdv1.ShellResponse_Opened{Opened: &hostdv1.ShellOpened{ShellId: "p-20261021-1412-k3f9", ProjectName: "p", GuestUser: "u"}}}
	frame := &hostdv1.ShellResponse{Msg: &hostdv1.ShellResponse_Frame{Frame: exitCode(3)}}
	c := &fakeShellClient{responses: []*hostdv1.ShellResponse{opened, frame, opened}}
	stream, shell, err := OpenShell(c, "0123456789abcdef0123456789abcdef", &terminalv1.Setup{})
	if err != nil || shell.ID != "p-20261021-1412-k3f9" {
		t.Fatalf("OpenShell = %+v, %v", shell, err)
	}
	if c.sent[0].GetOpen().GetProjectId() != "0123456789abcdef0123456789abcdef" {
		t.Errorf("first request %v", c.sent[0])
	}
	if f, err := stream.Recv(); err != nil || f.GetExit().GetCode() != 3 {
		t.Errorf("Recv = %v, %v", f, err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Error("a second ShellOpened passed as a frame")
	}
	if err := stream.Send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Hangup{Hangup: &terminalv1.Hangup{}}}); err != nil || c.sent[1].GetFrame().GetHangup() == nil {
		t.Errorf("Send: %v, sent %v", err, c.sent)
	}

	for _, first := range []*hostdv1.ShellResponse{frame, {Msg: &hostdv1.ShellResponse_Opened{Opened: &hostdv1.ShellOpened{}}}} {
		if _, _, err := OpenShell(&fakeShellClient{responses: []*hostdv1.ShellResponse{first}}, "p", nil); err == nil {
			t.Errorf("OpenShell accepted a first response %v", first)
		}
	}
}

// TestOpenShellRefusedBeforeOpen: when wb-hostd ends the stream before
// it reads the ShellOpen, Send returns io.EOF and OpenShell returns the
// status Recv reads, not the EOF. Another send error is returned as is.
func TestOpenShellRefusedBeforeOpen(t *testing.T) {
	refusal := errors.New("wb-hostd is in refusing mode")
	_, _, err := OpenShell(&fakeShellClient{sendErr: io.EOF, end: refusal}, "p", nil)
	if !errors.Is(err, refusal) {
		t.Errorf("OpenShell after an EOF send = %v, want the refusal", err)
	}
	broken := errors.New("broken")
	_, _, err = OpenShell(&fakeShellClient{sendErr: broken, end: refusal}, "p", nil)
	if !errors.Is(err, broken) {
		t.Errorf("OpenShell after a failed send = %v, want the send's error", err)
	}
}

func TestSummaryLines(t *testing.T) {
	out := termfilter.New(io.Discard)
	errOut := termfilter.New(io.Discard)
	_, _ = out.Write([]byte("\x1b]52;c;eA==\x07\x1b]9;hi\x07\x1b]8;;https://docs.example.com/security\x07Security guide\x1b]8;;\x07"))
	_, _ = errOut.Write([]byte("\x1b]52;c;eA==\x07\x1b]8;;file:///home/u/src/main.go\x07src/main.go\x1b]8;;\x07"))
	_ = out.Close()
	_ = errOut.Close()
	got := summaryLines(out, errOut)
	want := []string{
		"  terminal: dropped 4 link sequences, 2 clipboard writes, 1 notification became a bell",
		"  links:    Security guide  https://docs.example.com/security",
		"            src/main.go     file:///home/u/src/main.go  (guest path)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("summary\n%q\nwant\n%q", got, want)
	}
	if got := summaryLines(termfilter.New(io.Discard)); got != nil {
		t.Errorf("summary of no drops %q", got)
	}
}

// TestSummaryStripped: link text and URLs are guest text, stripped of
// the escaped set.
func TestSummaryStripped(t *testing.T) {
	f := termfilter.New(io.Discard)
	_, _ = f.Write([]byte("\x1b]8;;https://x\x1b[2J.example\x07te\u200bxt\x1b]8;;\x07"))
	_ = f.Close()
	for _, l := range summaryLines(f) {
		for _, r := range l {
			if r < 0x20 || r == '\u200b' {
				t.Errorf("summary line %q holds %U", l, r)
			}
		}
	}
}
