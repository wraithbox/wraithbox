# S05-02 - Exit Status and Signals

**Purpose:** What `wb claude` exits with, and what it does with each signal.

## Design

### Exit status

`wb claude` exits as `claude` did, and with 255 when Wraith Box
failed, as `ssh` "exits with the exit status of the remote command or
with 255 if an error occurred" (`man ssh`):

| Outcome | `wb` exits with |
|---|---|
| `claude` exited with code n, and its work was returned | n |
| `claude` was ended by signal n, and its work was returned | 128 + n, as a shell reports it |
| a usage error in the `wb` flags | 2 |
| any other failure of Wraith Box: a refusal before the session starts, a lost session (S06-09-session-lifecycle), a WIP commit or push that failed, an exit status from the guest out of range | 255 |

`claude` can exit with 2 or 255 itself. When the code comes from Wraith
Box, the last line `wb` writes to standard error starts with
`wraith box:` and says what failed. When the work wasn't returned, that
line also gives `claude`'s own exit status. A script then never takes
work that didn't reach the host for returned work.

Signals:

### From the keyboard

In interactive use `Ctrl-C`, `Ctrl-\` and `Ctrl-Z`
are bytes in the terminal stream, and the PTY in the guest turns
them into signals for `claude`, as a local terminal does. A window
size change (`SIGWINCH`) becomes a resize message.

### Hangup

`SIGHUP` to `wb` ends the session (S05-01-agent-commands, "Session behavior").
`wb` still waits for the end, writes nothing more to the terminal,
and exits as the table says. When `wb` is killed instead, `wb-hostd`
sees its connection close and hangs up the session the same way.

### Forwarded

`wb` forwards `SIGINT`, `SIGQUIT` and `SIGTERM` that
it receives to `claude`'s process group, in interactive and
non-interactive use, and keeps waiting for `claude` to exit. Other
signals have their default effect on `wb`. One that ends it ends the
session as a hangup does.

### Job control

When `claude` stops itself in interactive use, as
it does on `Ctrl-Z`, the session helper in the guest sees it
(S06-09-session-lifecycle, "Session helper"), and `wb-guestd` reports it.
The report is guest input, and it can only make `wb` give the
terminal back early. `wb` then resets the terminal and drains its
input as on exit (S16-terminal-stream, "Around the stream"),
restores the user's window title, and stops itself with `SIGSTOP`, so the user's shell has the
terminal again. On `SIGCONT` (`fg`), `wb` sets its title and raw
mode again and has `wb-guestd` continue `claude`, which redraws. A
`SIGTSTP` sent to `wb` from outside, which a terminal in raw mode
never sends, gets the same steps, after `wb` has `wb-guestd` stop
`claude`'s process group. In non-interactive use, `wb` forwards
`SIGTSTP` to `claude` before it stops itself, and `SIGCONT` after
it continues. A stopped `wb` keeps its connection to `wb-hostd`, so
the session stays. That job control works through the helper is not
checked yet (I135).

### A closed output

`wb` ignores `SIGPIPE`, and an `EPIPE` on its
standard output in non-interactive use (`wb claude -p … | head -1`)
is how it learns that the reader closed its end. `wb-guestd` then closes
`claude`'s standard output, so `claude` sees a broken pipe as it
would on the host, and `wb` exits as the table says for `claude`'s
exit.

**Status:** Draft
