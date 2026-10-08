# S16 - Terminal Stream

**Purpose:** Specify how `wb` guards boundary 6 of T00-index, the
guest output that `wb` writes to the user's terminal.

**Requirements:** FR01-drop-in, SEC03-no-host-exec, SEC10-audit, SEC14-no-fake-approvals, NFR06-explained-refusals.

Brief: B37-terminal-boundary

## Design

The terminal emulator parses the guest's output and acts on some of it
on the host: OSC 52 writes the clipboard, iTerm2's OSC 1337 saves
files, kitty graphics can read a file by its path, and every parser
bug is a path to the host. So guest output is boundary 6 of T00-index,
and `wb` filters it with an allowlist that fails closed, as `wb-netd`
filters packets (SEC03-no-host-exec).

- **Where.** On every stream that `wb claude` or `wb shell` writes to a
  terminal. On WSL that is the WSL-side `wb`, which writes to the
  terminal (S12-platforms). A stream that goes to a pipe or a file
  passes the same full filter (decided on I37, 2026-10-06). Scripts
  read such a stream (`claude -p`), and a later `| cat` puts it on the
  terminal. The filter passes only output that is safe in a terminal,
  so a later `cat` of the file is as safe as the live session. One
  filter keeps one code path and one fuzz target, and keeps colors for
  `less -R` and CI logs. It passes text, Markdown and JSON unchanged
  when they are valid UTF-8 and don't hold a control character that
  the table below drops (C1, `DEL`, or a C0 control other than the ones it
  passes). Invalid bytes become U+FFFD, so binary output through
  standard output isn't supported. If moving files out of the guest is
  ever needed, it gets its own command, not an unfiltered stream.
  Rejected: passing the stream unchanged, which weakens
  SEC03-no-host-exec, and dropping only `ESC`-introduced sequences,
  which is a second filter to keep and fuzz.
- **How.** The filter splits the stream into text, controls and
  sequences, and passes a token only when a rule below allows it. It
  drops any other token whole, from its `ESC` to its end, so the
  terminal's parser is back in its ground state after every token. It
  never escapes a dropped sequence into visible text, which would move
  what the TUI drew.
- **Ends of a sequence.** A CSI ends at its final byte (`@` to `~`). A
  string sequence (OSC, DCS, APC, PM, SOS) ends at ST, written as
  `ESC \` or as U+009C, and an OSC also at BEL. CAN and SUB end any
  sequence, and so does an `ESC` inside a string sequence that doesn't
  start ST, which then begins the next token. The filter buffers at most 64 bytes of an unfinished CSI
  and 16 KB of an unfinished string sequence. Past that it discards
  everything up to the sequence's end, counted as one drop, so the tail
  of a long payload never shows as text.
- **Counts.** Each drop is counted by rule, and the session summary
  reports the counts (NFR06-explained-refusals). A sequence that a new
  Claude Code version starts to use shows up there ("Drop rules").

| Class | Passes | Dropped |
|---|---|---|
| Text | valid UTF-8 | C1 controls (U+0080 to U+009F); invalid bytes become U+FFFD |
| C0 controls, DEL | BEL (at most one per second), BS, HT, LF, CR, SI | the rest, DEL included |
| `ESC` | `7`, `8`, `M`, `D`, `E`, `=`, `>`, `( B`, `( 0`, `) B`, `) 0` | the rest, `ESC c` included |
| CSI parameters | digits, `;` and `:`, at most 16 values of at most 5 digits, 64 bytes in all | any other |
| CSI screen | `A` to `H`, `f`, `` ` ``, `a`, `d`, `e`, `@`, `L`, `M`, `P`, `X`, `b`, `S`, `T`, `r`, `I`, `Z`, `g`, `m`; `J` and `K` with 0 to 2; `s` and `u` without parameters | `CSI 3 J`, which erases the host's scroll-back |
| CSI modes | `? h/l` for 25, 1000, 1002, 1003, 1004, 1006, 1016, 1049, 2004, 2026, 2031; `? … $ p` (mode query) | other modes |
| CSI keyboard | kitty keyboard `> u` (push, at most 4 deep per screen), `< u` (pop, only entries the guest pushed on that screen), `? u`; `> 4 ; 2 m` and `> 4 m`; `SP q`; `! p` | `= u`, which changes the current entry in place; other `> m` |
| CSI queries | `c`, `> c`, `5 n`, `6 n`, `> q`, `14 t`, `16 t`, `18 t` | other `t`: window moves, title reports, the title stack |
| OSC | 9 ; 4 (progress) with numeric parameters; 10, 11, 12 with `?` only; 9, 99 and 777 (notifications) become one BEL | the rest: 0 to 2 (title), 8 (links), 52, 1337, 7, 133, … |
| DCS, APC, PM, SOS | | all, whole |

