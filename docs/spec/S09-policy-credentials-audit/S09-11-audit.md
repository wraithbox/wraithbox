# S09-11 - Audit

**Purpose:** What the audit log records, the budgets that bound it, and
what happens when the log can't be written.

**Requirements:** SEC10-audit

## Design

### Where records go

The audit log is JSONL in `<logs>` (S12-platforms), which is
`~/Library/Logs/WraithBox/` on macOS.

`wb-hostd` writes it from events sent by `wb`, `wb-netd`, `wb-proxyd`,
`wb-guestadmin` and `wb-download` (S17-install-and-upgrade). That
includes each secret store access, without its value
(S15-least-privilege).

### Retention

Rotation deletes records by age only, never to make room.

A file is deleted once its newest record is older than
`audit_retention_days` in `config.toml`. The default is 90, and the
least is 30. A setting below 30 is refused at load, with the rule
`audit-retention-min`.

A size cap would let a guest push older records out by flooding events.

### Budgets

#### Never dropped

No record of these classes is ever dropped:

- Device Config State Change (5019);
- Detection Finding (2004);
- approvals;
- returned work;
- recovery pushes;
- secret store accesses;
- the records of VM starts and stops, and of sessions;
- the records of `wb-download`, which belong to no VM, and count
  against no VM's budget (S17-03-downloads).

Their sources bound them already: approval limits, push limits, session
limits, and the terminal filter's records per session
(S09-12-terminal-filter).

#### Events per minute

The high-volume classes, Network Activity (4001) and HTTP Activity
(4002), have a budget per VM per minute. It is
`audit_events_per_minute` in `config.toml`, default 6000. A minute is a
calendar minute of UTC.

Over the budget, `wb-hostd` doesn't drop records. It refuses new work
instead, with the rule `audit-budget`, until the minute turns:

- the VM's `wb-netd` refuses new lookups and connections;
- `wb-proxyd` refuses new streams and requests.

The refusals under that rule are written as one record per minute, with
their count.

#### Runs with a count

Only the refusal classes that S07-egress-gateway aggregates per rule
(S07-02-packet-path, "Events and rate limits") are written as runs with
a count.

Each open run's count is written when its window ends, and in the VM's
stop record. So no count is lost with the VM.

#### Bytes per day

Each VM has a byte budget per day, `audit_vm_bytes_per_day`, default 64
MiB. The day is a calendar day of UTC.

Over the budget, new work gets the same refusals until the day turns,
with the rule `audit-bytes`, and `wb status` says so. The classes that
are never dropped are still written.

So the high-volume classes of two VMs add at most 2 × 64 MiB to the log
a day, at the defaults.

#### Per VM

Both budgets are per VM, because the host can't tell the projects in a
VM apart (S07-01-per-vm-policy). In the work VM, one project's traffic
can use up the budget. The other projects' new work is then refused
with it (T11-shared-vm-grants).

The defaults are not measured yet (I175).

### Free space and failed writes

#### Free space

`wb-hostd` keeps free space for the log. At each VM start, each session
start and every minute, it checks the volume that holds `<logs>`
against `audit_free_space_floor`, default 1 GiB.

- **At VM start:** a VM starts only when the free space is at least the
  floor, plus the room the VM's data disk can still grow on that
  volume, up to its cap (SEC13-bounded-resources). Otherwise the start
  is refused with the rule `audit-free-space`. The guest controls what
  the data disk holds. The system disk's growth isn't counted. The
  check every minute covers the data disk while the VM runs.
- **Every minute:** below the floor, `wb-hostd` stops each VM whose
  data disk grew since the last check, before any write fails. It
  shows a native notification and a `wb status` line. It refuses VM
  and session starts with the rule `audit-free-space`.
- **Reserve:** at start, `wb-hostd` allocates a 64 MiB reserve file in
  `<logs>`. It frees the reserve only when a write fails for lack of
  space. So the records that follow, a VM's stop record among them,
  can still be written. It allocates the reserve again once the free
  space is back above the floor.

#### Failed writes

When a record can't be written, `wb-hostd` stops the VM the record
belongs to first. A failed record that belongs to no VM stops every VM.

`wb-hostd` refuses a session start until it can write that start's
record.

So no guest keeps its network while its decisions aren't recorded
(SEC10-audit). `wb status` says why.

### Records

#### Classes

Events use OCSF 1.8.0 classes, as OpenShell's do
(X24-openshell-artifacts):

- Network Activity (4001), for connections;
- HTTP Activity (4002), for inspected requests;
- Device Config State Change (5019), for policy, approvals and project
  settings;
- Detection Finding (2004), for refused placeholders, foreign
  credentials removed from a request (S07-egress-gateway), pin
  mismatches, the terminal filter's drops and dropped links, and other
  signs of an attack.

A SIEM can read the log without a custom parser.

#### Project settings

`wb trust`, `wb untrust`, and the `wb project` commands that change a
project write a 5019 event. It has the values before and after:
`config_trust`, `placement`, the location, the name, or the recorded
remote URLs and root commits (S05-03-management-commands).

#### Fields

A record holds:

- timestamp;
- VM;
- project, session and guest user. For network events these are
  derived from the guest's label ("Attribution of network events");
- destination host and port;
- decision, and the rule that made it;
- HTTP method and path, for inspected requests;
- bytes in and out;
- approval actions;
- returned work and its flags;
- the terminal filter's drop counts by rule, and its dropped links,
  escaped (S09-12-terminal-filter).

Process attribution reported by the guest is stored as an untrusted
label.

#### Attribution of network events

`wb-netd` and `wb-proxyd` know the VM. `wb-hostd` adds the projects and
sessions running in it.

The project, session, guest user and program of each connection come
only from the guest's label, stored as untrusted
(S07-01-per-vm-policy).

The per-session audit log of FR15-inspect holds:

- returned work and approvals, by the host's attribution;
- network events, by the guest's label, shown as reported by the
  guest.

Returned work keeps the project and session of its git transport
(S08-workspace-and-git).

#### Never recorded

Credential values, and request or response bodies.

## Decisions

### Delete by age, never by size

- **Why:** a size cap would let a guest push older records out by
  flooding events.

### Refuse new work, never drop records

- **Why:** no guest keeps its network while its decisions aren't
  recorded (SEC10-audit).

**Status:** Draft
