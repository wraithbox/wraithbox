package termfilter

import "testing"

// resetSeeds are the seeds of FuzzExitReset that #301 names: a change
// to each state the model keeps.
var resetSeeds = []string{
	"\x1b[>1u\x1b[?1049h\x1b[>1u\x1b[>1u",
	"\x1b[>1u\x1b[?1049h\x1b[>1u\x1b[?1049l",
	"\x1b=",
	"\x1b(0",
	"\x1b[5;10r",
	"\x1b[3g",
	"\x1b[7m",
	"\x1b[38;2;1;2;3m",
	"\x1b[?1003h",
	"\x1b[>4;2m",
	"\x1b[4 q",
	"\x1b[?2026h",
	"\x1b]9;4;1;50\x07",
	"\x1b]7501;state=blocked;kind=permission;msg=approve\x1b\\",
	"\x1b[1m\x1b[?1049h\x1b[0m\x1b7\x1b[7m\x1b(0\x1b[?1049l\x1b[!p\x1b[?25l",
}

// FuzzExitReset checks the last property of C10-terminal, "Fuzzing",
// with the BEL limit off: the filter's output followed by its exit
// reset, replayed into the terminal state model, leaves the model in
// its initial state, the user's kitty keyboard entry included.
func FuzzExitReset(f *testing.F) {
	for _, s := range resetSeeds {
		f.Add([]byte(s))
	}
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		m, reset := replay(t, in, "", false)
		if c := m.changed(); len(c) > 0 {
			t.Fatalf("after the reset %q the model differs in %v", reset, c)
		}
	})
}
