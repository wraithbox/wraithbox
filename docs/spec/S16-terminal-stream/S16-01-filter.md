# S16-01 - Filter

**Purpose:** Where `wb` filters guest output, how the filter splits it into
tokens, and the allowlist of what passes.

## Design

### Where

The filter runs on every stream that `wb claude` or `wb shell` writes
to a terminal. On WSL that is the WSL-side `wb`, which writes to the
terminal (S12-platforms).

A stream that goes to a pipe or a file passes the same full filter.
Scripts read such a stream (`claude -p`), and a later `| cat` puts it
on the terminal. The filter passes only output that is safe in a
terminal, so a later `cat` of the file is as safe as the live session.

The filter passes text, Markdown and JSON unchanged when they:

- are valid UTF-8;
- don't hold a control character that the allowlist below drops: C1,
  `DEL`, or a C0 control other than the ones it passes.

Invalid bytes become U+FFFD, so binary output through standard output
isn't supported. If moving files out of the guest is ever needed, it
gets its own command, not an unfiltered stream.

### How

The filter splits the stream into text, controls and sequences. It
passes a token only when a rule below allows it.

It drops any other token whole, from its `ESC` to its end. The
terminal's parser is then back in its ground state after every token.

It never escapes a dropped sequence into visible text, which would
move what the TUI drew.

### Ends of a sequence

- A CSI ends at its final byte (`@` to `~`).
- A string sequence (OSC, DCS, APC, PM, SOS) ends at ST, written as
  `ESC \` or as U+009C. An OSC also ends at BEL.
- CAN and SUB end any sequence.
- An `ESC` inside a string sequence that doesn't start ST also ends
  it, and then begins the next token.

The filter buffers at most 64 bytes of an unfinished CSI, and 16 KB of
an unfinished string sequence. Past that it discards everything up to
the sequence's end, counted as one drop. So the tail of a long payload
never shows as text.

### Counts

Each drop is counted by rule, and the session summary reports the
counts (NFR06-explained-refusals). A sequence that a new Claude Code
version starts to use shows up there (S16-02-drops-and-links, "Drop
rules").

### Allowlist

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

### Where the list comes from

The list is the one X19-terminal-filter measured with Claude Code
2.1.289, tightened as the security review of PR66 asked:

- modes the recordings don't hold are left off;
- each cap is above the largest value recorded.

OSC 10 to 12 queries stay, because the Claude Code binary holds the
background color query.

Queries pass because the reply goes to the guest. Claude Code uses
them to learn what the terminal supports. What a reply tells the guest
is T09-terminal-fingerprint.

### Kitty keyboard stacks

The kitty keyboard protocol keeps one stack per screen. So `wb` counts
the guest's pushes on the main and the alternate screen apart. It drops
a pop that would reach an entry the user's shell pushed.

### Never on the list

These never join the list:

- the title report (`CSI 21 t`);
- OSC 52 with `?`;
- DECRQSS (a DCS).

Each makes the terminal type guest-chosen text back into its input.
That text would reach the user's shell after `wb` exits.

## Decisions

### Filter pipes and files too

A stream that goes to a pipe or a file passes the same full filter as
terminal output.

- **Why:** one filter keeps one code path and one fuzz target, and
  keeps colors for `less -R` and CI logs.
- **Rejected:** passing the stream unchanged, which weakens
  SEC03-no-host-exec; dropping only `ESC`-introduced sequences, which
  is a second filter to keep and fuzz.
- **Decided:** I37, 2026-10-06.

**Status:** Draft
