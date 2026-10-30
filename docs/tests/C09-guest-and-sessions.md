# C09 - Guest and Sessions

**Purpose:** The test cases for what a project user can reach inside
the guest, `wb-guestd`'s update and the guest clock, the shared
toolchain prefix, the guest admin password, and the session lifecycle.

**Verifies:** S06-01-image-layers ("Toolchain prefix"),
S06-03-image-updates ("Creation"),
S06-05-disks ("Mounting the data volume"),
S06-06-devices-and-warm-start ("Time and sleep"),
S06-09-session-lifecycle,
S06-10-leftover-processes,
S06-12-guest-agent ("Update at start"),
S13-guest-confinement, S15-04-guest-admin

## Fuzzing

- The toolchain manifest parser in `wb-hostd`, and the name check in
  `wb-guestd` (S06-01-image-layers, "Toolchain prefix").
- The parser of `diskutil info -plist` in `wb-guestd`, `FuzzParseInfo`
  in `internal/platform/guestmount`: an accepted report has a UUID and
  a device of the form `disk<N>s<N>`, and a report without an encryption
  property reads as encrypted (S06-05-disks, "The mount").
- The checks of a kill result in `wb-hostd`, `FuzzKillResult` in
  `internal/hostd/kills`: a KillUser answer or a cleanup record of any
  bytes is taken only for a kill ID sent for its project in the VM
  generation, with a result and count in their bounds, and a record out
  of bounds is never acknowledged (S06-10-leftover-processes,
  "Acceptance").
- The parser of `dscl -plist` records in `wb-guestd`,
  `FuzzDirectoryRecord` in `internal/platform/guestusers`: guest root can
  write the values, and an accepted record keeps the bounds on its
  values and their count (S06-03-image-updates, "Creation").

## Conformance

### From the guest

- Read or change policy, credentials, or the audit log from the guest.
- As a project user outside the session profile, bind a free vsock port
  below 1024, on the wildcard CID and on the guest's own, and see
  `EACCES`. Bind `wb-guestd`'s port the same two ways while it runs, and
  see the bind refused. The rule for ports below 1024 isn't in
  Apple's developer documentation, only in the XNU source
  (`bsd/kern/vsock_domain.c`, `VSOCK_PORT_RESERVED`, `proc_suser`), so
  each guest image is checked. In `conformance/guestsock`:
  `TestFreeLowPortsNeedRoot` and `TestGuestdPortRefused`.
- As root, connect to `wb-guestd`'s port on the guest's own CID in a
  loop for 30 s. See `wb-guestd` still hold the port (a root bind gets
  `EADDRINUSE`), and its log hold at most one `guestd-accept-failed`
  or `guestd-peer-not-host` record per 10 s (S04-02-vm-provider,
  "Guest ports below 1024", I115). Meanwhile, connect from the host,
  and see the host still reach the port and each of its connections
  logged as served from CID 2. In `conformance/guestsock`:
  `TestSelfCIDFlood`, with the host side run by hand.
- Kill `wb-guestd` with `SIGKILL`, and see launchd start it again and it
  hold its port again.
- Inside a session, open an `AF_VSOCK` socket, and see `EPERM`
  (X27-vsock-confinement).
- As root, run `sudo -l -U` for each project user, and see no rules
  listed, and no `admin` or `wheel` group in its `id`
  (S06-vm-lifecycle, X27-vsock-confinement).

### Update at start and the clock

Spec: S06-12-guest-agent ("Update at start"),
S06-06-devices-and-warm-start ("Step or slew").

- As root, step the clock 5 s forward and back, and see the wall clock
  follow at once. Slew it by 0.5 s each way, and see the kernel hold
  the slew. In `conformance/guestclock`: `TestStep` and `TestSlew`.
- Boot a guest whose installed `wb-guestd` has another build version
  than its tools disk. See the hello answer with the disk's version,
  and the wait for the disk and the update logged. Boot it again, and
  see `tools disk is current` logged.
