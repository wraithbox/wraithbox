# C11 - Host Daemons, Audit, and Disks

**Purpose:** The test cases for the host daemons' own checks:
`wb-vmd`'s device set and slots, the image build bundle, supervision,
the accounting and event channels, the daemon log pipes, the audit
writer, free space, and the data disk steps.

**Verifies:** S04-02-vm-provider ("The device set is fixed and checked", "Guest ports below 1024", "Hand-off on macOS", "The host opens every connection", "Caps and closing"), S06-12-guest-agent ("The hello"),
S06-11-reconnect-and-recovery ("Reconnect"), S06-06-devices-and-warm-start ("When the host sends the time"), S04-08-records-and-bounds ("Host work the guest can cause"),
S04-03-self-sandboxing ("Each host daemon is self-sandboxed", "Confined first thing in `main`"),
S04-07-supervision, S07-05-stream-hand-off ("Message form"), S04-08-records-and-bounds ("Accounting and event channels", "Logs and health"), S06-05-disks, S06-13-data-disk-format,
S09-11-audit, S04-04-vm-processes, S05-03-management-commands ("`wb vm`"), S06-07-slot-admission ("Other software"), S06-12-guest-agent ("Guest tools disk")

## Unit tests

### wb-vmd

- **Device set** (S04-02-vm-provider, "The device set is fixed and checked", X25-vmd-sandbox). Pass the check each of these, and see
  each refused with its rule: a NAT network, a bridged network, a
  file-handle network while the set has none, a shared directory, a
  console or SPICE device, an audio device, a disk or serial port file
  outside the VM's bundle, a second network device, a symbolic link at
  a bundle file's name, and a missing bundle file. The fixed set of
  S06-vm-lifecycle is accepted.
- **Bundle file names** (S06-05-disks, "Names on the host"). Each
  guest-written bundle file has an extension that maps to a dynamic
  type, so Launch Services doesn't open it as a disk image. A clone of a spike
  bundle that kept `aux.img`, `sys.img` and `data.img` is refused with
  `bundle-file-missing`.
- **Slot.** Start `wb-vmd` with `WB_VMD_SLOT` unset, with an unknown
  value, with the `build` slot, with its bundle a symbolic link to the
  other slot's bundle, and with its bundle missing. See
  `vmd-slot-missing`, `vmd-slot-unknown`, `vmd-slot-build-unsupported`,
  `vmd-slot-bundle-mismatch` and `vmd-bundle-missing` logged before
  `sandbox_init`.