The list is the one X19-terminal-filter measured with Claude Code
2.1.289, tightened as the security review of PR66 asked: modes the
recordings don't hold are left off, and each cap is above the largest
value recorded. OSC 10 to 12 queries stay because the Claude Code
binary holds the background color query. Queries pass because the
reply goes to the guest, and Claude Code uses them to learn what the
terminal supports. What a reply tells the guest is T09-terminal-fingerprint.
The kitty keyboard protocol keeps one stack per screen, so `wb` counts
the guest's pushes on the main and the alternate screen apart, and
drops a pop that would reach an entry the user's shell pushed. The
title report (`CSI 21 t`), OSC 52 with `?`, and DECRQSS (a DCS) never
join the list: each makes the terminal type guest-chosen text back
into its input, which would reach the user's shell after `wb` exits.

**Drop rules.** The counts are by these rules. They are a fixed list,
so a guest can't make up new ones, and the audit log records the
counts by these names (S09-policy-credentials-audit, "Audit").

| Rule | Counts |
|---|---|
| `terminal-c1` | C1 controls in text |
| `terminal-c0` | the C0 controls and `DEL` the table drops, and each BEL over the rate |
| `terminal-esc` | `ESC` sequences the table drops |
| `terminal-csi` | CSI sequences the table drops, other than `t` |
| `terminal-csi-window` | CSI `t` sequences the table drops: window moves, title reports, the title stack |
| `terminal-osc-title` | OSC 0 to 2 |
| `terminal-osc-link` | OSC 8 |
| `terminal-osc-clipboard` | OSC 52 |
| `terminal-osc-file` | OSC 7 (current directory), 1337 (iTerm2 files and clipboard) and 5113 (kitty file transfer) |
| `terminal-osc` | the other OSC sequences the table drops |
| `terminal-osc-notify` | OSC 9, 99 and 777, which the filter turns into a BEL rather than drop |
| `terminal-dcs` | DCS |
| `terminal-apc` | APC, the kitty graphics protocol among them |
| `terminal-pm-sos` | PM and SOS |

A sequence cut at a buffer cap counts under the rule of its class, as
far as its first bytes tell it. A CSI cut at the 64-byte cap, or ended
by CAN, SUB or `ESC` before its final byte, counts as `terminal-csi`.
A CSI with the final byte `t` counts as `terminal-csi-window` whatever
its parameters, also when they break the CSI parameter caps. An OSC
without a number before its first `;` counts as `terminal-osc`. An
OSC `9 ; 4` whose parameters aren't all numeric is a malformed
progress report, not a notification: the filter drops it, counted as
`terminal-osc`, and doesn't turn it into a BEL. An invalid byte that
the filter replaces with U+FFFD isn't counted as a drop and has no
rule, because the text around it stays.

