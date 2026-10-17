# S04-08 - Records, Versions and Bounds

**Purpose:** The records daemons send `wb-hostd`, the build version rules between
processes, the bounds on host work a guest can cause, and logs.

## Design

### Accounting and event channels

`wb-netd` and `wb-proxyd` send `wb-hostd` two kinds of record, on
inherited sockets of type `SOCK_SEQPACKET` or `SOCK_DGRAM`. macOS uses
`SOCK_DGRAM` (I172).

- One record goes in one message. A record arrives whole or not at
  all.
- Each record is at most 4 KiB, in a fixed format.
- `wb-hostd` parses them with a parser that has a fuzz target
  (C11-host-daemons).

`wb-hostd` treats these as a crash of the process that sent them: a
malformed record, an oversized record, and one received with
`MSG_TRUNC`. It ends that process, and counts the exit toward the
restart limit.

### Accounting records

Accounting records carry the counts and the name mapping of
S04-07-supervision ("What a restart keeps", "The kept name mapping"):

- each wildcard budget debit;
- each new or renewed mapping entry, with its name and address;
- each release.

From `wb-proxyd` they carry the Go clock's accepted calibration point,
each new verified tree head, and a `go-sumdb-inconsistent` finding.
`wb-hostd` checks each of those, stamps it with its receipt time, keeps
it in `state.db`, and passes it back at start (S07-13-publish-time, "Persisted state").

They aren't audit events. Nothing rate-limits, suppresses or drops
them.

- `wb-netd` writes one before any drop or suppression decision about
  the event that goes with it, and before it sends the DNS answer.
- A write that fails, or that blocks for more than 100 ms, counts as
  failed. The lookup is then refused (S07-03-dns).
- `wb-hostd` reads what is left in the socket after `wb-netd` exits,
  before it starts a new one.

### Mapping entries are checked

`wb-netd` derives the names from guest queries. So `wb-hostd` checks
each entry before it keeps it:

- **The name** is in canonical form (S07-03-dns, "Name form"):
  lowercase, no trailing dot, and at most 253 bytes in all. Each label
  is 1 to 63 bytes of letters, digits and hyphens, with no dot byte
  inside it.
- **The address** is inside 198.18.0.0/15.
- **The count** is at most 131,072 live entries per VM.

An entry that fails a check is logged with the rule
`mapping-entry-invalid`, rate-limited. It ends the `wb-netd` that sent
it, as a malformed record does. So `wb-netd` never runs on with a
mapping that differs from the one `wb-hostd` keeps.

`wb-hostd` stamps each entry with its own receipt time. It never uses a
time from `wb-netd`.

### Placeholder records

Placeholder records carry a VM, a binding, and one placeholder that
`wb-proxyd` generated (S09-05-credentials, "Model credential").

At a session start that needs a binding with a placeholder:

1. `wb-hostd` asks `wb-proxyd` for the VM's placeholder on its control
   channel.
2. `wb-proxyd` fetches the binding, and generates a placeholder if the
   VM has none for it.
3. `wb-proxyd` replies with a placeholder record on the same control
   channel, not on the one-way accounting socket. The record has the
   size limit and fixed format of the records above.
4. The session start waits for that reply before `wb-guestd` starts
   `claude` (S06-08-projects-and-sessions, "Claude Code in the guest").
   It waits as it does for a locked secret store
   (S15-least-privilege).

`wb-proxyd` also sends a record on the control channel for each
placeholder it draws after a failed check.

`wb-hostd` checks the form of each record before it keeps the
placeholder or puts it in a guest's environment:

- letters, digits and `-` only;
- 32 to 256 characters;
- at most 8 kept per binding per VM, with the oldest dropped past that.

A record that fails the check is a malformed record. `wb-hostd` ends
`wb-proxyd`, and counts the exit toward the restart limit. It refuses
the waiting session start with the rule `placeholder-record-invalid`.

### Event records

Event records carry what goes to the audit log (S09-11-audit).

An event write can block for more than 100 ms, or fail without
blocking (`ENOBUFS` or `EAGAIN` on a `SOCK_DGRAM` socket). Then, with
the rule `event-channel-blocked`:

- `wb-netd` refuses new lookups and connections;
- `wb-proxyd` refuses new streams and requests.

It keeps the record and retries it until a write succeeds, so no event
is lost. So a slow or hung `wb-hostd` makes the VM's network refuse,
not run without records.

### Version skew

Every host program comes from one bundle with one build version. Every
build task sets it in the Go and the Swift code alike, the dev build
and CI included (S10-02-packaging-and-signing). `wb` is
linked from that bundle as well.

- The version includes the VCS revision, and marks a modified working
  tree.
- Only a bare toolchain build (`go build`, `go run`, `swift build`) is
  unstamped. It has no version, and two unstamped builds refuse each
  other. Versions match only when they are equal as bytes.

### Between host processes

