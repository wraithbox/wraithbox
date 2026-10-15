# S04-02 - VM Provider and Streams

**Purpose:** How `wb-vmd` hands guest connections and the NIC endpoint to other
processes, the fixed device set of a VM, and where `wb-proxyd` takes
a stream's identity from.

## Design

### The VM provider passes descriptors, not bytes

`wb-vmd` opens
host-guest socket connections and creates the NIC endpoint, then
hands the descriptors (handles on Windows) to `wb-hostd`, which
passes the NIC endpoint on to that VM's `wb-netd`
(S07-egress-gateway). Guest bytes stay out of `wb-vmd`'s code path:
it does not relay or parse guest traffic, so the native code has the
smallest possible exposure to guest input. The device set of a VM is
fixed in `wb-vmd`'s code, and no device type is ever taken from the
contract ("The device set is fixed and checked", below). On macOS
(X18-vsock-handoff):
- A vsock connection's descriptor is a Unix stream socket to
  Virtualization's own service process, which moves the bytes to
  the device. The receiver can't read the vsock ports from it, so
  `wb-vmd` sends them along.
- The hand-off channel is a socketpair that `wb-hostd` passes to
  `wb-vmd` at spawn (`SOCK_SEQPACKET` or `SOCK_DGRAM`, no path), one
  for each VM's `wb-vmd` (S04-03-self-sandboxing, "Each host daemon is self-sandboxed"). Each
  message carries exactly one descriptor and the fields request id,
  VM generation, direction, source port and destination port. The
  receiver refuses the message, closes any descriptor in it, and logs
  the refusal when the descriptor count is not one, the socket type
  is wrong, or the destination port is not one it asked for. The
  full contract is in I52.
- The host opens every host-guest connection (B29-vsock-handoff).
  `wb-vmd` doesn't register a host vsock listener, so nothing in the
  guest can dial the host. `wb-hostd` asks `wb-vmd` to connect to
  `wb-guestd`, at boot, after each restore, and after `wb-guestd`
  restarts. `wb-hostd` routes a connection only on its destination
  port, which is the guest port it asked `wb-vmd` to connect to. A
  connection the guest opened would say nothing trustworthy about who
  opened it: every process in the guest that can open `AF_VSOCK` can
  dial, and it picks its own source port.
- Requests that start in the guest, from `git-remote-wb` and guest
  events, go to `wb-guestd` over a local Unix socket. There the peer
  user ID names the project user (S13-guest-confinement). `wb-guestd`
  sends them to `wb-hostd` on a connection the host opened: multiplexed
  on an existing one, or on a further connection the host opens at
  `wb-guestd`'s request. Each request names the project it came
  from, and the stream framing on that connection is a parser of
  guest bytes with a fuzz target (C11-host-daemons).
- Attribution fails closed (SEC08-proj-isolation, SEC10-audit).
  `wb-guestd` refuses a peer user ID that isn't an active, unlocked
  project user of this VM: root, system accounts, user IDs created
  at runtime, and a locked project user are all refused. `wb-hostd`
  refuses a request for a project that isn't in the VM's effective
  policy. Both log each refusal with the rule. Root in the guest can
  still pose as any project user, which T11-shared-vm-grants and the
  isolated slot cover.
