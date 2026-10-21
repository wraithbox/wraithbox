# S16-03 - Around the Stream

**Purpose:** What `wb` writes to the terminal around the filtered stream, the
terminal environment the guest gets, and how guest text in `wb`'s
other output is escaped.

## Design

### Around the stream

`wb` itself writes these to the terminal:

- the window title ("Title");
- the reset on exit ("Reset on exit");
- the `CSI ? 6 n` sentinel query of the input drain ("Input drain").

It writes them only when its standard output is a terminal. The drain
also needs standard input to be one.

Standard error with frames of its own passes a filter of its own. When
it goes to a terminal and got output, it also gets that filter's reset.
So `wb claude -p … > out.txt` in a terminal still resets what the
guest's standard error changed, before the summary on standard error.

`wb claude -p … > out.txt` gets nothing else. Its output still passes
the filter (S16-01-filter, "Where").

Before `wb` stops itself for job control, it writes the reset, drains
the input and restores the title too (S05-02-exit-status-and-signals,
"Job control").

### Title

1. `wb` saves the user's window title on the terminal's title stack
   (`CSI 22 t`).
2. It sets its own: `wb: <session id>`, or the shell id for `wb shell`.
3. On exit it restores the saved one (`CSI 23 t`).

A guest title would reach desktop notifications through the bell
(SEC14-no-fake-approvals). iTerm2's Notification Center alerts and
Ghostty's `bell-features = title` do that.

### Reset on exit

After the relay ends, and before `wb` prints anything, it resets what
the guest may have changed:

- colors and style;
- the alternate screen;
- mouse modes, bracketed paste, focus events and color scheme reports;
- synchronized output;
- cursor visibility and style;
- the scroll region and the character sets;
- the kitty keyboard stacks: the guest's pushes popped, the alternate
  screen's before leaving it;
- `modifyOtherKeys` (`CSI > 4 m`) and the keypad mode (`ESC >`);
- tab stops (`CSI ? 5 W`);
- the progress indicator;
- the program status records, with `OSC 7501 ; state=clear`, when the
  guest sent a report (S16-01-filter, "Program status").

When the guest left pushes on the alternate screen and then returned
to the main screen, `wb` visits the alternate screen to pop them, with
synchronized output on so the terminal doesn't draw the visit.

Then it starts a new line. It doesn't send `ESC c`, which can clear
the scroll-back.

Without the reset, the guest can print a fake summary in the
background color, and hide the real flags that SEC03-no-host-exec
relies on. The summary is advisory, and `wb diff` is authoritative.

### Input drain

Before it restores cooked mode, `wb`:

1. sends a sentinel query that the allowlist never passes: `CSI ? 6 n`
   (extended cursor position);
2. reads and discards input until the reply of that form
   (`CSI ? … R`) arrives, or 100 ms pass.

Terminals answer in order. So every reply to a guest query comes
first, and a guest `CSI 6 n` can't end the drain early. A late reply
to a guest query doesn't reach the user's shell.

Input to the guest is not filtered.

### Environment

The guest session gets these host variables, and no others:

- `TERM`, `TERM_PROGRAM`, `TERM_PROGRAM_VERSION` and `COLORTERM`.
  Claude Code picks its sequences from these and from query replies.
- `LANG`, `LC_ALL` and `LC_CTYPE`. They keep the guest's character
  encoding the same as the host's, also on a host that sets `LC_ALL`
  or `LC_CTYPE` without `LANG`. `LC_ALL` also sets the formatting
  categories, as it does on the host.

These stay out:

- the other `LC_*` variables, which set one formatting category each
  (`LC_TIME`, `LC_NUMERIC`, …);
- `TMUX` and `STY`, which make Claude Code wrap sequences in DCS;
- `SSH_*`, `TERMINFO`, `LC_TERMINAL` and `ITERM_SESSION_ID`.

The window size crosses as resize messages.

### Terminfo

`TERM` crosses unchanged. `wb` and `wb-guestd` never map it to another
name, so what Claude Code prints is the same as on the host.

Claude Code doesn't read terminfo. Programs in the session such as
`less`, `vim` and `top` look `TERM` up in it. `TERMINFO` names a path
on the host, and doesn't cross.

The guest looks `TERM` up in the terminfo database of its image
(S06-02-image-build, "Terminfo"). That database has ncurses' entries,
and the names Ghostty and kitty set: `xterm-ghostty` and `xterm-kitty`.
The `less`, `vim` and `top` that macOS ships can't read the kitty
entries (S06-02-image-build, "macOS's ncurses").

When the guest has no entry for `TERM`, those programs report an
unknown terminal. `TERM` still isn't changed (I83).

### Guest text elsewhere

`wb` prints text the guest influenced in other places too:

- the id in the window title;
- the session summary and its link list;
- the `wraith box:` line of a failure;
- `wb diff`, `wb sessions`, `wb approve`, `wb audit`,
  `wb learn report` and `wb status`.

There it never passes a code point of the escaped set:

- a control: Unicode category Cc, which holds `ESC`, and so every
  escape sequence;
- a format character (Cf), such as the bidirectional controls and the
  zero-width characters;
- a line or paragraph separator (Zl, Zp);
- a `Default_Ignorable_Code_Point`, such as the tag characters, the
  variation selectors and the Hangul fillers.

`wb diff`, logs and audit records show them escaped, where a reviewer
sees them. S09-11-audit gives the form for audit records. Other output
removes them (SEC10-audit, SEC14-no-fake-approvals).

## Decisions

### Write around the stream only to a terminal

What `wb` writes around the stream needs standard output to be a
terminal, and the drain also standard input.

- **Decided:** I37, 2026-10-06.

### Reset on exit, not an alternate screen

`wb` resets the terminal on exit.

- **Rejected:** running the session on an alternate screen of `wb`'s
  own. That resets everything, but doesn't leave the session's output
  in the scroll-back, unlike `claude` (FR01-drop-in).
- **Decided:** I37, 2026-10-06.

**Status:** Draft