- In a boot without the tools disk, attach a disk image whose volume is
  named `wb-guest-tools` as a user other than root, and start
  `wb-guestd` again. See the mount refused with
  `guestd-tools-untrusted` and nothing installed. Attach it as root,
  read-only, and see `wb-guestd` install its programs and run the new
  one under the same process ID.

### Data volume

Spec: S06-05-disks ("Mounting the data volume"), SEC08-proj-isolation,
SEC11-root-gains-nothing, NFR06-explained-refusals.

Unit tests in `internal/guestd`, with a fake `diskutil`, `statfs` and
`lstat`:

- `TestMountFresh`: the mount of a fresh volume unmounts it from
  `/Volumes`, mounts it at `/private/var/wbhomes`, and claims its root,
  with the owner it had before the claim logged.
- `TestMountWaitsForVolume`: the guest mounts a volume that appears
  after 5 s.
- `TestMountVolumeNeverAppears`: a volume that never appears is
  refused with `data-volume-missing` after 30 s.
- `TestMountRefusals`: an encrypted volume, one whose report doesn't
  say, a failed unmount or mount, a mount point that is a link or owned
  by a user, and a failed claim are each refused with their rule,
  logged with what was found.
- `TestMountAgain`: a second mount, also after a restart of
  `wb-guestd`, only checks the volume it finds mounted, and refuses
  another UUID with `data-mount-volume`.
- `TestMountAgainFirstMount`: without `first_mount` the root isn't
  claimed. With it, a volume found mounted is claimed once its mount
  passes the check, and not when it fails.
- `TestMountDeadline`: the whole mount runs under a deadline of 40 s,
  between the 30 s wait and the host's 45 s, and a deadline that passes
  ends the wait with `data-volume-missing`.
- `TestCheck`: after a mount, the check before a session refuses with
  `data-mount-flags` without each of `nosuid`, `nodev`, `nobrowse` or
  ownership, with `data-mount-volume` for another volume or none, with
  `data-volume-encrypted` for an encrypted one, and with
  `data-mount-root` for a root that is a link, a file, or has another
  owner or mode.
- `TestCheckPending`: before any mount the check refuses with
  `data-mount-pending`.
- `TestSameMount`: the mount `fstatfs` reports of an opened root passes
  only when it is the one the last check passed. Another mount, a check
  that failed since, or no check is `data-mount-volume`.
- `TestMountDataRPC`: `MountData` answers each refusal with
  `FAILED_PRECONDITION` and one `ErrorInfo` whose reason is the rule,
  and a UUID of the wrong form with `INVALID_ARGUMENT` and
  `guestd-mount-invalid`.
- `TestMountRulesListed`: the rules of `wb-guestd` are the list
  `wb-hostd` takes.

The host side is under "Guest link" in C11-host-daemons.

In the guest, after `wb-hostd` had `wb-guestd` mount the volume, in
`conformance/datamount`:

- As a standard user and as root, see the mount carry every flag, the
  volume reported unencrypted, and the check pass (`TestMounted`).
- As root, copy the test binary onto the volume as a setuid-root file,
  run it as the standard user, and see it report that user's UID as
  its effective UID. The same copy in `/private/var/tmp` must report
  root, or the test fails (`TestSetuidIgnored`).
- As root, remount the volume with `mount -u -o suid`, and see the
  check refuse with `data-mount-flags`. Set `nosuid` back, and see it
  pass (`TestRemountRefused`).
- Boot a VM with a fresh data disk, and see the volume mounted by its
  UUID and its root before the claim logged. Stop and start it, and
  see it mounted again without a claim. Run `wb vm reset-data`, and
  see the next boot claim the root again.

### Project users

Spec: S06-03-image-updates ("Creation"), SEC08-proj-isolation,
SEC11-root-gains-nothing, SEC12-least-privilege.

