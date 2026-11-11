# C11 - Host Daemons, Audit, and Disks

**Purpose:** The test cases for the host daemons' own checks:
`wb-vmd`'s device set and slots, the image build bundle, supervision,
the accounting and event channels, the daemon log pipes, the audit
writer, free space, and the data disk steps.

**Verifies:** S04-02-vm-provider ("The device set is fixed and checked", "Guest ports below 1024", "Hand-off on macOS", "The host opens every connection", "Caps and closing"), S06-12-guest-agent ("The hello"),
S06-11-reconnect-and-recovery ("Reconnect"), S06-06-devices-and-warm-start ("When the host sends the time", "Network device per slot", "The Xcode disk", "MAC address"), S07-02-packet-path ("Packet transport", "MTU"), S04-08-records-and-bounds ("Host work the guest can cause"),
S04-03-self-sandboxing ("Each host daemon is self-sandboxed", "Confined first thing in `main`"),
S04-07-supervision, S04-05-helper-programs ("`wb-build-ssh`"), S07-05-stream-hand-off ("Message form", "Streams", "Resets"), S07-06-name-mapping ("Name reports", "Releases"), S04-08-records-and-bounds ("Accounting and event channels", "Logs and health"), S06-05-disks, S06-13-data-disk-format,
S09-11-audit, S04-04-vm-processes, S05-03-management-commands ("`wb vm`"), S06-07-slot-admission ("Other software"), S06-12-guest-agent ("Guest tools disk")

## Unit tests

### wb-vmd

- **Device set** (S04-02-vm-provider, "The device set is fixed and checked", X25-vmd-sandbox). Pass the check each of these, and see
  each refused with its rule: a NAT network, a bridged network, a
  file-handle network in the build or layer slot, a shared directory, a
  console or SPICE device, an audio device, a disk or serial port file
  outside the VM's bundle, a second network device, a symbolic link at
  a bundle file's name, and a missing bundle file. The fixed set of
  S06-vm-lifecycle is accepted in every slot. A work or isolated VM
  without its file-handle network device is refused with
  `device-set-count`, and every other attachment is refused in every
  slot, in place of the file handle too (`DeviceSetNetworkTests`).
- **Xcode disk** (S06-06-devices-and-warm-start, "The Xcode disk"). In
  the `layer` slot with the Xcode flag, the set with the read-only
  Xcode disk on `xcode.img` is accepted. The check refuses with
  `device-set-count` a set with the Xcode disk but without the flag, or
  in the `work`, `isolated` or `build` slot, a set with it twice, and a
  set without it when the flag is set. It refuses a writable one with
  `device-set-disk`, and one outside the bundle with
  `device-set-file-outside-bundle`. A start call with the flag in any
  slot but `layer` is refused with `vmd-xcode-disk-refused` before any
  device is created.
- **Bundle file names** (S06-05-disks, "Names on the host"). Each
  guest-written bundle file has an extension that maps to a dynamic
  type, so Launch Services doesn't open it as a disk image. A clone of a spike
  bundle that kept `aux.img`, `sys.img` and `data.img` is refused with
  `bundle-file-missing`.
- **Host.** On a macOS older than 27, `wb-vmd` is refused with
  `vmd-host-unsupported` before `sandbox_init`, whatever its slot.
- **Slot.** Start `wb-vmd` with `WB_VMD_SLOT` unset, with an unknown
  value, with the `layer` slot, with its bundle a symbolic link to the
  other slot's bundle, and with its bundle missing. See
  `vmd-slot-missing`, `vmd-slot-unknown`, `vmd-slot-layer-unsupported`,
  `vmd-slot-bundle-mismatch` and `vmd-bundle-missing` logged before
  `sandbox_init`.
