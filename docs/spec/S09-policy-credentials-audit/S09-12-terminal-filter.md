# S09-12 - Terminal Filter Records

**Purpose:** What `wb` reports to `wb-hostd` about the terminal filter,
and the audit records `wb-hostd` writes from it.

**Requirements:** SEC10-audit

## Design

### Terminal filter

`wb` filters the guest's terminal stream. It sends `wb-hostd`, over its
connection for the session, what the filter counted and the dropped
links it keeps (S16-02-drops-and-links, "Drop rules" and "Links").

#### Reports

`wb` sends each kept link when it keeps it: its URL, its text, whether
either was cut, and its mark, if it has one.

`wb` sends the counts every 10 seconds while they change, and once more
when the stream ends:

- the counts by rule;
- the links past the 100 kept, counted by mark and as `unmarked`.

Each report holds the totals so far, not the change.

URL and text cross as protobuf `bytes`. A `string` field must be valid
UTF-8, and the URL is raw guest bytes.

#### State

`wb-hostd` keeps the last counts and the kept links with the session's
record in `state.db`. So `wb sessions` shows the same summary, links
included, for a session whose `wb` is gone (S05-01-agent-commands, "Session behavior").

#### Form

`wb-hostd` takes these reports only from the `wb` of the session. It
checks them:

- rule names and marks are from S16-terminal-stream's lists;
- mark counts are under those names, or `unmarked`;
- totals never go down;
- at most 100 links per session;
- URLs of at most 2048 bytes;
- texts of valid UTF-8, of at most 256 code points.

It refuses a report that fails the check, or that arrives after the end
records are written, with the rule `terminal-report-form`. It logs the
refusal with the session.

### Records of the filter

#### Link records

Each kept link is a Detection Finding (2004) when it arrives, with its
URL, its text, its mark and its cut flags.

#### End records

`wb-hostd` writes the end records from the stored counts, at whichever
comes first (S06-09-session-lifecycle):

- both the session has ended and `wb`'s connection for the session has
  closed;
- 30 seconds after the session ended.

The deadline covers a `wb` that is stopped with job control, or hangs.

The session's end record holds every count by rule and every count by
mark. It says whether the report from the end of the stream arrived.

When `wb` crashed, that report didn't arrive. Its closed connection hung
up the session. The end records then hold the counts of the last
report, at most 10 seconds old, and the links `wb` had sent.

#### Written once

1. `wb-hostd` checks the "end records written" flag before it writes
   the end records.
2. It writes them.
3. It waits until the log has them on disk.
4. It sets the flag, with the session's record in `state.db`.

So a lost session that ends after a `wb-hostd` restart
(S06-09-session-lifecycle) gets its end records from the stored counts.
A session that already has them doesn't get them again.

A `wb-hostd` that stops between the write and the flag writes them
again after its restart. Each end record has an ID made of the session
and the rule, `links-past-100` or `session-end`. So a reader sees the
repeat as one record.

#### Findings per rule

Each rule with drops is also a Detection Finding, with its count. The
exceptions are `terminal-osc-title`, `terminal-osc-notify` and
`terminal-osc-status-text`, whose counts are only in the session's end
record. Claude Code writes titles, notifications and program status in
nearly every session. A notification becomes a BEL, and a status report
passes without its text, rather than being dropped.

When links went past the 100 kept, their counts by mark are one more
Detection Finding.

So a session writes at most:

- 100 link findings;
- one finding per drop rule;
- one finding for the links past 100.

#### Attribution

Every one of these records has the session's VM, project and session by
the host's attribution. They come from the `wb` that started the
session, not from a guest label.

#### Severity

Severity is low for:

- a marked link;
- the links-past-100 finding, when any of its marked counts is above
  zero;
- the rules whose sequence makes a terminal act on the host or type
  text into its input: `terminal-osc-clipboard`, `terminal-osc-file`,
  `terminal-csi-window`, `terminal-dcs` and `terminal-apc`.

It is informational for the rest. The filter dropped each sequence these
findings count, so none needs action at once.

#### Escaping

A link's URL and text are guest bytes. The record holds:

- each backslash as `\\`;
- each byte that isn't valid UTF-8 as `\xNN`, in uppercase hex;
- each code point in the escaped set of S16-03-around-the-stream ("Guest text elsewhere") as `\u{H…}`: the code point in 1 to 6 uppercase hex
  digits, without leading zeros, such as `\u{7F}`, `\u{202E}` or
  `\u{E0041}`.

The escaped set is controls (Cc), format characters (Cf), line and
paragraph separators (Zl, Zp), and every `Default_Ignorable_Code_Point`.
So a reader of the log sees them, and no viewer acts on them.

JSON's own escaping isn't enough. A JSON reader turns `\u202e` back
into the character it names. A raw U+2028 starts a forged line in a
viewer that breaks lines there.

The length limits apply before escaping, so an escape is never cut.

**Status:** Draft
