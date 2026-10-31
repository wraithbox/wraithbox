# S05-03 - Management Commands

**Purpose:** The commands of `wb` other than the agent commands.

## Design

### Commands

| Command | Purpose |
|---|---|
| `wb status` | VMs, sessions, approvals and host processes ("`wb status`") |
| `wb sessions [--project P]` | List sessions and their returned work |
| `wb diff <session>` | Review returned work, with flagged paths highlighted |
| `wb land <session> [--branch NAME]` | Fetch returned work into the host repo as a branch. It never checks out or merges |
| `wb discard <session>` | Drop returned work, and the session's guest worktree with its history link. A lost session ends with no recovery push (S06-11-reconnect-and-recovery, "Discarding a lost session") |
| `wb approve <id>` / `wb deny <id>` | Answer a pending approval request ("`wb approve` and `wb deny`") |
| `wb allow <host> [--project P]` | Add an allowlist entry (SEC05-default-deny) |
| `wb policy show/explain/edit [--project P]` | The effective policy, and why a request was allowed or refused (NFR06-explained-refusals) |
| `wb learn report [--project P]` | The suggested allowlist from a learn-mode session (FR10-learn-mode) |
| `wb trust [<repo>] [--project P]` / `wb untrust [<repo>] [--project P]` | Set or clear the project's `config_trust` ("`wb trust` and `wb untrust`") |
| `wb project show [--project P]` | The project's id, name, location, recorded identity (remote URLs, root commits), `placement` and `config_trust` |
| `wb project move <dir> [--project P]` | Point the project at the repository in `<dir>`. It keeps its id, state, approvals and policy (S05-04-naming) |
| `wb project rename <name> [--project P]` | Change the project's name. The id stays |
| `wb project place work\|isolated [--project P]` | Set the project's `placement` ("`wb project place`") |
| `wb project confirm` | Accept the current remote URLs and root commits of the repository in the directory as the project's, and clear `config_trust` (S05-04-naming). It takes no `--project`, because it reads the identity from a repository and `--project` names none |
| `wb project rm [--project P]` | Remove a project and everything Wraith Box holds for it (S05-04-naming) |
| `wb cred set/list/rm <binding>` | Manage the binding credentials in the secret store ("`wb cred`") |
| `wb audit tail/search` | Read the audit log (SEC10-audit) |
| `wb gate reset go-clock` | Reset the Go clock after a `go-sumdb-inconsistent` finding ("`wb gate reset go-clock`") |
| `wb vm reset-ca <vm>` | Replace a VM's CAs after a `ca-key-restore` refusal ("`wb vm reset-ca`") |
| `wb vm reset-data <vm>` | Give a stopped VM a new, empty data disk after a data disk refusal ("`wb vm reset-data`") |
| `wb vm start/stop/suspend/status` | Explicit VM control ("`wb vm`") |
| `wb vm admin <vm>` | A root shell in a running VM for the person at the terminal, which marks the VM's system disk for replacement (S15-05-admin-path) |
| `wb image build/list/use` | Base image management ("`wb image`") |
| `wb shell [--project P]` | A debug shell as the project user, labeled as a debug shell ("`wb shell`") |
| `wb setup` | Check the host, and install and start the per-user services ("`wb setup`") |
| `wb help`, `wb version` | Help and version |

Each command has its own flags, after the command name. The names of
commands are reserved: a new agent command may not reuse one.

`wb trust`, `wb untrust` and each `wb project` command that changes a
project write a Device Config State Change (5019) event. Those
`wb project` commands are `move`, `rename`, `place`, `confirm` and
`rm`. The event holds the project, and the values before and after
(SEC10-audit, S09-11-audit).

### `wb status`

`wb status` shows:

- the VMs and their state;
- running sessions;
- pending approvals;
- each host process, with its state, version, restarts and last exit
  reason (S04-08-records-and-bounds, "Logs and health").

On macOS it also shows the expiry date of the signing profile in the
helper bundles, and the fix. It does that when the date is 30 days away
or less, or after it (S12-03-host-prerequisites, "Development builds on macOS").

### `wb approve` and `wb deny`

`wb approve <id>` and `wb deny <id>` answer the pending approval
request with that id. They are the alternative to the notification.

- Without an id, they are refused, with an error that lists the pending
  ids.
- An id that doesn't name an open request is refused with
  `approval-stale` (S09-10-approval-flow).

Both print what is being approved. That text includes guest-influenced
values, such as hostnames and paths. `wb` strips control and escape
sequences from every such value before printing it. So the guest can't
rewrite the approval screen (SEC14-no-fake-approvals).

### `wb trust` and `wb untrust`

