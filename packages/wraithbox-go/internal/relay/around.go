package relay

import (
	"fmt"
	"io"
	"runtime/debug"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
)

// What wb writes around the stream (S16-03-around-the-stream).
const (
	// titleSave pushes the user's title on the title stack.
	titleSave = "\x1b[22t"
	// titleRestore pops it.
	titleRestore = "\x1b[23t"
	// drainQuery is the sentinel of the input drain: the extended
	// cursor position report, which the filter never passes.
	drainQuery = "\x1b[?6n"
)

// maxTitleID is the most bytes of the id the title shows.
const maxTitleID = 128

func (r *relay) makeRaw() error {
	if r.restoreRaw != nil {
		return nil
	}
	restore, err := r.cfg.Terminal.MakeRaw()
	if err != nil {
		return err
	}
	r.restoreRaw = restore
	return nil
}

// restoreMode restores the terminal mode raw mode replaced. It runs on
// every way out of the relay.
func (r *relay) restoreMode() {
	if r.restoreRaw == nil {
		return
	}
	restore := r.restoreRaw
	r.restoreRaw = nil
	if err := restore(); err != nil {
		r.log.Error("relay: the terminal mode can't be restored", "err", err)
	}
}

// title is the window title wb sets, `wb: <id>`, with the id stripped of
// the escaped set (S16-03-around-the-stream, "Title" and "Guest text
// elsewhere") and cut to maxTitleID bytes. OSC 2 ends at BEL, which the
// strip removes, as it removes ESC and every other control.
func (r *relay) title() string {
	id := guesttext.Strip(r.cfg.ID)
	if len(id) > maxTitleID {
		id = guesttext.Strip(id[:maxTitleID])
	}
	return "\x1b]2;wb: " + id + "\a"
}

// setTitle saves the user's title and sets wb's, only when standard
// output is a terminal (S16-03-around-the-stream, "Title").
func (r *relay) setTitle() {
	if !r.cfg.TTY.Stdout || r.titled || r.hungUp {
		return
	}
	r.raw(titleSave + r.title())
	r.titled = true
}

func (r *relay) restoreTitle() {
	if !r.titled {
		return
	}
	r.titled = false
	if !r.hungUp {
		r.raw(titleRestore)
	}
}

// raw writes wb's own bytes to standard output, not through the filter.
// Only the title, the reset and the drain query come here.
func (r *relay) raw(s string) {
	if r.out.closed {
		return
	}
	if _, err := io.WriteString(r.out.w, s); err != nil {
		r.log.Info("relay: write to the terminal failed", "err", err)
	}
}

// writeReset writes a filter's reset to its output, when that output is
// a terminal. Standard output always gets it then. Standard error gets
// it only when output frames came for it: in interactive use without a
// separate standard error it had none, and its terminal is the one
// standard output reset.
func (r *relay) writeReset(o *output, reset []byte) {
	if !o.tty || o.closed || r.hungUp || (o == r.errOut && !o.used) {
		return
	}
	if _, err := o.w.Write(reset); err != nil {
		r.log.Info("relay: write of the reset failed", "output", o.name, "err", err)
	}
}

// drain sends the sentinel query and discards input until its reply or
// DrainTimeout, so no late reply to a guest query reaches the user's
// shell (S16-03-around-the-stream, "Input drain"). It needs standard
// input and standard output to be terminals.
func (r *relay) drain() {
	if !r.cfg.TTY.Interactive() || r.hungUp || r.input == nil {
		return
	}
	r.raw(drainQuery)
	timer := time.NewTimer(r.cfg.DrainTimeout)
	defer timer.Stop()
	var d drainScanner
	for {
		select {
		case c := <-r.input:
			if c.err != nil {
				r.input = nil
				return
			}
			if _, ok := d.feed(c.data); ok {
				return
			}
		case <-timer.C:
			r.log.Info("relay: no reply to the drain query", "timeout", r.cfg.DrainTimeout)
			return
		}
	}
}

// finish ends the relay: the filters' last bytes, then, unless wb got
// SIGHUP, the reset, the drain and the user's title, then cooked mode,
// and the report on standard error.
func (r *relay) finish(e end) int {
	r.cancelStopTimer()
	r.closeFilter(r.out)
	r.closeFilter(r.errOut)
	r.writeReset(r.out, r.out.filter.ExitReset())
	r.writeReset(r.errOut, r.errOut.filter.ExitReset())
	r.drain()
	r.restoreTitle()
	r.restoreMode()
	// The drain above still reads standard input, so the goroutines
	// stop only now.
	close(r.done)
	if err := r.cfg.Stream.CloseSend(); err != nil {
		r.log.Info("relay: close of the stream failed", "err", err)
	}
	if e.fail == nil && r.writeFailed != nil {
		e = end{code: ExitFailure, fail: &failure{rule: RuleOutputWrite, message: r.writeFailed.Error()}}
	}
	r.report(e)
	return e.code
}

func (r *relay) closeFilter(o *output) {
	if o.closed || (r.hungUp && o.tty) {
		return
	}
	if err := o.filter.Close(); err != nil {
		r.writeError(o, err)
	}
}

// report prints the drop summary and the failure line on standard
// error. After SIGHUP it prints nothing to a terminal.
func (r *relay) report(e end) {
	if e.fail != nil {
		r.log.Error("relay: failed", "rule", e.fail.rule, "message", e.fail.message)
	}
	if r.errOut.closed || (r.hungUp && r.errOut.tty) {
		return
	}
	lines := summaryLines(r.out.filter, r.errOut.filter)
	if e.fail != nil {
		lines = append(lines, e.fail.line())
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(r.errOut.w, l); err != nil {
			r.log.Info("relay: write of the report failed", "err", err)
			return
		}
	}
}

// recoverPanic ends a relay whose own goroutine panicked: it restores
// the terminal mode and the title, and exits with 255.
func (r *relay) recoverPanic(code *int) {
	p := recover()
	if p == nil {
		return
	}
	r.log.Error("relay: panic", "panic", p, "stack", string(debug.Stack()), "rule", RulePanic)
	r.restoreTitle()
	r.restoreMode()
	f := failure{rule: RulePanic, message: fmt.Sprintf("internal error in the terminal relay: %v", p)}
	if !r.hungUp || !r.errOut.tty {
		_, _ = fmt.Fprintln(r.errOut.w, f.line())
	}
	*code = ExitFailure
}