**Links.** OSC 8 is dropped and its text stays as plain text (H1,
I30). A link's text can differ from its target, and a link to
`localhost` or a private address names a service on the host, not in
the guest. Terminals still make a bare URL clickable. `wb` keeps each
dropped link's URL (its first 2048 bytes, as the guest sent them,
an invalid byte counting as one) and text (its first 256 code points
after the text filter, an invalid byte counting as the one U+FFFD that
replaces it), and notes when it cut either. It records them in the
audit log through `wb-hostd` (S09-policy-credentials-audit, "Audit"),
and lists them, without repeats, in the session summary. It keeps at
most 100 entries. Past that it counts the rest by mark, the ones
without a mark as `unmarked`. The summary then says "and N more". An
entry is marked when the URL is `file:` (a guest path), an IP literal,
`localhost` or a name under `.localhost`, `.local`, `.internal`,
`.home.arpa` or `.lan`, a name without a dot, a URL with a user name, `http:`, or another scheme. The audit log names
these marks `guest-path`, `ip-literal`, `local-name`, `dotless-name`,
`user-name`, `http` and `other-scheme`, in the same order, and an
entry gets the first one that applies. The mark is a
heuristic on the name alone: an unmarked name can still resolve to a
host-local address, so no mark doesn't mean safe.
Rejected: rewriting a link inline as `text (url)`, which changes line
widths the TUI has laid out; passing `https://` links to public hosts,
which leaves the text-target mismatch; and passing a link only when
its text equals its URL, which makes the filter follow the screen.

**Around the stream.** What `wb` itself writes to the terminal (the
window title, the reset on exit, and the `CSI ? 6 n` sentinel query of
the input drain) happens only when `wb`'s standard output is a
terminal, and the drain also needs standard input to be one (decided
on I37, 2026-10-06). `wb claude -p … > out.txt` gets none of them, and
its output still passes the filter ("Where").

- **Title.** `wb` saves the user's window title on the terminal's
  title stack (`CSI 22 t`), sets its own (`wb: <session id>`), and
  restores the saved one on exit (`CSI 23 t`). A guest title would
  reach desktop notifications through the bell, as iTerm2's
  Notification Center alerts and Ghostty's `bell-features = title` do
  (SEC14-no-fake-approvals).
- **Reset on exit.** After the relay ends and before `wb` prints
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
- **Input drain.** Before restoring cooked mode, `wb` sends a sentinel
  query that the allowlist never passes, `CSI ? 6 n` (extended cursor
  position), and reads and discards input until the reply of that form
  (`CSI ? … R`) arrives or 100 ms pass. Terminals answer in order, so
  every reply to a guest query comes first, and a guest `CSI 6 n` can't
  end the drain early. A late reply to a guest query doesn't reach the
  user's shell. Input to the guest is not filtered.
- **Environment.** The guest session gets these host variables and no
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
- **Terminfo.** `TERM` crosses unchanged. `wb` and `wb-guestd` never
  map it to another name, so what Claude Code prints is the same as on
  the host. Claude Code doesn't read terminfo, but programs in the
  session such as `less`, `vim` and `top` look `TERM` up in it.
  `TERMINFO` names a path on the host and doesn't cross. The guest
  looks `TERM` up in the terminfo database of its image
  (S06-vm-lifecycle, "Terminfo"). That database has ncurses' entries
  and the names Ghostty and kitty set, `xterm-ghostty` and
  `xterm-kitty`. When the guest has no entry for `TERM`, those programs
  report an unknown terminal, and `TERM` still isn't changed (I83).

**Guest text elsewhere.** Wherever else `wb` prints text the guest
influenced (the session summary and its link list, `wb diff`,
`wb sessions`, `wb approve`, `wb audit`, `wb learn report`,
`wb status`), it never passes a code point of the escaped set: a
control (Unicode category Cc, which holds `ESC` and so every escape
sequence), a format character (Cf, such as the bidirectional controls
and the zero-width characters), a line or paragraph separator (Zl,
Zp), or a `Default_Ignorable_Code_Point` (such as the tag characters,
the variation selectors and the Hangul fillers). `wb diff`, logs and
audit records show them escaped, where a reviewer sees them, and
S09-policy-credentials-audit ("Audit") gives the form for audit
records. Other
output removes them (SEC10-audit, SEC14-no-fake-approvals).

## Out of scope

- The TTY relay itself, and the exit status and signals of `wb claude`,
  are in S05-cli ("Agent commands", "Exit status and signals").

**Status:** Draft
