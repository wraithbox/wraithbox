# X19 - Terminal stream filtering

**Brief:** B30-terminal-filter

**Question:** Which terminal escape sequences does Claude Code emit, and
can `wb` drop the ones that act on the host (clipboard writes, file
transfer, terminal multiplexer control sequences) without visible damage
in the common macOS terminals? (I30)

**Answer:** Yes, with conditions, for Ghostty and Terminal.app as far
as a headless emulator shows, and pending the check by eye in I62. In
seven recorded sessions Claude Code 2.1.289 emitted 56 distinct controls
and sequences. A short allowlist passes them unchanged except four kinds, plus
`file://` links within OSC 8: OSC 52 (clipboard), two kinds of desktop
notification, and a kitty graphics query. With those dropped or rewritten, every screen of every recording
is unchanged on replay in a headless terminal emulator, except that the
`file://` links are no longer links. The recordings imitated each
terminal's answers to Claude Code's queries. The iTerm2 and VS Code
profiles stay unchecked in the real terminals, because I62 covers only
Ghostty and Terminal.app. The filter spends about 12 ms of CPU per MB
of Claude Code output. The conditions are below.

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
  agent's terminal) is kept, and the filter closes one path: guest text
  no longer reaches desktop notifications through OSC 9, 99 and 777.
  The filter leaves a second path open. The guest sets the window
  title and rings the bell, and iTerm2's Notification Center alerts and
  Ghostty's `bell-features = title` then show that title. I37 has `wb`
  own the title (see the list at the end). FR12-clipboard (clipboard
  opt-in, per direction, with approval) is kept: OSC 52 is dropped.
  FR01-drop-in (`wb claude` behaves as `claude`) holds with the losses
  above.
- **Assumed:** that a headless emulator (`charmbracelet/x/vt`) shows
  what Ghostty and Terminal.app would show. I62 checks that by eye. That
  Claude Code chooses its sequences from `TERM_PROGRAM` and from the
  answers to its queries, which the recorder imitated per terminal. The
  iTerm2 and VS Code profiles stay unchecked after I62.
- **Open decisions:**
  1. `/copy`: leave it broken until FR12-clipboard is designed (I38), or
     have `wb` turn OSC 52 into a clipboard request with approval.
     Recommended: leave it to I38, and name OSC 52 as the input there.
  2. Notifications: rewrite to a bell (what the spike does), or drop
     them. Recommended: the bell, which keeps the "Claude needs you"
     signal without the notification's text.
  3. Hyperlinks: drop OSC 8 in V1, pass a link only when its text equals
     its URL, or accept the mismatch as a residual risk. Recommended:
     drop OSC 8 in V1 ("What it means for the specs").
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
   `XTVERSION`, the kitty keyboard query) the way it expects Ghostty,
   Terminal.app, iTerm2 or the VS Code terminal to answer. No real
   terminal answered, so each column below is a profile, not the
   terminal itself. Ghostty also ran with the
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
   side makes 71% to 99% of the screens differ per recording: 15 of 21
   in `onboarding-ghostty`, 97 of 109 in `copy-ghostty`, and over 97% in
   each of the five full sessions. So the comparison does see style.
   Its output is `results-compare-control.md` on the spike branch.
5. **Cost and fuzzing.** Go benchmarks on the recordings and on
   synthetic worst cases, and a fuzz target.

## Inventory

Counts over seven recordings, 818 KB of output, of which 335 KB is text.
The Ghostty profile column adds the full-screen, onboarding and `/copy`
recordings.

| Sequence | Meaning | Ghostty profile | Terminal.app profile | iTerm2 profile | VS Code profile | Filter |
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
| `OSC 777 ; notify` | desktop notification, Ghostty profile ("Claude needs your permission") | 4 | | | | **rewrite to BEL** |
| `OSC 9` | desktop notification, iTerm2 profile, same text | | | 2 | | **rewrite to BEL** |
| `OSC 52` | write the clipboard (`/copy`) | 1 | | | | **drop** |
| `APC G … a=q` | kitty graphics support query | 4 | | 1 | 1 | **drop** |

Claude Code adapts to the terminal. For the Terminal.app profile it left out
hyperlinks, notifications, synchronized output, and the kitty keyboard
protocol. The filter passed that recording byte for byte.

