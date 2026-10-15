# S09-10 - Audit

**Purpose:** What the audit log records, the budgets that bound it, and
the records of the terminal filter.

**Requirements:** SEC10-audit

## Design

### Where records go

- JSONL in `<logs>` (S12-platforms; `~/Library/Logs/WraithBox/` on macOS),
  written by `wb-hostd` from events
  sent by `wb`, `wb-netd`, `wb-proxyd`, `wb-guestadmin` and
  `wb-download` (S17-install-and-upgrade), including
  each secret store access without its value (S15-least-privilege).

### Retention

- Rotation deletes records by age only, never to make room. A file is
  deleted once its newest record is older than `audit_retention_days`
  in `config.toml` (default 90, at least 30). A setting below 30 is
  refused at load with the rule `audit-retention-min`. A size cap
  would let a guest push older records out by flooding events.

### Budgets

- No record of these classes is ever dropped: Device Config State
  Change (5019), Detection Finding (2004), approvals, returned work,
  recovery pushes, secret store accesses, the records of VM starts
  and stops and of sessions, and the records of `wb-download`, which
  belong to no VM and count against no VM's budget
  (S17-03-downloads). Their sources bound them already
  (approval limits, push limits, session limits, and the terminal
  filter's records per session, "Terminal filter" below).
- The high-volume classes, Network Activity (4001) and HTTP Activity
  (4002), have a budget per VM per minute: `audit_events_per_minute`
  in `config.toml` (default 6000). Over the budget `wb-hostd` doesn't
  drop records. It refuses new work instead: the VM's `wb-netd` refuses
  new lookups and connections, and `wb-proxyd` new streams and
  requests, with the rule `audit-budget`, until the minute turns. The
  refusals under that rule are written as one record per minute with
  their count. A minute is a calendar minute of UTC.
- Only the refusal classes that S07-egress-gateway aggregates per rule
  (S07-02-packet-path, "Events and rate limits") are written as runs with a count. Each
  open run's count is written when its window ends and in the VM's stop
  record, so no count is lost with the VM.
- Each VM has a byte budget per day, `audit_vm_bytes_per_day` (default
  64 MiB). The day is a calendar day of UTC. Over the budget, new work
  gets the same refusals until the day turns, with the rule
  `audit-bytes`, and `wb status` says so. The classes that are never
  dropped are still written. So the high-volume classes of two VMs add
  at most 2 × 64 MiB to the log a day, at the defaults.
- Both budgets are per VM, because the host can't tell the projects in
  a VM apart (S07-01-per-vm-policy). In the work VM,
  one project's traffic can use up the budget, and the other projects'
  new work is refused with it (T11-shared-vm-grants). The defaults are
  not measured yet (I175).

### Free space and failed writes

- `wb-hostd` keeps free space for the log. At each VM start, each
  session start and every minute, it checks the volume that holds
  `<logs>` against `audit_free_space_floor` (default 1 GiB).
  - A VM starts only when the free space is at least the floor plus the
    room the VM's data disk can still grow on that volume, up to its cap
    (SEC13-bounded-resources). The guest controls what the data disk
    holds. The system disk's growth isn't counted, and the stop rule
    below covers the data disk while the VM runs. Otherwise the start is refused with the
    rule `audit-free-space`.
  - Below the floor at the check every minute, `wb-hostd` stops each VM
    whose data disk grew since the last check, before any write fails,
    shows a native notification and a `wb status` line, and refuses VM
    and session starts with the rule `audit-free-space`.
  - At start it allocates a 64 MiB reserve file in `<logs>`. It frees
    the reserve only when a write fails for lack of space, so the
    records that follow, a VM's stop record among them, can still be
    written. It allocates the reserve again once the free space is back
    above the floor.
- When a record can't be written, `wb-hostd` stops the VM the record
  belongs to first. A failed record that belongs to no VM stops every
  VM. It refuses a session start until it can write that start's
  record. So no guest keeps its network while its decisions aren't
  recorded (SEC10-audit). `wb status` says why.

### Records

- Events use OCSF 1.8.0 classes, as OpenShell's do
  (X24-openshell-artifacts): Network Activity (4001) for
  connections, HTTP Activity (4002) for inspected requests, Device
  Config State Change (5019) for policy, approvals and project
  settings, and Detection Finding (2004) for
  refused placeholders, foreign credentials removed from a request
  (S07-egress-gateway), pin mismatches, the terminal filter's drops
  and dropped links, and other signs of an attack. A SIEM can read the
  log without a custom parser.
- Project settings. `wb trust`, `wb untrust`, and the `wb project`
  commands that change a project write a 5019 event with the values
  before and after: `config_trust`, `placement`, the location, the
  name, or the recorded remote URLs and root commits (S05-03-management-commands).
- Records: timestamp; VM; project, session and guest user (for
  network events, derived from the guest's label, see below);
  destination host and port; decision and the rule that made it; HTTP
  method and path for inspected requests; bytes in and out; approval
  actions; returned work and its flags; the terminal filter's drop
  counts by rule and its dropped links, escaped ("Terminal filter").
  Process attribution reported by the guest is stored as an untrusted
  label.

- Attribution of network events. `wb-netd` and `wb-proxyd` know the VM,
  and `wb-hostd` adds the projects and sessions running in it. The
  project, session, guest user and program of each connection come
  only from the guest's label, stored as untrusted (S07-01-per-vm-policy). The per-session audit log of FR15-inspect holds
  returned work and approvals by the host's attribution, and network
  events by the guest's label, shown as reported by the guest.
  Returned work keeps the project and session of its git transport
  (S08-workspace-and-git).
- Never recorded: credential values and request or response bodies.

### Terminal filter

`wb` filters the guest's terminal stream and sends
`wb-hostd`, over its connection for the session, what the filter
counted and the dropped links it keeps (S16-02-drops-and-links, "Drop
rules" and "Links").
- *Reports.* `wb` sends each kept link when it keeps it: its URL, its
  text, whether either was cut, and its mark, if it has one. Every 10
  seconds while they change, and once more when the stream ends, it
  sends the counts: the counts by rule, and the links past the 100
  kept, counted by mark and as `unmarked`. Each report holds the
  totals so far, not the change. URL and text cross as protobuf
  `bytes`, because a `string` field must be valid UTF-8 and the URL
  is raw guest bytes.
- *State.* `wb-hostd` keeps the last counts and the kept links with
  the session's record in `state.db`. So `wb sessions` shows the
  same summary, links included, for a session whose `wb` is gone
  (S05-01-agent-commands, "Session behavior").
- *Form.* `wb-hostd` takes these reports only from the `wb` of the
  session, and checks them: rule names and marks from
  S16-terminal-stream's lists, mark counts under those names or
  `unmarked`, totals that never go down, at most 100 links per
  session, URLs of at most 2048 bytes, and texts of valid UTF-8 of
  at most 256 code points. It refuses a report that fails the check,
  or that arrives after the end records are written, with the rule
  `terminal-report-form`, and logs the refusal with the session.

#### Records of the filter

- *Link records.* Each kept link is a Detection Finding (2004) when
  it arrives, with its URL, its text, its mark, and its cut flags.
- *End records.* `wb-hostd` writes them from the stored counts,
  when both the session has ended and `wb`'s connection for the
  session has closed, or 30 seconds after the session ended,
  whichever comes first (S06-09-session-lifecycle). The
  deadline covers a `wb` that is stopped with job control or hangs.
  The session's end record holds every count by rule and every count
  by mark, and says whether the report from the end of the stream
  arrived. When `wb` crashed, it didn't. Its closed connection hung
  up the session. The end records then hold the counts of the last
  report, at most 10 seconds old, and the links `wb` had sent.
  `wb-hostd` writes the end records, waits until the log has them on
  disk, and then sets an "end records written" flag with the
  session's record in `state.db`. It checks the flag before it writes
  them. So a lost session that ends after a `wb-hostd` restart
  (S06-09-session-lifecycle) gets its end records from
  the stored counts, and a session that already has them doesn't get
  them again. A `wb-hostd` that stops between the write and the flag
  writes them again after its restart. Each end record has an ID
  made of the session and the rule, `links-past-100` or
  `session-end`, so a reader
  sees the repeat as one record. Each rule with drops is also a
  Detection Finding with its count, except `terminal-osc-title` and
  `terminal-osc-notify`. Claude Code writes titles and notifications
  in nearly every session, and a notification becomes a BEL rather
  than a drop, so those two counts are only in the session's end
  record. When links went past the 100 kept, their counts by mark are
  one more Detection Finding. So a session writes at most 100 link
  findings, one finding per drop rule, and one for the links past
  100.
- *Attribution.* Every one of these records has the session's VM,
  project and session by the host's attribution, because they come
  from the `wb` that started the session, not from a guest label.
- *Severity.* Low for a marked link, for the links-past-100 finding
  when any of its marked counts is above zero, and for the rules
  whose sequence makes a terminal act on the host or type text into
  its input: `terminal-osc-clipboard`, `terminal-osc-file`,
  `terminal-csi-window`, `terminal-dcs` and `terminal-apc`.
  Informational for the rest. The filter dropped each sequence these
  findings count, so none needs action at once.
- *Escaping.* A link's URL and text are guest bytes. The record holds
  each backslash as `\\`, each byte that isn't valid UTF-8 as `\xNN`
  in uppercase hex, and as `\u{H…}` (the code point in 1 to 6 uppercase hex digits,
  without leading zeros, such as `\u{7F}`, `\u{202E}` or `\u{E0041}`)
  each code point in the escaped set of S16-03-around-the-stream ("Guest text elsewhere"): controls (Cc), format characters (Cf), line and
  paragraph separators (Zl, Zp), and every
  `Default_Ignorable_Code_Point`. So a reader of the log sees them,
  and no viewer acts on them. JSON's own escaping isn't enough, because a JSON reader turns `\u202e`
  back into the character it names, and a raw U+2028 starts a forged
  line in a viewer that breaks lines there. The length limits apply
  before escaping, so an escape is never cut.

**Status:** Draft