The first message on every host connection, gRPC or not, is the
sender's version record (S04-architecture, "The version record", which
gives the form, the 69-byte bound and the refusal rules). A peer of
another build version is refused, and the connection closed. Both versions are logged,
and the installed bundle is checked (S04-07-supervision, "Upgrades").
So there are no compatibility rules between host processes.

- When `wb-hostd` refuses `wb`, `wb` prints a `wraith box:` line that
  names both versions, and exits with 255.
- When `wb-hostd` exits because the bundle was replaced, the line says
  that Wraith Box is restarting after an upgrade, and to run the
  command again.

### `.proto` packages

Each package name ends in its major version (`wraithbox.<area>.v1`). A
change that an older peer can't read is a new major package next to
the old one, never an edit of the old one (I52).

### With `wb-guestd`

After the host opens a connection, the first message from `wb-guestd`
gives its build version, and the major version of each package it
serves.

In V1, `wb-hostd` accepts only its own build version:

- The running bundle started each running VM, because VMs stop with
  `wb-hostd`.
- `wb-guestd` updates itself at start from that bundle's guest tools
  disk (S06-12-guest-agent, "`wb-guestd`").

The package list is refused unless it holds at most 16 names of at
most 64 bytes, each `wraithbox.<lowercase letters>.v<number>`, and
equals the host's own list. The host uses it only to refuse.

A `wb-guestd` that reports another version is refused before any
session request. `wb-hostd` logs both versions. A session start in
that VM is refused with a message that names both versions
(NFR06-explained-refusals). It says to stop and start the VM, and to
rebuild the image with `wb image build` if that doesn't help.

### The guest's version is a label

`wb-guestd` runs as root in the guest, which T00-index takes as the
adversary. The version string is checked against a fixed form first:

- printable ASCII, at most 64 bytes;
- `MAJOR.MINOR.PATCH`, with an optional `-` prerelease part and `+`
  build part of letters, digits, dots and hyphens.

Any other string is refused with the rule `guest-version-form`.
Wherever the string is shown, in a log record or in `wb`'s message, it
is escaped and cut to 64 bytes.

The host uses the reported version only to refuse. It never uses it to
allow something, or to pick how a message is parsed. Each message gets
the same checks whatever the version (SEC11-root-gains-nothing).

### Host work the guest can cause

SEC13-bounded-resources caps each VM, and also the host work each VM
can cause. Every host handler that takes input from a guest has:

- a per-VM rate limit;
- a size limit on each input;
- a bound on the work it runs at once.

A request over a limit is refused or dropped, never queued without
bound, and logged with the limit's rule. For the refusal classes that
S07-02-packet-path, "Events and rate limits", aggregates, a run of
refusals is logged as one record with its count. A new handler gets
its limits before it merges.

| Guest input | Process | Limits | Where |
|---|---|---|---|
| Host-guest socket connections | `wb-vmd`, `wb-hostd` | connections opened and not yet passed, connections per VM and per project, reconnects per minute | S04-02-vm-provider, "The VM provider passes descriptors, not bytes", S06-11-reconnect-and-recovery, "Reconnect" |
| RPCs from `wb-guestd`, with the guest requests multiplexed on them | `wb-hostd` | message size and streams per connection, set on whichever side `wb-hostd` is, client or server, and never left at the library default, requests per second per VM, entries in the reconnect session list | values in code, S06-11-reconnect-and-recovery, "Reconnect" |
| The version `wb-guestd` reports | `wb-hostd` | 64 bytes, fixed form | "Version skew" |
| The package list `wb-guestd` reports | `wb-hostd` | 16 names of at most 64 bytes, fixed form | "With `wb-guestd`" |
| Ethernet frames, DHCP and DNS | `wb-netd` | per-VM caps, query rate, name length, wildcard budget, synthetic pool | S07-02-packet-path |
| Held DNS queries | `wb-netd` | 1,024 held queries per VM by default (configuration), each sent after 4 seconds, over the cap dropped with `dns-hold-full` | S07-04-dns-refusals, "Hold limits" |
| Connections and streams | `wb-netd`, `wb-proxyd` | connections in flight, stream resets and refusals per rule | S07-egress-gateway |
| Dependency gate clock lookups to `sum.golang.org` | `wb-proxyd` | "Clock lookup limits", below | S07-12-dependency-gate, S07-13-publish-time |
| Placeholder scan of requests on inspected hosts | `wb-proxyd` | a 64 KiB cap on the request head (`http-header-size`); per request, a held-back window three times the length of the VM's longest placeholder, at most 768 bytes with the 256-character cap; at most 8 placeholders per binding per VM | S07-08-requests-and-credentials ("Request form", "Header cap"), S07-09-placeholder-binding ("Bounds") |
| Upstream name resolution | `wb-proxyd` | one lookup per stream, so bounded by the connections in flight | S07-05-stream-hand-off |
| Leaf certificates | `wb-proxyd` | a leaf only for a name the VM resolved, cached per VM, CA and name, so at most the allowlisted names plus the wildcard budget | S09-06-ca-keys |
| Pushes and fetches | `wb-hostd`, `wb-git` | the pack scanner's caps, git's memory limit, deadlines, one push per project at a time | S08-04-push-checks, "Pack scanner", and S08-05-push-limits, "Bounds" |
| Leftover-process results | `wb-hostd` | "Leftover-process limits", below | S06-10-leftover-processes, S06-11-reconnect-and-recovery ("Reconnect") and S06-09-session-lifecycle ("Deadlines") |
| Recovery pushes | `wb-hostd`, `wb-git` | one per lost session, to its own branch only, under the same limits as other pushes | S06-11-reconnect-and-recovery, "Recovery" |
| Approval requests | `wb-netd`, `wb-hostd`, `wb-prover` | 16 pending requests and 10 new requests a minute per VM, one open request per name, prover runs at once | S09-10-approval-flow, and S09-09-approvals |
| Native notifications | `wb-hostd` and the notification helper | one per pending approval request, one per daemon outage | S04-07-supervision, "Restarting a daemon", and S09-09-approvals |
| Audit events | `wb-hostd` | a budget per class and per VM, refusals of new work instead of lost records, a byte budget per VM per day | S09-11-audit |
| Accounting and event records | `wb-hostd` | 4 KiB per record, fixed format, a malformed record ends the sender | "Accounting and event channels" |
| Daemon log lines | `wb-hostd` | 100 lines per second per process, 4 KiB per line | "Logs and health" |

