# S15-05 - The Admin Path

**Purpose:** How the person at the host's terminal gets a root shell
in their own guest, on a path that no agent, session, or guest process
can start or reach.

**Requirements:** SEC08-proj-isolation, SEC10-audit,
SEC11-root-gains-nothing, SEC14-no-fake-approvals

**Tests:** C09-guest-and-sessions

Brief: B419-admin-path

![The admin path next to the agent paths. wb vm admin calls a service of its own in wb-hostd, which marks the system disk and connects through wb-vmd to the admin port, where wb-guestroot runs a root shell. wb claude and wb shell reach wb-guestd on port 1023. wb-guestd never calls wb-guestroot, and a project user can neither bind nor open its port.](S15-05-admin-path.svg)

## Design

### What it is for

The admin path gives the person at the host's terminal a root shell
in a running VM. It serves:

- root-only checks in a real guest, such as the data mount cases of
  C09-guest-and-sessions;
- a look inside a VM whose `wb-guestd` is dead or wedged;
- a developer's own changes to their own VM.

It runs as the user, as every Wraith Box process on the host does
(SEC12-least-privilege). It uses no secret store item and no `wbadmin`
password (S15-04-guest-admin).

### Who can start it

Only `wb vm admin <vm>` starts it. `<vm>` is `work` or `isolated`.

- It is refused without a terminal on standard input and standard
  output, with the rule `admin-no-terminal`.
- It prints the VM, and says that the VM then takes no project session
  until its next cold start replaces its system disk ("Marked" below).
- The person types the VM's name to go on. Any other answer ends it
  with `admin-not-confirmed`. No flag skips the question.
- It asks in its own terminal, never in a session's stream
  (SEC14-no-fake-approvals: no approval in the agent's terminal
  stream). It is never part of a guest RPC.

Agents run in the guest, which can't reach `wb` or `wb-hostd`'s local
endpoint (S04-architecture). The terminal and the typed name make the
start a person's act, as for `wb vm reset-data`
(S05-03-management-commands).

`wb-hostd` then refuses, each with its rule:

- `vm-not-running` unless the VM runs;
- `admin-sessions` while the VM has a session that is starting,
  running, paused or ending, a debug shell included. The refusal names
  each one;
- `admin-before-rotation` until first contact has rotated `wbadmin`'s
  password, or marked the disk "rotation unknown" (S15-04-guest-admin,
  "Rotation failure"). Rotation relies on nothing else running
  ("What first contact relies on" there).

### Apart from the agent paths

The admin path shares no RPC service, guest process, guest port or
host handler with sessions and `wb shell`.

- In the guest, `wb-guestroot` is a root LaunchDaemon of its own,
  apart from `wb-guestd`. It listens on its own vsock port below 1024,
  which only guest root can bind (X27-vsock-confinement). It closes a
  connection from any CID but the host's.
- It serves one service of its own `.proto` package, which runs
  `/bin/zsh -l` as root on a PTY. `wb-guestd` neither serves nor calls
  it.
- On the host, `wb vm admin` calls a service of its own on
  `wb-hostd`'s endpoint, not `ShellService` or `HostService`.
- `wb-hostd` asks `wb-vmd` for the admin port only for that call. It
  routes the port's connections only to that service's handler. Any
  other ask for the port is refused with `admin-port-unrequested`.

`wb-hostd` is the one shared process, because it terminates every
host-guest connection (S04-architecture). The paths share only the
controls on guest bytes: the version record, the caps of a guest
connection (S04-08-records-and-bounds), and the terminal filter
(S16-01-filter). `wb` runs the filter on the admin stream as on
`wb shell`'s.

A project user and the proxy can't reach `wb-guestroot`:

- the session profile denies `AF_VSOCK` sockets
  (S13-02-filesystem-profile, "Host-guest socket");
- `wb-netd` and `wb-proxyd` hold no host-guest connection;
- no request from the guest makes `wb-hostd` open the admin port.

The design relies on `wb-guestroot` answering while `wb-guestd` is
dead, wedged or restarting. X31-admin-channel measures that.

### Its version