Unit tests in `internal/guestd/users`, with a fake directory and volume
root:

- `TestNewUser`: a new user gets its group, then its user record, then
  its home, with `GeneratedUID` first and the IDs last in each record.
- `TestSameUser`: a user whose records and home match is kept, with
  nothing written.
- `TestRecreate`: a home on the data disk with no records, as after a
  new system disk, gets its records back, and the home stays as it is.
- `TestPartialRecord`, `TestMissingGroupRecord`, `TestMissingHome`: a
  record that stopped halfway, a missing group record and a missing
  home are completed.
- `TestConflicts`, `TestConflictNewUser`: a record with another value,
  an alias, an `AuthenticationAuthority`, a shadow hash, a member or a
  nested group is refused with `project-user-conflict`. So is another
  record that holds one of the user's names, IDs or `GeneratedUID`s,
  in any case, or nests the project group. Each is refused before any
  write and logged with the attribute, and the request's other users are
  still made.
- `TestHomes`, `TestHomeOfKeptUser`: a home owned by another UID is
  `project-home-other-owner`. A link, a file, another group, another
  mode or a special bit is `project-home-invalid`.
- `TestIDsOwnEntry`: a UID or GID that owns another entry of the
  volume's root is `project-ids-own-entry`.
- `TestRootCheckRefused`: a root that fails the caller's check refuses
  each user with the check's rule, and a request without a check with
  `project-home-root`, before any write.
- `TestRootRefused`, `TestDirectoryFailures`, `TestLockedRecord`,
  `TestDeadline`: a root that can't be read, a failed list, read or
  write, a write the directory didn't keep, a home path macOS locked
  once the IDs were set, and a request past its deadline each refuse
  with their rule.
- `TestInvalidUser`, `TestCheckRequest`: a value outside the allocation
  is `project-user-request-invalid` with nothing read, and a request
  with no user, too many, a UID or name twice, or a `GeneratedUID` that
  two users share is refused as a whole.

