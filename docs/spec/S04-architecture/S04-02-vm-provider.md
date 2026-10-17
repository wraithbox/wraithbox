# S04-02 - VM Provider and Streams

**Purpose:** How `wb-vmd` hands guest connections and the NIC endpoint to other
processes, the fixed device set of a VM, and where `wb-proxyd` takes
a stream's identity from.

## Design

### The VM provider passes descriptors, not bytes

`wb-vmd` opens host-guest socket connections and creates the NIC
endpoint. Then it hands the descriptors (handles on Windows) to
`wb-hostd`. `wb-hostd` passes the NIC endpoint on to that VM's
`wb-netd` (S07-egress-gateway).

Guest bytes stay out of `wb-vmd`'s code path. It does not relay or
parse guest traffic, so the native code has the smallest possible
exposure to guest input.

The device set of a VM is fixed in `wb-vmd`'s code. No device type is
ever taken from the contract ("The device set is fixed and checked",
below).

### Hand-off on macOS

On macOS (X18-vsock-handoff):

- A vsock connection's descriptor is a Unix stream socket to
  Virtualization's own service process, which moves the bytes to the
  device. The receiver can't read the vsock ports from it, so `wb-vmd`
  sends them along.
- The hand-off channel is a socketpair that `wb-hostd` passes to
  `wb-vmd` at spawn, with no path. Its type is `SOCK_DGRAM`, because macOS
  has no `SOCK_SEQPACKET` for Unix domain sockets
  (S07-05-stream-hand-off, "Transport"). There is one for each VM's
  `wb-vmd` (S04-03-self-sandboxing, "Each host daemon is self-sandboxed").
- Each message carries exactly one descriptor, and these fields:
  request id, VM generation, direction, source port and destination
  port. The message is `HandoffMessage` in
  `proto/wraithbox/vmd/v1/vmd.proto` (S04-08-records-and-bounds,
  "`.proto` packages").
- The receiver refuses the message when the descriptor count is not
  one, the socket type is wrong, or the destination port is not one it
  asked for. It closes any descriptor in it, and logs the refusal. The
  comments on `HandoffMessage` list every refusal rule.

### The host opens every connection

The host opens every host-guest connection (B29-vsock-handoff).

- `wb-vmd` doesn't register a host vsock listener, so nothing in the
  guest can dial the host.
- `wb-hostd` asks `wb-vmd` to connect to `wb-guestd`: at boot, after
  each restore, and after `wb-guestd` restarts.
- `wb-hostd` routes a connection only on its destination port. That is
  the guest port it asked `wb-vmd` to connect to.

A connection the guest opened would say nothing trustworthy about who
opened it. Every process in the guest that can open `AF_VSOCK` can
dial, and it picks its own source port.

### Requests from the guest

Requests that start in the guest, from `git-remote-wb` and guest
events, go to `wb-guestd` over a local Unix socket. There the peer
user ID names the project user (S13-guest-confinement).

`wb-guestd` sends them to `wb-hostd` on a connection the host opened.
That is either multiplexed on an existing connection, or a further
connection the host opens at `wb-guestd`'s request. Each request names
the project it came from. The stream framing on that connection is a
parser of guest bytes with a fuzz target (C11-host-daemons).

Attribution fails closed (SEC08-proj-isolation, SEC10-audit):

- `wb-guestd` refuses a peer user ID that isn't an active, unlocked
  project user of this VM. Root, system accounts, user IDs created at
  runtime, and a locked project user are all refused.
- `wb-hostd` refuses a request for a project that isn't in the VM's
  effective policy.
- Both log each refusal with the rule.

Root in the guest can still pose as any project user.
T11-shared-vm-grants and the isolated slot cover that.

### Guest ports below 1024

A project user can't pose as `wb-guestd` (X27-vsock-confinement). On
guests that use vsock (macOS and Linux, S06-vm-lifecycle):

- `wb-guestd` listens on a port below 1024, and every guest port
  `wb-hostd` connects to is below 1024.
- The guest kernel lets only root bind those ports. So no project
  process can take one while `wb-guestd` restarts, inside the Seatbelt
  profile or outside it.
- The session profile also denies `AF_VSOCK` sockets
  (S13-guest-confinement).

From 1024 up, any guest user can bind a free port. In
X27-vsock-confinement one answered the host as `wb-guestd` for 21.7 s
while launchd restarted the daemon.

`wb-guestd` logs and skips a failed `accept`. That is `ECONNABORTED`,
which a guest process causes by connecting to the guest's own CID. So
`wb-guestd` never stops listening because of one.

A guest process can repeat that connect in a loop: `connect` has no
port check, and each one takes a slot in the listen queue until the
`accept` fails. So the listener bounds what a flood costs it
(SEC13-bounded-resources):

- It closes a connection from any CID but the host's, because only the
  host may talk to `wb-guestd`.
- It logs failed accepts and refused peers at most once per 10 seconds
  for each kind. Each record carries the count it held back since the
  last one.
- After an error other than an aborted connection, such as running out
  of descriptors, it pauses before the next `accept`, longer each time,
  up to a second.

