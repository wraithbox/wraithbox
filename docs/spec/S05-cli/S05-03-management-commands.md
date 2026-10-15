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
| `wb project confirm [--project P]` | Accept the repository's current remote URLs and root commits as the project's, and clear `config_trust` (S05-04-naming) |
| `wb project rm [--project P]` | Remove a project and everything Wraith Box holds for it (S05-04-naming) |
| `wb cred set/list/rm <binding>` | Manage the binding credentials in the secret store ("`wb cred`") |
| `wb audit tail/search` | Read the audit log (SEC10-audit) |
| `wb gate reset go-clock` | Reset the Go clock after a `go-sumdb-inconsistent` finding ("`wb gate reset go-clock`") |
| `wb vm reset-ca <vm>` | Replace a VM's CAs after a `ca-key-restore` refusal ("`wb vm reset-ca`") |
| `wb vm start/stop/suspend/status` | Explicit VM control ("`wb vm`") |
| `wb image build/list/use` | Base image management ("`wb image`") |
| `wb shell [--project P]` | A debug shell as the project user, labeled as a debug shell |
| `wb setup` | Check the host, and install and start the per-user services ("`wb setup`") |
| `wb help`, `wb version` | Help and version |

Each command has its own flags, after the command name. The names of
commands are reserved: a new agent command may not reuse one.

`wb trust`, `wb untrust` and each `wb project` command that changes a
project write a Device Config State Change (5019) event. Those
`wb project` commands are `move`, `rename`, `place`, `confirm` and
`rm`. The event holds the project, and the values before and after
(SEC10-audit, S09-10-audit).

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
  `approval-stale` (S09-09-approval-flow).

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
  (S15-least-privilege, "Keychain").

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
confirm. It is never part of a guest RPC (S07-08-dependency-gate).

### `wb vm reset-ca`

This command follows a `ca-key-restore` refusal.

1. It prints the VM's CAs, and asks for confirmation.
2. Then `wb-hostd`:
   - drops the CAs from its record;
   - writes a 5019 event with the command and their fingerprints;
   - puts their keys on the removal list;
   - deletes the VM's saved state.

The next start then cold boots with a new CA (S09-05-ca-keys, "Valid
CA").

The command is refused while the VM runs. It is refused without a
terminal, because a person has to confirm. It is never part of a guest
RPC.

### `wb vm`

`wb vm start/stop/suspend/status` controls a VM explicitly.

`stop` and `suspend` are refused while the VM has a session that is
starting, running, paused or ending. The refusal names each such
session, the process ID of its `wb`, and how it ends: close its
terminal, or wait for its deadline. `wb sessions` lists them
(S06-09-session-lifecycle, "Idle suspend").

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

### `wb setup`

`wb setup`:

1. checks the host prerequisites (S12-platforms), and the minimum git
   version (S08-workspace-and-git);
2. creates the projects root `<data>/projects`, and its probe
   directory. `wb-hostd` can't write them (S04-03-self-sandboxing,
   "Each host daemon is self-sandboxed");
3. installs and starts the per-user services. On macOS those are the
   `wb-hostd` LaunchAgent and the `~/.local/bin/wb` link
   (S17-02-setup).

Per platform:

- On Windows, it refuses a host without the platform crypto provider.
- On Linux, it records whether the CA key is in a TPM (S09-05-ca-keys,
  "Key"). `--ca-key-store tpm|software` changes the recorded choice.
- On macOS, in an ad-hoc signed build, it refuses the Keychain backend
  with `keychain-needs-team`, and says how to sign the build. It still
  installs and starts the services, and exits with a nonzero status.
- On macOS, in a development build, it names the signing team. It says
  that every program the team signs can reach Wraith Box's Keychain
  groups (S15-least-privilege, "The store holds credentials"). It says
  when the signing profile expires, once that is 30 days away or less,
  or after it (S12-03-host-prerequisites, "Development builds on macOS").

`wb setup --remove` refuses while a VM runs. Otherwise it unloads the
services and deletes those files. It keeps the user's data and store
items.

**Status:** Draft
