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
isn't supported. The filter's output is for a terminal that reads
UTF-8. On a terminal that reads 8-bit bytes, some bytes inside a valid
character act as C1 controls, 0x9B among them. If moving files out of the guest is ever needed, it
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
- Any byte that isn't a parameter, intermediate, or final byte ends a
  CSI early. The CSI is dropped, counted as `terminal-csi`, and the
  byte begins the next token, unless that byte is CAN or SUB. So no
  token the filter passes holds a control inside a CSI.
- A string sequence (OSC, DCS, APC, PM, SOS) ends at ST, written as
  `ESC \` or as U+009C. An OSC also ends at BEL.
- CAN and SUB end any sequence, and are dropped with it.
- An `ESC` inside a string sequence that doesn't start ST also ends
  it, and then begins the next token.

An OSC the filter passes keeps its terminator when that is BEL or
`ESC \`. One that ended with U+009C is written with `ESC \` instead,
because a terminal that doesn't read U+009C as ST would take the bytes
after it as the payload.

The filter buffers at most 64 bytes of an unfinished CSI, and 16 KB of
an unfinished string sequence. Past that it discards everything up to
the sequence's end, counted as one drop. So the tail of a long payload
never shows as text.

### Counts

Each drop is counted by rule, and the session summary reports the
counts (NFR06-explained-refusals). A sequence that a new Claude Code
version starts to use shows up there (S16-02-drops-and-links, "Drop rules").

### Allowlist

| Class | Passes | Dropped |
|---|---|---|
| Text | valid UTF-8 | C1 controls (U+0080 to U+009F); invalid bytes become U+FFFD |
| C0 controls, DEL | BEL (at most one per second), BS, HT, LF, CR, SI | the rest, DEL included |
| `ESC` | `7`, `8`, `M`, `D`, `E`, `=`, `>`, `( B`, `( 0`, `) B`, `) 0` | the rest, `ESC c` included |
| CSI parameters | digits, `;` and `:`, at most 16 values of at most 5 digits, 64 bytes in all | any other |
| CSI screen | `A` to `H`, `f`, `` ` ``, `a`, `d`, `e`, `@`, `L`, `M`, `P`, `X`, `b`, `S`, `T`, `r`, `I`, `Z`, `g`, `m`; `J` and `K` with 0 to 2; `s` and `u` without parameters | `CSI 3 J`, which erases the host's scroll-back |
| CSI modes | `? h/l` for 25, 1000, 1002, 1003, 1004, 1006, 1016, 1049, 2004, 2026, 2031; `? … $ p` (mode query) for those modes | other modes |
| CSI keyboard | kitty keyboard `> u` (push, at most 4 deep per screen), `< u` (pop, only entries the guest pushed on that screen), `? u`; `> 4 ; 2 m` and `> 4 m`; `SP q` | `= u`, which changes the current entry in place; other `> m`; `! p` (soft reset), because kitty then clears the user's kitty keyboard stacks and a terminal may turn line wrap off |
| CSI queries | `c`, `> c`, `5 n`, `6 n`, `> q`, `14 t`, `16 t`, `18 t` | other `t`: window moves, title reports, the title stack |
| OSC | 9 ; 4 (progress) with numeric parameters; 10, 11, 12 with `?` only; 7501 (program status) as "Program status" says; 9, 99 and 777 (notifications) become one BEL | the rest: 0 to 2 (title), 8 (links), 52, 1337, 7, 133, … |
| DCS, APC, PM, SOS | | all, whole |

### Where the list comes from

The list is the one X19-terminal-filter measured with Claude Code
2.1.289, tightened as the security review of PR66 asked:

- modes the recordings don't hold are left off;
- each cap is above the largest value recorded.
- the soft reset `! p`, which the recordings hold, is left off (I345).

OSC 10 to 12 queries stay, because the Claude Code binary holds the
background color query.

OSC 7501 joined after I62 saw Claude Code 2.1.296 send its query at
every start (I270).

Queries pass because the reply goes to the guest. Claude Code uses
them to learn what the terminal supports. What a reply tells the guest
is T09-terminal-fingerprint.

### Kitty keyboard stacks

The kitty keyboard protocol keeps one stack per screen. So `wb` counts
the guest's pushes on the main and the alternate screen apart. It drops
a pop that would reach an entry the user's shell pushed.

### Program status

OSC 7501 is the Program Status Protocol. A program reports whether it
is working, waiting on the user, or done, and the terminal may show
that outside the window, for example as a notification. Claude Code
2.1.295 and later ask `OSC 7501 ; ?` at start, and send reports only
when the terminal answers.

The filter passes the query `?` as it is. It rebuilds a report from
these pairs only:

| Key | Value |
|---|---|
| `state` | `idle`, `working`, `done`, `blocked`, `error` or `clear`; required |
| `id` | 1 to 8 segments of 1 to 32 characters from `A-Z`, `a-z`, `0-9`, `_ . + -`, joined by `/`; at most 128 bytes |
| `kind` | `permission`, `question` or `auth` |
| `progress` | an integer from 0 to 100 |
| `app` | 1 to 32 characters from `A-Z`, `a-z`, `0-9`, `_ . + -` |

- It removes `title`, `msg` and every other key, and counts a report
  it shortens that way under `terminal-osc-status-text`
  (S16-02-drops-and-links).
- It drops the whole report when `state` is missing, a kept key has a
  value outside its form, a key repeats, or the sequence is longer than
  4096 bytes. That counts under `terminal-osc`.
- The rebuilt report ends with `ESC \`.

The terminal's answer to the query names the states and kinds it
supports, and goes to the guest (T09-terminal-fingerprint). The
protocol has no other reply.

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

### Pass program status without its text

OSC 7501 passes with `state`, `id`, `kind`, `progress` and `app`, and
without `title` and `msg`.

- **Why:** the state tells the user that Claude Code waits for them,
  which a terminal can show in its tab. `title` and `msg` are text the
  guest writes, and a terminal may show them in a desktop
  notification. A `blocked` report with a `msg` that asks for an
  approval is the forgery SEC14-no-fake-approvals is about. The other
  keys are short, fixed-form values.
- **Rejected:**
  - dropping OSC 7501, which counts a drop in every session and so
    teaches the user to ignore the counts (NFR06-explained-refusals);
  - passing reports whole, and relying on the terminal to keep
    controls out of the text, which doesn't stop a plain sentence;
  - removing `app` too. A guest can name itself `wb`, but 32
    characters without spaces can't ask the user a question.
- **Decided:** I270, 2026-10-16.

**Status:** Draft
