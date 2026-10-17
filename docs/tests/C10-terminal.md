# C10 - Terminal

**Purpose:** The test cases for the terminal filter in `wb`, which
stands between the guest's output and the host terminal.

**Verifies:** S16-terminal-stream, X19-terminal-filter

## Fuzzing

The terminal filter's fuzz target checks these properties, with the
BEL rate limit off:

- every output token is one the allowlist passes unchanged, a BEL that
  replaced a notification, or U+FFFD for an invalid byte;
- filtering twice gives the bytes of filtering once;
- where a read splits the input doesn't change the output;
- no OSC 0 to 2, 8, 52 or 1337 survives, and no DCS, APC, PM, or SOS
  introducer;
- no parameter exceeds the caps, and no `J` or `K` has a parameter
  above 2;
- the link list holds at most 100 entries, and marks every URL that
  matches a mark rule of S16-terminal-stream;
- the filtered output followed by the exit reset, replayed into a
  terminal state model, leaves it in its initial state. The model
  tracks the modes on the allowlist, colors and style, the active
  screen, cursor visibility and style, the scroll region, G0 and G1
  and which one is invoked, the keypad mode, `modifyOtherKeys`, tab
  stops, a kitty keyboard stack per screen, the progress indicator,
  and whether a program status report is open. Its main stack starts
  with one entry of the user's own, which the reset must keep. It is a
  model and not a terminal emulator, because the emulator
  X19-terminal-filter used tracks neither the kitty keyboard stacks
  nor `modifyOtherKeys`.

For each of the states in the last property, a test removes its step
from the reset and sees the property fail, for example `ESC =` with no
`ESC >`. Other tests check the model itself on known sequences
(`FuzzExitReset`, `TestExitResetWithoutStep` and `TestModel` in
`internal/termfilter`).

## Conformance

- Forge an approval prompt through terminal output, and see that
  nothing treats it as one.
- Print every sequence the terminal filter drops, a guest window title,
  and a fake session summary in the background color, then exit. See
  that the host terminal received none of the sequences, still shows
  the title `wb` set, and shows the real summary legibly.

**Status:** Draft
