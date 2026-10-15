# S16-02 - Drops and Links

**Purpose:** The fixed rules that drops are counted by, and how `wb` records the
links the filter drops.

## Design

### Drop rules

The counts are by these rules. They are a fixed list,
so a guest can't make up new ones, and the audit log records the
counts by these names (S09-10-audit).

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

### Links

OSC 8 is dropped and its text stays as plain text (H1,
I30). A link's text can differ from its target, and a link to
`localhost` or a private address names a service on the host, not in
the guest. Terminals still make a bare URL clickable. `wb` keeps each
dropped link's URL (its first 2048 bytes, as the guest sent them,
an invalid byte counting as one) and text (its first 256 code points
after the text filter, an invalid byte counting as the one U+FFFD that
replaces it), and notes when it cut either. It records them in the
audit log through `wb-hostd` (S09-10-audit),
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

**Status:** Draft
