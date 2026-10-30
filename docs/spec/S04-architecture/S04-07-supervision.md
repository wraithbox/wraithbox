# S04-07 - Supervision

**Purpose:** Who starts and restarts each host process, what happens when
`wb-hostd` stops or the bundle is upgraded, and daemon outages.

## Supervision and failure

The service manager keeps `wb-hostd` running, and `wb-hostd` keeps the
other host processes running. A failure stops egress and never opens
it. The work a guest can make the host do is bounded (SEC10-audit,
SEC11-root-gains-nothing, SEC13-bounded-resources,
NFR06-explained-refusals).

### Who starts whom

- **The service manager** (S12-01-platform-interfaces, "Service manager") runs
  `wb-hostd` and no other Wraith Box process. It starts `wb-hostd` at
  login, and again whenever it exits. On macOS the LaunchAgent sets
  `KeepAlive` and doesn't set `AbandonProcessGroup`.
- **`wb-hostd`** supervises every other host process. It decides when
  `wb-proxyd` and each VM's `wb-vmd` and `wb-netd` start, and whether
  one starts again after it exits. On macOS it starts them through
  `wb-launcher` (S04-03-self-sandboxing, "Each host daemon is self-sandboxed"). Elsewhere it starts them itself.
- **`wb-launcher`** is the parent of the programs it starts, so only it
  sees their wait status. It reports each exit to `wb-hostd` on its
  request channel, in one fixed-format message. The message holds the
  program table entry, the instance ID it returned when it started the
  process, and the exit code or the signal. An instance ID is never
  reused while that `wb-launcher` runs. It never restarts anything
  itself. It also sends the kill of a `wb-diskfmt` it started, with its
  process group, when `wb-hostd` asks, and only before it reaps the
  program (S04-04-vm-processes, "`wb-diskfmt`'s kill").
- **The programs started for one run** are never restarted: `wb-git`,
  `wb-prover`, `wb-guestadmin`, `wb-build-ssh`, `wb-diskfmt`,
  `wb-release-check` and `wb-download`. `wb-release-check` has an
  instance cap of one per build. A run whose program exits before it
  reports a result fails, and the caller gets the exit reason
  (S08-workspace-and-git, S09-policy-credentials-audit,
  S15-least-privilege).

### When `wb-hostd` stops

VMs stop with `wb-hostd`, for V1 (see "Decisions").

`wb-launcher`, `wb-vmd`, `wb-netd` and `wb-proxyd` each inherit a
control channel to `wb-hostd`: a socket, never a path. End of file or
an error on that channel means `wb-hostd` is gone. Each of them then
acts on its own:

- **Each `wb-vmd`** stops its VM at once and exits. It doesn't ask the
  guest to shut down, and it doesn't save the VM. The sessions in that
  VM are lost. Their worktrees stay on the data disk until recovery at
  the next connection to the VM (S06-09-session-lifecycle). Writes the
  guest flushed are on the disk (S06-05-disks).
- **`wb-netd` and `wb-proxyd`** close every stream and exit. The
  guest's only network path is gone, so egress stops
  (SEC05-default-deny).
- **`wb-launcher`** sends `SIGTERM` to every program it started that
  still runs. It sends `SIGKILL` to any left after 5 seconds, waits for
  them, and exits. So no old `wb-launcher`, `wb-netd` or `wb-proxyd`
  runs next to the ones a new `wb-hostd` starts, and each per-run
  program ends with the `wb-hostd` that asked for it. A `wb-diskfmt`
  has nothing to clean up. It gets `SIGKILL` at once, sent to its
  process group and to the shim before the reap, which stops each
  `diskutil` it ran as well.

The per-run programs don't hold a control channel, and can't learn
that `wb-hostd` has gone. `wb-launcher` ends them. On hosts without
`wb-launcher`, the service manager ends them with `wb-hostd`. That is
to be confirmed when those hosts are built (S12-platforms, I176).

On macOS, launchd also kills the processes left in `wb-hostd`'s process
group when `wb-hostd` exits (`man launchd.plist`,
`AbandonProcessGroup`). That is a second layer. The control channel is
the rule on every host.

### A new `wb-hostd`

A new `wb-hostd` takes over nothing. It:

1. starts a new `wb-launcher` and new daemons;
2. marks the sessions it has as starting, running, paused or ending as
   lost (S06-11-reconnect-and-recovery, "Lost");
3. starts a VM again only when a session or warm start asks for one
   (S06-06-devices-and-warm-start, "Warm start").