The binary also knows these, which the recordings did not trigger: OSC
1 and 2 (title), OSC 4, 10, 11 and 12 and their resets 104 and 110 to
112 (colors), OSC 7 (working directory), OSC 99 (kitty notification), OSC
133 (prompt marks, written as `A;redraw=0`, `C` and `D`), OSC 1337
(iTerm2) and OSC 21337 (tab status). Inside `tmux` or `screen`
(detected from `TMUX` and `STY`) it wraps sequences in a DCS
pass-through. None of those is needed for the TUI to work.

### Findings beyond the list

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
  777 make iTerm2 and Ghostty show a desktop notification with any text
  the guest chooses. S05-cli sends approvals as native notifications, so
  a guest-written "approve?" notification is the forgery
  SEC14-no-fake-approvals is about. The window title reaches the
  desktop too, by way of the bell: iTerm2's Notification Center alerts
  and Ghostty's `bell-features = title` show the title the guest set.
  The filter passes both the title and BEL, so guest text can still
  reach host notifications (security review of PR66, F3).

## Allowlist

Input for I37. The table describes the filter that was measured, and
the list at the end says where I37 tightens it. The filter passes a token only if it matches a rule here,
drops it otherwise, and counts every drop by rule. A dropped sequence is
dropped whole, including its `ESC`, so the terminal's parser is back in
its normal state after each token the filter passes.

