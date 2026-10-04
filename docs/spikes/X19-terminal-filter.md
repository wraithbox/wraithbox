# X19 - Terminal stream filtering

**Brief:** B30-terminal-filter

**Question:** Which terminal escape sequences does Claude Code emit, and
can `wb` drop the ones that act on the host (clipboard writes, file
transfer, terminal multiplexer control sequences) without visible damage
in the common macOS terminals? (I30)

**Answer:** Yes, with conditions. In seven recorded sessions Claude
Code 2.1.289 emitted 56 distinct controls and sequences. All but five
pass a short allowlist unchanged: OSC 52 (clipboard), two kinds of
desktop notification, a kitty graphics query, and links to `file://`
URLs. With those dropped or rewritten, every screen of every recording
is unchanged on replay in a headless terminal emulator, except that the
`file://` links are no longer links. The filter
spends about 12 ms of CPU per MB of Claude Code output. The conditions are below.

## For review

- **Decides:** that an allowlist filter on the host terminal stream
  works with Claude Code, and what is on the list. It supplies the list
  that I37 (B37-terminal-boundary) waits for. It doesn't change a spec:
  T00-index, S05-cli and S11-verification-and-spikes change in I37.
- **You are approving:** the allowlist below as the starting point for
  I37, and three visible losses: `/copy` no longer reaches the host
  clipboard, the text of Claude Code's desktop notifications becomes a
  plain bell, and file paths are no longer links.
- **Controls touched:** SEC03-no-host-exec (nothing the agent produces
  runs on the host without the user) is kept: the filter drops iTerm2
  file transfer, kitty graphics (which can read host files) and
  `file://` links. SEC14-no-fake-approvals (approvals never come from the
  agent's terminal) is kept and made stronger: guest text no longer
  reaches the host's desktop notifications. FR12-clipboard (clipboard
  opt-in, per direction, with approval) is kept: OSC 52 is dropped.
  FR01-drop-in (`wb claude` behaves as `claude`) holds with the losses
  above.
- **Assumed:** that a headless emulator (`charmbracelet/x/vt`) shows
  what Ghostty and Terminal.app would show. I62 checks that by eye. That
  Claude Code chooses its sequences from `TERM_PROGRAM` and from the
  answers to its queries, which the recorder imitated per terminal.
- **Open decisions:**
  1. `/copy`: leave it broken until FR12-clipboard is designed (I38), or
     have `wb` turn OSC 52 into a clipboard request with approval.
     Recommended: leave it to I38, and name OSC 52 as the input there.
  2. Notifications: rewrite to a bell (what the spike does), or drop
     them. Recommended: the bell, which keeps the "Claude needs you"
     signal without guest text.
- **Brief:** B30-terminal-filter

## Method

All on the host (macOS, Apple M2), with the installed Claude Code
2.1.289 and the maintainer's own login, model Haiku 4.5. No Wraith Box
VM was involved. The question is what Claude Code writes to a PTY, and
that depends on the terminal it believes it talks to, not on the
machine it runs on.

1. **Record.** `cmd/record` runs `claude` in a PTY behind a headless
   terminal emulator and writes every byte it prints to an asciicast
   file. It drives Claude Code from a script (`scripts/session.txt`):
   folder trust dialog, a Markdown answer with a table, code block and
   link, a Bash tool call whose output holds OSC 52, OSC 8 and color,
   Write and Edit with their permission prompts and diffs, a bracketed
   paste, focus out and in, mode cycling with Shift+Tab, a resize, a
   300-line answer, `/cost`, and exit. Claude Code picks its sequences
   by terminal, so the recorder sets `TERM`, `TERM_PROGRAM` and related
   variables and answers queries (device attributes, mode reports,
   `XTVERSION`, the kitty keyboard query) as Ghostty, Terminal.app,
   iTerm2 or the VS Code terminal would. Ghostty also ran with the
   full-screen renderer (`CLAUDE_CODE_NO_FLICKER=1`). Two short scripts
   cover first-run onboarding without credentials, and `/copy` (with a
   stub `pbcopy` first in `PATH`).
2. **Classify.** `cmd/classify` splits the output into tokens with the
   filter's own tokenizer and counts each kind of sequence.
3. **Static check.** The strings in the Claude Code binary name the OSC
   numbers it knows, so sequences it can emit but did not during the
   recordings are listed too.
4. **Filter and compare.** `cmd/compare` replays each recording into two
   emulators, one fed the raw output and one fed the filtered output,
   and compares their screens, text and style, after every read from the
   PTY. A negative control that also strips colors from the filtered
   side makes 97 of 109 chunks differ, so the comparison does see style.
