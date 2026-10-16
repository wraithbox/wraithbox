# S16-02 - Drops and Links

**Purpose:** The fixed rules that drops are counted by, and how `wb` records the
links the filter drops.

## Design

### Drop rules

The counts are by these rules. They are a fixed list, so a guest can't
make up new ones. The audit log records the counts by these names
(S09-11-audit).

The allowlist in the table is the one of S16-01-filter ("Allowlist").

| Rule | Counts |
|---|---|
| `terminal-c1` | C1 controls in text |
| `terminal-c0` | the C0 controls and `DEL` the allowlist drops, and each BEL over the rate |
| `terminal-esc` | `ESC` sequences the allowlist drops |
| `terminal-csi` | CSI sequences the allowlist drops, other than `t` |
| `terminal-csi-window` | CSI `t` sequences the allowlist drops: window moves, title reports, the title stack |
| `terminal-osc-title` | OSC 0 to 2 |
| `terminal-osc-link` | OSC 8 |
| `terminal-osc-clipboard` | OSC 52 |
| `terminal-osc-file` | OSC 7 (current directory), 1337 (iTerm2 files and clipboard) and 5113 (kitty file transfer) |
| `terminal-osc` | the other OSC sequences the allowlist drops |
| `terminal-osc-notify` | OSC 9, 99 and 777, which the filter turns into a BEL rather than drop |
| `terminal-dcs` | DCS |
| `terminal-apc` | APC, the kitty graphics protocol among them |
| `terminal-pm-sos` | PM and SOS |

### Which rule a drop counts under

- A sequence cut at a buffer cap counts under the rule of its class, as
  far as its first bytes tell it.
- A CSI cut at the 64-byte cap, or ended by CAN, SUB or `ESC` before
  its final byte, counts as `terminal-csi`.
- A CSI with the final byte `t` counts as `terminal-csi-window`,
  whatever its parameters. That holds also when they break the CSI
  parameter caps.
- An OSC without a number before its first `;` counts as
  `terminal-osc`.
- An OSC `9 ; 4` whose parameters aren't all numeric is a malformed
  progress report, not a notification. The filter drops it, counted as
  `terminal-osc`, and doesn't turn it into a BEL.

An invalid byte that the filter replaces with U+FFFD isn't counted as
a drop, and has no rule, because the text around it stays.

### Links

OSC 8 is dropped, and its text stays as plain text (B30-terminal-filter, option H1).

- A link's text can differ from its target.
- A link to `localhost` or a private address names a service on the
  host, not in the guest.

Terminals still make a bare URL clickable.

### What `wb` keeps of a link

For each dropped link, `wb` keeps:

- the URL: its first 2048 bytes, as the guest sent them, with an
  invalid byte counting as one;
- the text: its first 256 code points after the text filter, with an
  invalid byte counting as the one U+FFFD that replaces it;
- a note when it cut either.

It records them in the audit log through `wb-hostd` (S09-11-audit). It
lists them, without repeats, in the session summary.

It keeps at most 100 entries. Past that it counts the rest by mark, and
the ones without a mark as `unmarked`. The summary then says "and N
more".

### Marks

An entry is marked when its URL matches a row below. The audit log
names the marks as the table does. An entry gets the first one that
applies, in this order:

| The URL | Mark |
|---|---|
| is `file:` (a guest path) | `guest-path` |
| has an IP literal | `ip-literal` |
| has `localhost` or a name under `.localhost`, `.local`, `.internal`, `.home.arpa` or `.lan` | `local-name` |
| has a name without a dot | `dotless-name` |
| has a user name | `user-name` |
| is `http:` | `http` |
| has another scheme | `other-scheme` |

The mark is a heuristic on the name alone. An unmarked name can still
resolve to a host-local address, so no mark doesn't mean safe.

## Decisions

### Drop links, keep their text

OSC 8 is dropped, and its text stays as plain text.

- **Why:** a link's text can differ from its target, and a link to a
  local name or private address names a service on the host.
- **Rejected:**
  - rewriting a link inline as `text (url)`, which changes line widths
    the TUI has laid out;
  - passing `https://` links to public hosts, which leaves the
    text-target mismatch;
  - passing a link only when its text equals its URL, which makes the
    filter follow the screen.

**Status:** Draft