A restart can find the port still held by the old process. Then
`wb-guestd` retries `bind` with a backoff from 100 ms that doubles up
to 2 s, and logs each try with its errno. It doesn't exit: launchd
would start it again only after its throttle of 10 s, and the project
processes stay stopped while the link is down (S13-01-process-hardening,
"When the link drops"). Any other bind error, `EACCES` included, makes
it exit non-zero with the errno logged, because such an error means the
install is broken.

### Caps and closing

- `wb-vmd` caps the connections it has opened but not yet passed.
  `wb-hostd` caps connections per VM and per project. Both log each
  refusal (SEC13-bounded-resources).
- `wb-vmd` closes its copy of a descriptor once it has passed it. The
  passed descriptor keeps working, also while `wb-vmd` is stopped.

### Restore

A restore ends every connection. The saved VM's descriptors read EOF
when it stops. The guest sees EOF on its old connections when the
restored VM resumes.

A frame written to the old network descriptor raises no error and draws
no answer. So before `resume`, `wb-hostd`:

1. passes the new network descriptor to `wb-netd`;
2. resets that VM's TCP flows and their `wb-proxyd` streams.

It doesn't reset the guest's DHCP lease or the synthetic DNS name
mapping that goes with it. Cached answers in the resumed guest then
still work.

Each restore starts a new VM generation. Hand-off messages and audit
entries carry it, and a message from an older generation is refused.
`wb-hostd` connects to `wb-guestd` again after every restore.

### The device set is fixed and checked

A VM's devices are the ones in S06-06-devices-and-warm-start,
"Devices", fixed in `wb-vmd`'s code (X25-vmd-sandbox,
SEC02-no-host-fs-share, SEC05-default-deny).

- **Check before start.** Before every start and restore, `wb-vmd`
  checks its own description of the configuration against that set. It
  does that before it creates any Virtualization device or attachment
  object. It refuses any other device, any other attachment, a second
  network device, and a disk or serial port file outside the VM's
  bundle. Until V1-M3-network-floor it refuses every network device
  (S06-06-devices-and-warm-start, "No network device yet"). Each
  refusal is logged with its rule, and returns the rule in the gRPC
  error.
- **Why first.** Creating a disk attachment on a path the profile
  denies ends the confined process in an uncaught exception
  (X25-vmd-sandbox).
- **Profile.** On macOS, `wb-vmd`'s sandbox profile also refuses a
  shared directory, host audio input, and a disk or serial port file
  outside the bundle. It denies the lookup of the host clipboard
  service.

### What only the code keeps out

The profile can't refuse these, so only `wb-vmd`'s code keeps them out:

- a NAT network, which Apple's Virtualization service provides and
  which would bypass `wb-netd` and `wb-proxyd`;
- a disk in the bundle;
- a device on a descriptor `wb-vmd` holds.

A bridged network failed both ways in X25-vmd-sandbox. Confined, the
framework offered no host interface to bridge to. Unconfined, it
refused the attachment without the `com.apple.vm.networking`
entitlement, which `wb-vmd` isn't signed with.

Three checks verify the parts the profile doesn't enforce:

1. a unit test of the check (C11-host-daemons);
2. an in-guest conformance check that the guest has one network
   interface, with its lease from `wb-netd` (C01-network-and-dns);
3. a static check in CI, the SwiftLint custom rule
   `vmd_refused_device_class` in `.swiftlint.yml`. It rejects at least
   these in the Swift package's sources outside its tests:
   `VZNATNetworkDeviceAttachment`, `VZBridgedNetworkDeviceAttachment`,
   `VZSharedDirectory`, `VZVirtioFileSystemDeviceConfiguration`,
   `VZHostAudioInputStreamSource`, `VZSpiceAgentPortAttachment` and
   `VZVirtioSoundDeviceInputStreamConfiguration`. It also names the
   classes of the other kinds the check refuses with a rule of their
   own (network, share, audio input, console and storage), and of USB
   passthrough and custom virtio devices.

The static check excepts one file,
`Sources/wb-dev-bundle/ProvisionBoot.swift`: the NAT network that test
guests in the spike cache get on their first boot. It is in
`wb-dev-bundle`, test tooling that is never in the app bundle
(S10-tech-stack, "Test tooling"), and `wb-vmd` doesn't link it. A
second rule, `dev_bundle_refused_device_class`, checks that file
against every other class, so it can add only the NAT network.

### `wb-proxyd` takes a stream's identity from the connection, not from `wb-netd`

This serves SEC04-no-guest-secrets and SEC08-proj-isolation.

`wb-hostd` creates a hand-off socket for each VM at each start and
restore, and at the restarts of S04-07-supervision, "Restarting a daemon". It passes one end to `wb-proxyd`, naming the VM, and the
other to that VM's `wb-netd`.

- A stream's VM comes from the socket it arrived on. So do its policy,
  credential bindings and CA.
- Its name comes from the name reports that `wb-proxyd` checked and
  kept.
- What `wb-netd` writes with a stream can only narrow.

So a compromised `wb-netd` can't label a stream as another VM's, or as
the stream of a project that has no session in the VM
(S07-05-stream-hand-off).

**Status:** Draft