They set or clear the project's `config_trust`, which allows
repository-supplied configuration (SEC09-host-policy). They don't
change the VM.

`<repo>` defaults to the current directory. It resolves to its project
by the rules of S05-04-naming.

### `wb project place`

`wb project place` sets the project's `placement`
(S06-04-vms-and-placement).

In a repository that isn't a project yet, it first registers one, by
the rules of S05-04-naming. Those include the moved-project refusal and
the second-clone placement. So a new clone can start isolated.

### `wb cred`

`wb cred set/list/rm <binding>` manages the binding credentials in the
secret store. `wb-proxyd` fetches them when proxying needs them
(S09-policy-credentials-audit, S15-least-privilege).

- `wb` itself doesn't have store access.
- Each command runs `wb-credset` for one operation.
- `set` passes the value on `wb-credset`'s standard input
  (S15-02-secret-store, "Keychain").

### `wb gate reset go-clock`

This command follows a `go-sumdb-inconsistent` finding.

1. It prints the finding's two tree heads and their receipt times, and
   asks for confirmation.
2. Then `wb-hostd`, asked over local IPC:
   - deletes the finding and the Go clock's stored tree heads;
   - writes a 5019 event that names the command and the finding;
   - restarts `wb-proxyd`.

The dependency gate then allows Go downloads again, from a fresh head.
The restart:

- resets every proxied stream of both VMs, `claude` API streams
  included;
- starts a new wait of up to 30 s for the first point;
- doesn't count toward the restart limit.

The command is refused without a terminal, because a person has to
confirm. It asks in its own terminal, never in a session's stream
(SEC14-no-fake-approvals). It is never part of a guest RPC
(S07-12-dependency-gate).

### `wb vm reset-ca`

This command follows a `ca-key-restore` refusal.

1. It prints the VM's CAs, and asks for confirmation.
2. Then `wb-hostd`:
   - drops the CAs from its record;
   - writes a 5019 event with the command and their fingerprints;
   - puts their keys on the removal list;
   - deletes the VM's saved state.

The next start then cold boots with a new CA (S09-06-ca-keys, "Valid CA").

The command is refused while the VM runs. It is refused without a
terminal, because a person has to confirm. It asks in its own terminal,
never in a session's stream (SEC14-no-fake-approvals). It is never
part of a guest RPC.

### `wb vm reset-data`

This command follows a data disk refusal: `data-disk-missing`,
`data-disk-unrecorded`, or a data volume `wb-guestd` can't mount
(S06-13-data-disk-format). `<vm>` is `work` or `isolated`.

1. It prints the VM, its data disk and the space the disk takes on the
   host, from the file's `st_blocks`. It says that every project's home
   on that disk is lost: its Claude Code state, and commits not yet
   pushed. It asks for confirmation.
2. Then `wb-hostd`, asked over local IPC:
   - refuses with `vm-running` unless the VM is stopped, and holds the
     VM's start lock until it is done, so a start meanwhile is refused
     with `vm-resetting`;
   - deletes the file at the data disk's final name and any temporary
     file, without following a symbolic link or opening either;
   - drops the disk's record;
   - deletes the VM's saved state, because a saved state restores only
     onto its own disks (S06-06-devices-and-warm-start);
   - has `wb-diskfmt` make a new disk, and records it (S06-13-data-disk-format,
     "Formatting a data disk");
   - writes a 5019 event with the command, the VM, and the old and new
     volume UUIDs.

The project users' host records stay, because their IDs are never
reused (S06-03-image-updates). A new, empty home is made on the new
volume.

The command is refused without a terminal, because a person has to
confirm. It asks in its own terminal, never in a session's stream, and
no flag skips the question (SEC14-no-fake-approvals). It is never part
of a guest RPC.

### `wb vm`

`wb vm start/stop/suspend/status` controls a VM explicitly. Each takes
the VM as its argument, `work` or `isolated`, and `status` without one
shows both. `wb` names the VM to `wb-hostd` as a value of the
contract's VM enum, never as a path. A command without a VM, with
another name, or with an extra argument is a usage error (exit 2).
`wb` asks `wb-hostd` over local IPC. Any refusal or failure ends with
one `wraith box: <message> (<rule>)` line and exit 255
(S05-02-exit-status-and-signals).

`start` returns after the VM runs and `wb-hostd` accepted or refused
the hello of its `wb-guestd`, or after a deadline. The deadline is in
code, above the cold boot of X18-vsock-handoff with a margin. A refused
hello and a link that isn't up by the deadline both exit 255, and leave
the VM running for `stop`. A start or a stop that `wb-hostd` began runs
to its end when `wb` goes away, as from `Ctrl-C`. When its answer
doesn't arrive, `wb` says the VM may still be starting or stopping, and
names `wb vm status`.

