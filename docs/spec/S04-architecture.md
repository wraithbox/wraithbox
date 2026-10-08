# S04 - Architecture

**Purpose:** Name the processes, the boundaries between them, and which
requirement each boundary serves.

## Overview

![The processes on the host and in the guest. Two channels cross the VM boundary: the host-guest socket, which wb-hostd terminates, and Ethernet frames, which wb-netd terminates.](S04-architecture.svg)

Exactly two channels cross the VM boundary: the **host-guest socket**
(vsock on macOS and Linux, Hyper-V sockets on Windows; terminated by
`wb-hostd`) and **Ethernet frames** (terminated by `wb-netd`). There is
no shared directory, no NAT to the host network, and no other device
that carries data (SEC01-separate-kernel, SEC02-no-host-fs-share, SEC05-default-deny).

## Processes

| Process | Lang | Runs as | Holds | Faces the guest via | Purpose |
|---|---|---|---|---|---|
| `wb` | Go | user, per invocation | nothing; writes binding secrets to the secret store (`wb cred set`, S15-least-privilege) | none | The only CLI: dispatcher for every command, TTY relay for agent sessions (S05-cli) |
| `wb-hostd` | Go | per-user service | policy, landing repos, state | host-guest socket | Sessions, git gateway, approvals, audit writer, admission control (S06-vm-lifecycle, S08-workspace-and-git, S09-policy-credentials-audit) |
| `wb-launcher` | Go, standard library only | user, started by `wb-hostd` before it confines itself (macOS only) | nothing | none | Start the programs in its fixed table when `wb-hostd` asks, so that each can confine itself ("Each host daemon is self-sandboxed") |
| `wb-git` | Go | user, per git gateway transfer, started through `wb-launcher` (on macOS) (decided in B34-sandboxed-daemons, not yet built) | nothing | pack data from the guest, through `wb-hostd` | Confine itself to one repository, then run `git receive-pack` or `git upload-pack` (S08-workspace-and-git) |
| `wb-prover` | Go | user, per boundary check (at `wb trust` and for an approval request whose result isn't cached), started through `wb-launcher` (on macOS); instance cap one per VM plus one for `wb trust` | nothing | repository policy, and a host name the guest asked for in an approval request, both through `wb-hostd` | Set its memory cap, confine itself to its inherited descriptors (the two documents and the result pipe), check the prover binary's hash, then run `openshell-prover check` with fixed arguments (S09-policy-credentials-audit) |
| `wb-vmd` | platform-native | user, spawned for `wb-hostd` (through `wb-launcher` on macOS) | VM handles; during an image build, the provisioning password on a pipe until the start call returns (S15-least-privilege) | none (devices only) | Create, start, stop, save, and restore VMs; hand guest socket connections and the NIC endpoint to other processes (S12-platforms) |
| `wb-netd` | Go | user, one per VM, spawned for `wb-hostd` (through `wb-launcher` on macOS) | nothing | Ethernet frames | Network stack, DHCP, DNS, stream hand-off (S07-egress-gateway) |
| `wb-proxyd` | Go | user, spawned for `wb-hostd` (through `wb-launcher` on macOS) | the binding secrets of the VM's sessions, fetched just in time, and the use of the CA signing key (S15-least-privilege) | streams from `wb-netd` | HTTP policy, credential replacement, dependency gate, upstream connections (S07-egress-gateway, S09-policy-credentials-audit) |
| `wb-guestadmin` | Go | user, one run per image build, `wbadmin` rotation or cleanup, started through `wb-launcher` (on macOS), then exits | the `wbadmin/*` secret store items it writes and fetches, and nothing else | one write-only host-guest connection to `wb-guestd`; pipes to the build's `wb-vmd` and `wb-build-ssh` | The admin side of the image build, `wbadmin` rotation at each VM's first contact, and removing the items of images and system disks that are gone (S15-least-privilege) |
| `wb-build-ssh` | Go | user, one per image build, started through `wb-launcher` (on macOS) | the build password on its standard input, for one session | one SSH session to the build guest, on the build VM's own network | Confine itself, then run the system `ssh` with compiled-in arguments and environment for the build's one install session (S15-least-privilege) |
| `wb-askpass` | Go | user, started by `ssh` for the login prompt | one line of `ssh`'s standard input, until it prints it | none | Print the first line of its standard input to `ssh` (S15-least-privilege) |
| `wb-guestd` | Go | root / SYSTEM inside the guest | nothing | n/a (runs in the guest) | Users, PTY exec, git transport (S06-vm-lifecycle) |

Native user-interface helpers (notifications with actions, later a tray
or menu-bar item) are separate small processes per platform. They render
approval requests and return the user's answer to `wb-hostd`, and decide
nothing themselves (S12-platforms).

## Design decisions

- **One CLI, dispatcher style.** `wb` is the entry point for every
  command. Agent commands (`wb claude …`) pass every following argument
  to the agent untouched, so the agent's own flags and subcommands can
  never collide with Wraith Box's (S05-cli).
- **Cross-platform core, native edges.** Session management, policy,
  approvals, audit, the git gateway, the network stack, the proxy, the
  guest agent, and the CLI are Go and the same on every host OS. Only the
  pieces that must call a platform API with no good Go binding are
  native, and each is a separate process behind a gRPC contract with no
  policy decisions of its own: `wb-vmd` on macOS (Virtualization
  framework) and the user-interface helpers (S12-platforms, NFR07-maintainability).
- ~~**Secrets live in one process, and not the most exposed one.**~~
  Superseded by S15-least-privilege on 2026-10-07. This was a terse
  early decision, and the detailed design showed that it gives one
  guest-exposed process, `wb-proxyd`, every secret. Now credentials are
  held by the configured secret store, and each process fetches or uses
  only its declared items, just in time. `wb-proxyd` gets only what
  proxying needs. Still true: `wb-netd` parses raw guest packets, the
  largest attack surface, and holds no secrets and opens no outbound
  connections. `wb-proxyd` sees only TCP byte streams already
  reassembled by `wb-netd` and validated against the DNS mapping.
  `wb-hostd` has no store access, and may pass on a descriptor with a
  secret in it without reading it (SEC04-no-guest-secrets,
  SEC12-least-privilege).
- **The guest admin password has a process of its own.**
  `wb-guestadmin` does the admin side of an image build, rotates
  `wbadmin`'s password at each VM's first contact, and removes the
  items of images and system disks that are gone, including the old
  item when a VM's system disk is replaced (S15-least-privilege). It may touch the `wbadmin/*`
  items in the secret store and nothing else. `wb-hostd` starts it
  through `wb-launcher` for one run, and it exits when that run ends.
  Its channels are pipes to the build's `wb-vmd` and `wb-build-ssh`,
  whose read ends `wb-launcher` hands over at spawn, and one host-guest
  connection to `wb-guestd` that `wb-hostd` has `wb-vmd` open for it.
  It only writes to that connection, so it parses nothing from the
  guest. Before it writes, it checks with `LOCAL_PEERPID` that the
  connection's peer is Virtualization's service process, and refuses
  any other descriptor. It doesn't talk to `wb-proxyd`. Its requests
  carry only item names in the fixed format of S15-least-privilege,
  which `wb-launcher` and `wb-guestadmin` both check. A compromised
  `wb-hostd` can still keep a read end of a build pipe and so read
  that build's password, because a pipe has no peer to check. That
  password is burned at the build VM's first contact, and the residual
  stays limited to builds. It runs apart from `wb-hostd` for least
  privilege: `wb-hostd` runs the host-guest
  socket handlers, and so never reads a credential.
- **The VM provider passes descriptors, not bytes.** `wb-vmd` opens
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
    `wb-vmd` at spawn (`SOCK_SEQPACKET` or `SOCK_DGRAM`, no path). Each
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
    guest bytes with a fuzz target (S11-verification-and-spikes).
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
- **The device set is fixed and checked.** A VM's devices are the ones
  in S06-vm-lifecycle, "Devices", fixed in `wb-vmd`'s code
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
    test of the check (S11-verification-and-spikes), an in-guest
    conformance check that the guest has one network interface with
    its lease from `wb-netd` (S11-verification-and-spikes), and a
    static check in CI that rejects at least
    `VZNATNetworkDeviceAttachment`, `VZBridgedNetworkDeviceAttachment`,
    `VZSharedDirectory`, `VZVirtioFileSystemDeviceConfiguration`,
    `VZHostAudioInputStreamSource`, `VZSpiceAgentPortAttachment` and
    `VZVirtioSoundDeviceInputStreamConfiguration` in `wb-vmd`'s sources outside its
    tests (a grep or a SwiftLint custom rule, not yet built).
- **Each host daemon is self-sandboxed.** Host processes confine
  themselves at startup with the platform's mechanism (S12-platforms) so that
  each gets only what it needs: `wb-netd` no filesystem and no network
  beyond inherited descriptors; `wb-proxyd` outbound network and its
  credential store items; `wb-guestadmin` its `wbadmin/*` store items
  and inherited descriptors; `wb-hostd` its state directories; `wb-vmd` the
  hypervisor and the VM bundles. This hardens our own code; it is not how
  the agent is contained (the VM is). How it is done (X23-sandboxed-daemons,
  SEC12-least-privilege):
  - A daemon confines itself first thing in `main`, from a profile
    compiled into it, and exits if that fails. Nothing before that reads
    an inherited descriptor, the arguments or the environment. Right
    after, it tries one operation its profile denies, and exits if that
    succeeds.
  - `wb-hostd` is one exception. Before it confines itself, it reads its
    own configuration, sets `GOMAXPROCS`, loads the local time zone,
    starts `wb-launcher` (on macOS), resolves the git binary from
    `<config>` or the fixed platform path, never from `PATH`, checks
    its version against the minimum (S08-workspace-and-git, "Host git")
    and that it honors `GIT_ALLOC_LIMIT` (S08-workspace-and-git, "Pack
    scanner"), and works out the paths for its profile. None of that reads
    anything the guest sent.
  - `wb-vmd` on macOS is a second, bounded exception. Before it confines
    itself, it calls `confstr(_CS_DARWIN_USER_CACHE_DIR)` once, because
    the framework needs the per-user cache directory and the lookup
    goes to a system service the profile denies. libSystem keeps the
    answer. It also looks up the user's home directory and resolves the
    bundle path with `realpath`, to get its profile parameter. It
    doesn't read anything from the guest or `wb-hostd` before it
    confines itself.
    The profile's parameters are the bundle path, from the fixed
    platform path (S12-platforms, "Paths") resolved to its real path,
    and the cache directory from `confstr`. The gRPC contract carries
    no path. The profile allows reading and writing the bundles, the
    Virtualization service, the `sysctl` values and system files the
    framework checks (the CPU name among them, without which saved
    states don't restore across profiles), and the sandbox extensions
    the framework hands to its service process, with the cache
    directory extension limited to its `com.apple.metal-` and
    `com.apple.paravirtualizedgraphics-` entries. It denies the user's
    other files, the network, starting programs, and other services
    (X25-vmd-sandbox). The extension classes were measured with the
    X18-vsock-handoff configuration, which also had a Mac graphics
    device with one display and a USB keyboard and pointer. They are
    ablated again against the final device set of S06-vm-lifecycle.
  - Open (X25-vmd-sandbox, decision 1): one `wb-vmd` serves both VMs
    and can read and write all of `<data>/vms`. A compromise of
    `wb-vmd` that starts in the isolated VM, through framework code
    that runs in `wb-vmd`'s process, then reaches the work VM's disks
    and saved state, which breaks the separation SEC08-proj-isolation
    puts the isolated VM there for. Recommended: one `wb-vmd` per VM,
    with two fixed `wb-launcher` entries (`wb-vmd-work`,
    `wb-vmd-isolated`), each with that VM's bundle as its profile
    parameter. Each instance learns its slot without arguments, from
    its program path (two installed names for one binary) or from a
    fixed environment variable in its launcher entry. Which of the two
    is part of this decision. If the maintainer keeps one `wb-vmd`, T00-index records
    the residual risk.
  - Open (X25-vmd-sandbox, decision 2): installing from a restore image
    needs three more rules (reading the image, a read extension for
    it, and the installation service). Recommended: a fixed
    `wb-launcher` entry, `wb-vmd-install`, whose compiled-in profile
    adds them, with the restore image at a fixed path under
    `<data>/images/`. The request doesn't choose the mode or a path.
  - `wb-guestadmin` confines itself at start the same way, from a
    compiled-in profile (X23-sandboxed-daemons, X25-vmd-sandbox). The
    profile allows the secret store service for its `wbadmin/*` items
    and its inherited descriptors, and denies files, the network, and
    starting programs. `wb-launcher` has one fixed entry per run kind
    (`wb-guestadmin-build`, `wb-guestadmin-rotate`,
    and `wb-guestadmin-cleanup`, which removes the items of images and
    system disks that are gone), so a request
    never chooses what it does.
  - `wb-netd`, `wb-vmd`, `wb-guestadmin` and `wb-build-ssh` write
    their logs to an inherited pipe or socket to `wb-hostd`, which
    frames, attributes and rate-limits each line. None of them holds a
    descriptor on any file in `<logs>` or `<data>` for its log.
  - On macOS a confined process can't confine itself again, and its
    children inherit its profile. So `wb-hostd` starts `wb-vmd`,
    `wb-netd`, `wb-proxyd`, `wb-git`, `wb-prover`, `wb-guestadmin` and
    `wb-build-ssh` through `wb-launcher`, which has no profile. On Linux, Landlock and seccomp restrictions stack, so a
    child can confine itself further and needs no launcher (assumed from
    their documentation, not tested; to be confirmed when Linux
    self-sandboxing is built).
    `wb-launcher` doesn't widen what a compromised `wb-hostd` can do
    only while all of these hold:
    - It is a separate, small program that uses only the Go standard
      library, not `wb-hostd` started in another mode.
    - Its request channel is a `SOCK_SEQPACKET` or `SOCK_DGRAM`
      socketpair inherited at start, never a path. Each message is one
      fixed-format request. A request with unexpected descriptors has
      them closed and is refused.
    - It starts each program with no arguments and a fixed environment.
      It passes only the descriptors `wb-hostd` hands it. The two
      bounded exceptions are `wb-git` and `wb-build-ssh`, below.
    - Each program's profile and its parameters are compiled into it or
      come from configuration `wb-hostd` read before it confined
      itself, never over the request channel.
    - Its program table holds absolute paths in the installed bundle,
      never under `<config>`, `<data>` or `<logs>`, and it checks that
      when it starts.
    - Each program has an instance cap (SEC13-bounded-resources).
  - git, which the git gateway runs on data the guest sends, starts
    through `wb-launcher` as `wb-git`, a shim that applies a profile for
    one repository and then runs git (decided in B34-sandboxed-daemons,
    not yet built). The launcher table
    has two fixed entries, `wb-git-receive` (`git receive-pack`) and
    `wb-git-upload` (`git upload-pack`), so the subcommand is never a
    free string. Besides its program, a `wb-git` request holds one
    field, the repository. `wb-launcher` takes the real path of the
    projects root (`<data>/projects`, from configuration `wb-hostd` read
    before it confined itself) once, when it starts. It accepts the
    repository only if it equals, after cleaning,
    `<real path of the projects root>/<id>/<basename>`, with `<id>` in
    the project ID format and `<basename>` either `landing.git` or
    `export.git` (I41). It never resolves the request string itself,
    which would follow a symbolic link an attacker placed. The shim
    repeats the same check before it calls `sandbox_init`. The
    repository becomes the shim's profile parameter and git's one path
    argument. This is a bounded exception to "no arguments". The
    Seatbelt match on the resolved path is the control that matters,
    and the path check is defense in depth (X23-sandboxed-daemons).
    Instead of a path, `wb-hostd` could hand over an inherited
    directory descriptor that the launcher checks with `F_GETPATH`,
    which keeps the path out of the request. That is the fallback if
    the path check turns out to be fragile.
  - `wb-build-ssh` is the second bounded exception. It is a fixed shim
    in the bundle that takes no request field. It confines itself, then
    runs the one program outside the bundle that the launcher allows,
    the system `ssh` at its fixed platform path, with arguments and
    environment compiled into the shim (S15-least-privilege, "The
    build's SSH session"). Its only connection is to the build guest's
    port 22, through `wb-netd` on the build VM's own network
    (S06-vm-lifecycle, "Images").
  - The measured, weaker fallback is git as a child of `wb-hostd`,
    under the `wb-hostd` profile. A git bug would then reach every
    project's policy, the approvals in `state.db`, other projects'
    landing repositories, and the audit log. The maintainer chose the
    `wb-git` shim on I34. Falling back would need a spec change and a
    T00-index entry for the residual risk.
  - Open: `wb-hostd` can't read the user's repository under its profile,
    which S08-workspace-and-git's `upload-pack` and export repository
    need (I67).
- **Everything in the guest is untrusted, including `wb-guestd`.** The
  host validates every message from the guest as adversarial input. The
  guest agent is a convenience for the host, not a security component.
- **Guest confinement narrows, never widens.** Controls inside the
  guest (Seatbelt profiles, a Network Extension that labels flows with
  the program that opened them) can only deny what the host would allow.
  Every `SEC*` control holds without them (S13-guest-confinement, T06-forged-labels).
- **Policy is evaluated on the host, twice.** `wb-netd` decides which
  names resolve and which connections are accepted. `wb-proxyd`
  re-checks the hostname against its own copy of the name mapping and
  the VM's effective policy, then the SNI and the HTTP request. A bug
  in one layer does not open egress on its own.
- **`wb-proxyd` takes a stream's identity from the connection, not from
  `wb-netd`** (SEC04-no-guest-secrets, SEC08-proj-isolation).
  `wb-hostd` creates a hand-off socket for each VM at each start and
  restore, and at the restarts of "Restarting a daemon". It passes one
  end to `wb-proxyd`, naming the VM, and the other to that
  VM's `wb-netd`. A stream's VM, and so its policy, credential bindings
  and CA, comes from the socket it arrived on. Its name comes from the
  name reports `wb-proxyd` checked and kept, and what `wb-netd` writes
  with a stream can only narrow. So a compromised `wb-netd` can't label
  a stream as another VM's, or as a project's that has no session in
  the VM (S07-egress-gateway, "Stream hand-off").
- **One work VM per guest OS, many projects; an isolated VM for the
  rest.** The work VM hosts every project placed there for its guest
  OS, new projects included, separated by guest user accounts. An
  isolated VM takes projects placed there and `--isolated` sessions
  (S06-vm-lifecycle, "VMs"). For macOS guests this fits Apple's two-VM limit (SEC08-proj-isolation, NFR05-two-macos-vms).
  The accounts keep the projects' files apart. The host can't tell
  their connections apart, so it enforces the policy and the
  credential bindings of a VM's projects for the whole VM
  (S07-egress-gateway, "Enforced per VM", T11-shared-vm-grants).
- **No root or administrator rights at run time.** The packet transport
  and the userspace stack replace host networking features that would
  need them (SEC12-least-privilege, NFR04-host-platforms).

## Supervision and failure

The service manager keeps `wb-hostd` running, and `wb-hostd` keeps the
other host processes running. A failure stops egress and never opens
it, and the work a guest can make the host do is bounded
(SEC10-audit, SEC11-root-gains-nothing, SEC13-bounded-resources,
NFR06-explained-refusals). The maintainer
decided the VM behavior on I48 (B48-process-supervision).

### Who starts whom

- The service manager (S12-platforms, "Service manager") runs
  `wb-hostd` and no other Wraith Box process. It starts `wb-hostd` at
  login and again whenever it exits. On macOS the LaunchAgent sets
  `KeepAlive` and doesn't set `AbandonProcessGroup`.
- `wb-hostd` supervises every other host process. It decides when
  `wb-vmd`, each VM's `wb-netd` and `wb-proxyd` start, and whether one
  starts again after it exits. On macOS it starts them through
  `wb-launcher` ("Each host daemon is self-sandboxed"). Elsewhere it
  starts them itself.
- `wb-launcher` is the parent of the programs it starts, so only it
  sees their wait status. It reports each exit to `wb-hostd` on its
  request channel, in one fixed-format message: the program table
  entry, the instance ID it returned when it started the process, and
  the exit code or the signal. It never restarts anything itself.
- The programs started for one run (`wb-git`, `wb-prover`,
  `wb-guestadmin`, `wb-build-ssh`) are never restarted. A run whose
  program exits before it reports a result fails, and the caller gets
  the exit reason (S08-workspace-and-git, S09-policy-credentials-audit,
  S15-least-privilege).

### When `wb-hostd` stops

VMs stop with `wb-hostd`, for V1 (decided on I48). `wb-launcher`,
`wb-vmd`, `wb-netd` and `wb-proxyd` each inherit a control channel to
`wb-hostd`, a socket and never a path. End of file or an error on that
channel means `wb-hostd` is gone, and each of them acts on its own:

- `wb-vmd` stops every VM it runs at once and exits. It doesn't ask
  the guest to shut down, and it doesn't save the VM. The sessions in those
  VMs are lost, and their worktrees stay on the data disk until
  recovery at the next connection to the VM (S06-vm-lifecycle,
  "Session lifecycle"). Writes the guest flushed are on the disk
  (S06-vm-lifecycle, "Disks").
- `wb-netd` and `wb-proxyd` close every stream and exit. The guest's
  only network path is gone, so egress stops (SEC05-default-deny).
- `wb-launcher` sends `SIGTERM` to every program it started that still
  runs, sends `SIGKILL` to any left after 5 seconds, waits for them,
  and exits. So no old `wb-launcher`, `wb-netd` or `wb-proxyd` runs
  next to the ones a new `wb-hostd` starts, and every per-run program
  ends with the `wb-hostd` that asked for it.

The per-run programs don't hold a control channel and can't learn that
`wb-hostd` has gone. `wb-launcher` ends them. On hosts without
`wb-launcher`, the service manager ends them with `wb-hostd`, which is
to be confirmed when those hosts are built (S12-platforms, I176).

On macOS, launchd also kills the processes left in `wb-hostd`'s
process group when `wb-hostd` exits (`man launchd.plist`,
`AbandonProcessGroup`). That is a second layer. The control channel is
the rule on every host.

A new `wb-hostd` takes over nothing. It starts a new `wb-launcher` and
new daemons, marks the sessions it has as starting, running, paused or
ending as lost (S06-vm-lifecycle, "Lost"), and starts a VM again only
when a session or warm start asks for one (S06-vm-lifecycle, "Warm
start"). A start that fails because the old VM is still stopping is a
temporary failure, which `wb-hostd` retries a bounded number of times,
as for a restore.

### Upgrades

An upgrade takes the same path as a crash. The installer replaces the
bundle (I49), the running `wb-hostd` exits, the service manager starts
the new one, and the running VMs stop with their sessions lost.

`wb-hostd` notices a replaced bundle itself and restarts. It reads the installed bundle's
build version (on macOS from the app bundle's `Info.plist`, elsewhere
from a version file next to the programs, I49) when a host peer
reports a build version other than its own ("Version skew"), and
before it starts a daemon. When the installed version isn't its own,
it logs the rule `bundle-replaced` with both versions and exits, and
the service manager starts the new `wb-hostd`. A daemon refused this
way doesn't count as an exit toward the restart limit. When the
installed version is its own and the peer's isn't, the peer is
refused, and a daemon refused this way counts as a failed start. When
it can't read the installed version, it logs that with the rule
`bundle-version-unreadable`, treats the installed version as its own,
doesn't exit, and `wb status` shows it. So a damaged bundle can't make
`wb-hostd` exit in a loop.

Rejected for V1:

- `wb-vmd` saves each VM and exits when it loses `wb-hostd`, and the
  new `wb-hostd` restores them, so sessions pause instead of ending
  (B48-process-supervision, option A). It depends on save and restore
  of a VM with its vsock device and network attachment
  (X02-warm-start), and needs a bounded save on every exit path. A
  later version may move to it. The guest version rule below then
  becomes a range.
- `wb-vmd` keeps its VMs running without `wb-hostd`, and a new
  `wb-hostd` takes them over (option B). That needs a takeover
  protocol and a `wb-vmd` outside `wb-hostd`'s process group. An
  upgrade replaces `wb-vmd` as well, so it would stop the VMs anyway.

### Restarting a daemon

`wb-hostd` restarts `wb-proxyd`, and each VM's `wb-netd`, after an exit
it didn't ask for:

- It waits 0.5 seconds before the first restart and doubles the wait
  after each one, up to 30 seconds. After the fifth exit in 10
  minutes it stops restarting that process and logs that it gave up,
  with the last exit reason. So a crash a guest can cause doesn't
  become a stream of fresh processes. A `wb-netd` it gave up on leaves
  its VM without network until the VM stops, and the VM's next start
  gets a new one. A `wb-proxyd` it gave up on is tried once more at the
  next session start in any VM.
- Once it gives up, every session start that needs the process is
  refused with the rule `daemon-given-up`. The refusal names the
  process, the VM, its last exit reason, and what starts it again:
  stopping the VM for `wb-netd`, the next session start for `wb-proxyd`
  (NFR06-explained-refusals).
- The guest sees resets and refusals while a daemon is down, never a
  path around it. Nothing else on the host forwards guest traffic.
- A new `wb-netd` gets the same start as the first one: its VM's
  packet transport descriptor, the VM's MAC address and leased
  address, the effective policy, the VM's synthetic name mapping and
  its counts (below), and a new hand-off socket to `wb-proxyd`. The
  guest keeps its lease, and its cached DNS answers still work.
- `wb-hostd` keeps its copy of each VM's packet transport descriptor
  only to hand it to a new `wb-netd`. It holds the copy in a type that
  has no read or write path, and closes it when the VM stops and when a
  restore hands over a new descriptor for a new VM generation.
- A restart doesn't reset what bounds the guest. `wb-netd` checks and
  debits the VM's wildcard budget (S07-egress-gateway, "Packet path",
  DNS). `wb-hostd` only keeps the count across restarts. It also keeps
  every other count S07-egress-gateway has `wb-netd` or
  `wb-proxyd` keep for the active period. `wb-hostd` keeps the
  authoritative count of pending approval requests and the rate of new
  ones, and a new `wb-netd` gets both at start, with the policy
  (S09-policy-credentials-audit, "Approval flow").
- `wb-hostd` keeps each VM's synthetic name mapping too: every live
  entry, with its name, its address, and the time `wb-hostd` received
  its last record. It keeps an entry until the entry's release record
  arrives, and has no clock of its own for it. `wb-netd` decides when
  to release an address (S07-egress-gateway, "Synthetic pool"). It
  learns the mapping the way it learns the wildcard debits, from
  accounting records ("Accounting and event channels"). `wb-netd`
  sends each new or renewed entry before the DNS answer that uses it,
  and refuses the lookup when the write fails. It sends each release
  before it gives the address to another name. A new `wb-netd` starts
  with the kept entries and gives none of their addresses to another
  name until it has released that address. Without them, an address the guest still has cached
  for one name could go to another, and a raw or pass-mode stream
  meant for the first would reach the second.
- A new `wb-proxyd` has no streams. It fetches binding secrets again
  when proxying needs them (S15-least-privilege).
- When a VM's `wb-netd` restarts within a VM generation, `wb-hostd`
  creates a new hand-off socket between it and `wb-proxyd`, for the
  same VM and generation, and closes the old one. `wb-proxyd` serves
  every VM, so when it restarts, `wb-hostd` creates a new hand-off
  socket for every running VM, each for that VM and its current
  generation. A new `wb-netd` gets its end at start. A running
  `wb-netd` gets its end on its control channel, in a message with
  exactly one descriptor and the VM and generation it is for. It
  refuses the message, closes any descriptor in it, and logs the
  refusal when the descriptor count isn't one, the socket type is
  wrong, or the VM or generation isn't its own, as for the hand-off
  channel of `wb-vmd` ("The VM provider passes descriptors, not
  bytes"). On each new socket `wb-netd` first replays the VM's live
  mapping, so that `wb-proxyd` has it again (S07-egress-gateway). The
  replay is the same set either way: the entries `wb-hostd` kept, which
  a new `wb-netd` got at start and a running one sent `wb-hostd` entry
  by entry, with every release.
- `wb-vmd` isn't restarted in a loop. When it exits, its VMs have
  stopped with it, and their sessions are lost. `wb-hostd` starts a
  new `wb-vmd` when a VM is next needed, with the same backoff and the
  same limit.

`wb-hostd` shows a native notification once per outage of a VM's
`wb-netd` or `wb-proxyd`. It says which process is down, for which VM,
and why, how many secret store accesses the restarts caused
(S15-least-privilege), and asks for nothing (SEC14-no-fake-approvals).
`wb` in non-interactive use prints the same as one `wraith box:` line
on its standard error. In interactive use `wb` doesn't write into the
agent's terminal, and the session's summary says that its network was
down and for how long (S05-cli, "Session behavior"). `wb status` shows
the outage while it lasts.

One `wb-proxyd` serves both VMs. A crash that a guest finds in it takes
the network of the other VM down too, until the restart, and so do its
given-up state, a blocked event channel (`event-channel-blocked`), and
the secret store prompts its restarts cause. That is a residual risk to
availability, not to egress (T14-shared-proxyd).

### Accounting and event channels

`wb-netd` and `wb-proxyd` send `wb-hostd` two kinds of record on
inherited sockets of type `SOCK_SEQPACKET` or `SOCK_DGRAM`
(`SOCK_DGRAM` on macOS, I172), one record per message. A record arrives
whole or not at all. Each record is at most 4 KiB, in a fixed format.
`wb-hostd` parses them with a parser that has a fuzz target
(S11-verification-and-spikes), and treats a malformed or oversized
record, and one received with `MSG_TRUNC`, as a crash of the process
that sent it: it ends that process and counts the exit toward the
restart limit.

- **Accounting records** carry the counts and the name mapping of
  "Restarting a daemon": each wildcard budget debit, each new or
  renewed mapping entry with its name and address, and each release.
  They aren't audit events. Nothing rate-limits, suppresses or drops
  them, and `wb-netd` writes one before any drop or suppression
  decision about the event that goes with it, and before it sends the
  DNS answer. A write that fails, or that blocks for more than 100 ms,
  counts as failed, and the lookup is refused (S07-egress-gateway,
  "Packet path", DNS). `wb-hostd` reads what is left in the socket
  after `wb-netd` exits before it starts a new one.
- **Mapping entries are checked.** `wb-netd` derives the names from
  guest queries, so `wb-hostd` checks each entry before it keeps it:
  the name in canonical form (lowercase, no trailing dot, each label 1
  to 63 bytes of letters, digits and hyphens with no dot byte inside
  it, and at most 253 bytes in all, the name form of
  S07-egress-gateway), the address
  inside 198.18.0.0/15, and at most 131,072 live entries per VM. An
  entry that fails a check is logged with the rule
  `mapping-entry-invalid`, rate-limited, and ends the `wb-netd` that
  sent it, as a malformed record does. So `wb-netd` never runs on with
  a mapping that differs from the one `wb-hostd` keeps. `wb-hostd`
  stamps each entry with its own receipt time and never uses a time
  from `wb-netd`.
- **Event records** carry what goes to the audit log
  (S09-policy-credentials-audit, "Audit"). When an event write blocks
  for more than 100 ms, or fails without blocking (`ENOBUFS` or
  `EAGAIN` on a `SOCK_DGRAM` socket), `wb-netd` refuses new lookups and
  connections, and `wb-proxyd` new streams and requests, with the rule
  `event-channel-blocked`. It keeps the record and retries it until a
  write succeeds, so no event is lost. So a slow or hung
  `wb-hostd` makes the VM's network refuse, not run without records.

### Version skew

Every host program comes from one bundle with one build version, which
every build task sets in the Go and the Swift code alike, the dev
build and CI included (S10-tech-stack, "Packaging and signing"). The
version includes the VCS revision, and marks a modified working tree.
Only a bare toolchain build (`go build`, `go run`, `swift build`) is
unstamped. It has no version, and every peer counts it as another
version. Two unstamped builds refuse each other. `wb` is linked
from that bundle as well.

- **Between host processes.** The first message on every host
  connection, gRPC or not, gives the sender's build version. A peer of
  another build version is refused, the connection closed, and both
  versions logged (and the installed bundle checked, "Upgrades"). So
  there are no compatibility rules between host processes. When
  `wb-hostd` refuses `wb`, `wb` prints a `wraith box:` line that names
  both versions and exits with 255. When `wb-hostd` exits because the
  bundle was replaced, the line says that Wraith Box is restarting
  after an upgrade and to run the command again.
- **`.proto` packages.** Each package name ends in its major version
  (`wraithbox.<area>.v1`). A change that an older peer can't read is a
  new major package next to the old one, never an edit of the old one
  (I52).
- **With `wb-guestd`.** After the host opens a connection, the first
  message from `wb-guestd` gives its build version and the major
  version of each package it serves. In V1 `wb-hostd` accepts only its
  own build version. The running bundle started each running VM,
  because VMs stop with `wb-hostd`, and `wb-guestd` updates itself at
  start from that bundle's guest tools disk (S06-vm-lifecycle,
  "`wb-guestd`"). A `wb-guestd` that reports another version is
  refused before any session request. `wb-hostd` logs both versions,
  and a session start in that VM is refused with a message that names
  both and says to stop and start the VM, and to rebuild the image with
  `wb image build` if that doesn't help (NFR06-explained-refusals).
- **The guest's version is a label.** `wb-guestd` runs as root in the
  guest, which T00-index takes as the adversary. The version string is
  checked against a fixed form first: printable ASCII, at most 64
  bytes, `MAJOR.MINOR.PATCH` with an optional `-` prerelease and `+`
  build part of letters, digits, dots and hyphens. Any other string is
  refused with the rule `guest-version-form`, and wherever it is shown,
  in a log record or in `wb`'s message, it is escaped and cut to 64
  bytes. The host uses the reported version only to refuse, never to
  allow something or to pick how a message is parsed. Each message gets
  the same checks whatever the version (SEC11-root-gains-nothing).

### Host work the guest can cause

SEC13-bounded-resources caps each VM, and also the host work each VM
can cause. Every host handler that takes input from a guest has a per-VM
rate limit, a size limit on each input, and a bound on the work it runs
at once. A request over a limit is refused or dropped, never queued
without bound, and logged with the limit's rule. For the refusal
classes S07-egress-gateway aggregates ("Events and rate limits"), a
run of refusals is logged as one record with its count. A new handler
gets its limits before it merges.

| Guest input | Process | Limits | Where |
|---|---|---|---|
| Host-guest socket connections | `wb-vmd`, `wb-hostd` | connections opened and not yet passed, connections per VM and per project, reconnects per minute | "The VM provider passes descriptors, not bytes", S06-vm-lifecycle, "Reconnect" |
| RPCs from `wb-guestd`, with the guest requests multiplexed on them | `wb-hostd` | message size and streams per connection, set on the server and never left at the library default, requests per second per VM, entries in the reconnect session list | I52 for the values, S06-vm-lifecycle, "Reconnect" |
| The version `wb-guestd` reports | `wb-hostd` | 64 bytes, fixed form | "Version skew" |
| Ethernet frames, DHCP and DNS | `wb-netd` | per-VM caps, query rate, name length, wildcard budget, synthetic pool | S07-egress-gateway, "Packet path" |
| Held DNS queries | `wb-netd` | 1,024 held queries per VM by default (configuration), each sent after 4 seconds, over the cap dropped with `dns-hold-full` | S07-egress-gateway, "Packet path", DNS, "Hold limits" |
| Connections and streams | `wb-netd`, `wb-proxyd` | connections in flight, stream resets and refusals per rule | S07-egress-gateway |
| Dependency gate clock lookups to `sum.golang.org` | `wb-proxyd` | 8 lookups in flight and 6,000 a minute per VM, 16 in flight per `wb-proxyd`, a queue of 5,000 per VM, 5,000 versions per `@v/list`, 100,000 cached records, calibration on its own timer only, over a cap refused with `go-clock-lookup-rate` or `go-list-too-long` | S07-egress-gateway, "Dependency gate", "Publish time" |
| Upstream name resolution | `wb-proxyd` | one lookup per stream, so bounded by the connections in flight | S07-egress-gateway, "Stream path" |
| Leaf certificates | `wb-proxyd` | a leaf only for a name the VM resolved, cached per VM, CA and name, so at most the allowlisted names plus the wildcard budget | S09-policy-credentials-audit, "TLS inspection certificate authority" |
| Pushes and fetches | `wb-hostd`, `wb-git` | the pack scanner's caps, git's memory limit, deadlines, one push per project at a time | S08-workspace-and-git, "Pack scanner", "Bounds" |
| Recovery pushes | `wb-hostd`, `wb-git` | one per lost session, to its own branch only, under the same limits as other pushes | S06-vm-lifecycle, "Recovery" |
| Approval requests | `wb-netd`, `wb-hostd`, `wb-prover` | 16 pending requests and 10 new requests a minute per VM, one open request per name, prover runs at once | S09-policy-credentials-audit, "Approval flow", "Approvals" |
| Native notifications | `wb-hostd` and the notification helper | one per pending approval request, one per daemon outage | S09-policy-credentials-audit, "Approvals", "Restarting a daemon" |
| Audit events | `wb-hostd` | a budget per class and per VM, refusals of new work instead of lost records, a byte budget per VM per day | S09-policy-credentials-audit, "Audit" |
| Accounting and event records | `wb-hostd` | 4 KiB per record, fixed format, a malformed record ends the sender | "Accounting and event channels" |
| Daemon log lines | `wb-hostd` | 100 lines per second per process, 4 KiB per line | "Logs and health" |

### Logs and health

- `wb-hostd` writes its own log and the lines the other daemons send it
  to `<logs>`, one file per process, apart from the audit log. It
  writes at most 100 lines per second per process, cuts a line at 4 KiB
  with a truncation marker, and writes one record with the count of
  lines it dropped over the rate. Each file is rotated at 10 MiB, and
  the last 5 are kept. A daemon log isn't the record of a decision. The
  audit log is (S09-policy-credentials-audit, "Audit").
- For each host process `wb-hostd` keeps its state (running, waiting
  to restart, given up, stopped), its version, when it started, its
  restarts in the last 10 minutes, and its last exit reason (the exit
  code or signal). `wb status` shows them, and for each VM without
  network, which process is down and why (NFR06-explained-refusals).
- When `wb` can't reach `wb-hostd`, `wb status` says so and says to
  run `wb setup`, which installs and starts the service
  (S05-cli, "Management commands").

## Host state

Locations follow each OS's conventions (paths in S12-platforms); the logical
layout is the same everywhere:

```
<config>/                             user-editable configuration (S09-policy-credentials-audit)
  config.toml                         global settings and defaults
  policy.yaml                         global network policy (S09-policy-credentials-audit)
  projects/<project-id>.toml          per-project settings, toolchain manifest reference
  projects/<project-id>.policy.yaml   per-project network policy
<data>/
  images/                             base images (copy-on-write clones)
  vms/<guest-os>-{work,isolated}/     VM bundles: disks, machine identity, saved state
  projects/<project-id>/export.git    bare repo the guest fetches from: selected refs only (S08-workspace-and-git)
  projects/<project-id>/landing.git   bare repo receiving session branches (S08-workspace-and-git)
  state.db                            SQLite: projects, sessions, approvals, caches
  run/                                user-only directory for local IPC endpoints
<logs>/                               audit JSONL and daemon logs
```

## Inter-process contracts

All IPC is protobuf over gRPC. On the host it runs over a local IPC
endpoint only the user can open: Unix sockets in a `0700` directory on
macOS and Linux, named pipes with an ACL for the user's SID on Windows.
The peer's identity is checked on every connection. To the guest it runs
over the host-guest socket. The `.proto` files are the single contract
for every language (S10-tech-stack).

**Status:** Draft