A start that fails because the old VM is still stopping is a temporary
failure. `wb-hostd` retries it a bounded number of times, as for a
restore.

### Upgrades

An upgrade takes the same path as a crash:

1. the installer replaces the bundle (S17-05-upgrades-and-migrations, "Upgrades");
2. the running `wb-hostd` exits;
3. the service manager starts the new one;
4. the running VMs stop, with their sessions lost.

`wb-hostd` notices a replaced bundle itself and restarts. It reads the
installed bundle's build version (S17-install-and-upgrade):

- on macOS from the `WBBuildVersion` key of the app bundle's
  `Info.plist`;
- elsewhere from the file `wraithbox-version` next to the programs.

It reads it when a host peer reports a build version other than its own
(S04-08-records-and-bounds, "Version skew"), and before it starts a
daemon. Then:

- **The installed version isn't its own.** It logs the rule
  `bundle-replaced` with both versions and exits. The service manager
  starts the new `wb-hostd`. A daemon refused this way doesn't count as
  an exit toward the restart limit.
- **The installed version is its own, and the peer's isn't.** The peer
  is refused. A daemon refused this way counts as a failed start.
- **It can't read the installed version.** It logs that with the rule
  `bundle-version-unreadable`, treats the installed version as its own,
  and doesn't exit. `wb status` shows it. So a damaged bundle can't
  make `wb-hostd` exit in a loop.

### Restarting a daemon

`wb-hostd` restarts `wb-proxyd`, and each VM's `wb-netd`, after an exit
it didn't ask for.

- **Backoff.** It waits 0.5 seconds before the first restart, and
  doubles the wait after each one, up to 30 seconds.
- **Giving up.** After the fifth exit in 10 minutes it stops restarting
  that process. It logs that it gave up, with the last exit reason. So
  a crash a guest can cause doesn't become a stream of fresh
  processes.
- **After giving up.** A `wb-netd` it gave up on leaves its VM without
  network until the VM stops, and the VM's next start gets a new one. A
  `wb-proxyd` it gave up on is tried once more at the next session
  start in any VM.
- **Refusals.** Once it gives up, every session start that needs the
  process is refused with the rule `daemon-given-up`. The refusal names
  the process, the VM, its last exit reason, and what starts it again:
  stopping the VM for `wb-netd`, the next session start for
  `wb-proxyd` (NFR06-explained-refusals).
- **No path around.** The guest sees resets and refusals while a daemon
  is down, never a path around it. Nothing else on the host forwards
  guest traffic.

### A new `wb-netd`

A new `wb-netd` gets the same start as the first one:

- its VM's packet transport descriptor;
- the VM's MAC address and leased address;
- the effective policy;
- the VM's synthetic name mapping and its counts ("What a restart
  keeps", below);
- a new hand-off socket to `wb-proxyd`, except for the build VM, whose
  `wb-netd` has none.

The guest keeps its lease, and its cached DNS answers still work.

`wb-hostd` never restarts the build VM's `wb-netd`. When it exits, the
image build fails with its exit reason (S07-18-build-network).

`wb-hostd` keeps its copy of each VM's packet transport descriptor only
to hand it to a new `wb-netd`. It holds the copy in a type that has no
read or write path. It closes it when the VM stops, and when a restore
hands over a new descriptor for a new VM generation.

### What a restart keeps

A restart doesn't reset what bounds the guest.

- `wb-netd` checks and debits the VM's wildcard budget
  (S07-03-dns, "Wildcard budget"). `wb-hostd` only keeps the count
  across restarts.
- `wb-hostd` also keeps every other count S07-egress-gateway has
  `wb-netd` or `wb-proxyd` keep for the active period.
- `wb-hostd` keeps the authoritative count of pending approval requests
  and the rate of new ones. A new `wb-netd` gets both at start, with
  the policy (S09-10-approval-flow).

### The kept name mapping

`wb-hostd` keeps each VM's synthetic name mapping too: every live
entry, with its name, its address, and the time `wb-hostd` received its
last record.

- It keeps an entry until the entry's release record arrives. It has
  no clock of its own for it. `wb-netd` decides when to release an
  address (S07-03-dns, "Synthetic pool").
- It learns the mapping the way it learns the wildcard debits, from
  accounting records (S04-08-records-and-bounds, "Accounting and event channels").
- `wb-netd` sends each new or renewed entry before the DNS answer that
  uses it. It refuses the lookup when the write fails.