5. **Cost and fuzzing.** Go benchmarks on the recordings and on
   synthetic worst cases, and a fuzz target.

## Inventory

Counts over seven recordings, 818 KB of output, of which 335 KB is text.
The Ghostty column adds the full-screen, onboarding and `/copy`
recordings.

| Sequence | Meaning | Ghostty | Terminal.app | iTerm2 | VS Code | Filter |
|---|---|---|---|---|---|---|
| LF, CR, SI | line feed, carriage return, back to the normal character set | 13335 | 22072 | 21255 | 11755 | pass |
| `ESC 7`, `ESC 8`, `ESC ( B` | save and restore cursor, character set | 39 | 12 | 12 | 12 | pass |
| `CSI A` … `CSI T`, `CSI r` | cursor movement, erase, scroll, scroll region | 15838 | 9798 | 9271 | 8048 | pass |
| `CSI m` | colors and text style (SGR) | 6974 | 4616 | 4270 | 3725 | pass |
| `CSI ? 2026 h/l` | synchronized output | 3330 | | 2150 | 1648 | pass |
| `CSI ? 25`, `1004`, `2004`, `2031`, `1016` `h/l` | cursor visible, focus events, bracketed paste, color scheme reports, pixel mouse off | 79 | 23 | 23 | 23 | pass |
| `CSI ? 1000`, `1002`, `1003`, `1006` `h/l` | mouse reporting | 72 | 8 | 8 | 8 | pass |
| `CSI ? 1049 h/l` | alternate screen (full-screen renderer only) | 2 | | | | pass |
| `CSI > 5 u`, `CSI < u`, `CSI ? u` | kitty keyboard protocol: push, pop, query | 58 | 1 | 21 | 1 | pass |
| `CSI > 4 ; 2 m`, `CSI > 4 m` | xterm `modifyOtherKeys` | 31 | 2 | 11 | 2 | pass |
| `CSI c`, `CSI > 0 q`, `CSI ? … $ p`, `CSI 16 t` | queries: device attributes, terminal version, mode state, cell size in pixels | 32 | 4 | 10 | 10 | pass |
| `OSC 0` | window title (`✳ Claude Code`) | 74 | 41 | 32 | 39 | pass |
| `OSC 8` to `https://`, and `OSC 8 ;;` | hyperlink, and the end of one | 7 and 26 | | 2 and 10 | 2 and 9 | pass |
| `OSC 8` to `file://` | link to a local path: the workspace, and a path printed by a tool | 19 | | 8 | 7 | **drop** |
| `OSC 9 ; 4` | progress indicator | 33 | | | | pass |
| `OSC 777 ; notify` | Ghostty desktop notification ("Claude needs your permission") | 4 | | | | **rewrite to BEL** |
| `OSC 9` | iTerm2 desktop notification, same text | | | 2 | | **rewrite to BEL** |
| `OSC 52` | write the clipboard (`/copy`) | 1 | | | | **drop** |
| `APC G … a=q` | kitty graphics support query | 4 | | 1 | 1 | **drop** |

Claude Code adapts to the terminal. For Terminal.app it left out
hyperlinks, notifications, synchronized output, and the kitty keyboard
protocol. The filter passed that recording byte for byte.

The binary also knows these, which the recordings did not trigger: OSC
1 and 2 (title), OSC 4, 10, 11 and 12 and their resets 104 and 110 to
112 (colors), OSC 7 (working directory), OSC 99 (kitty notification), OSC
133 (prompt marks, written as `A;redraw=0`, `C` and `D`), OSC 1337
(iTerm2) and OSC 21337 (tab status). Inside `tmux` or `screen`
(detected from `TMUX` and `STY`) it wraps sequences in a DCS
pass-through. None of those is needed for the TUI to work.

Three findings that matter beyond the list:

- **`/copy` writes OSC 52 every time.** It runs `pbcopy` and also emits
  OSC 52 with the text, base64-encoded (`ESC ] 52 ; c ; cGluZWFwcGxl
  BEL` for "pineapple"). In a guest, `pbcopy` reaches only the guest's
  pasteboard, so after filtering `/copy` copies nothing to the host. It
  also writes the text to a file in the guest's temporary directory.
- **Tool output keeps its hyperlinks.** Claude Code strips OSC 52 from
  the output of a Bash tool call, but it keeps the colors and renders the
  output's `OSC 8` link to `file:///etc/hosts` as a live link. A file in
  the repository that a tool prints can put a link in the host
  terminal, without the agent being compromised.