### Clock lookup limits

The dependency gate's clock lookups to `sum.golang.org`, in `wb-proxyd`:

- **In flight:** 4 requests per VM, lookups and their tile fetches. 8
  requests per `wb-proxyd`, with one kept for the timer's calibration
  and its tiles.
- **Rate and queue:** 6,000 uncached lookups a minute per VM, and a
  queue of 2,000 per VM. Requests are served first in, first out.
- **Time:** one retry, and 10 s per upstream request. A deadline per
  request of 42 s, plus 125 ms per uncached lookup and per lookup
  queued ahead, counted at admission, and at most 5 minutes.
- **Size:** 2,000 versions per `@v/list`. 100,000 cached records and
  16 MiB of cached tiles per VM. 1 MiB of calibration tiles.
- **Calibration** runs on its own timer only.
- **Refusals:** over a cap, a request is refused with
  `go-clock-lookup-rate`, `go-clock-deadline` or `go-list-too-long`.
- **Stored state:** `wb-hostd` checks the stored heads, points and
  findings. It keeps at most 192 heads of 1 KiB.

### Leftover-process limits

Leftover-process results, in `wb-hostd`:

- one per kill loop, kept until acknowledged, in a session report or a
  cleanup record;
- at most one record per project user in the VM, apart from the 64
  session entries;
- accepted only on an exact kill ID that `wb-hostd` sent, before the
  kill's 7-minute deadline. When none has arrived by then, that counts
  as processes left.

### Input from the internet

The internet is a second source of input, through `wb-download` only.
It runs during an image build, which the user starts. Its bounds are
in S17-03-downloads:

| Internet input | Process | Limits |
|---|---|---|
| Apple's catalog, a `HEAD`, the manifest and its signature | `wb-download` | 4 MiB for the catalog, 1 MiB and 64 KiB for the manifest and signature, 2 minutes per request, 30 s to connect, 60 s without a byte |
| The restore image and the Claude Code binary | `wb-download` | the size from the catalog's entry, at most 40 GiB, and 1 GiB; a file size limit set by `wb-launcher`; 12 hours and 30 minutes; free space checked before the run |
| The result records | `wb-hostd` | a fixed form per field, at most 16 requests per run |
| Audit records of downloads | `wb-hostd` | one per request or refusal, at most 16 per run, never dropped |

### Logs and health

`wb-hostd` writes its own log, and the lines the other daemons send it,
to `<logs>`, in one file per process, apart from the audit log.

- It writes at most 100 lines per second per process.
- It cuts a line at 4 KiB, with a truncation marker.
- It writes one record with the count of lines it dropped over the
  rate.
- Each file is rotated at 10 MiB, and the last 5 are kept.

A daemon log isn't the record of a decision. The audit log is
(S09-11-audit).

For each host process, `wb-hostd` keeps:

- its state: running, waiting to restart, given up, or stopped;
- its version, and when it started;
- its restarts in the last 10 minutes;
- its last exit reason: the exit code or signal.

`wb status` shows them. For each VM without network, it shows which
process is down and why (NFR06-explained-refusals).

When `wb` can't reach `wb-hostd`, `wb status` says so. It says to run
`wb setup`, which installs and starts the service (S05-03-management-commands).

**Status:** Draft
