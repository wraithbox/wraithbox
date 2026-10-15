# C09 - Guest and Sessions

**Purpose:** The test cases for what a project user can reach inside
the guest, the shared toolchain prefix, the guest admin password, and
the session lifecycle.

**Verifies:** S06-vm-lifecycle ("Toolchain prefix", "Session
lifecycle"), S13-guest-confinement, S15-least-privilege (the guest
admin password)

## Fuzzing

- The toolchain manifest parser in `wb-hostd`, and the name check in
  `wb-guestd` (S06-vm-lifecycle, "Toolchain prefix").

## Conformance

### From the guest

- Read or change policy, credentials, or the audit log from the guest.
- As a project user outside the session profile, bind a free vsock port
  below 1024, and see `EACCES`. Bind `wb-guestd`'s port while it runs,
  and see the bind refused. The rule for ports below 1024 isn't in
  Apple's developer documentation, only in the XNU source
  (`bsd/kern/vsock_domain.c`, `VSOCK_PORT_RESERVED`, `proc_suser`), so
  each guest image is checked.
- Inside a session, open an `AF_VSOCK` socket, and see `EPERM`
  (X27-vsock-confinement).
- As root, run `sudo -l -U` for each project user, and see no rules
  listed, and no `admin` or `wheel` group in its `id`
  (S06-vm-lifecycle, X27-vsock-confinement).

### The toolchain prefix

Spec: S06-vm-lifecycle, "Toolchain prefix", X20-shared-homebrew.

- As a project user, replace a link in the prefix's `bin`, write to a
  binary in its `Cellar`, and run `brew install`, and see each refused.
- As root, check that every file in the prefix belongs to the toolchain
  user and to its own group, that the group has no other members, and
  that others can't write any of them.
- Hand `wb-hostd` manifests with a `tap`, a `cask`, a `system` line,
  `brew "--force"`, `brew "--HEAD"`, `brew "jq.rb"`, a name with a `/`,
  a name over the length cap, and too many lines. See each refused with
  the line and the rule. Send the same names to `wb-guestd` directly,
  and see it refuse them too.
- Declare a formula with no bottle for the guest, and one whose
  dependency has none, and see the session refused with the formula's
  name before anything is installed.
- Remove the formula data snapshot, and see the reconcile refused.
- With a session of project A running, reconcile project B, whose
  formulae share dependencies with A's, and see every `opt/*` link of
  A's formulae point where it did before (I180).
- Run A's tools with the session `PATH`, which leaves out the prefix's
  shared `bin`.
- See Homebrew's temporary and cache files only in the toolchain user's
  mode-700 home.

### The guest admin password

Spec: S15-least-privilege.

At first contact, with a data disk whose project homes hold a
LaunchAgent and a login item:

- see the rotation finish before `wb-hostd` sends the time and before
  `wb-guestd` lists its sessions, and nothing from the data disk run
  before it;
- see `wbadmin` disabled again with a usable secure token, and the
  image password refused by `dscl . -authonly`;
- drop the link during a rotation, and see the disk marked "rotation
  unknown" and every session start refused with its rule.

### Session lifecycle

Spec: S06-vm-lifecycle, "Session lifecycle".

With a `wb-guestd` that the test drives as guest root. Each case checks
the audit entry with its rule.

Exit codes and lost sessions:

- Report an exit code of 256 and a signal number of 0 and of 65, and
  see `wb` exit with 255.
- Drop the link, then list a running session of the other VM, an ended
  session, a malformed id, an unknown id and a running session of an
  earlier VM generation. See "end" for each, and no session of the
  other VM change state.
- Drop the link and leave a running session out of the list, and see
  it lost and `wb` exit with 255.
- Send a list of 65 entries, and see it refused and the connection
  closed.
- Drop the link again and again, and see at most 20 connections to the
  VM in a minute.

Reports:

- List one id both as a held session and as a report, and see the list
  refused.
- Send a report for another VM's session and for an unknown id, and see
  "end" and the report gone from the guest's directory.
- Send a report again for an ended session, and see it acknowledged
  with no change.
- Drop the link right after a session ends, before its report reaches
  the host, and see the session end with that report, not as lost.

The WIP commit and push:

- Drop the link while the WIP commit runs, bring it back, and see the
  stopped commit killed, the commit run again and the session end
  normally.
- Report a WIP push that never reached `receive-pack`, and see a failed
  push, with `wb` exiting 255 and the summary's count taken from
  `landing.git`.
- Never report a WIP commit, and see the session end as a failure at
  its deadline, with `wb` exiting 255 and the summary naming the
  worktree's path and `wb shell`.

Commands and pushes around a session:

- Run `wb vm stop` and `wb vm suspend` with a running session, and see
  both refused with the session named.
- During recovery, push to the recovering session's branch and to
  another session's, and see only the first accepted.
- Push to an ended session's branch, and to a discarded one's, and see
  both refused.
- Close the terminal while a child of `claude` has left the session
  with `setsid` and another session of the project runs, and see the
  child logged as a survivor (S13-guest-confinement). With no other
  session, see it killed.

**Status:** Draft