- **Notifications put guest text on the host desktop.** OSC 9 and OSC
  777 make Ghostty and iTerm2 show a desktop notification with any text
  the guest chooses. S05-cli sends approvals as native notifications, so
  a guest-written "approve?" notification is the forgery
  SEC14-no-fake-approvals is about.

## Allowlist

Input for I37. The filter passes a token only if it matches a rule here,
drops it otherwise, and counts every drop by rule. A dropped sequence is
dropped whole, including its `ESC`, so the terminal's parser is back in
its normal state after each token the filter passes.

| Class | Passes | Everything else |
|---|---|---|
| Text | valid UTF-8 without C1 controls | invalid bytes become U+FFFD; C1 code points (U+0080 to U+009F) dropped |
| C0 controls | BEL, BS, HT, LF, CR, SI | dropped (ENQ, SO, VT, FF, NUL, DEL, …) |
| `ESC` sequences | `7`, `8`, `M`, `D`, `E`, `=`, `>`, `( B`, `( 0`, `) B`, `) 0` | dropped, including `ESC c` (full reset) |
| CSI, general | parameters only digits, `;` and `:`; at most 64 bytes | dropped |
| CSI, screen | cursor movement and position, erase, insert and delete, scroll, scroll region, tab stops, SGR, save and restore cursor without parameters | |
| CSI, modes | `? h/l` for 1, 7, 12, 25, 47, 1000, 1002, 1003, 1004, 1006, 1016, 1047, 1048, 1049, 2004, 2026, 2027, 2031; `? … $ p` for any mode | other modes dropped |
| CSI, keyboard | kitty keyboard (`> u`, `< u`, `= u`, `? u`), `> 4 ; n m`, cursor style (`SP q`), soft reset (`! p`) | |
| CSI, queries | `c`, `> c`, `5 n`, `6 n`, `> q`, `14 t`, `16 t`, `18 t` | |
| CSI `t` | title stack push and pop (`22`, `23`) | window moves, resizes, iconify and title reports dropped |
| OSC 0, 1, 2 | title of at most 512 bytes, valid UTF-8, no controls | dropped |
| OSC 8 | `http://` and `https://` URLs of at most 2048 printable ASCII bytes; the closing `OSC 8 ;;` | `file://` and other schemes dropped |
| OSC 9 ; 4 | progress with numeric parameters | |
| OSC 9, 99, 777 | rewritten to one BEL | |
| OSC 10, 11, 12 | the query `?` only | setting a color dropped |
| OSC, other | | dropped: 52 (clipboard), 1337 (iTerm2 files and clipboard), 7, 4, 104, 110 to 112, 133, 21337, everything else; at most 16 KB is buffered |
| DCS, APC, PM, SOS | | dropped whole: DCS pass-through and `XTGETTCAP`, kitty graphics (whose `t=f` mode asks the terminal to read a file by its path) |

Queries pass because the terminal's reply goes to the guest, not to the
host, and Claude Code sends them to find out what the terminal supports. OSC 52 with `?` (read the
clipboard) is dropped with the rest of OSC 52, so no query can make the
terminal send host data back.

Sequences Claude Code knows but did not emit are left off, even
harmless ones such as OSC 133 prompt marks and color resets: the list
holds what was seen, and a drop count shows when it is too short. OSC
10 to 12 queries are the exception, because the binary holds the
background color query. OSC 9 ; 4 progress passes because it shows only
a percentage in the tab, which I37 may revisit.

## What the filter changes

Every screen, after every read, raw against filtered:

| Recording | Terminal | Reads | Differing screens | Of which only links | Dropped or rewritten |
|---|---|---|---|---|---|
| `session-ghostty` | Ghostty | 922 | 393 | 393 | 7 `file://` links, 2 notifications, 1 graphics query |
| `session-ghostty-fullscreen` | Ghostty, full screen | 906 | 180 | 180 | 12 `file://` links, 2 notifications, 1 graphics query |
| `copy-ghostty` | Ghostty | 109 | 0 | 0 | 1 OSC 52, 1 graphics query |
| `onboarding-ghostty` | Ghostty, no login | 21 | 0 | 0 | 1 graphics query |
| `session-apple` | Terminal.app | 1271 | 0 | 0 | nothing |
| `session-iterm` | iTerm2 | 1199 | 660 | 660 | 8 `file://` links, 2 notifications, 1 graphics query |
| `session-vscode` | VS Code | 958 | 377 | 377 | 7 `file://` links, 1 graphics query |

Every difference is a `file://` link that is plain text after filtering.
The screens show the same characters, colors, and cursor. The links point
at guest paths (the workspace path in the status line, and a path a tool
printed), which on the host name a different file or none.

