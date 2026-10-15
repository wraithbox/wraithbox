# S16-03 - Around the Stream

**Purpose:** What `wb` writes to the terminal around the filtered stream, the
terminal environment the guest gets, and how guest text in `wb`'s
other output is escaped.

## Design

### Around the stream

What `wb` itself writes to the terminal (the
window title, the reset on exit, and the `CSI ? 6 n` sentinel query of
the input drain) happens only when `wb`'s standard output is a
terminal, and the drain also needs standard input to be one (decided
on I37, 2026-10-06). `wb claude -p … > out.txt` gets none of them, and
its output still passes the filter (S16-01-filter, "Where").

### Title

`wb` saves the user's window title on the terminal's
title stack (`CSI 22 t`), sets its own (`wb: <session id>`), and
restores the saved one on exit (`CSI 23 t`). A guest title would
reach desktop notifications through the bell, as iTerm2's
Notification Center alerts and Ghostty's `bell-features = title` do
(SEC14-no-fake-approvals).

### Reset on exit

After the relay ends and before `wb` prints
anything, it resets what the guest may have changed: colors and
style, the alternate screen, mouse modes, bracketed paste, focus
events, color scheme reports, synchronized output, cursor visibility
and style, the scroll region, the character sets, the kitty keyboard
stacks (the guest's pushes popped, the alternate screen's before
leaving it), `modifyOtherKeys` (`CSI > 4 m`), the keypad mode
(`ESC >`), tab stops (`CSI ? 5 W`), and the progress indicator.
Then it starts a new line. Without it, the guest can print a fake
summary in the background color and hide the real flags that
SEC03-no-host-exec relies on. The summary is advisory, and `wb diff`
is authoritative. Not `ESC c`, which can clear the scroll-back.
Rejected: running the session on an alternate screen of `wb`'s own,
which resets everything but doesn't leave the session's output in the
scroll-back, unlike `claude` (FR01-drop-in, decided on I37,
2026-10-06).

### Input drain

Before restoring cooked mode, `wb` sends a sentinel
query that the allowlist never passes, `CSI ? 6 n` (extended cursor
position), and reads and discards input until the reply of that form
(`CSI ? … R`) arrives or 100 ms pass. Terminals answer in order, so
every reply to a guest query comes first, and a guest `CSI 6 n` can't
end the drain early. A late reply to a guest query doesn't reach the
user's shell. Input to the guest is not filtered.

### Environment

The guest session gets these host variables and no
others: `TERM`, `TERM_PROGRAM`, `TERM_PROGRAM_VERSION`, `COLORTERM`,
`LANG`, `LC_ALL`, `LC_CTYPE`. Claude Code picks its sequences from the
first four and from query replies. The locale ones keep the guest's
character encoding the same as the host's, also on a host that sets
`LC_ALL` or `LC_CTYPE` without `LANG`. `LC_ALL` also sets the
formatting categories, as it does on the host. The other `LC_*`
variables, which set one formatting category each (`LC_TIME`,
`LC_NUMERIC`, …), are left out. Not `TMUX` or `STY`,
which make Claude Code wrap sequences in DCS, and not `SSH_*`,
`TERMINFO`, `LC_TERMINAL` or `ITERM_SESSION_ID`. The window size
crosses as resize messages.

### Terminfo

`TERM` crosses unchanged. `wb` and `wb-guestd` never
map it to another name, so what Claude Code prints is the same as on
the host. Claude Code doesn't read terminfo, but programs in the
session such as `less`, `vim` and `top` look `TERM` up in it.
`TERMINFO` names a path on the host and doesn't cross. The guest
looks `TERM` up in the terminfo database of its image
(S06-02-image-build, "Terminfo"). That database has ncurses' entries
and the names Ghostty and kitty set, `xterm-ghostty` and
`xterm-kitty`. The `less`, `vim` and `top` that macOS ships can't
read the kitty entries yet (S06-02-image-build, "Terminfo", I203).
When the guest has no entry for `TERM`, those programs
report an unknown terminal, and `TERM` still isn't changed (I83).

### Guest text elsewhere

Wherever else `wb` prints text the guest
influenced (the session summary and its link list, `wb diff`,
`wb sessions`, `wb approve`, `wb audit`, `wb learn report`,
`wb status`), it never passes a code point of the escaped set: a
control (Unicode category Cc, which holds `ESC` and so every escape
sequence), a format character (Cf, such as the bidirectional controls
and the zero-width characters), a line or paragraph separator (Zl,
Zp), or a `Default_Ignorable_Code_Point` (such as the tag characters,
the variation selectors and the Hangul fillers). `wb diff`, logs and
audit records show them escaped, where a reviewer sees them, and
S09-10-audit gives the form for audit
records. Other
output removes them (SEC10-audit, SEC14-no-fake-approvals).

**Status:** Draft
