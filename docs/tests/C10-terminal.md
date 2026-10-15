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
  headless terminal emulator, leaves it in its initial state: modes,
  kitty keyboard stacks on both screens, keypad mode, character sets,
  scroll region, tab stops, and colors and style.

For each of the states in the last property, a test removes its step
from the reset and sees the property fail, for example `ESC =` with no
`ESC >`.

## Conformance

- Forge an approval prompt through terminal output, and see that
  nothing treats it as one.
- Print every sequence the terminal filter drops, a guest window title,
  and a fake session summary in the background color, then exit. See
  that the host terminal received none of the sequences, still shows
  the title `wb` set, and shows the real summary legibly.

**Status:** Draft
