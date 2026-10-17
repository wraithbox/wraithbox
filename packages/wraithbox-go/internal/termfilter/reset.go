package termfilter

import "strconv"

// passed records the reports that passed the filter and that the exit
// reset clears (S16-03-around-the-stream, "Reset on exit").
type passed struct {
	// progress is set once an OSC 9 ; 4 progress report passed.
	progress bool
	// status is set once an OSC 7501 report passed. The query `?` is
	// not a report.
	status bool
}

// resetStep is one item of S16-03-around-the-stream, "Reset on exit".
// add appends the step's bytes for the filter's state, which may be
// none.
type resetStep struct {
	name string
	add  func(f *Filter, b []byte) []byte
}

// resetSteps is the exit reset in the order it runs. Each step has a
// name, so a test can remove one and see the C10-terminal property
// fail. The order matters:
//
//  1. Synchronized output goes off first, so the terminal draws what
//     follows.
//  2. The guest's pushes on the alternate screen are popped while that
//     screen is active, because the kitty keyboard protocol keeps one
//     stack per screen. Then the alternate screen is left, which also
//     restores the cursor the switch saved.
//  3. The guest's pushes on the main screen are popped.
//  4. The modes and the style are reset after the screen switch,
//     because leaving the alternate screen restores the style and
//     character sets saved when it was entered. The scroll region goes
//     first in this group, because its save and restore of the cursor
//     also restores a style and character sets that later steps reset.
//  5. The reports clear the progress indicator and the program status.
//  6. A new line, so what `wb` prints next starts at the left edge.
//
// The mode and style steps run every time, because what the guest set
// is unknown. No step sends `ESC c`, which can clear the scroll-back.
var resetSteps = []resetStep{
	{"synchronized-output", fixed("\x1b[?2026l")},
	{"alternate-pops", alternatePops},
	{"alternate-screen", leaveAlternate},
	{"main-pops", mainPops},
	// `CSI r` moves the cursor home, so a save and restore keeps it
	// where the session left it.
	{"scroll-region", fixed("\x1b7\x1b[r\x1b8")},
	{"style", fixed("\x1b[0m")},
	{"mouse", fixed("\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1016l")},
	{"bracketed-paste", fixed("\x1b[?2004l")},
	{"focus-events", fixed("\x1b[?1004l")},
	{"color-scheme", fixed("\x1b[?2031l")},
	{"cursor-visible", fixed("\x1b[?25h")},
	{"cursor-style", fixed("\x1b[0 q")},
	{"g0", fixed("\x1b(B")},
	{"g1", fixed("\x1b)B")},
	// SI invokes G0. The filter drops SO, so the guest can't invoke G1,
	// and this step is there in case a later allowlist passes it.
	{"shift-in", fixed("\x0f")},
	{"modify-other-keys", fixed("\x1b[>4m")},
	{"keypad", fixed("\x1b>")},
	// The filter drops `CSI ? 5 W`, so the guest can't send it. `wb`
	// does, to set a tab stop every 8 columns again.
	{"tab-stops", fixed("\x1b[?5W")},
	{"progress", progressClear},
	{"status", statusClear},
	{"new-line", fixed("\r\n")},
}

// ExitReset returns the bytes that undo what the guest may have
// changed in the terminal, for the filter's state now
// (S16-03-around-the-stream, "Reset on exit"). It writes nothing and
// changes nothing, so two calls give the same bytes. `wb` writes them
// after the relay ends and before it prints anything. Without them the
// guest could hide the real session summary, and the flags on returned
// work that SEC03-no-host-exec relies on, for example by printing in
// the background color.
func (f *Filter) ExitReset() []byte {
	return f.exitReset("")
}

// exitReset builds the reset without the step named skip.
func (f *Filter) exitReset(skip string) []byte {
	var b []byte
	for _, s := range resetSteps {
		if s.name != skip {
			b = s.add(f, b)
		}
	}
	return b
}

func fixed(seq string) func(*Filter, []byte) []byte {
	return func(_ *Filter, b []byte) []byte { return append(b, seq...) }
}

// kittyPopSeq appends `CSI < n u` for n entries, or nothing for none.
// The filter's counts hold only the guest's pushes, so the pop never
// reaches an entry the user's shell pushed (S16-01-filter, "Kitty
// keyboard stacks").
func kittyPopSeq(b []byte, n int) []byte {
	if n == 0 {
		return b
	}
	b = append(b, "\x1b[<"...)
	b = strconv.AppendInt(b, int64(n), 10)
	return append(b, 'u')
}

// alternatePops pops the guest's pushes on the alternate screen. When
// the main screen is active, it visits the alternate screen to pop them,
// with synchronized output on so the terminal doesn't draw the visit.
func alternatePops(f *Filter, b []byte) []byte {
	n := f.kb.pushes[1]
	if n == 0 || f.kb.alt {
		return kittyPopSeq(b, n)
	}
	b = append(b, "\x1b[?2026h\x1b[?1049h"...)
	b = kittyPopSeq(b, n)
	return append(b, "\x1b[?1049l\x1b[?2026l"...)
}

func leaveAlternate(f *Filter, b []byte) []byte {
	if !f.kb.alt {
		return b
	}
	return append(b, "\x1b[?1049l"...)
}

func mainPops(f *Filter, b []byte) []byte {
	return kittyPopSeq(b, f.kb.pushes[0])
}

// progressClear removes the progress indicator, only when a progress
// report passed: a terminal that reads every OSC 9 as a notification
// would otherwise show one at each exit.
func progressClear(f *Filter, b []byte) []byte {
	if !f.seen.progress {
		return b
	}
	return append(b, "\x1b]9;4;0\x1b\\"...)
}

// statusClear clears the program status records, only when a report
// passed (S16-03-around-the-stream, "Reset on exit").
func statusClear(f *Filter, b []byte) []byte {
	if !f.seen.status {
		return b
	}
	return append(b, "\x1b]7501;state=clear\x1b\\"...)
}