On the guest's own file system, in `internal/guestd/users`:
`TestRealHomeMade` (mode 700 under a umask of 0), `TestRealHomeLink`,
`TestRealHomeMode` and `TestRealIDsOwnEntry`. In
`internal/platform/guestusers`, `TestRootMkdirUndone` sees a home whose
`fchown` failed removed again, and `TestRootEntriesLongName` sees a
name of 300 bytes listed. Over a socket pair, in `internal/guestd`:
`TestEnsureUsersRPC`, `TestEnsureUsersOneBadUser` (one invalid user
refused, the others made), `TestEnsureUsersBeforeMount` (refused with
the data mount check's rule), `TestEnsureUsersRootOnOtherMount`
(`data-mount-volume`) and `TestEnsureUsersRequestRefused`, each read by
`wb-hostd`'s own check of the answer.

With the work VM, `TestC09GuestUsersVM` in `cmd/wb-hostd` (needs-vm,
needs-host): give the data disk two project users in `state.db`, and
see `wb-hostd` log both as ensured on a new link, after a reboot, and
after the system disk is cloned anew while the data disk stays. It logs
how long the guest took for each request, which the 50 s deadline for
128 users is judged by.

As root in the guest, after `wb-hostd` had `wb-guestd` make at least two
project users, in `conformance/guestusers`:

- `TestNoSudo`, `TestNotAdmin`, `TestNoSecureToken`: no project user
  has a `sudo` rule, `admin` membership or a secure token.
- `TestIDs`: each user's `id` shows its own UID and GID, and no group
  but its own, `everyone` and `localaccounts`. Each record has the
  password `*` and no `AuthenticationAuthority` or shadow hash.
- `TestHomes`, `TestHomesApart`: each home is a directory of mode 700
  owned by its user, and one project user can't list another's.
- `TestEnsureKeepsAndRefuses`: `EnsureUsers`' code, with every write
  refused, keeps each user, and refuses one with another
  `GeneratedUID` with `project-user-conflict`.

### The toolchain prefix

Spec: S06-01-image-layers, "Toolchain prefix", X20-shared-homebrew.

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

Spec: S06-09-session-lifecycle.

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

### Leftover processes

Spec: S06-10-leftover-processes, S06-11-reconnect-and-recovery ("Answers
to cleanup records"), SEC08-proj-isolation, SEC11-root-gains-nothing.

Unit tests of the host side in `internal/hostd/kills`, with a fake
clock:

- `TestKillCleanAccepted`: a clean result of a kill that was sent is
  accepted and logged.
- `TestKillAcceptanceRules`: a project ID of the wrong form, a kill ID
  never sent, one sent for another project, in another VM or in an older
  generation are each dropped with their rule, and the kill is still
  pending.
- `TestKillReplyForAnotherKill`: an answer that names another kill than
  its request is dropped.
- `TestKillAnotherGeneration`: a kill of one generation takes no result
  in the next.
- `TestKillLateResult` and `TestKillMissingResult`: a result after the
  7-minute deadline is dropped, and a kill with no result counts as
  processes left at its deadline.
- `TestKillProcessesLeft`: a result of processes left refuses sessions,
  learn mode and saves, logs the count capped at "more than 1000", and
  asks for a cold boot. Only a new generation clears it.
- `TestKillColdStop` and `TestKillColdStopAfterDeadline`: a cold stop
  closes the pending kills with its cause, and a kill past its deadline
  before it still counts as processes left.
- `TestKillBeginOldGeneration` and `TestKillIDs`: no kill is sent to a
  generation that a newer one or a cold stop ended, and kill IDs are
  never 0 and never repeat.
- `TestKillResultNewerGeneration`: a result for a generation newer than
  the slot's is dropped, and leaves the slot's kills pending.
- `TestKillResultAgain`: a result again for a kill is acknowledged and
  logged with the verdict kept, and one of processes left still counts.
- `TestListRefused` and `TestListAnswers`: a reconnect list with more
  records than project users, or two with one kill ID, is refused whole.
  Each other record gets "acknowledged" or "end".

In `internal/guestlink`, `TestCheckKill` and `TestCountLabel`: an answer
or record out of the contract's bounds is refused with
`guest-users-reply-invalid`.

In `internal/hostd/guestconn`, with a fake `wb-guestd`:

- `TestFirstConnectionKills`: the first connection of a VM generation
  sends a kill for every project user, each with its own ID, and session
  starts wait for them. A reconnect sends none again.
- `TestProcessesLeftRefusesSessions`: a result of processes left
  refuses session starts and saves until the next generation.
- `TestKillUser`: a session of a project is refused while a kill of its
  user is pending. `KillUser` returns `ErrKillNotSent` for a canceled
  call, after a cold stop, and for a generation that doesn't run.

In `internal/hostd/vm`, `TestOnEnded`: each run's end reaches the kill
tracker with its cause.

### The admin path

Spec: S15-05-admin-path.

- `wb vm admin` without a terminal, and with a wrong answer to its
  question, is refused with `admin-no-terminal` and
  `admin-not-confirmed`, and `wb-hostd` opens no connection.
- With a session running, and before first contact's rotation has
  ended, `wb-hostd` refuses with `admin-sessions` and
  `admin-before-rotation`.
- After the mark, a session start, a debug shell and `wb vm suspend`
  are refused with `vm-admin-used`, also after a `wb-hostd` crash
  between the mark and the connection. The next cold start replaces the
  system disk and clears the mark.
- An ask for `wb-guestroot`'s port from anything but the admin service
  is refused with `admin-port-unrequested`.
- In a guest, a project user can't bind `wb-guestroot`'s port while it
  restarts, and a connection from the guest's own CID is closed. The
  root shell opens while `wb-guestd` is stopped.
- Each use writes the two 5019 events.

**Status:** Draft
