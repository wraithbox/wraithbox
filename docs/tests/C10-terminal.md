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
  whether a program status report is open, and the window title and
  its stack. The kitty keyboard stack of the main screen starts with
  one entry of the user's own, which the reset must keep. It is a
  model and not a terminal emulator, because the emulator
  X19-terminal-filter used tracks neither the kitty keyboard stacks
  nor `modifyOtherKeys`.

For each of the states in the last property, a test removes its step
from the reset and sees the property fail, for example `ESC =` with no
`ESC >`. Other tests check the model itself on known sequences
(`FuzzExitReset`, `TestExitResetWithoutStep` and `TestModel` in
`internal/termfilter`).

## Relay

The relay in `wb` (`internal/relay`), with a fake stream and a fake
terminal unless a line says otherwise. Each test is named after its
item of S05-02-exit-status-and-signals or S16-03-around-the-stream:

- raw mode is on during the relay and off before it, and only in
  interactive use (`TestRawMode`);
- on a real PTY, raw mode has ISIG off, and the termios is restored
  after an exit frame, a failure frame, a closed stream, and a panic in
  the relay or its receiver (`TestRawModeRestored`, `TestPanicRestoresMode`);
- the title is saved and set, restored at exit and before a stop, with
  the id stripped, and not written when standard output isn't a
  terminal (`TestTitle`);
- the reset follows the last output and comes before the summary and
  the `wraith box:` line (`TestResetBeforeLine`), and standard error
  that is a terminal gets its own (`TestStderrReset`). On a pipe, the
  report starts a line of its own after guest text without a final
  new line (`TestReportOwnLine`);
- the drain ends on `CSI ? 1 ; 1 R`, ends after 100 ms with no reply,
  discards earlier input, and isn't ended by `CSI 1 ; 1 R` (`TestDrain`);
  its reply parser is fuzzed (`FuzzDrainReply`);
- of a host environment with every name S16-03-around-the-stream
  includes and excludes, only the seven cross and `TERM` is unchanged,
  and a value over 256 bytes or with a control byte is left out and
  named (`TestEnvironment`);
- `SIGINT`, `SIGQUIT`, `SIGTERM` and `SIGWINCH` give their frames
  (`TestForwarded`), after `SIGHUP` nothing more reaches the terminal
  (`TestHangup`) while piped input still crosses
  (`TestHangupPipedInput`), and `EPIPE` gives the
  standard-output-closed frame (`TestClosedOutput`);
- job control in interactive use, after a stopped frame and after an
  outside `SIGTSTP` with and without one (`TestJobControlInteractive`),
  and in non-interactive use, where a stopped frame is ignored
  (`TestJobControlNonInteractive`). With standard output a terminal,
  the reset and the title restore come before the stop
  (`TestJobControlNonInteractiveTerminal`). A stopped frame that comes
  after wb stopped without it is ignored (`TestLateStopped`);
- the exit table: codes 0, 2, 7 and 255, signal 9 as 137, a failure
  frame, a closed and a broken stream, and an exit code of 256 and a
  signal of 0 and 65, each 255 with a `wraith box:` last line
  (`TestExitStatus`). A response that isn't a frame gives 255 as well
  (`TestNotAFrame`). After a failed send, the exit frame wb-hostd
  queued still decides the status (`TestSendFailure`);
- a child process runs the relay as a real PTY's session leader, and
  gets real `SIGWINCH`, `SIGTSTP`, `SIGCONT` and `SIGHUP`, on darwin
  and linux (`TestSignalsOnPTY`).

## Conformance

- Forge an approval prompt through terminal output, and see that
  nothing treats it as one.
- Print every sequence the terminal filter drops, a guest window title,
  and a fake session summary in the background color, then exit. See
  that the host terminal received none of the sequences, still shows
  the title `wb` set, and shows the real summary legibly. At unit level,
  with a fake stream and a real PTY replayed into the terminal state
  model, which also tracks the title and its stack
  (`TestC10Conformance` in `internal/termfilter`).

**Status:** Draft
