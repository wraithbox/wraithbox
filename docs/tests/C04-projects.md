# C04 - Projects

**Purpose:** The test cases for project identity: which repository a
session belongs to, what happens when a repository moves, is cloned
again or changes its remote, and the project commands.

**Verifies:** S05-cli ("Naming"), S07-01-per-vm-policy

## Conformance

### Remotes and root commits

- Change a remote URL of a registered project's repository, start a
  session, and see it refused with both URLs named. Then run `wb
  project confirm`, and see the next start pass, `config_trust`
  cleared, and a 5019 event with both URL sets.
- Switch the remote between its SSH and HTTPS forms, and see the start
  pass.
- Start a session in a second `git worktree` of the repository, and see
  it use the same project (S05-cli, "Naming").
- In a repository without a remote, replace it at the same path with
  another repository without a remote, and see the start refused with
  both root commit sets named.
- Register a repository with no remote and no commit, commit, start a
  session, and see the root commit recorded with a 5019 event. Then
  replace the repository, and see the start refused.

### Moves and second clones

- Move project A from `/x` to `/y` with `wb project move`, clone
  another repository into `/x`, start a session there, and see a new
  project with a new id, none of A's settings, policy or approvals, and
  A still at `/y`.
- Move a project's directory without `wb project move`, start a session
  at the new path, and see it refused with the old project named and
  `wb project move` and `wb project rm` offered.
- Clone the same repository a second time while the first clone stays,
  and see the second registered as its own project, with a notice that
  names the first.
- Do the same for a project with `placement = isolated`, and see the
  second clone start isolated, with none of the first's approvals or
  `config_trust`.
- On macOS, start sessions in one repository as `~/git/foo` and
  `~/Git/foo` and through a symbolic link, and see one project.

### Git directories that point elsewhere

- Start a session in a directory whose `.git` file reads
  `gitdir: <project A>/.git`, and in one whose `.git` file reads
  `gitdir: <project A>/.git/modules/sub`. See both refused with the reason and
  nothing registered.
- Start a session with `GIT_DIR`, `GIT_CONFIG_PARAMETERS` or
  `GIT_CONFIG_COUNT` set in the environment to point at project A or
  rewrite its configuration, and see it resolve to the directory's own
  repository, not A.
- Run `wb trust` and `wb project place` in the first two directories,
  and see them refused the same way.

### Project commands

- Run `wb project rm` with the VM stopped, restart `wb-hostd`, start the
  VM, and see the guest user removal sent.
- Send a removal reply from the guest for another project, and see that
  project's record kept.
- Run `wb project place isolated` for a project that has a home on a
  data disk, and see it refused with I232 named. Run it while the
  project has a session, and see it refused with the session named.
- Run `wb project place isolated` in a new clone before its first
  session, and see the first session start in the isolated VM.
- Run `wb trust`, `wb untrust`, and each changing `wb project` command,
  and see a 5019 event with the values before and after.

**Status:** Draft