- `wb-netd` sends each release before it gives the address to another
  name.
- A new `wb-netd` starts with the kept entries. It gives none of their
  addresses to another name until it has released that address.

Without them, an address the guest still has cached for one name could
go to another. A raw or pass-mode stream meant for the first would then
reach the second.

### A new `wb-proxyd`

A new `wb-proxyd` has no streams. It fetches binding secrets again when
proxying needs them (S15-least-privilege).

`wb-hostd` hands it each running VM's placeholder set on its control
channel, before it creates that VM's new hand-off socket. So the
placeholder scan has every placeholder before the first stream arrives
(S09-05-credentials, "Model credential"). At each restore of a
suspended VM, `wb-hostd` hands back the VM's set the same way, before
it creates the VM's hand-off socket.

### New hand-off sockets

- When a VM's `wb-netd` restarts within a VM generation, `wb-hostd`
  creates a new hand-off socket between it and `wb-proxyd`, for the
  same VM and generation. It closes the old one.
- `wb-proxyd` serves every VM. So when it restarts, `wb-hostd` creates
  a new hand-off socket for every running VM, each for that VM and its
  current generation.

A new `wb-netd` gets its end at start. A running `wb-netd` gets its end
on its control channel, in a message with exactly one descriptor and
the VM and generation it is for. It refuses the message when the
descriptor count isn't one, the socket type is wrong, or the VM or
generation isn't its own. It then closes any descriptor in it and logs
the refusal, as for the hand-off channel of `wb-vmd`
(S04-02-vm-provider, "The VM provider passes descriptors, not bytes").

On each new socket, `wb-netd` first replays the VM's live mapping, so
that `wb-proxyd` has it again (S07-egress-gateway). The replay is the
same set either way. It is the entries `wb-hostd` kept, which a new
`wb-netd` got at start, and a running one sent `wb-hostd` entry by
entry, with every release.

### `wb-vmd` and `wb-vmd-install`

- A VM's `wb-vmd` isn't restarted in a loop. When it exits, its VM has
  stopped with it, and the VM's sessions are lost. `wb-hostd` starts a
  new `wb-vmd` for that VM when the VM is next needed, with the same
  backoff and the same limit.
- `wb-vmd-install` is never restarted. When it exits during a build,
  the build fails and `wb-hostd` deletes the build bundle
  (S15-04-guest-admin, "The build's SSH session"). Nothing starts
  again until the user runs the next build.

### Outages

`wb-hostd` shows a native notification once per outage of a VM's
`wb-netd` or `wb-proxyd`. It asks for nothing (SEC14-no-fake-approvals).
It says:

- which process is down, for which VM, and why;
- how many secret store accesses the restarts caused
  (S15-least-privilege).

Only the 1Password backend, after V1 (S15-02-secret-store), can
prompt for those accesses. The Keychain doesn't show a dialog in any
build (S15-03-access-rules, access rule 4).

- `wb` in non-interactive use prints the same as one `wraith box:` line
  on its standard error.
- In interactive use, `wb` doesn't write into the agent's terminal. The
  session's summary says that its network was down, and for how long
  (S05-01-agent-commands, "Session behavior").
- `wb status` shows the outage while it lasts.

### One `wb-proxyd` for both VMs

One `wb-proxyd` serves both VMs. These take the network of the other VM
down too:

- a crash that a guest finds in it, until the restart;
- its given-up state;
- a blocked event channel (`event-channel-blocked`);
- the 1Password prompts its restarts cause, after V1.

That is a residual risk to availability, not to egress
(T14-shared-proxyd).

## Decisions

### VMs stop with `wb-hostd`, for V1

- **Rejected:** each `wb-vmd` saves its VM and exits when it loses
  `wb-hostd`, and the new `wb-hostd` restores them, so sessions pause
  instead of ending (B48-process-supervision, option A). It depends on
  save and restore of a VM with its vsock device and network attachment
  (X02-warm-start). It needs a bounded save on every exit path. A later
  version may move to it. The guest version rule
  (S04-08-records-and-bounds, "Version skew") then becomes a range.
- **Rejected:** each `wb-vmd` keeps its VM running without `wb-hostd`,
  and a new `wb-hostd` takes them over (option B). That needs a
  takeover protocol, and a `wb-vmd` outside `wb-hostd`'s process group.
  An upgrade replaces `wb-vmd` as well, so it would stop the VMs anyway.
- **Decided:** I48, B48-process-supervision.

**Status:** Draft