- **Start interface** (S04-03-self-sandboxing, "How `wb-vmd` is
  started"). An argument is refused with `vmd-arguments`, and a missing
  descriptor or one of the wrong kind with `vmd-descriptor-invalid`.
  Closing the lifeline stops the VM. Unconfined, the deny probe refuses.
  An empty sandbox parameter, or one with a NUL byte, is refused with
  `vmd-sandbox-param-invalid` before confinement.
- **Peer check and version record** (S04-architecture, "The version
  record"). A peer of another user ID is closed with
  `ipc-peer-uid` and doesn't get a byte, and the next connection is
  served.
  A peer of another version, an unstamped one, one outside the version
  form, a silent one and one that sends a wrong magic each get their
  rule, and no handler runs. Arbitrary bytes never parse to a version
  that isn't 1 to 64 bytes of printable ASCII.
- **VMService.** With a fake VM, each refusal comes back with its rule
  as the `ErrorInfo` reason: `vm-resources`, `vm-already-running`,
  `vm-generation`, `vm-stop-mode`, `vm-starting`, `vm-config`,
  `vm-watch-limit`, and a device-set refusal passed through. A stop for
  `wb-vmd`'s exit during a start waits for it, then stops the VM.
- **ConnectGuest** (S04-02-vm-provider, "Hand-off on macOS", "Caps
  and closing"). With a fake socket device and a real hand-off
  socketpair (`ConnectGuestTests`), `guest-port-not-reserved`,
  `vm-request-id`, `vm-generation`, `vm-not-running`, `connect-cap`,
  `vm-connect-failed`, `vm-connect-timeout` and `handoff-send-failed`
  each come before any record is sent. A connection is sent once with
  the request's fields, and `wb-vmd`'s copy is closed before the record
  is read.

### VM boot (needs-vm)

- With `WB_VM_TEST_BUNDLE` naming a spike bundle, `mise run
  swift:test-vm` clones it into the work slot and starts the signed
  `wb-vmd` with its start interface. See `RUNNING` through
  `WatchState`, the logged device list equal to the fixed set, a forced
  stop reach `STOPPED`, a data disk linked out of the bundle refused
  with `device-set-file-outside-bundle` and `wb-vmd` still answering,
  and a closed lifeline end the process with status 0. The slot is
  deleted afterwards. Pass or fail, the test prints how `wb-vmd` ended
  and every record it logged.
- **Restore image.** A `wb-vmd-install` whose restore image is a
  symbolic link, or not at the fixed path, logs
  `vmd-restore-image-mismatch` before `sandbox_init`.
- **Provisioning.** An instance in the `work` or `isolated` slot
  refuses provisioning options with `vmd-provisioning-refused`.
- **Launcher table.** A table test on the `wb-launcher` entries checks
  that each `wb-vmd` entry's `WB_VMD_SLOT` value and its bundle
  parameter belong to the same slot. `TestDiskfmtEntries` checks the
  same of each `wb-diskfmt` entry's `WB_DISKFMT_SLOT`, and that its
  environment holds nothing else.

### Local IPC

- **Endpoints, peers and versions** (S04-architecture, "Inter-process contracts", "The version record"). Tests in `internal/platform/paths`, `internal/platform/localipc` and `internal/hostversion`: `HOME` doesn't change the paths, an unsafe run directory or endpoint file is refused and left in place, a peer of another user ID is closed with `ipc-peer-uid` and the next one served, each bad version record gets its rule on both sides, and over gRPC no handler runs for a refused version.

### Image build

- Leave a symbolic link, a hard-linked file, or a directory at the
  sealed disk's name in the build bundle, and see the build fail with
  `build-bundle-not-regular`, nothing cloned into `<data>/images/`,
  and nothing outside the bundle deleted.

### Confinement

- **Self-sandbox** (S04-03-self-sandboxing, "Confined first thing in `main`"). In a child, `(deny default)` denies files, connects and running programs on 32 threads and keeps inherited descriptors working. `(allow default)`, a syntax error, a second call and each bad parameter fail with their rule.

### Descriptor channels

- **One descriptor per message** (S07-05-stream-hand-off, "Message form"). Over real socketpairs, records with and without a descriptor round-trip. An oversized record, two, three and 254 descriptors are each refused with every descriptor closed. A message whose descriptors don't fit the full descriptor table is dropped, and a send to a full peer fails at once.
- **Hand-off receiver** (S04-02-vm-provider, "Hand-off on macOS"). Tests in `internal/platform/vmhandoff` and `internal/vmdlink`: over a real socketpair, each refusal of the receiver and of `fdchan` has its rule, and no descriptor of the record is open after it (`fcntl`). A good record gives its request a working connection, and the hello runs over it end to end with a fake `wb-vmd`. `FuzzHandoffMessage` checks that no record of arbitrary bytes, the empty one included, is accepted unless it names the open request, the current generation, the host's direction and the requested port.

### Supervision

Spec: S04-07-supervision.

- Close each daemon's control channel, and see `wb-netd` and
  `wb-proxyd` exit, `wb-launcher` end what it started and exit, and a
  fake VM provider stop its VMs.
- On a fake clock, kill `wb-netd` five times in 10 minutes, and see:
  - the backoff;
  - each new `wb-netd` start with the wildcard budget and the
    synthetic name mapping its predecessor left, a debit written just
    before the kill included;
  - no restart after the fifth exit;
  - a session start then refused with `daemon-given-up`.
- Run the same test with the VM's audit event budget exceeded and its
  refusal events suppressed, and see every debit, every mapping entry
  and every release counted.
- Restart `wb-proxyd`, and see every running VM get a new hand-off
  socket.
- Send a running `wb-netd` a socket message with two descriptors, and
  one with another VM's generation, and see each refused.
- The type that holds `wb-hostd`'s copy of the packet transport
  descriptor has no read or write method, and the copy is closed at VM
  stop and at a restore.

### VM manager

Spec: S04-04-vm-processes, S06-07-slot-admission ("Other software"),
S06-12-guest-agent ("Guest tools disk"), S05-03-management-commands
("`wb vm`"). Tests in `internal/hostd/vm`, with the fake provider and
cloner of `internal/hostd/vm/vmtest`:

- A start and a stop of each slot. A second start is refused with
  `vm-running`, and a stop of a stopped VM with `vm-not-running`.
- Refusing mode refuses a start with its rule, before any step.
- Managers that share one `state.db` give strictly increasing
  generations, and the generation is committed before the provider
  sees it.
  Generation 0 is refused with `vm-generation-invalid` on start, stop
  and connect. A connection for any generation but the current one is
  refused with `vm-generation-stale`. Each run that reaches running is
  reported once to `OnRunning`, whose channel closes when the run ends.
- A provider refusal is passed on with its rule. A start that fails on
  the framework's limit of running VMs is refused with
  `vm-limit-other-software`, naming the running VM, with no retry.
- A VM that stops on its own reaches stopped with its error kept, and
  is logged with `vm-stopped-unexpectedly`.
- A request stop that doesn't finish in time goes to a force stop. A
  provider that never answers a stop still ends the run, with the
  instance closed once, and one that never opens or never answers a start
  is refused with `vm-start-timeout`.
- While a stop runs, a second stop and a connection are refused with
  `vm-stopping`, and a start with `vm-running`. Of concurrent starts of
  one slot, one opens an instance. A stop that races `wb-vmd`'s exit
  closes the instance once. After a restart, every call goes to the
  new instance.
- A provider rule that names one of the manager's own rules is refused
  with `vm-provider-failed`, and the provider's text is cut to 1 KiB.
- A bundle path that is a symbolic link or a file is refused with
  `vm-bundle-not-directory`, and one that can't be created with
  `vm-bundle-create-failed`.
- The configured resources reach the provider.
- A stop with a fake session is refused with `vm-live-sessions`, and
  the refusal lists it.
- **Tools disk**, in a temporary directory with a cloner that copies:
  `tools.img` is replaced before each start. A symbolic link at
  `tools.img` to a file outside the bundle is replaced, with the target
  unchanged. A leftover temporary file is deleted. A clone failure
  refuses the start with `tools-disk-clone-failed`, and the temporary
  file is gone. An old copy with mode 000 is still replaced, which
  shows the manager never opens it (SEC15-no-guest-disk-mount).

### Accounting and event channels

Spec: S04-architecture.

- Fail an event write with `ENOBUFS` or `EAGAIN`, and see new work
  refused with `event-channel-blocked`, and the record written once the
  retry succeeds.
- Send a mapping entry with an uppercase or non-LDH name, a trailing
  dot, an address outside 198.18.0.0/15, or one past 131,072 live
  entries. See it logged with `mapping-entry-invalid`, and the
  `wb-netd` that sent it ended, as for a record received with
  `MSG_TRUNC`.
- See entries kept until their release record, with `wb-hostd`'s
  receipt time.
- Block an accounting write past 100 ms, and see the lookup refused
  with `accounting-write-failed`.

### Daemon log pipes

Spec: S04-08-records-and-bounds, "Logs and health". Tests in
`internal/hostd/logpipe`, on a fake clock:

- 101 lines in one second write 100 lines and one `daemon-log-rate`
  record with the count 1.
- A 5 KiB line is cut at 4 KiB with the truncation marker, and the next
  line is intact.
- A line with a quote, a control byte and invalid UTF-8 becomes one
  well-formed JSON record of the spawn's process.
- Rotation at 10 MiB keeps 5 files.
- A blocked file write doesn't stop the reader from draining the pipe.
  The lines it drops are counted in `daemon-log-backlog` records.

### Host-guest socket

Spec: S04-02-vm-provider ("Guest ports below 1024"), S06-12-guest-agent
("The port", "The hello").

- **Listener** (`internal/platform/guestsock`, with a fake socket and
  clock). Failed accepts are skipped and the next host connection is
  served. A connection from CID 3 and one from CID 0 are closed and
  logged with `guestd-peer-not-host`. Each kind logs one record per
  10 s window with the count held back. `EADDRINUSE` is retried from
  100 ms doubling to 2 s with `guestd-bind-in-use`, and `EACCES` is
  returned at once with `guestd-bind-failed`. Port 1023 is below 1024.
- **Hello** (`internal/guestlink`). An empty version, one of 65 bytes,
  one with a control byte or a non-ASCII byte, another version, an
  answer over 4 KiB, a guest that never answers, one that answers
  `UNIMPLEMENTED`, and package lists that are empty, other, longer or
  out of form are each refused with their rule and the connection
  closed. A good answer passes, and `wb-guestd`'s Hello passes over a
  socketpair.

### Guest link

Spec: S04-02-vm-provider ("The host opens every connection", "Caps and
closing"), S06-11-reconnect-and-recovery ("Reconnect"),
S06-06-devices-and-warm-start ("When the host sends the time"),
S04-08-records-and-bounds ("Host work the guest can cause", its row of
RPCs from `wb-guestd`). Tests
`TestC11GuestLink*` in `internal/hostd/guestconn`, with the VM manager,
the fake provider, a fake clock, and `wb-guestd`'s GuestService serving
the guest end of each socketpair:

- A good hello links on port 1023 of the current generation, and the
  time hook runs after it, then every 60 s.
- Port 0, 22, 1022, 1024 and 65535 are refused with
  `guest-port-unrouted` before the provider is asked.
- Each cap refuses at its limit with its rule: `guest-conn-cap-vm`,
  `guest-conn-cap-project`, and `guest-reconnect-rate` for the 21st
  open in a minute. A connection that opens after its run ended is
  closed with `guest-conn-stale`. After `Close`, an open is refused with
  `guest-links-closed` before the provider is asked.
- While every try fails, by a failed connection or a refused hello,
  the waits are 0.1, 0.2, 0.4 s and so on up to 5 s. A guest that drops
  the link after each hello gets 20 connections, then the link waits
  for the window and logs `guest-reconnect-rate` once.
- Closing the guest end logs `guest-link-dropped` once and reconnects
  after 0.1 s, the old connection closed, and grpc-go never asks
  the link's dialer for a second connection (`guest-redial-refused`).
- A VM stop ends the loop and closes every connection, and the next
  start links to the new generation. No goroutine of the link, its
  loop or a gRPC client transport is left after `Close`.

The gRPC limits, in `internal/guestlink`:

- `TestOptionsSetEveryLimit` and `TestLimitsValues`: the client and the
  server options of a guest connection set every limit explicitly.
- `TestLinkNamesItself`: the guest sees `:authority` `wb-guestd` and the
  user agent `wb-hostd` with grpc-go's version, and no host path.
- An answer over 64 KiB after the hello, and a request over 64 KiB to a
  server on a guest connection, are refused. One guest connection runs
  at most 16 requests at once.
- Over a socketpair, a test handler refuses past the rate with
  `guest-rpc-rate` and past the bound of requests in flight per VM,
  over three connections, with `guest-rpc-inflight`.
- A link closes itself and its connection when the connection leaves
  `READY`, and its dialer refuses a second call with
  `guest-redial-refused`.

### Build versions

- Have `wb-guestd` report another build version, a version string that
  isn't ASCII, one longer than 64 bytes, and one not of the fixed
  form. Connect a host peer of another build version, and one of an
  unstamped build. See each refused before any other request, with the
  version escaped and cut in the log. The guest cases are
  `TestHelloRefuses` and `TestHelloUnstampedHost` in
  `internal/guestlink`, and `TestC11GuestLinkVersionRefused` in
  `internal/hostd/guestconn`, which also checks that the link status
  keeps both versions for `wb vm status`.
- Connect a host peer from a replaced bundle, and see `wb-hostd` exit
  with `bundle-replaced`. An unreadable installed version doesn't make
  it exit.

### Audit writer

Spec: S09-11-audit.

- Exceed the per-class budget, and see no 5019, 2004, approval,
  returned-work, store-access, VM or session record dropped, and
  `wb-netd` and `wb-proxyd` refuse with `audit-budget` until the window
  turns.
- Stop a VM with an open run of suppressed refusals, and see its count
  in the stop record.

### Free space

- On a fake volume, drop the free space below the floor, and see the
  VM whose data disk grew stopped before any write fails, and the
  reserve allocated again once space returns.

### Data disk steps

Spec: S06-13-data-disk-format, S04-04-vm-processes ("`wb-diskfmt`"),
S05-03-management-commands ("`wb vm reset-data`").

The steps, in `internal/diskfmt`, with a fake `diskutil`. In each case
the fake `diskutil` is never asked to attach a file at the final name
(SEC15-no-guest-disk-mount).

- Leave a temporary file, and see it deleted, never attached.
- Have the fake `diskutil` report another backing image path at the
  check after the attach, and at the check before `eraseDisk`.
- Replace the temporary file with another file at the same path, or
  add a hard link to it, before the attach and before the format.
- Put a file at the final name before the rename.
- Ask for a size outside the bounds, and see `diskfmt-size-invalid`
  before any file is made.

In each failure, see `wb-diskfmt` fail with the rule and the file at
the final name kept. A failure after the attach detaches that device,
and one before it detaches nothing.

`cmd/wb-diskfmt`:

- Start it with `WB_DISKFMT_SLOT` unset, with an unknown value, with an
  argument, with its bundle missing, and with its bundle a symbolic link
  to the other slot's. See `diskfmt-slot-missing`,
  `diskfmt-slot-unknown`, `diskfmt-arguments`, `diskfmt-bundle-missing`
  and `diskfmt-slot-bundle-mismatch` logged before `sandbox_init`.
- Confined by its own profile, it refuses a size outside the bounds,
  a request out of order, and a peer of another build version. It
  can't read another file of its bundle, such as the system disk.
- `TestHostFormat`, opt-in with `WB_DISKFMT_HOST_TEST=1` and attended
  (I228): the steps with the real `diskutil`, confined, on a bundle
  under `.scratch/`. See a formatted image at the final name with far
  fewer blocks than its size, a canonical volume UUID, no device left
  attached, and a read outside the bundle denied.
- Each data disk name ends in an extension that DiskImageMounter's
  `Info.plist` doesn't claim.

`wb-hostd`, with a fake `wb-diskfmt` and a temporary `state.db`:

- Give it a file at the final name that `state.db` doesn't record as
  formatted, and a link there, and see the VM start refused with
  `data-disk-unrecorded` and the file kept. Remove a disk recorded as
  formatted, and see `data-disk-missing`. No `wb-diskfmt` runs.
- See the records written in the order of steps 7 and 9, and a crash
  between the rename and step 9 refused at the next start with
  `data-disk-unrecorded`.
- Answer with a refusal of `wb-diskfmt`'s own, one of another rule, a
  UUID not in canonical form and another name, and see each refused,
  with nothing recorded as formatted.
- A second format or reset of a slot while one runs is refused with
  `data-disk-busy`.

`wb vm reset-data`:

- Refused without a terminal, and with a flag. A declined confirmation
  leaves the disk and its record as they were. While the VM runs, `wb-hostd` refuses with
  `vm-running`, and a start during a reset is refused with
  `vm-resetting`.
- An accepted one deletes the old file, drops the record, and records a
  new disk with a new UUID. The log line holds the old and new UUID.

## Fuzzing

- The receive decision of the descriptor channels, `FuzzRecvDecision`: at most one descriptor kept, and every other one closed (S07-05-stream-hand-off, "Message form").
- The accounting records (debits, mapping entries and releases) and the
  event records `wb-hostd` reads from `wb-netd` and `wb-proxyd`
  (S04-08-records-and-bounds, "Accounting and event channels").
- The host version record, `FuzzVersionRecord`: no more than 69 bytes read, and an accepted record holds 1 to 64 printable ASCII bytes equal to the reader's own version (S04-architecture, "The version record").
- The daemon log pipes, `FuzzLogPipe`: arbitrary bytes in arbitrary
  chunks. Each record is one JSON line with at most 4 KiB of input in
  its line field. The reader's buffer doesn't grow past its bound
  (S04-08-records-and-bounds, "Logs and health").
- Every vsock RPC handler in `wb-hostd`. The first is
  `FuzzHelloResponse` in `internal/guestlink`: a refused answer never
  yields a version, and an accepted one is 1 to 64 bytes of printable
  ASCII equal to the host's own (S04-08-records-and-bounds, "With
  `wb-guestd`"). The second is `FuzzGuestLink` there: arbitrary bytes
  in arbitrary chunks from the guest end of a socketpair as the answer
  to the link's open: the HTTP/2 preface, the settings, and the hello. The
  link ends linked or refused within its deadline, and never links to a
  version other than its own.
- The stream framing that multiplexes guest requests on a host-opened
  connection, with the project each request is attributed to
  (S04-architecture, B29-vsock-handoff).

**Status:** Draft