`wb-guestroot` is on the guest tools disk (S06-12-guest-agent, "Guest
tools disk"). At its start it updates itself from that disk, with the
steps and checks of S06-12-guest-agent, "Update at start". So a dead
`wb-guestd` doesn't keep it at an old version. Each install writes a
new file and renames it over the old one, so an install by either
daemon leaves a whole program.

`wb-hostd` accepts only a `wb-guestroot` of its own build version, as
it does for `wb-guestd`, and refuses another with
`peer-version-mismatch`.

### Marked

Before `wb-hostd` opens the admin connection, it marks the VM's system
disk `admin-used` in `state.db`, with the time. A crash after the
mark leaves the disk marked.

While the disk is marked:

- every session start in the VM is refused with `vm-admin-used`, a
  debug shell included;
- `wb vm suspend` is refused with `vm-admin-used`, so the next start
  is a cold start;
- `wb vm status` shows the mark, its time, and that the next cold
  start replaces the system disk.

At the VM's next cold start, `wb-hostd` replaces the marked system
disk with a fresh clone of the image, as break-glass does
(S15-04-guest-admin, "Break-glass"). First contact rotates `wbadmin`
again, and the mark goes with the old disk.

The host's controls don't depend on the guest
(SEC11-root-gains-nothing: guest root grants no control over
enforcement, secrets or policy). Project users' separation in the guest
rests on what the sealing scan checked (S06-02-image-build, "Sealing"),
such as no sudoers entry and no extra admin account (SEC08-proj-isolation).
A root shell can undo any of that.

### The data disk

The admin path leaves the data volume as it is, mounted when
`wb-guestd` mounted it. The root shell can read and change every
project's home on it, and that is the person's own data.

What the shell wrote there stays when the system disk is replaced,
as T18-data-disk-persistence says for guest root. `wb vm reset-data`
is the way out (S05-03-management-commands).

### Audit

- `wb-hostd` writes a Device Config State Change (5019) event when it
  marks the disk, and one when the admin connection ends. Each names
  the VM, the system disk, and the command (SEC10-audit).
- It logs each refusal with its rule.
- The shell's input and output aren't recorded. The person typed them
  in their own terminal.

### Tests

Tests reach `wb-guestroot` through `wb-dev-vm`'s relay to a guest port
below 1024, on bundles from `wb-dev-bundle` (S10-tech-stack, "Test
tooling"). That tooling is never in the app bundle. No shipped binary
has a test-only way to guest root.

Root checks in a VM that the app's `wb-vmd` booted use `wb vm admin`,
in an attended run. The admin path adds no network device. The guest
reaches the network only through `wb-netd` (SEC05-default-deny:
only allowlisted hosts are reachable).

## Decisions

### A guest process of its own

The guest end is `wb-guestroot`, a root daemon apart from `wb-guestd`,
on a port of its own.

- **Why:** it can answer when `wb-guestd` is dead or wedged, and it
  shares no process or port with the agent paths.
- **Rejected:** a second port in `wb-guestd`. It stops answering
  with `wb-guestd`, and shares its process with the sessions.
- **Rejected:** a login as `wbadmin` on a console of `wb-vmd`. The
  fixed device set refuses console devices (S04-02-vm-provider, "What
  only the code keeps out"). An enabled `wbadmin` reverses I156 and
  the sealing scan.
- **Rejected:** recoveryOS with `wbadmin` (I158). The guest system is
  offline there, so root checks of the running guest can't run.

### Marked until the system disk is replaced

- **Why:** after a root shell, no scan has checked the system disk that
  project users would run on.
- **Rejected:** no mark, only the audit event. Project users would run
  on a disk with whatever the shell changed.
- **Rejected:** a mark the person clears by confirming. That trusts a
  disk no scan has checked.
- **Rejected:** a new data disk as well. It deletes every project's
  home, and `wb vm reset-data` is there when the person wants that.

## Out of scope

- A root shell in the build VM.
- Moving files between host and guest. The stream is a filtered
  terminal (S16-01-filter).
- Windows and Linux guests (S12-platforms).

**Status:** Draft
