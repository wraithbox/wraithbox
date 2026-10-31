//go:build darwin || linux

package shell

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// failingTable fails ByUID while fails isn't 0, and counts it down when
// it is above 0.
type failingTable struct {
	inner sessionproc.Table

	mu    sync.Mutex
	fails int
}

func (f *failingTable) ByUID(uid uint32) ([]sessionproc.Proc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails != 0 {
		if f.fails > 0 {
			f.fails--
		}
		return nil, errors.New("process table not read")
	}
	return f.inner.ByUID(uid)
}

func (f *failingTable) Kill(p sessionproc.Proc) error { return f.inner.Kill(p) }

func (f *failingTable) setFails(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fails = n
}

// TestKillTableFails: a process table the kill can't read counts as
// processes left (fail closed). The kill retries and kills the program
// once the table reads again, and when it never does, it logs the kill
// incomplete and sends a failure, not an exit.
func TestKillTableFails(t *testing.T) {
	t.Run("retries", func(t *testing.T) {
		h := newHarness(t, "ignore-hup")
		table := &failingTable{inner: sessionproc.System{}}
		h.srv.cfg.Table = table
		h.start(interactive(24, 80))
		h.waitOutput("ready")
		table.setFails(3)
		h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Hangup{Hangup: &terminalv1.Hangup{}}})
		wantExitSignal(t, h.end(), 9)
		log := h.log.String()
		for _, want := range []string{`"decision":"count-as-left"`, `"decision":"kill"`} {
			if !strings.Contains(log, want) {
				t.Errorf("log has no %s", want)
			}
		}
	})
	t.Run("never reads", func(t *testing.T) {
		h := newHarness(t, "ignore-hup")
		table := &failingTable{inner: sessionproc.System{}}
		h.srv.cfg.Table = table
		h.start(interactive(24, 80))
		h.waitOutput("ready")
		sid, uid := h.sid(), h.users.u.UID
		t.Cleanup(func() { killSession(sid, uid) })
		table.setFails(-1)
		h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Hangup{Hangup: &terminalv1.Hangup{}}})
		last := h.end()
		if last.GetFailure().GetRule() != RuleKilled {
			t.Fatalf("last frame %v, want a failure %s", last, RuleKilled)
		}
		if !strings.Contains(h.log.String(), `"decision":"incomplete"`) {
			t.Error("the kill wasn't logged incomplete")
		}
	})
}

// killSession kills what a test left of the POSIX session sid.
func killSession(sid int32, uid uint32) {
	ps, _ := sessionproc.System{}.ByUID(uid)
	for _, p := range ps {
		if p.SID == sid {
			_ = sessionproc.System{}.Kill(p)
		}
	}
}

// TestExitWithProcessesLeft: a helper that reports the exit and runs on
// gets the hangup and the kill, and then the exit frame.
func TestExitWithProcessesLeft(t *testing.T) {
	h := newHarness(t, "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h.srv.cfg.Helper = []string{exe, testRawHelperArg, "exit-lingers"}
	h.start(&terminalv1.Setup{})
	wantExitCode(t, h.end(), 7)
	log := h.log.String()
	for _, want := range []string{`"reason":"exit-with-processes-left"`, `"decision":"kill"`} {
		if !strings.Contains(log, want) {
			t.Errorf("log has no %s", want)
		}
	}
}

// keepTable lists processes but kills none, as for a program that
// forks faster than the kill. It keeps what it was asked to kill, for
// the test to kill at its end.
type keepTable struct {
	sessionproc.System

	mu   sync.Mutex
	kept map[int32]sessionproc.Proc
}

func (k *keepTable) Kill(p sessionproc.Proc) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.kept[p.PID] = p
	return nil
}

func (k *keepTable) killKept() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, p := range k.kept {
		_ = sessionproc.System{}.Kill(p)
	}
}

// TestExitKillIncomplete: when processes may be left after the kill
// that follows an exit report, the last frame is the failure
// shell-hangup-kill, not the reported exit.
func TestExitKillIncomplete(t *testing.T) {
	h := newHarness(t, "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	table := &keepTable{kept: map[int32]sessionproc.Proc{}}
	t.Cleanup(table.killKept)
	h.srv.cfg.Table = table
	h.srv.cfg.Helper = []string{exe, testRawHelperArg, "exit-lingers"}
	h.start(&terminalv1.Setup{})
	last := h.end()
	if last.GetFailure().GetRule() != RuleKilled {
		t.Fatalf("last frame %v, want a failure %s", last, RuleKilled)
	}
	log := h.log.String()
	for _, want := range []string{`"reason":"exit-with-processes-left"`, `"decision":"incomplete"`} {
		if !strings.Contains(log, want) {
			t.Errorf("log has no %s", want)
		}
	}
	if strings.Contains(log, `"decision":"exit"`) {
		t.Error("the exit was logged after an incomplete kill")
	}
}

// TestResumeForgets: a resume drops the session's processes from the
// stop loop's set.
func TestResumeForgets(t *testing.T) {
	h := newHarness(t, "sleep")
	h.start(interactive(24, 80))
	h.waitOutput("ready")
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Resume{Resume: &terminalv1.Resume{}}})
	deadline := time.Now().Add(10 * time.Second)
	for h.forgotten() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if h.forgotten() == 0 {
		t.Error("the resume forgot no process")
	}
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Hangup{Hangup: &terminalv1.Hangup{}}})
	h.end()
}

func (h *harness) forgotten() int {
	h.stop.mu.Lock()
	defer h.stop.mu.Unlock()
	return len(h.stop.forgot)
}

// TestOutputNoBudget: a chunk past the drain's budget sends no frame.
func TestOutputNoBudget(t *testing.T) {
	st := &fakeStream{ctx: context.Background(), out: make(chan *terminalv1.ServerFrame, 1)}
	l := &loop{stream: st, outputs: 1}
	if n := l.output(chunk{stream: stdout, data: []byte("late")}, 0); n != 0 {
		t.Errorf("sent %d bytes", n)
	}
	select {
	case f := <-st.out:
		t.Errorf("frame %v past the budget", f)
	default:
	}
	if l.output(chunk{stream: stdout}, 0); l.outputs != 0 {
		t.Errorf("outputs %d after the end of the stream", l.outputs)
	}
}