- **Build slot** (S04-10-vmd-install, "`wb-vmd-install`", "The
  restore image"; `BuildSlotTests`). The `build` slot resolves to the
  build bundle and the real path of the restore image. The `work`
  slot resolves without one. A restore image that is missing, a
  directory, a symbolic link, or a hard link is refused with
  `vmd-restore-image-mismatch` before `sandbox_init`. The build profile
  is the `wb-vmd` profile plus the three install rules with `IPSW`, and
  the `work`, `isolated` and `layer` profiles are the `wb-vmd` profile
  and mention neither `IPSW` nor the installation service. Only the build
  slot passes `IPSW`, and a build slot without a restore image path is refused
  with `vmd-sandbox-param-invalid`.
- **Start interface** (S04-11-vmd-start, "How `wb-vmd` is
  started"). An argument is refused with `vmd-arguments`, and a missing
  descriptor or one of the wrong kind with `vmd-descriptor-invalid`.
  Closing the lifeline stops the VM. Unconfined, the deny probe refuses.
  An empty sandbox parameter, or one with a NUL byte, is refused with
  `vmd-sandbox-param-invalid` before confinement. In the build slot,
  descriptor 5 is accepted as a pipe, and a missing one, a socket, or a
  file is refused with `vmd-descriptor-invalid`. In every other slot
  an open descriptor 5 is refused with `vmd-descriptor-invalid`
  (`BuildSlotTests`). `vmdspawn` in Go starts the build slot
  with the password pipe as descriptor 5 and no other descriptor
  (`TestSpawnBuild`), and refuses a build slot without the pipe and any
  other slot with one before it makes anything (`TestConfigRefused`).
- **Build password input** (S15-04-guest-admin, "Build";
  `ProvisioningInputTests`). A printable ASCII password of up to 256
  bytes is read to end of file. An empty, a 257-byte, and a non-ASCII
  password, one with a control byte, one whose writer doesn't close the
  pipe within the deadline, and a second read are each refused with
  `vmd-provisioning-password-invalid`, and no refusal holds the
  password's bytes. The pipe is closed after the read.
- **Install** (S04-10-vmd-install, "The install call";
  `InstallTests`, with a fake engine). In the `work`, `isolated` and
  `layer` slots `Install` is refused with `vmd-install-refused`, with
  no file created and the restore image not read. A build bundle that
  already holds `hw.bin`, `mid.bin`, `aux.wbstore` or `sys.wbdisk` is
  refused with `vmd-install-refused` and left unchanged. Resources
  outside the host's range are refused with `vm-resources` before any
  file. An image the host can't run is refused with
  `vmd-restore-image-unsupported`, with no file created. An install
  creates the four files, with the image's hardware model in `hw.bin`
  and a 64 GiB sparse `sys.wbdisk`, installs from the fixed restore
  image path with the build slot's fixed set and no network device,
  and streams rising whole percents that end at 100. A second install,
  and one while the VM runs, are refused with `vmd-install-refused`. A
  framework failure ends the stream with a message that holds its
  `VMError`, and the call is refused with `vm-install-failed`. `Start`
  in the build slot boots the build slot's fixed set. A `Start` with
  `provisioning` set is refused in every slot with
  `vmd-provisioning-refused`, before the engine gets a request.
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
  the request's fields, and `wb-vmd`'s copy is closed once the test
  acknowledges the record. A passed connection keeps its place under
  `connect-cap` until then.
- **Hand-off acknowledgment** (S04-02-vm-provider, "Hand-off on macOS";
  S04-12-descriptor-passing, "Acknowledgments are input";
  `HandoffKeeperTests`). A sent
  connection and a sent packet transport are kept until their
  `HandoffAck`, accepted or refused, and closed at a short deadline
  without one, logged with `handoff-ack-timeout`. An acknowledgment that
  comes late, names another request or generation, is of the wrong form,
  is empty, over 256 bytes, or carries a descriptor is refused with
  `handoff-ack-unexpected` or `handoff-ack-malformed`, and the right
  one still closes the copy. A descriptor that comes with an
  acknowledgment is closed. A refused acknowledgment and a single
  failed receive are skipped, and the next acknowledgment still closes
  its copy (I751). The reader ends, logged with
  `handoff-ack-channel-ended` once, when `wb-hostd` closes its end, on
  control data that was cut, or after the bound of failed receives in a
  row. The kept copies then close at their deadline. Closing the keeper closes every kept
  copy at once and refuses later sends. `closeRights` closes the
  descriptors of one or two `SCM_RIGHTS` messages and none of a message
  whose length is under the header's or past the end, or of another
  type or level.
- **Packet transport** (S07-02-packet-path, "Packet transport", "MTU";
  `NetworkTransportTests`). Both ends of the socketpair are `AF_UNIX`
  datagram sockets, close-on-exec, with `SO_RCVBUF` at least twice
  `SO_SNDBUF`. The attachment has MTU 65535, the VM's MAC address and
  its own copy of the VM end. `Start` sends one `KIND_NETWORK` record
  with the host end and the generation only before the VM starts, and
  `wb-vmd` keeps its copy until the record's acknowledgment. A record that can't be sent refuses the
  start with `handoff-send-failed`.
- **MAC address** (S06-06-devices-and-warm-start, "MAC address"). A
  multicast, a universally administered, a broadcast, a short, and an
  empty address are each refused with `vmd-mac-invalid`, and `Start`
  refuses one before it builds a VM or sends a record.

### VM boot (needs-vm)

- With `WB_VM_TEST_BUNDLE` naming a spike bundle, `mise run
  swift:test-vm` clones it into the work slot and starts the signed
  `wb-vmd` with its start interface. See `RUNNING` through
  `WatchState`, the logged device list equal to the fixed set, a forced
  stop reach `STOPPED`, a data disk linked out of the bundle refused
  with `device-set-file-outside-bundle` and `wb-vmd` still answering,
  and a closed lifeline end the process with status 0. The slot is
  deleted afterwards. Pass or fail, the test prints how `wb-vmd` ended
  and every record it logged. After the first start reaches `RUNNING`,
  the test reads the `KIND_NETWORK` record from the hand-off channel and
  waits up to 300 s for a DHCPDISCOVER from the VM's MAC address on the
  transport.
- **Install** (`VMInstallTests`, needs-vm and needs-host). With
  `WB_VM_TEST_RESTORE_IMAGE` naming a restore image in the spike
  cache, `mise run swift:test-vm` clones it to
  `<data>/images/macos-restore.ipsw`, makes the build bundle with
  `tools.img` and an empty sparse 8 GiB `data.wbdisk` in it, and starts
  the signed `wb-vmd` in the build slot with descriptor 5 a pipe. The
  test refuses to run when the bundle or the image is already there.
  See `Install` stream rising percents that end at 100 and no error,
  the bundle hold the six bundle files, the bundle boot to `RUNNING`
  with the build slot's fixed set, a forced stop reach `STOPPED`, and a
  closed lifeline end the process with status 0. The install's time is
  logged with `measure:`. The test deletes the bundle, the clone, and
  the directories it made. The install calls Apple's signing server
  over the host's network (X30-installer-trust).
- **`wb-hostd` on a VM** (`TestC11HostdVM` in `cmd/wb-hostd`, needs-vm
  and needs-host). With `WB_VM_TEST_BUNDLE` naming a provisioned test
  bundle, `mise run go:test-vm` builds the bundle and runs the test
  binary as `wb-hostd` from it, with `<config>` and `<logs>` in
  `.scratch/go-test-vm`. `wb-vmd` boots only the slot bundle under the
  real `<data>`. Nothing else may use Wraith Box on the Mac during the
  run. The test makes `<data>`, and refuses to run when it exists. At
  its end it removes `<data>`, unless `<data>` holds something it
  didn't make. Through `wb vm` it checks:
  - `start` links, `status` shows the guest's version equal to the
    build's and the generation, `tools.img` has a new inode, and the
    boot's time call took under 1 s;
  - closing `wb-hostd`'s end of the link (SIGUSR1 to the child) links
    again within 2.1 s;
  - `stop` reaches stopped, and `wb-vmd` exits;
  - after `kill -9` of `wb-hostd`, its `wb-vmd` exits by the lifeline.
    A new `wb-hostd` starts the VM at once, and the test logs the
    outcome;
  - a bundle whose tools disk has another build's `wb-guestd` and
    `VERSION`: `start` exits 255, and `status` shows the refusal with
    `peer-version-mismatch` and both versions.

  Each measurement is logged with `measure:`. Runs:
  - 2026-10-25, macOS 27 on Apple Silicon, I288 fixture, build
    `0.0.0+g90b07d3d8a955e8e`: pass. Start to accepted hello 14.1 s,
    boot time call 2.7 ms, link closed to accepted hello 103 ms. `stop`
    took 30.0 s, because the request-stop timeout ran out (I414). After
    `kill -9`, the old `wb-vmd` exited 11.8 s after the kill, and the
    new `wb-hostd`'s start exited 0 at generation 3.
  - 2026-10-25, same Mac, build `0.0.0+g753eae127c860ddf`: failed only on
    two wrong expectations of the test, fixed since. Start to accepted
    hello 19.4 s. The other times were within 10 ms of the run above.
- **Restore image.** A `wb-vmd-install` whose restore image is a
  symbolic link, or not at the fixed path, logs
  `vmd-restore-image-mismatch` before `sandbox_init` (`BuildSlotTests`
  checks the slot resolution without a VM).
- **Provisioning.** An instance in the `work`, `isolated` or `layer`
  slot refuses provisioning options with `vmd-provisioning-refused`,
  and the `build` slot accepts them (`ProvisioningCheckTests`).
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
- Send `wb-hostd` a port 22 socket from a session VM's `wb-netd`, one
  while no build waits for it, and one in a message with two
  descriptors. See each refused with `build-socket-refused` and every
  descriptor closed. Start `wb-build-ssh` with a socket of another type,
  and see it exit with `build-ssh-socket-refused` before it reads its
  standard input (S04-05-helper-programs, "`wb-build-ssh`").
- See `wb-hostd` log `build-socket-accepted` for the build VM's
  socket.
- Run `wb-build-ssh` against a test SSH server whose output repeats
  the password, whole and split across two reads and across a line
  cut, and see the password in no log line. See the server's banner in
  no log line, and its version and disconnect text escaped and cut.
- Run it against a server that never finishes the handshake, one that
  goes quiet in the session, and one that writes output without end.
  See `build-ssh-login-timeout`, `build-ssh-idle-timeout` and
  `build-ssh-output-limit`, and the build fail.
- Run it against a server that writes one byte of output every 90 s,
  past the session deadline, and see `build-ssh-deadline` and the build
  fail.
- See it offer only password authentication to the test server.

### Confinement

- **Self-sandbox** (S04-03-self-sandboxing, "Confined first thing in `main`"). In a child, `(deny default)` denies files, connects and running programs on 32 threads and keeps inherited descriptors working. `(allow default)`, a syntax error, a second call and each bad parameter fail with their rule.

### Descriptor channels

- **One descriptor per message** (S07-05-stream-hand-off, "Message form"). Over real socketpairs, records with and without a descriptor round-trip. An oversized record, two, three and 254 descriptors are each refused with every descriptor closed. A message whose descriptors don't fit the full descriptor table is dropped, and a send to a full peer fails at once.
- **Order and no silent drop** (S07-05-stream-hand-off, "Transport"). On macOS, `TestOrderUnderLoad` in `internal/platform/fdchan` sends 100,000 numbered records of mixed sizes up to 4 KiB, every tenth with a pipe, over `Socketpair(4096)`. The sender gives each record a 5 s deadline and retries a send that fails with `ENOBUFS` or `EAGAIN` before it sends the next. A receiver that pauses now and then sees every record once, in order, with its bytes, and reads each pipe under a read deadline. The counts sent and received are equal, and at least one send found the channel full. `TestQueueDepth` sends with no retry until a send fails with `ENOBUFS` or `EAGAIN`, and logs the depth: 19 records of 4 KiB, 292 of 264 bytes.
- **Egress contract form** (S07-05-stream-hand-off, "The contract"). In `internal/egressmsg`, each decoder refuses a record that is empty or over 4 KiB, doesn't decode, holds a field of another message or of a later build at any depth, doesn't set a kind, or carries a descriptor its kind doesn't take or lacks one it does. A name in another form than canonical is refused with `name-form`, an address outside 198.18.0.0/15 with `synthetic-range`, and a VM that isn't a slot with `vm-slot`. A `HandoffSocketClosed` with another rule than `name-report-malformed` or another check is refused. A control message before the start message, and a second start message, are refused. `TestLinkRoundTrip`: a `NetdStart` gives back the link it was written from. `TestDecodeHostdToNetd`: one with no addressing, a short or multicast MAC address, one MAC for both ends, a prefix length outside 8 to 30, host bits in the subnet, a gateway outside it, or the network or broadcast address as the lease is refused with the check `addressing`. `TestMACOrder`: with both MACs wrong, the guest's is the one refused. A payload pipe that isn't a pipe, has a length over 64 MiB, isn't whole by its deadline, or has a byte after it is refused with `payload-read`. `TestMaxPayloads` builds the largest payloads the bounds allow and sees each under 64 MiB.
- **Hand-off receiver** (S04-02-vm-provider, "Hand-off on macOS"). Tests in `internal/platform/vmhandoff` and `internal/vmdlink`: over a real socketpair, each refusal of the receiver and of `fdchan` has its rule, and no descriptor of the record is open after it (`fcntl`). A record without a kind, or of an unknown one, is refused with `handoff-kind`. A good record gives its request a working connection, and the hello runs over it end to end with a fake `wb-vmd`. `FuzzHandoffMessage` checks that no record of arbitrary bytes, the empty one included, is accepted unless it names the open request, the current generation, the host's direction and the requested port, or is the one packet transport of the current generation, and that every
  record that decodes has a `HandoffAck` that names it. `TestAckAccepted` and
  `TestAckRefused`: an accepted connection, an accepted packet transport and a
  refused record are each acknowledged, and a record that doesn't decode isn't.
  `TestAckSendFailed`: an acknowledgment that can't be sent is logged with
  `handoff-ack-send-failed`.
- **Packet transport hand-off** (S07-02-packet-path, "Packet transport"). In `internal/platform/vmhandoff`: a `KIND_NETWORK` record of the current generation with an `AF_UNIX` datagram socket gives the waiter a `Transport` whose only methods are `Close` and `StartWith`. `TestStartWith`: a child started with it inherits the descriptor in the given slot, the slot is empty again after the start, a failed one included, and a slot that isn't an empty placeholder is refused. A record of generation 0 or another generation, one nobody asked for, a generation's second one, one with a request ID, direction or port, and one with a stream socket are each refused with `handoff-network-refused`, with no descriptor left open. A newer generation's request closes the older one's. In `internal/vmdlink`: `Start` returns the transport, a refused or missing one is refused with its rule and doesn't leave a descriptor open, and a refused `Start` returns `wb-vmd`'s error.

### Hand-off sender and accounting in `wb-netd`

Spec: S07-05-stream-hand-off, S07-06-name-mapping, S04-08-records-and-bounds ("Accounting and event channels"). Tests in `internal/netd/handoff` and `internal/netd/hostlink`, over real `fdchan` socketpairs on macOS:

- **Records before the answer** (S07-06-name-mapping, "Name reports"). `TestAnswerAfterRecords`: when a DNS answer is decided, its mapping entry is already on the accounting channel and its name report on the hand-off socket. `TestAnswerBlocked`: a peer that holds back its version record blocks the write or the send, and after at least 100 ms the name gets the answer every refusal gets, with `accounting-write-failed` or `handoff-send-failed` logged and no approval event. A failed accounting write stops the name report too.
- **Releases** (S07-06-name-mapping, "Releases"). `TestReleaseRetried`: a release that fails on a new hand-off socket keeps its address out of the pool, and is sent before the next name report once the socket works. Its accounting release is written once. `TestKeptMapping`: a pool started with kept entries answers them with their kept addresses, and gives a kept address out again only after its release reached both peers.
- **Streams** (S07-05-stream-hand-off, "Streams", "Resets"). `TestStreamEnds`: the guest sees a FIN when the proxy's end closes, and an RST when a `StreamReset` came first. `TestStreamHold`: `wb-netd` keeps its copy of the end it sent until `wb-proxyd`'s first byte or a `StreamReset` (S07-05-stream-hand-off, "Transport"), so a close without either reaches the guest as a FIN only after the hold deadline, logged with `stream-hold-expired`. `TestGuestWriteFails`: a guest write that fails or passes its deadline resets the guest, logged with `guest-write-failed` or `guest-write-timeout`, the stack's own timeout error included. `TestStreamReadFails`: a read error on the stream's socket resets the guest with `proxyd-stream-read-failed`, or with `stream-reset` when a `StreamReset` came first. `TestStreamSendFails`: a stream send that fails resets the guest and logs `handoff-send-failed`. `TestSocketEnds`: a closed hand-off socket, a malformed message on it, a message with a descriptor, and a new socket each reset the old socket's streams. `TestCloseOrder`: when wb-proxyd closes the hand-off socket and then a stream's socket, the guest gets an RST, also when `wb-netd` reads the stream's end of file while the socket is still ending. `TestStreamResetUnknown`: a `StreamReset` for an unknown stream is logged and ignored. `TestVersionRecordRefused`, `TestNoVersionRecord` and `TestNoSocket` cover a refused version record, none within the version timeout (`peer-version-timeout`), and a send with no socket.
- **Control channel** (S04-07-supervision, S07-05-stream-hand-off, "One hand-off socket per VM"). `TestStart` and `TestStartRefused`: the start message gives the kept entries and the wildcard counts, and a policy change first, an address outside the range, or a missing payload is refused. `TestStartNoVersion`: no version record within the version timeout is refused with `peer-version-timeout`. `TestHandoffSocketRefused`: a socket for another VM, for another generation, or of stream type is closed and logged, and the channel goes on to take a good one. `TestControlEnds`: two descriptors, a second start message, a sequence that doesn't increase, and a refused policy end the channel. `TestPolicyChange`: an accepted policy reaches the policy and gets a `PolicyAck`. `TestServeStops`: the end of the context, or `wb-hostd` closing the channel, ends it without a `control-message-refused` record.
- **Accounting channel** (S04-08-records-and-bounds, "Accounting and event channels"). `TestAccountingVersionRefused`: another version record, or none within the version timeout, ends the channel. `TestAccountingEnds`: a record `wb-hostd` sends after its version record, or a message with two descriptors, ends the channel with `control-message-refused`, and later writes fail.

### `wb-netd`

Spec: S04-09-netd-start.

- **Start interface.** In `cmd/wb-netd` on macOS, the tests start the built program as `wb-hostd` does, with no arguments, an empty environment, descriptors 2 to 6, and a fake guest on a datagram socketpair. `TestChildServes`: a flood of frames before the start is drained, DHCP leases the start message's address, an allowed name gets a synthetic address only after its accounting record and its name report, and the confined process exits 0 when the control channel closes. `TestChildRefused`: an argument exits with `netd-arguments`, and no descriptors or a stream socket as the transport, and an open descriptor above 6, exit with `netd-descriptor-invalid`, each with status 1. `TestChildTransportEnds`: a guest end that closes ends `wb-netd` with `netd-failed` and status 1, not `netd-stopped`. `TestFirstOpenFrom` in `internal/platform/fdchan`: the walk finds an open descriptor and skips a closed one. `TestCloseOnExecFrom`: a descriptor without close-on-exec gets it. After a descriptor refusal, the confinement has already denied a read outside the profile.
- **Drain before the start** (S04-09-netd-start, "Drain before the start"). `TestGateDrains`: frames before the start message are read at once and dropped with `link-not-started`, and the stack reads the frames after it. `TestPolicies`: before the first policy no name and no port is allowed (SEC05-default-deny), and after it the policy's are.
- **Started by `wb-hostd`.** In `internal/hostd/netdproc` on macOS, with the real `wb-netd`: `TestC11NetdStartAndStop` starts it, sends the start message and the first policy, sees the acknowledgment, and at the stop sees it exit 0 with `netd-stopped`. `TestNetdEndedExits`: a `wb-netd` ended with `netd-ended` exits. `TestStartRefused`: a transport that can't hand its descriptor on, a multicast or short MAC address, and a program that isn't `wb-netd` are each refused with `netd-start-failed`. `TestInheritedDescriptorNotPassed`: a descriptor without close-on-exec in `wb-hostd` is marked with `inherited-descriptor` and doesn't reach `wb-netd`, which starts. `TestNetdStopTimeoutKills`: a stopped `wb-netd` that doesn't exit after the control channel closed is killed with `netd-stop-timeout`.
- **The VM's run** (`internal/hostd/vm`). `TestC11NetdStartsAndStopsWithTheRun`: the run's `wb-netd` gets the VM's MAC address and transport, is stopped before the transport copy closes, and its open accounting runs are in the stop record. `TestNetdStartFailedRefused`: a failed start refuses the VM start with `netd-start-failed`. `TestNetdExitStopsTheVM`: a `wb-netd` that exits during a run stops the VM with `netd-exited`. `TestNetdExitDuringStartStopsTheVM`: a `wb-netd` that exits before the VM is running still stops it with `netd-exited`. `TestNetdExitDuringStopKeepsTheStop`: an exit during a requested stop leaves that stop's `vm-stop` rule. `TestNetdPreparedBeforeTheVM`: the policy is loaded before the provider's start, and a failed load refuses the start with `netd-start-failed` before the VM starts.

### Supervision

Spec: S04-07-supervision.

- Close each daemon's control channel, and see `wb-netd` and
  `wb-proxyd` exit, `wb-launcher` end what it started and exit, and a
  fake VM provider stop its VMs. A hung fake `wb-diskfmt`'s child in
  its process group is killed too.
- **Kill request** (S04-04-vm-processes, "`wb-diskfmt`'s kill"). Ask
  `wb-launcher` to kill an instance of another entry, an instance ID it
  never returned, and a reaped `wb-diskfmt`. See the first two logged
  with `launcher-kill-refused`, the third answered as already exited,
  and nothing signaled in any.
- Ask it to kill a hung fake `wb-diskfmt`, and see `launcher-kill`
  logged and the shim's child in its process group stopped too.
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
- Send a running `wb-netd` a socket message with another VM's
  generation, and see it refused and the control channel go on. Send
  it one with two descriptors, and see both closed and the control
  channel end (S04-07-supervision, "New hand-off sockets").
- The type that holds `wb-hostd`'s copy of the packet transport
  descriptor has no read or write method (`TestTransportHasNoReadOrWrite`), and the copy is closed at VM
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
- **MAC address and packet transport** (S06-06-devices-and-warm-start,
  "MAC address"; S07-02-packet-path, "Packet transport"). In
  `internal/hostd/state`, a slot's MAC address is made once, is locally
  administered unicast, is kept across calls and restarts, and differs
  between slots. A new address that is another slot's is made again,
  and an address in the database that `wb-vmd` would refuse is an error.
  In the manager, each `StartRequest` carries the slot's address, the
  same on every start. A failure to get it is refused with
  `vm-mac-failed` before any provider instance opens. The transport is
  held while the VM runs and closed once when the run ends, by a stop
  or by `wb-vmd`'s exit. A transport that comes after the start's
  deadline is closed. A start without one is refused with
  `vm-network-missing`, and the instance is closed. The type the
  manager holds it in has `Close` and no other method.
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
  retry succeeds. In `internal/eventchan`, on a fake clock:
  `TestFailedWriteBlocksAndRetries`, `TestAdmitRefusesWhileBlocked`,
  `TestSlowWriteBlocks` (a write past 100 ms), `TestQueueCapStallsInput`
  (a full queue stalls the caller, refuses new work and doesn't drop a
  record), `TestEmitOfBadFormCounts`, `TestControlTruncatedEndsReads`
  and `TestUnencodedRecordRefusesForGood`. `TestRealChannelFullPeerBlocks`
  fills a real socketpair, and sees every record arrive in order once
  the peer reads.
- In `internal/hostd/events`, `TestRefusedRecordEndsSender` sends
  records that don't decode, break a bound, skip a sequence number, or
  name another VM, or hold a field the contract lacks at any depth, and
  sees each logged with `event-record-invalid`, the
  sender ended, and nothing written. So do
  `TestRepeatedSequenceEndsSender`, `TestDescriptorEndsSender` and
  `TestProxydNamesStoppedVM`. `TestRecvErrorsEndSender` ends the sender
  with `fdchan`'s rule or `event-channel-failed`, and
  `TestRealChannelOversizeEndsSender` with `fdchan-record-size` for a
  record over 4 KiB.
- A sender that ignores its budget is held back (S04-08-records-and-bounds,
  "Host work the guest can cause"). `TestFloodHeldBack` floods a real
  socketpair with well-formed records that ignore every notice. It sees
  the audit writer's queue stay at its cap, logged with
  `event-queue-full`, then the receiver hold at the budget plus its
  margin, logged with `event-budget-ignored`, the sender's writes fail
  each time, and every record it sent written. The flood tests run on
  macOS only, because `fdchan.Socketpair` is unsupported elsewhere, and
  the tests in `hold_test.go` check the holds on every OS.
  `TestFloodStopWhileQueueFull`, `TestQueueFullReleasedWhenSenderGone`,
  `TestBudgetHoldReleasedWhenSenderGone`, `TestStoppingReleasesBudgetHold`
  and `TestSenderGoneWakesRecv` see a VM stop not wait for either hold.
  `TestStoppingReadsTheAllowance` sees the receiver read no more than
  the stop allowance past a hold, and log the rest with
  `event-stop-allowance`. `TestHoldLogLimited` sees a repeated hold
  logged at most once a second. `TestStopReadsPastAFullQueue` in
  `internal/hostd/netdproc` starts a `wb-netd` stand-in that fills its
  event channel while the audit writer holds its cap, and sees the stop
  return its open run before `StopTimeout`, with no record dropped at
  its close. `TestQueueCapRoom` and `TestRoomOnClose` in
  `internal/hostd/audit` check the writer's cap.
- A `wb-netd` that ignores its stop gets no more than the stop
  allowance read (S09-11-audit, "Telling the senders").
  `TestStopBoundsASenderThatIgnoresIt` in `internal/hostd/netdproc`
  starts the stand-in built to keep sending after its control channel
  closes. The test kills the stand-in once it has written past the
  allowance plus the slack, and the stop returns and logs the drop
  with `event-stop-allowance`, while `TestNetdStopTimeoutKills` covers
  the kill at `StopTimeout`. `TestRoomCountsUnsynced` and
  `TestRoomAfterFailedSyncAtCap` in `internal/hostd/audit` see the
  records written but not synced count against the writer's cap, and
  `Room` open again once a failed sync at the cap is retried.
- Send a mapping entry with an uppercase or non-LDH name, a trailing
  dot, an address outside 198.18.0.0/15, one past 131,072 live
  entries, or one whose name or address the VM holds for another. See
  it logged with `mapping-entry-invalid`, and the `wb-netd` that sent
  it ended, as for a record received with `MSG_TRUNC`. In
  `internal/hostd/egress`: `TestMappingEntryInvalid`, with the bounds
  lowered.
- See entries kept until their release record, with `wb-hostd`'s
  receipt time: `TestEntriesKeptUntilRelease`. `TestReleaseUnheld`
  logs a release of an address the VM doesn't hold with
  `mapping-release-unheld`, and the mapping stays as it was.
  `TestRepeatsCountOnce` and `TestLifetime` cover repeated debits and
  names, a stop, a restore and a new active period.
- **The other records** (S07-06-name-mapping, "Kept by `wb-hostd`").
  In `internal/hostd/egress`, `TestRecordMalformed` refuses a record
  that doesn't decode, has a descriptor, is of another sender's kind,
  names a VM that wasn't started, is a mapping entry from `wb-proxyd`,
  or is a debit past the name bound, with `accounting-record-malformed`
  and its check. `TestProxydNamesServedVM` keeps a `ProxydWildcardName`
  for a stopped VM, or one restored with no acknowledged hand-off
  socket, logged with `proxyd-name-unserved`, and drops one after a
  start that cleared the counts. `TestStaleProxydNameDropped` sees the
  new run's last name kept at the full budget.
  `TestProxydNameAfterStopKeepsProxyd` reads such a name after a stop
  or a restart, and sees `wb-proxyd` go on serving the other VM.
- **The reader** (S04-08-records-and-bounds, "Accounting and event
  channels"). In `internal/hostd/egress`, `TestReaderKeepsRecords`
  keeps records after the version record, and a sender that closes
  its channel ends nothing. `TestRefusedRecordEndsSender` ends the
  sender once, logged with the rule and check, and doesn't read on.
  `TestReaderVersionRefused` and `TestVersionSendFails` end the
  sender for another version record, one with a descriptor, none
  within the version timeout, and one that can't be sent.
  `TestRecvErrorsEndSender` ends it with `fdchan`'s rule as the check,
  or `accounting-channel-failed`.
- **`wb-proxyd`'s control channel** (S07-05-stream-hand-off, "One
  hand-off socket per VM", S04-architecture, "Control channels"). In
  `internal/hostd/egress`, over a fake channel, `TestHandoffAfterAck`
  returns `wb-netd`'s end only after the `HandoffSocketAck`. `TestHandoffNoAck`, `TestHandoffProxydEnds`,
  `TestHandoffSendFails` and `TestHandoffRefusedBeforeSend` fail the
  start with `proxyd-setup-failed` and each check, with no end left
  open, and ignore a late acknowledgment. `TestControlMessageRefused`
  and `TestProxydVersionRefused` end `wb-proxyd` with
  `control-message-refused` or the version rule.
  `TestHandoffSocketClosed` ends the VM's `wb-netd` once with
  `name-report-malformed`, and logs a stale notice with
  `handoff-notice-stale`. `TestPolicyWaitsForAck` waits for the
  `PolicyAck`. `TestHandoffNotStarted` and `TestHandoffPending` fail
  the start with `vm-not-started` or `pending` before anything is sent.
  `TestHandoffRetryAfterTimeout` ignores the late acknowledgment of a
  timed-out attempt and returns the retry on its own.
  `TestPayloadWriteFails` ends `wb-proxyd` with
  `control-channel-failed` and the check `payload-write`.
- **Hand-off socket states** (S07-20-handoff-sockets, S07-21-vm-end;
  built in I728 and I512). Over fake channels, in `internal/hostd/egress`:
  - a `wb-netd` that withholds `HandoffSocketHeld` fails the start with
    `held-timeout`, and on a running VM is ended with
    `handoff-netd-ended`;
  - an `ack-timeout` on a running VM ends `wb-proxyd` with
    `handoff-proxyd-ended`, and no `wb-netd` restart is counted;
  - after a `wb-netd` crash and restart, a late `HandoffSocketClosed`
    for the old socket is stale and doesn't end the new `wb-netd`;
  - a `HandoffSocketAck` or `HandoffSocketHeld` for a number never
    offered, or a second one, is refused;
  - each rule of "Closing a socket" ends the daemon it is charged to,
    and the other VM's socket goes on;
  - at a VM stop, `HandoffSocketEnd` comes before `wb-netd`'s control
    channel closes, covers a socket that closed or failed first, and the
    VM's events end only after the record its answer names;
  - a socket end and a stop end for the same number, after a failure
    and a stop, a failed start socket and a refused start, or a
    `wb-netd` exit just before the stop, each get their own answer, and
    `wb-proxyd` goes on;
  - no answer by the deadline ends `wb-proxyd` with
    `handoff-end-timeout` and writes `proxyd-records-lost`;
  - a `wb-proxyd` that exits with an end unanswered ends that wait,
    writes `proxyd-records-lost` and ends the VM's events, and the new
    `wb-proxyd` gets no end;
  - a stop answer whose sequence is below the last record read that
    names the VM and generation is refused, a socket end's answer with
    a sequence other than 0 is refused, and a read that doesn't reach
    the sequence in time ends `wb-proxyd` with `handoff-end-read-timeout`;
  - with `wb-proxyd`'s queue and socket buffer full under a hold, the
    honest stop answer, past the allowance by the end's records, is
    accepted and read to its sequence;
  - an answer with a sequence past the read's bound is accepted, the
    read ends at its last sequence, the cut is named in the stop record,
    and a later record of the generation past that is refused;
  - no `PolicyChange` or `ActivePeriodStart` for a VM, and no message
    that names its ended generation, goes out after its stop end;
  - a `wb-netd` that withholds `HandoffSocketHeld` after a `wb-proxyd`
    restart doesn't delay the other VM's socket, and one VM's stop end
    doesn't delay the other VM's acknowledgment;
  - a `wb-proxyd` record that names a VM and generation outside its
    window ends `wb-proxyd` with `event-record-invalid`.
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

### Guest update and clock

Spec: S06-12-guest-agent ("Update at start"),
S06-06-devices-and-warm-start ("Step or slew").

- **Update** (`internal/guestupdate`, with a fake platform and
  temporary directories). A disk of `wb-guestd`'s own version doesn't
  install anything. A disk of another version, and a `wb-guestd` without a
  version, install every program with the disk's bytes, mode 0755,
  `wb-guestd` last, and run the installed `wb-guestd`. A link at a
  program's name or at the temporary name is replaced, and its target
  left alone. A `VERSION` that is missing, empty, a link, a directory,
  over 64 bytes, ends in a newline or isn't a version is refused with
  `guestd-update-version-invalid`. A program that is missing, a link, a
  directory or over the size cap is refused with
  `guestd-update-program-invalid`. Both refusals come before anything
  is installed. An installed `wb-guestd` that holds the disk's bytes
  already is refused with `guestd-update-no-effect`, and one that
  differs in any byte is replaced. The update reads the disk only
  through the directory that passed its check. A `wb-guestd` that
  can't be installed leaves the programs before it installed, and
  nothing runs. A disk that never passes its check is refused after the
  wait with the platform's rule, and a stop during the wait ends it.
- **Tools disk and install directory** (`internal/platform/guesttools`).
  Only an APFS volume mounted read-only by root at the mount point
  passes. One mounted read-write, by a user, or of another file system
  is refused with `guestd-tools-untrusted`, and a path with no volume
  mounted on it with `guestd-tools-not-mounted`. A directory that is a
  link or a file, has another owner, or is writable by its group or by
  others is refused with `guestd-install-dir-unsafe`. So is a path with
  such a parent, before anything is made under it. One that can't be
  looked at fails with `guestd-install-dir-failed`.
- **Clock** (`internal/guestd`, with a fake clock over a socketpair).
  `SetTime` steps the clock for a boot, a restore and a wake, and for a
  periodic update more than 1 s off either way, and slews it by the
  offset otherwise. A time of 0 or less and a reason that isn't one of
  `TimeReason`'s are refused with `InvalidArgument`, and leave the clock
  alone. A clock the OS doesn't let `wb-guestd` set answers `Internal`.
  On the host, `internal/platform/guestclock` gets `EPERM` as a user
  other than root.

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
- **Time** (`TestC11GuestLinkTime*`). The first time call of a
  generation has the reason boot, and every later one periodic: after
  60 s, and after a reconnect. The next generation boots again. A call
  that fails with `DeadlineExceeded` or `Unavailable` drops the link
  with `guest-time-unanswered`, and the link connects again after
  0.1 s. Any other failure logs `guest-time-failed` and keeps the
  link. Either way the next call is a boot again. `SendTime`, the hook
  of `wb-hostd`, makes `wb-guestd`'s clock step to the host's time.
- **Data mount** (`TestC11GuestLinkSendMount`,
  `TestC11GuestLinkMountRefused`, `TestC11GuestLinkMountTimeout`;
  S06-05-disks, "Mounting the data volume"). `SendMount`, the hook of
  `wb-hostd`, asks `wb-guestd` to mount the recorded volume with
  `first_mount` set, records the first success in `state.db`, and
  asks again without it after a reconnect. A refusal by the guest, or a
  record that is missing, not formatted, or unreadable
  (`guest-mount-record`), keeps the link. Every session start is then
  refused with the rule and a message that names `wb vm stop`. Only a
  refusal by the guest also names `wb vm reset-data`. A mount left
  unanswered (`guest-mount-timeout`) or failed with `UNAVAILABLE`
  (`guest-mount-unavailable`) reconnects, and the next link asks
  again. A session start is refused with `session-no-link` for another
  generation or without a link, and with `session-mount-pending`
  until the mount is answered, also after a reconnect
  (`TestCheckSessionStartPending`).
- In `internal/guestlink` (`TestMountData*` and
  `TestMountRefusalHidesGuestText`), `MountData` takes only a
  `FAILED_PRECONDITION` with one `ErrorInfo` in the guest's domain
  whose reason is a listed rule of `wb-guestd`. Anything else, an
  answer over 4 KiB included, is `guest-reply-invalid`, and the
  guest's message is never shown. A call that fails with
  `UNAVAILABLE` is `guest-mount-unavailable`. The call's deadline is
  45 s.
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
  in the stop record (`TestEndVMWritesOpenRuns`,
  `TestOpenRunsInStopRecord`, `TestOpenRunsPastTheCapAreWritten`,
  `TestEndForgetsTheVM`).
- Budgets: `TestEventsBudget` and `TestBytesBudget` in
  `internal/hostd/audit` see the rule and its end at the turn of the
  minute or day, with every record still written. That includes the
  5019 and 2004 records past the budget. `TestBudgetWithWriter`
  in `internal/hostd/events` writes 6001 records and sees one notice
  that ends at the minute's turn. In `internal/eventchan`,
  `TestBudgetNoticeRefusesAndCounts` and `TestBytesNoticeRefuses` see
  the sender refuse with `audit-budget` or `audit-bytes` and write one
  run per minute, and `TestInvalidNoticeIgnored` sees a notice for
  another VM ignored and logged. `TestStatusAuditBudget` checks both
  budgets in `wb vm status`.
- In `internal/hostd/audit`: `TestClassesRequired` checks each record
  against the OCSF 1.8.0 subset in `testdata`, `TestNoCredentialFields`
  checks that no record type can hold a credential, and
  `FuzzGuestLabel` feeds guest labels. `TestRetention` and
  `TestRetentionMinimum` delete files by age only.
- A failed write (`TestFailedWrite`, `TestFailedWriteOfNoVM`,
  `TestFailedWriteStopsQueued`, `TestLastingFailure`,
  `TestWriteGivenUp`, `TestFailedOpen`): see the VM of the record and
  of every queued record stopped first, then the `audit-write-failed`
  finding, and starts refused until a write succeeds.
- In `internal/hostd/vm` (`TestAudit*`), a VM start is refused, or the
  VM stopped, when its record can't be written. In
  `internal/hostd/hostservice`, a shell doesn't open while the log
  refuses new sessions or its open record can't be written
  (`TestShellAuditRefusesOpen`, `TestShellAuditOpenNotWritten`,
  `TestShellNoAuditLog`), and `TestStatusAuditRefusal` shows the
  refusal in `wb vm status`. In `internal/hostd/kills`,
  `TestKillProcessesLeft` and `TestKillColdStop` see the 2004 records.

### Free space

- On a fake volume, drop the free space below the floor, and see the
  VM whose data disk grew stopped before any write fails, and the
  reserve allocated again once space returns.
- `TestFloor`, `TestReserve`, `TestReserveAllocateFails` and
  `TestCanStartDisk` in `internal/hostd/audit` use a fake volume.
  `internal/platform/volspace` reads a real one (`TestStat`,
  `TestAllocate`, `TestAllocatedSparse`, `TestAllocatedLink`).

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

`wb vm start`, `stop`, `suspend` and `status`, in
`internal/hostd/hostservice` and `internal/vmcmd`:

- In refusing mode `wb-hostd` refuses every call of `HostService` with
  its rule, and a refusal of the manager arrives as the `ErrorInfo`
  reason. A `wb` of another build version is refused before the
  service, and `wb` names both versions.
- `start` exits 0 once the guest is linked. A refused hello and a link
  that isn't up by the deadline exit 255, and leave the VM running.
- A guest version with escape sequences prints without them, cut to
  64 bytes (`FuzzGuestText`). `suspend` is refused without a
  connection to `wb-hostd`. A usage error exits 2.

## Fuzzing

- What `wb-hostd` keeps of the accounting records,
  `FuzzAccountingRecords` in `internal/hostd/egress`: sequences of
  records from `wb-netd` and `wb-proxyd`, with and without a
  descriptor, between starts, stops, restores and acknowledged
  hand-off sockets. The kept state stays within the bounds, with one
  address per name and one name per address, each name in canonical
  form and each address in the range. A record with a descriptor is
  never kept (S07-06-name-mapping, "Kept by `wb-hostd`").
- The receive decision of the descriptor channels, `FuzzRecvDecision`: at most one descriptor kept, and every other one closed (S07-05-stream-hand-off, "Message form").
- The accounting records (debits, mapping entries and releases) and the
  event records `wb-hostd` reads from `wb-netd` and `wb-proxyd`
  (S04-08-records-and-bounds, "Accounting and event channels").
  `FuzzAccountingRecord` checks that an accepted record has no
  descriptor, is of a kind its sender sends, has a canonical name, an
  address inside 198.18.0.0/15 and a VM slot, and encodes again within
  4 KiB.
  `FuzzEventRecord` checks that no record is written after a refused
  one, that every written record marshals, and that an accepted record
  has no unknown field, a known class, every field within its bound,
  and a VM its sender may name. `FuzzBudgetNotice` checks
  that a sender takes no notice for another VM, of an unknown rule, or
  that ends more than 25 hours ahead.
- The output filter of `wb-build-ssh`, `FuzzBuildSSHOutput`: arbitrary
  output in arbitrary chunks, with the password placed anywhere in it.
  No log line holds the password or a part of it cut at a read or a
  line, and the total read stops at its bound (S04-05-helper-programs,
  "What reaches the log").
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
  version other than its own. The third is `FuzzMountDataResponse`
  there: arbitrary bytes as the answer to `MountData` and as its status
  details. A refusal names a listed rule or `guest-reply-invalid`, and
  its text doesn't hold a control byte.
- The control channels between `wb-hostd` and `wb-netd` and
  `wb-proxyd`, `FuzzControlMessage`: each direction, with and without a
  descriptor. An accepted message has the descriptor its kind takes, a
  VM slot, a generation or sequence of at least 1, and encodes again
  within 4 KiB. `FuzzPayload` reads arbitrary payloads and checks the
  start payloads: nothing accepted holds a name out of form, an address
  outside the range, or more entries or names than the bounds allow
  (S04-architecture, "Control channels").
- What `wb-netd` reads from `wb-hostd` and `wb-proxyd`,
  `FuzzNetdControl` in `internal/netd/hostlink`: arbitrary records,
  with and without a payload pipe, on the control channel, the
  accounting channel and the hand-off socket. `wb-netd` sends no name
  report, release, stream or accounting record. Each channel gets its
  version record only, and the control channel one `PolicyAck` per
  policy applied (S07-05-stream-hand-off, "Message form").
- The stream framing that multiplexes guest requests on a host-opened
  connection, with the project each request is attributed to
  (S04-architecture, B29-vsock-handoff).

**Status:** Draft