`status` shows, for each VM, its state, its generation, the guest
link with `wb-guestd`'s version or the refusal with both versions, and
the last error of the Virtualization framework. The guest's version is
guest input, so `wb` removes control and escape characters from it and
cuts it to 64 bytes ("`wb approve` and `wb deny`").

`suspend` saves the VM's state for a warm start
(S06-06-devices-and-warm-start). Until warm start is built, `wb`
refuses it with `vm-suspend-needs-warm-start` without asking
`wb-hostd`.

`stop` and `suspend` are refused while the VM has a session that is
starting, running, paused or ending (`vm-live-sessions`). The refusal
names each such session, the process ID of its `wb`, and how it ends:
close its terminal, or wait for its deadline. `wb sessions` lists them
(S06-09-session-lifecycle, "Idle suspend"). With a `wb vm admin` shell
open, both are refused with `vm-admin-open` (S15-05-admin-path).

`stop` asks the guest to shut down, and stops the VM at once when the
guest hasn't stopped by a deadline. A `stop` of a stopped VM is refused
with `vm-not-running`, and a `start` of a VM that isn't stopped with
`vm-running`. A `start` whose `wb-vmd` doesn't open or answer within a
deadline is refused with `vm-start-timeout`, and that `wb-vmd` is
closed.

`suspend` is also refused while a kill of a project user is pending, or
after a leftover-process result (S06-10-leftover-processes). That
refusal says to wait up to 7 minutes for the cleanup result, or to stop
and start the VM.

`start` of the isolated VM is refused while `wb image build` has
borrowed its slot. The refusal names the build (S06-07-slot-admission).

### `wb image`

`wb image build/list/use` manages base images (S06-vm-lifecycle).

- `list` shows each image's Claude Code version (S14-01-install).
- `build` borrows the isolated VM's slot. It is refused while the
  isolated VM has a session that is starting, running, paused or
  ending, a debug shell (`wb shell`) included (S06-07-slot-admission).

### `wb shell`

`wb shell [--project P]` runs `/bin/zsh -l` as the project user, in
that user's home, in the project's VM. It takes no command to run. It
resolves the project from the directory unless `--project` is given
(S05-04-naming, "Which commands"). It refuses a directory that isn't a
registered project, and names `wb claude` and `wb project place` as the
ways to register one. It refuses a host terminal whose charset isn't
UTF-8, as `wb claude` does (S05-01-agent-commands, "A terminal that
isn't UTF-8").

`wb` labels it as a debug shell:

- the window title is `wb: <shell id>` (S16-03-around-the-stream,
  "Title"). The shell id has the session id form (S05-04-naming,
  "Session id and branch");
- before the relay starts, one line on standard error names the
  project and the guest user, and says that a debug shell returns no
  work.

The shell's output reaches the terminal through the filter, and `wb`
writes the title, the reset and the drain around it, as for an agent
session (S16-terminal-stream). Its exit status and signals are those
of S05-02-exit-status-and-signals, with the shell in place of `claude`.

### `wb setup`

`wb setup`:

1. checks the host prerequisites (S12-platforms), and the minimum git
   version (S08-workspace-and-git). A refused git, or a `config.toml`
   it can't read, refuses `wb setup` before it creates or installs
   anything (S08-08-host-git, "Where it is checked");
2. creates the projects root `<data>/projects`, and its probe
   directory. `wb-hostd` can't write them (S04-03-self-sandboxing, "Each host daemon is self-sandboxed");
3. installs and starts the per-user services. On macOS those are the
   `wb-hostd` LaunchAgent and the `~/.local/bin/wb` link
   (S17-02-setup).

Per platform:

- On Windows, it refuses a host without the platform crypto provider.
- On Linux, it records whether the CA key is in a TPM (S09-06-ca-keys, "Key"). `--ca-key-store tpm|software` changes the recorded choice.
- On macOS, in an ad-hoc signed build, it refuses the Keychain backend
  with `keychain-needs-team`, and says how to sign the build. It still
  installs and starts the services, and exits with a nonzero status.
- On macOS, in a development build, it names the signing team. It says
  that every program the team signs can reach Wraith Box's Keychain
  groups (S15-02-secret-store). It says
  when the signing profile expires, once that is 30 days away or less,
  or after it (S12-03-host-prerequisites, "Development builds on macOS").

`wb setup --remove` refuses while a VM runs. Otherwise it unloads the
services and deletes those files. It keeps the user's data and store
items.

**Status:** Draft