- A project user can't pose as `wb-guestd` (X27-vsock-confinement).
  On guests that use vsock (macOS and Linux, S06-vm-lifecycle),
  `wb-guestd` listens on a port below 1024, and every guest port
  `wb-hostd` connects to is below 1024. The guest kernel lets only
  root bind those ports, so no project process can take one
  while `wb-guestd` restarts, inside the Seatbelt profile or outside
  it. From 1024 up, any guest user can bind a free port, and in
  X27-vsock-confinement one answered the host as `wb-guestd` for
  21.7 s while launchd restarted the daemon. The session profile
  also denies `AF_VSOCK` sockets (S13-guest-confinement).
  `wb-guestd` logs and skips a failed `accept` (`ECONNABORTED`, which
  a guest process causes by connecting to the guest's own CID), so
  it never stops listening because of one.
- `wb-vmd` caps the connections it has opened but not yet passed, and
  `wb-hostd` caps connections per VM and per project. Both log each
  refusal (SEC13-bounded-resources).
- `wb-vmd` closes its copy of a descriptor once it has passed it.
  The passed descriptor keeps working, also while `wb-vmd` is
  stopped.
- A restore ends every connection. The saved VM's descriptors read
  EOF when it stops, and the guest sees EOF on its old connections
  when the restored VM resumes. A frame written to the old network
  descriptor raises no error and draws no answer, so before `resume`
  `wb-hostd` passes the new network descriptor to `wb-netd` and
  resets that VM's TCP flows and their `wb-proxyd` streams, but not the guest's DHCP lease or the synthetic DNS name mapping that goes with it, so cached answers in the resumed guest still work. Each restore starts a new VM
  generation: hand-off messages and audit entries carry it, and a
  message from an older generation is refused. `wb-hostd` connects to
  `wb-guestd` again after every restore.

### The device set is fixed and checked

A VM's devices are the ones
in S06-06-devices-and-warm-start, "Devices", fixed in `wb-vmd`'s code
(X25-vmd-sandbox, SEC02-no-host-fs-share, SEC05-default-deny):
- Before every start and restore, `wb-vmd` checks its own description
  of the configuration against that set, before it creates any
  Virtualization device or attachment object. It refuses any other
  device, attachment, a second network device, and a disk or serial
  port file outside the VM's bundle. The check runs first because
  creating a disk attachment on a path the profile denies ends the
  confined process in an uncaught exception (X25-vmd-sandbox). Each
  refusal is logged with its rule (below) and returns the rule in the
  gRPC error.
- On macOS, `wb-vmd`'s sandbox profile also refuses a shared
  directory, host audio input, and a disk or serial port file outside
  the bundle, and it denies the lookup of the host clipboard service
  (the effect in the guest is untested).
- The profile can't refuse a NAT network, which Apple's
  Virtualization service provides and which would bypass `wb-netd`
  and `wb-proxyd`, nor a disk in the bundle or a device on a
  descriptor `wb-vmd` holds. Only `wb-vmd`'s code keeps those out.
  A bridged network failed both ways in X25-vmd-sandbox: confined,
  the framework offered no host interface to bridge to, and
  unconfined it refused the attachment without the
  `com.apple.vm.networking` entitlement, which `wb-vmd` isn't signed
  with.
- Three checks verify the parts the profile doesn't enforce: a unit
  test of the check (C11-host-daemons), an in-guest
  conformance check that the guest has one network interface with
  its lease from `wb-netd` (C01-network-and-dns), and a
  static check in CI that rejects at least
  `VZNATNetworkDeviceAttachment`, `VZBridgedNetworkDeviceAttachment`,
  `VZSharedDirectory`, `VZVirtioFileSystemDeviceConfiguration`,
  `VZHostAudioInputStreamSource`, `VZSpiceAgentPortAttachment` and
  `VZVirtioSoundDeviceInputStreamConfiguration` in `wb-vmd`'s sources outside its
  tests (a grep or a SwiftLint custom rule, not yet built).

### `wb-proxyd` takes a stream's identity from the connection, not from `wb-netd`

(SEC04-no-guest-secrets, SEC08-proj-isolation).
`wb-hostd` creates a hand-off socket for each VM at each start and
restore, and at the restarts of "Restarting a daemon". It passes one
end to `wb-proxyd`, naming the VM, and the other to that
VM's `wb-netd`. A stream's VM, and so its policy, credential bindings
and CA, comes from the socket it arrived on. Its name comes from the
name reports `wb-proxyd` checked and kept, and what `wb-netd` writes
with a stream can only narrow. So a compromised `wb-netd` can't label
a stream as another VM's, or as a project's that has no session in
the VM (S07-04-stream-hand-off).

**Status:** Draft