What a user would notice:

- File paths are not clickable.
- `/copy` reports "Copied to clipboard", but the host clipboard is
  unchanged.
- "Claude needs your permission" arrives as a bell, not as a desktop
  notification with text.
- Claude Code can't use kitty graphics (it only queried support, and
  didn't show an image in these sessions).

## Cost

Single core, Apple M2, Go 1.27.1, input in 32 KB reads:

| Input | Throughput | CPU per MB |
|---|---|---|
| The seven recordings | 85 MB/s | 12 ms |
| Plain ASCII text | 775 MB/s | 1.3 ms |
| Dense colors (`CSI m` every 2 characters) | 111 MB/s | 9 ms |
| OSC 52 payloads of 16 KB, all dropped | 290 MB/s | 3.4 ms |
| Random bytes | 33 MB/s | 30 ms |

A scripted Claude Code session printed 108 to 189 KB in two to five
minutes, so the filter spends a few milliseconds per session. Random
bytes, as from a `cat` of a binary file in `wb shell`, are the worst
case, at 33 MB/s. Memory is bounded: at most 16 KB of an unfinished OSC or 64 bytes
of a CSI is buffered, and a longer one is dropped whole.

## Fuzzing

The real filter parses guest bytes and needs a fuzz target
(S11-verification-and-spikes). The spike has one (`FuzzFilter`). It
checks that the output contains only tokens the allowlist passes as
they are, that filtering twice gives the same bytes as filtering once,
that where a read splits the input does not change the output, and that
no OSC 52, OSC 1337, DCS, or APC introducer survives. Two runs of two
minutes each passed (the first ran 16.7 million inputs). A short run
before them found a bug: text in front of an incomplete UTF-8
character was held back until the next read. The split check caught it,
and it is fixed in the spike.

## Check by eye

The headless emulator is not the user's terminal. I62 runs the relay in
Ghostty and Terminal.app, the two terminals installed on the
maintainer's Mac (iTerm2 and VS Code are not), and compares it with an
unfiltered run. From `spikes/x19-terminal-filter` on the spike branch:

```sh
GOWORK=off go run ./cmd/relay -log drops.txt -- claude
```

`-raw` instead of `-log drops.txt` gives the unfiltered baseline, and
`./scripts/hostile.sh` instead of `claude` prints each sequence the
filter must stop, between markers. The things to look at are in I62.

## What it means for the specs

This result leaves the specs as they are. The spec text goes in I37, with:

- T00-index: boundary (6), the host terminal stream, as B37-terminal-boundary
  decided.
- S05-cli: the relay for `wb claude` and `wb shell` applies the
  allowlist above, drops a sequence whole, rewrites notifications to
  BEL, and the session summary reports how many sequences it dropped by
  rule (NFR06-explained-refusals). Every other place `wb` prints guest
  text strips all control sequences.
- S11-verification-and-spikes: the terminal filter joins the list of
  parsers with a fuzz target, with the properties above.
- S05-cli or S06-vm-lifecycle: Claude Code chooses its sequences from
  `TERM`, `TERM_PROGRAM` and the replies to its queries. The guest
  session needs the host's `TERM` and `TERM_PROGRAM` for the output to
  match what the user's terminal expects. Without `SSH_CONNECTION` in
  the guest, `/copy` takes the local `pbcopy` path plus OSC 52, as
  recorded here.
- I38 (FR12-clipboard): `/copy` emits OSC 52, which the filter drops. If
  the guest-to-host clipboard is to work with `/copy`, `wb` would turn
  OSC 52 into a clipboard request with approval rather than pass it.

## Limits

- One version of Claude Code, 2.1.289. A new version can start using a
  sequence the list drops. The filter fails closed, and the drop counts
  in the session summary are how a missing rule shows up.
- The terminals' answers to queries were imitated, not taken from the
  real terminals. A real Terminal.app may answer differently and change
  what Claude Code emits. I62 covers this.
- The input direction (keys and query replies from the terminal to the
  guest) was not filtered and is out of scope here.
- The recordings ran with the maintainer's Claude Code settings, which
  allow some tools without a prompt, so not every tool call showed a
  permission dialog. The dialogs that did show used the same sequences.

## Spike code

Branch `spike/x19-terminal-filter`, commit
[`6c6028f`](https://github.com/wraithbox/wraithbox/tree/6c6028fdf70c24f4ab1b08b3f6915b521444ab80/spikes/x19-terminal-filter):
recorder, classifier, filter with tests and fuzz target, replay
comparison, relay, the seven recordings, and the raw result tables.

**Status:** Answered
