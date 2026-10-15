# S05-03 - Management Commands

**Purpose:** The commands of `wb` other than the agent commands.

## Design

| Command | Purpose |
|---|---|
| `wb status` | VMs, their state, running sessions, pending approvals, and each host process with its state, version, restarts and last exit reason (S04-08-records-and-bounds, "Logs and health"). On macOS, within 30 days of the expiry date of the signing profile in the helper bundles, or after it, that date and the fix (S12-platforms, "Development builds on macOS") |
| `wb sessions [--project P]` | List sessions and their returned work |
| `wb diff <session>` | Review returned work with flagged paths highlighted |
| `wb land <session> [--branch NAME]` | Fetch returned work into the host repo as a branch; never checks out or merges |
| `wb discard <session>` | Drop returned work and the session's guest worktree with its history link. A lost session ends with no recovery push (S06-11-reconnect-and-recovery, "Discarding a lost session") |
| `wb approve <id>` / `wb deny <id>` | Answer the pending approval request with that id (notification alternative). Without an id, refused with an error that lists the pending ids. An id that doesn't name an open request is refused with `approval-stale` (S09-09-approval-flow) |
| `wb allow <host> [--project P]` | Add an allowlist entry (SEC05-default-deny) |
| `wb policy show/explain/edit [--project P]` | Effective policy and why a request was allowed or refused (NFR06-explained-refusals) |
| `wb learn report [--project P]` | Suggested allowlist from a learn-mode session (FR10-learn-mode) |
| `wb trust [<repo>] [--project P]` / `wb untrust [<repo>] [--project P]` | Set or clear the project's `config_trust`, which allows repository-supplied configuration (SEC09-host-policy). `<repo>` (default: the current directory) resolves to its project by the rules of S05-04-naming. It doesn't change the VM |
| `wb project show [--project P]` | Project id, name, location, recorded identity (remote URLs, root commits), `placement` and `config_trust` |
| `wb project move <dir> [--project P]` | Point the project at the repository in `<dir>`, keeping its id, state, approvals and policy (S05-04-naming) |
| `wb project rename <name> [--project P]` | Change the project's name; the id stays |
| `wb project place work\|isolated [--project P]` | Set the project's `placement` (S06-04-vms-and-placement). In a repository that isn't a project yet, it registers one first by the rules of S05-04-naming (moved-project refusal, second-clone placement), so a new clone can start isolated |
| `wb project confirm [--project P]` | Accept the repository's current remote URLs and root commits as the project's, and clear `config_trust` (S05-04-naming) |
| `wb project rm [--project P]` | Remove a project and everything Wraith Box holds for it (S05-04-naming) |
| `wb cred set/list/rm <binding>` | Manage the binding credentials in the secret store, which `wb-proxyd` fetches when proxying needs them (S09-policy-credentials-audit, S15-least-privilege). `wb` itself doesn't have store access. Each command runs `wb-credset` for one operation, and `set` passes the value on its standard input (S15-least-privilege, "Keychain") |
| `wb audit tail/search` | Read the audit log (SEC10-audit) |
| `wb gate reset go-clock` | After a `go-sumdb-inconsistent` finding, print its two tree heads and their receipt times and ask for confirmation. Then `wb-hostd`, asked over local IPC, deletes the finding and the Go clock's stored tree heads, writes a 5019 event naming the command and the finding, and restarts `wb-proxyd`. The dependency gate then allows Go downloads again from a fresh head. The restart resets every proxied stream of both VMs, `claude` API streams included, starts a new wait of up to 30 s for the first point, and doesn't count toward the restart limit. Refused without a terminal, because a person has to confirm. It is never part of a guest RPC (S07-08-dependency-gate) |
| `wb vm reset-ca <vm>` | After a `ca-key-restore` refusal, print the VM's CAs and ask for confirmation. Then `wb-hostd` drops them from its record, writes a 5019 event with the command and their fingerprints, puts their keys on the removal list and deletes the VM's saved state, so the next start cold boots with a new CA (S09-05-ca-keys, "Valid CA"). Refused while the VM runs, refused without a terminal, because a person has to confirm, and never part of a guest RPC |
| `wb vm start/stop/suspend/status` | Explicit VM control. `stop` and `suspend` are refused while the VM has a session that is starting, running, paused or ending, and `suspend` also while a kill of a project user is pending or after a leftover-process result (S06-10-leftover-processes). That refusal says to wait up to 7 minutes for the cleanup result, or to stop and start the VM. The refusal names each one, the process ID of its `wb`, and how it ends: close its terminal, or wait for its deadline, and `wb sessions` lists them (S06-09-session-lifecycle, "Idle suspend"). `start` of the isolated VM is refused while `wb image build` has borrowed its slot, and the refusal names the build (S06-07-slot-admission) |
| `wb image build/list/use` | Base image management (S06-vm-lifecycle). `list` shows each image's Claude Code version (S14-claude-in-guest, "Install"). `build` borrows the isolated VM's slot, and is refused while the isolated VM has a session that is starting, running, paused or ending, a debug shell (`wb shell`) included (S06-07-slot-admission) |
| `wb shell [--project P]` | Debug shell as the project user, labeled as a debug shell |
| `wb setup` | Check host prerequisites (S12-platforms) and the minimum git version (S08-workspace-and-git), create the projects root `<data>/projects` and its probe directory, which `wb-hostd` can't write (S04-03-self-sandboxing, "Each host daemon is self-sandboxed"), then install and start the per-user services: on macOS the `wb-hostd` LaunchAgent and the `~/.local/bin/wb` link (S17-install-and-upgrade, "Install"). `wb setup --remove` refuses while a VM runs, then unloads the services and deletes those files, and keeps the user's data and store items. On Windows it refuses a host without the platform crypto provider, and on Linux it records whether the CA key is in a TPM (S09-05-ca-keys, "Key"). On macOS, in an ad-hoc signed build, it refuses the Keychain backend with `keychain-needs-team`, says how to sign the build, still installs and starts the services, and exits with a nonzero status. In a development build it names the signing team, says that every program the team signs can reach Wraith Box's Keychain groups, and says when the signing profile expires once that is 30 days away or less, or after it (S15-least-privilege, "The store holds credentials"; S12-platforms, "Development builds on macOS"). On Linux `--ca-key-store tpm\|software` changes the recorded choice (S09-05-ca-keys, "Key") |
| `wb help`, `wb version` | Help and version |

Each command has its own flags after the command name. Names of
commands are reserved: a new agent command may not reuse one.

`wb trust`, `wb untrust` and each `wb project` command that changes a
project (`move`, `rename`, `place`, `confirm`, `rm`) write a Device
Config State Change (5019) event with the project and the values
before and after (SEC10-audit, S09-10-audit).

`wb approve` and `wb deny` print what is being approved. That text
includes guest-influenced values (hostnames, paths); `wb` strips control
and escape sequences from every such value before printing it, so the
guest cannot rewrite the approval screen (SEC14-no-fake-approvals).

**Status:** Draft