| Class | Passes | Everything else |
|---|---|---|
| Text | valid UTF-8 without C1 controls | invalid bytes become U+FFFD; C1 code points (U+0080 to U+009F) dropped |
| C0 controls | BEL, BS, HT, LF, CR, SI | dropped (ENQ, SO, VT, FF, NUL, DEL, …) |
| `ESC` sequences | `7`, `8`, `M`, `D`, `E`, `=`, `>`, `( B`, `( 0`, `) B`, `) 0` | dropped, including `ESC c` (full reset) |
| CSI, general | parameters only digits, `;` and `:`, with no cap on a value or on the count; at most 64 bytes | dropped |
| CSI, screen | cursor movement and position (`A` to `H`, `f`, `` ` ``, `a`, `d`, `e`), erase (`J`, `K`, any parameter), insert and delete (`@`, `L`, `M`, `P`, `X`), repeat (`b`), scroll (`S`, `T`), scroll region (`r`), tabs (`I`, `Z`, `g`), SGR, save and restore cursor without parameters | |
| CSI, modes | `? h/l` for 1, 7, 12, 25, 47, 1000, 1002, 1003, 1004, 1006, 1016, 1047, 1048, 1049, 2004, 2026, 2027, 2031; `? … $ p` for any mode | other modes dropped |
| CSI, keyboard | kitty keyboard (`> u`, `< u`, `= u`, `? u`), `> m` with any parameters (`modifyOtherKeys`), cursor style (`SP q`), soft reset (`! p`) | |
| CSI, queries | `c`, `> c`, `5 n`, `6 n`, `> q`, `14 t`, `16 t`, `18 t` | |
| CSI `t` | title stack push and pop (`22`, `23`) | window moves, resizes, iconify and title reports dropped |
| OSC 0, 1, 2 | title of at most 512 bytes, valid UTF-8, no controls | dropped |
| OSC 8 | parameters of at most 256 printable ASCII bytes, with an `http://` or `https://` URL of at most 2048 printable ASCII bytes, or the closing `OSC 8 ;;` | `file://` and other schemes dropped |
| OSC 9 ; 4 | progress with numeric parameters | |
| OSC 9, 99, 777 | rewritten to one BEL | |
| OSC 10, 11, 12 | the query `?` only | setting a color dropped |
| OSC, other | | dropped: 52 (clipboard), 1337 (iTerm2 files and clipboard), 7, 4, 104, 110 to 112, 133, 21337, everything else; at most 16 KB is buffered |
| DCS, APC, PM, SOS | | dropped whole: DCS pass-through and `XTGETTCAP`, kitty graphics (whose `t=f` mode asks the terminal to read a file by its path) |

Queries pass because the terminal's reply goes to the guest, and
Claude Code sends them to find out what the terminal supports. The
replies tell the guest about the host: the terminal's name and version
(`CSI > q`), its colors (OSC 10 to 12), the window size in cells and
pixels (`CSI 14 t`, `16 t`, `18 t`), and the state of its modes
(DECRQM). No allowed query returns clipboard or file contents. OSC 52
with `?` (read the clipboard) is dropped with the rest of OSC 52, and so
are the title report `CSI 21 t` and DECRQSS, which is a DCS.

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
| `session-ghostty` | Ghostty profile | 922 | 393 | 393 | 7 `file://` links, 2 notifications, 1 graphics query |
| `session-ghostty-fullscreen` | Ghostty profile, full screen | 906 | 180 | 180 | 12 `file://` links, 2 notifications, 1 graphics query |
| `copy-ghostty` | Ghostty profile | 109 | 0 | 0 | 1 OSC 52, 1 graphics query |
| `onboarding-ghostty` | Ghostty profile, no login | 21 | 0 | 0 | 1 graphics query |
| `session-apple` | Terminal.app profile | 1271 | 0 | 0 | nothing |
| `session-iterm` | iTerm2 profile | 1199 | 660 | 660 | 8 `file://` links, 2 notifications, 1 graphics query |
| `session-vscode` | VS Code profile | 958 | 377 | 377 | 7 `file://` links, 1 graphics query |

Every difference is a `file://` link that is plain text after filtering.
The screens show the same characters, colors, and cursor. The links point
at guest paths (the workspace path in the status line, and a path a tool
printed), which on the host name a different file or none.

### Visible losses

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
| Plain ASCII text | 847 MB/s | 1.2 ms |
| Dense colors (`CSI m` every 2 characters) | 137 MB/s | 7.3 ms |
| OSC 52 payloads of 16 KB, all dropped | 363 MB/s | 2.8 ms |
| Random bytes | 29 MB/s | 34 ms |

The numbers are from `results-bench.txt` on the spike branch.

A scripted Claude Code session printed 108 to 189 KB in two to five
minutes, so the filter spends a few milliseconds per session. Random
bytes, as from a `cat` of a binary file in `wb shell`, are the worst
case, at 29 MB/s, about 34 ms per MB. Memory is bounded: at most 16 KB of an unfinished OSC or 64 bytes
of a CSI is buffered, and a longer one is dropped whole.

## Fuzzing

The real filter parses guest bytes and needs a fuzz target
(S11-verification-and-spikes). The spike has one (`FuzzFilter`). It
checks that the output contains only tokens the allowlist passes as
they are, that filtering twice gives the same bytes as filtering once,
that where a read splits the input does not change the output, and that
no OSC 52, OSC 1337, DCS, or APC introducer survives. Three runs of one to
two minutes passed. Only the last one has a committed log: 17.5 million
inputs in two minutes (`results-fuzz.txt` on the spike branch). A short run
before them found a bug: text in front of an incomplete UTF-8
character was held back until the next read. The split check caught it,
and it is fixed in the spike.

## Check by eye

The headless emulator is not the user's terminal. I62 runs the relay in
Ghostty and Terminal.app, the two terminals installed on the
maintainer's Mac (iTerm2 and VS Code are not), and compares it with an
unfiltered run. The iTerm2 and VS Code profiles stay unchecked after
I62. From `spikes/x19-terminal-filter` on the spike branch:

```sh
GOWORK=off go run ./cmd/relay -log drops.txt -- claude
```

`-raw` instead of `-log drops.txt` gives the unfiltered baseline, and
`./scripts/hostile.sh` instead of `claude` prints each sequence the
filter must stop, between markers. The things to look at are in I62.

## What it means for the specs

This result leaves the specs as they are. The items below are input for
I37, which writes the spec text. F1 to F11 refer to the security review
of PR66, whose cases passed this spike's filter unchanged.

The boundary and the filter:

- T00-index: boundary (6), the host terminal stream, as
  B37-terminal-boundary decided.
- S05-cli: the relay for `wb claude` and `wb shell` applies the
  allowlist above, tightened as below. It drops a sequence whole,
  rewrites notifications to BEL, and the session summary reports how
  many sequences it dropped by rule (NFR06-explained-refusals). Every
  other place `wb` prints guest text strips all control sequences.
- S11-verification-and-spikes: the terminal filter joins the list of
  parsers with a fuzz target, with the properties in "Fuzzing" above,
  plus two more: no parameter in the output exceeds the caps below, and
  no OSC 8 in the output names a host the OSC 8 rule below refuses.

Tightening the allowlist:

- `J` and `K` take parameters 0 to 2 only, because `CSI 3 J` erases the
  host's scrollback (F6).
- A numeric parameter has at most 5 digits, and a sequence at most 16 to
  32 parameters. The filter is also a defense against parser bugs in
  terminal emulators (F7).
- Modes that were not observed are dropped: 1, 7, 12, 47, 1047, 1048
  and 2027. `wb` caps the depth of the kitty keyboard stack (F10).
- `> m` passes with the parameters observed, `4 ; 2` and `4`, and no
  others.
- Hyperlinks (F4, F5). If OSC 8 stays, it passes only `https://` URLs to
  public dotted hostnames: no IP literals, no `localhost`, `.local` or
  `.internal`, no userinfo (`https://github.com@evil.example/`), and no
  `http://`. In V1 there is no port forwarding, so a host-local link
  names a host service, not the guest's. Link text can also differ from
  the link's target, and three answers are possible: drop OSC 8 in V1,
  pass a link only when its text equals its URL, or accept the mismatch
  as a residual risk in T00-index. Recommended: drop OSC 8 in V1.
  Terminals already make a bare URL clickable, and checking the text
  means the filter has to follow the screen, which it does not do.

Around the stream:

- Reset on exit (F1, F2). After the relay ends and before `wb` prints
  anything, it resets the terminal state the guest may have changed:
  SGR, alternate screen, mouse modes, bracketed paste, focus events,
  synchronized output, cursor visibility and style, scroll region,
  character set, the kitty keyboard stack, an open OSC 8 link, and the
  progress indicator. Then it starts a fresh line. Without the reset,
  the guest can hide the real summary: the reviewer read the background
  color with an OSC 11 query, set the same color as foreground, and
  printed a fake summary without the flagged paths. That defeats the
  flagging that SEC03-no-host-exec relies on. I37 should weigh `wb`
  switching to the alternate screen itself around the relay against FR01-drop-in
  (scroll-back after exit). The printed summary is advisory, and
  `wb diff` is authoritative.
- Title and bell (F3). `wb` sets the window title itself and restores it
  on exit, so guest text does not reach notifications through the bell.
  Any guest text placed outside the stream (a title, a notification, a
  summary line) has no Unicode format characters (bidirectional
  controls, zero-width joiners). `wb` rate-limits BEL.
- Input direction (F8). Before restoring cooked mode, `wb` sends a
  `CSI 6 n` and reads standard input until the reply, so late query
  replies do not reach the user's shell. The spec names the dropped
  sequences that make the terminal echo data back into input, because
  the security property depends on them: `CSI 21 t` (title report), OSC 52
  with `?`, and DECRQSS.
- Environment (F9). Claude Code chooses its sequences from `TERM`,
  `TERM_PROGRAM` and the replies to its queries, so the guest session
  needs the host's `TERM` and `TERM_PROGRAM`. The spec lists exactly
  which host environment variables cross into the guest, and no others:
  not `TMUX`, `STY`, `SSH_*` or `ITERM_SESSION_ID`. Without
  `SSH_CONNECTION` in the guest, `/copy` takes the local `pbcopy` path
  plus OSC 52, as recorded here. T00-index records what the guest learns
  about the host: these variables and the query replies listed under
  "Allowlist".
- Logs (F11). Any place that prints or logs a dropped sequence, such as
  the drop log or the audit log (SEC10-audit), escapes it.

Elsewhere:

- I38 (FR12-clipboard): `/copy` emits OSC 52, which the filter drops. If
  the guest-to-host clipboard is to work with `/copy`, `wb` would turn
  OSC 52 into a clipboard request with approval rather than pass it.

## Limits

- One version of Claude Code, 2.1.289. A new version can start using a
  sequence the list drops. The filter fails closed, and the drop counts
  in the session summary are how a missing rule shows up.
- The terminals' answers to queries were imitated, not taken from the
  real terminals. A real Terminal.app may answer differently and change
  what Claude Code emits. I62 covers Ghostty and Terminal.app. The
  iTerm2 and VS Code profiles stay unchecked.
- The input direction (keys and query replies from the terminal to the
  guest) was not filtered and is out of scope here.
- The recordings ran with the maintainer's Claude Code settings, which
  allow some tools without a prompt, so not every tool call showed a
  permission dialog. The dialogs that did show used the same sequences.

## Spike code

Branch `spike/x19-terminal-filter`, commit
[`e1adf23`](https://github.com/wraithbox/wraithbox/tree/e1adf23aa79d3fa597b424f17b2f048225d775a7/spikes/x19-terminal-filter):
recorder, classifier, filter with tests and fuzz target, replay
comparison, relay, the seven recordings, and the raw result tables
(`results-inventory.md`, `results-compare.md`,
`results-compare-control.md`, `results-bench.txt`, `results-fuzz.txt`).

**Status:** Answered
