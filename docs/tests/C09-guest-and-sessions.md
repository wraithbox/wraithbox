# C09 - Guest and Sessions

**Purpose:** The test cases for what a project user can reach inside
the guest, `wb-guestd`'s update and the guest clock, the shared
toolchain prefix, the guest admin password, and the session lifecycle.

**Verifies:** S06-01-image-layers ("Toolchain prefix"),
S06-03-image-updates ("Creation"),
S06-05-disks ("Mounting the data volume"),
S06-06-devices-and-warm-start ("Time and sleep"),
S06-08-projects-and-sessions ("Removing a home"),
S06-09-session-lifecycle,
S06-10-leftover-processes,
S06-12-guest-agent ("Update at start"),
S13-guest-confinement, S15-04-guest-admin, S15-05-admin-path

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
- The parser of a cleanup record file in `wb-guestd`,
  `FuzzCleanupRecordFile` in `internal/guestd/cleanup`: guest root can
  write the records' directory, and an accepted record has a kill ID
  other than 0, a project ID of its form and a count of at most 1001,
  and formats back to the same bytes (S06-10-leftover-processes, "Kill
  IDs and records").

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

### Locking, killing and removing

Spec: S06-08-projects-and-sessions ("Removing a home"),
S06-10-leftover-processes, S13-01-process-hardening ("Locked users",
"When the link drops", "Per-user launchd services"),
SEC08-proj-isolation, SEC11-root-gains-nothing.

Unit tests of the loops in `internal/guestd/procs`, with a fake process
table and clock:

- `TestKillClean`, `TestKillBootsOutWithNoProcess`: a kill ends every
  process whose UID or real UID is the user's, and no other, and runs
  `launchctl bootout` at least once.
- `TestKillRespawn`: a KeepAlive agent that launchd starts again is
  ended in a later round.
- `TestKillRoundBound`, `TestKillTimeBound`, `TestKillCap`: a process
  that survives is reported after 10 rounds or 30 seconds, logged with
  `project-user-kill-incomplete`, at most 5 names escaped and cut, and a
  count capped at 1001.
- `TestKillListError`, `TestKillCanceled`, `TestKillZombie`: a process
  table that can't be read counts as 1001 left, a canceled kill as at
  least one, and a zombie as none.
- `TestStopAll`, `TestStopAllAgain`: the stop loop stops every running
  process of the project range, and not root's or `nobody`'s. It
  records the ones it stopped itself, apart from those already stopped.
- `TestStopAllBound`, `TestStopAllListError`: a process that stays
  running is logged with `project-users-stop-incomplete`.
- `TestStopAllExclude`: while a removal empties a home, the stop loop
  leaves a process whose UID and real UID are both that user's running,
  and stops it again once the removal releases it.

Unit tests of the cleanup records in `internal/guestd/cleanup`:
`TestFormatParse`, `TestParseRefuses` and `TestFileName` (one format,
canonical numbers, the bounds), `TestStoreWriteList` (one record per
user, the newest), `TestStoreListSkips` (a bad file is logged and
skipped), `TestStoreOpenRefuses` (a records' directory that isn't a directory of
root's with mode 700 and no special bit, or opens through a link, is
refused with `cleanup-record-dir`) and `TestStoreWriteFails`.

Unit tests of the lock and the removal in `internal/guestd/users`, with
a fake directory, volume root, kill loop and child:

- `TestLocks`, `TestLocksKill`: every user is locked at start, only an
  unlock of a known user unlocks it, and a kill locks it again. An
  unlock while the kill runs is refused with
  `project-user-kill-running`.
- `TestRemoveKnown`: a removal kills, empties the home as the user,
  removes it, gives the record the shell `/usr/bin/false`, then
  disables and hides it, in that order. The record stays, `EnsureUsers`
  refuses the user afterwards, and a second removal answers removed.
- `TestRemoveAfterRestart`, `TestRemoveAfterDiskSwap`: with nothing
  known, a record with the request's IDs and home, or with no record a
  home the UID owns, is removed.
- `TestRemoveRefuses`, `TestRemoveRootCheck`: a request out of form, a
  known user of another project, a record or home of another UID, a
  failed directory read and a root off the data mount are refused before
  any kill or child, each logged with its rule. A known user of another
  project stays unlocked.
- `TestRemoveHomeChecks`: a home that is a link, a file or another
  UID's starts no child, and retires nothing.
- `TestRemoveProcessesLeft`: with processes left, no child starts and
  nothing else changes.
- `TestRemoveChild`, `TestRemoveChildDeadline`: a child that didn't
  start, ended on a signal, or ran past its 15 seconds, refuses with
  `project-home-child-failed` and retires nothing. A home the child
  couldn't empty stays, logged with `project-home-left`.
- `TestRemoveRetireFails`, `TestRemoveRunning`: a failed retirement
  refuses, and `EnsureUsers` refuses the half-retired record. A
  removal, `EnsureUsers`, kill or unlock of a user whose removal runs
  is refused with `project-user-removal-running`.

On the guest's own file system, in `internal/platform/guestusers`:
`TestRootRmdir`, `TestDirWriteRead`, `TestDirNames`, `TestDirReadRefuses`
(a link, a FIFO, a directory or a file over the cap isn't read),
`TestRootOpenDirLink`, `TestEmptyHomeRefuses` and `TestEmptyHomeArgs`
(the first pass runs chmod only on a directory the user closed). In
`internal/platform/guestprocs`, `TestListsOwnProcess` and `TestSignal`
(a PID below 2, or one whose start time changed, gets no signal).

Over a socket pair, in `internal/guestd`, each read by `wb-hostd`'s own
check of the answer where it has one:

- `TestUnlockUserRPC`: an unknown user stays locked with
  `project-user-unknown`.
- `TestKillUserRPC`, `TestKillUserLeft`: the answer and the cleanup
  record hold the kill's result.
- `TestKillUserRefused`: a kill ID of 0, a project ID out of form, a UID
  out of range (`guestd-kill-invalid`) and a known user of another
  project (`project-user-unknown`) are refused before any kill.
- `TestKillUserDuringRemoval`: a kill and an unlock of a user whose
  removal runs are refused with `project-user-removal-running`, and the
  kill boots nothing out.
- `TestKillUserRecordRefused`: a record that can't be kept, before the
  data mount or in a directory guest root left owned by a user, fails
  the call with its rule.
- `TestRemoveUserRPC`, `TestRemoveUserNotRemoved`: a removal answers
  removed, or processes left, or refused with the data mount check's
  rule.
- `TestLinkDropStops`: the end of the host's connection runs the stop
  loop.

As root in the guest, in `conformance/guestusers`, each only with its
variable naming the UID of a project user it may end:

- `TestKillUser` (`WB_CONFORMANCE_KILL_UID`): one kill ends a KeepAlive
  LaunchAgent, a detached process with SIGHUP ignored and a process in
  its own session. None
  is back 5 seconds later, and `launchctl print` of the user's domain
  fails or doesn't name the agent.
- `TestRemoveUser` (`WB_CONFORMANCE_REMOVE_UID`): a removal removes the
  home with a file the user made in it, and a directory the user set to
  mode 000 with a file in it, keeps the record, and
  `pwpolicy -authentication-allowed` reports the account disabled.

With the work VM (needs-vm, needs-host), by hand:

- `kill -9` of `wb-guestd` with a session running: the new process
  stops the project users' processes before it serves.
- A link drop with a session running stops its processes, and one
  stopped before the drop is recorded as such.
- `wb project rm` of a project whose home holds a file owned by root:
  the home stays, logged with `project-home-left`, and the removal is
  answered removed.
- A system disk swap with a removed project: the retired record is gone
  with the old system disk, and the home is still gone.

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

The session helper, unit tests in `internal/guestd/shell` on darwin and
linux. They run unprivileged, with no user switch and a test program in
place of `zsh`:

- See the helper lead the POSIX session, and the program lead its own
  process group, which is the PTY's foreground group, with the PTY as
  its controlling terminal.
- Write a `Ctrl-C` byte and see the program get `SIGINT`. Resize and
  see `SIGWINCH` with the new size.
- Have the program stop itself, and see one stopped frame. Resume it
  and see it run on.
- Exit with 0 and with 3, and kill the program with `SIGKILL`. See each
  reported, interactive and not, with the output before the exit frame.
- Hang up an interactive shell and see the program get `SIGHUP`. With a
  program that ignores it, see it killed after the hangup delay, and
  another user's process in the same POSIX session logged and left.
- Without a PTY, see standard input end with EOF, a closed standard
  output give `EPIPE`, and standard error on its own channel.
- Kill the helper and see `session-helper-gone` and the session killed.
  With a helper failure and a kill that leaves processes, see
  `shell-hangup-kill` and the helper's rule in the log.
- Exit cleanly after a child left the session with `setsid`, and see
  the child logged with `shell-process-outside-session` and left.
- Send a report out of form, one after the exit and an exit code of
  256, and see `session-helper-report` and the session killed. See the
  reader refuse a continued report first, two stops in a row, and a
  stop past 1024.
- Have a helper report the exit and run on, and see it hung up and
  killed before the exit frame. With a kill that leaves processes, see
  the kill logged `incomplete` and a `shell-hangup-kill` failure in
  place of the exit frame.
- Make the process table unreadable during the kill and see the kill
  retry. With a table that never reads, see the kill logged
  `incomplete` and a `shell-hangup-kill` failure.
- Start a shell while a stop loop runs and see it wait for the loop.
  See a new connection leave a running stop loop to its bounds.
- Resume a shell and see its processes dropped from the stop loop's
  set.
- Send each refused start: an id, size, user or environment out of
  form, an unknown, privileged or locked user, a data mount that fails
  its check, an id in use, and a helper that doesn't start. See each
  refused with its rule and nothing started.
- See the program's exact environment, and the replica owned by the
  user with mode 0600.
- End the stream on a live connection and see a hangup. End it with the
  link and see the shell left to the stop loop.
- `FuzzHelperReport`: arbitrary report bytes never give an exit code
  outside 0 to 255, a signal outside 1 to 64, or a report after the
  exit.

Debug shells in V1-M2 (S06-08-projects-and-sessions, "Debug shells in
V1-M2"), unit tests of `wb-hostd` in `internal/hostd/shells`,
`internal/hostd/hostservice` and `internal/hostd/guestconn`, with a fake
guest:

- Open a shell, run it to its exit, and see `wb` get the opened message
  and the exit, and the project user unlocked, then locked and killed
  once (`TestShellOpenAndExit`). Close `wb` and see the hangup and the
  kill (`TestShellClientGone`, `TestRelayClientClose`).
- Send an open out of form, for an unknown project, before the data
  mount, refused by the session start check, for a stopping VM, past 64
  shells and in refusing mode, and see each refused with its rule
  (`TestShellStartInvalid`, `TestShellProjectUnknown`,
  `TestShellMountPending`, `TestShellSessionRefused`,
  `TestShellVMStopping`, `TestShellSessionCap`, `TestShellRefusingMode`).
- Send guest frames out of form, an exit code of 256, signals 0 and 65,
  standard error that wasn't asked for, a frame over its cap, a frame
  after the exit, and repeated stops, and see each end the shell with
  its rule (`TestCheckFrame`, `TestRelayFailures`, `TestRelayAfterExit`,
  `TestRelayStoppedRepeat`, `FuzzGuestShellFrame`).
- Have a guest ignore the hangup, and see the stream cancelled after
  the hangup delay and its margin (`TestRelayHangupTimeout`).
- Drop the link, and see the shell lost and its user killed on the next
  link only (`TestRelayLost`, `TestLostKillsOnNextLink`,
  `TestLostKillRetried`).
- Stall one `wb`, and see another shell of the VM run to its end
  (`TestStalledClientDoesNotStallOthers`).
- Run `wb vm stop` with a shell open, and see it refused naming the
  shell (`TestVMStopNamesShells`).
- Fail the unlock, and see the shell refused and its user locked and
  killed (`TestShellUnlockFailed`). Hold the guest's stream past its
  open time, and see the shell end with `guest-shell-refused`
  (`TestShellOpenHeld`). Open a second project's shell in the isolated
  VM, and see it refused with `isolated-slot-taken`
  (`TestShellIsolatedTaken`, `TestRegisterIsolatedTaken`). End `wb`'s
  stream while its shell waits, and see `shell-client-gone` and nothing
  unlocked (`TestShellClientGoneWaiting`).
- End a shell while another of its project waits, so the kill is
  skipped, and see the waiting shell's release send it, and no kill
  without a skip (`TestReleaseSendsSkippedKill`,
  `TestReleaseWithoutSkipSendsNone`). Lose a shell after a relink, and
  see its user killed at once (`TestLostAfterRelinkKillsNow`). Fail a
  kill's send, and see it sent on the next link
  (`TestKillNotSentRetriedOnNewLink`). End a shell after close, and see
  no kill (`TestNoKillAfterClose`).
- Repeat stops, and see the first logged and the count logged at the
  end (`TestRelayStoppedRepeatsCounted`). Send frames after the exit,
  and see them dropped and the stream cancelled past 16
  (`TestRelayAfterLastFlood`). Close `wb` on the exit, and see no
  hangup (`TestRelayClientCloseOnExit`). Fail a send to the guest, and
  see the shell hung up (`TestRelayGuestSendFails`).

`wb shell` itself, in `internal/projectcmd` with a fake `ShellService`:
see it refuse an argument with exit 2, an unregistered project, a host
without terminal support and a charset other than UTF-8 with 255, and
print `wb-hostd`'s refusal with its rule (`TestParseShell`,
`TestShellNotRegistered`, `TestShellTerminalUnsupported`,
`TestShellCharsetRefused`, `TestShellHostdRefusal`). Send `SIGINT`
while the open waits, and see the call cancelled and `wb` exit with 255
and `shell-open-signal` (`TestShellSignalDuringOpen`).

`TestC09ShellVM` in `cmd/wb-hostd`, needs-vm and needs-host, opens
real shells in the work VM through `wb-hostd`, piped, for two projects
it adds. It checks:

- the project user, and that it can run the installed `wb-guestd`;
- the exact environment, with the host's `TERM` and `LANG` unchanged;
- `exit 3` gives 3 and `kill -9 $$` gives 137;
- 8 MiB of piped input to a program that doesn't read it;
- `ls` of the other project user's home fails with `Permission
  denied`, and `sudo -n true` fails;
- `echo 'echo hi; exit 4' | wb shell` prints `hi` and gives 4;
- `yes` into a reader that closes after one line, as `head -1` does,
  ends promptly with the shell's status;
- `wb vm stop` is refused, naming the shell, while a shell is open.

What needs a terminal stays attended, run by hand with
`wb shell --project P` in the work VM:

- the terminal's title and the label line;
- `tty` names a PTY, and `ps -o sess,pgid,uid -p $$` shows the shell
  as its session's leader, as the project user;
- after a window resize, `stty size` shows the new size;
- Ctrl-C ends `sleep 100` and leaves the shell;
- Ctrl-Z stops `sleep 100`, and `fg` resumes it;
- `nohup sleep 1000 &` and `setsid sleep 1000 &` before `exit`, and
  no process of the project user left after the shell ends;
- the second conformance case of C10, in a real terminal.

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
  `admin-before-rotation`, and writes each refusal as a 5019 event.
- With the isolated slot lent to the build VM, `wb vm admin isolated`
  is refused with `admin-slot-lent`, and `wb-vmd-install` is never
  asked for the admin port.
- A session start racing `wb vm admin` either runs and makes it refuse
  with `admin-sessions`, or comes after the mark and is refused with
  `vm-admin-used`. The two never run together.
- After the mark, a session start, a debug shell and `wb vm suspend`
  are refused with `vm-admin-used`, also after a `wb-hostd` crash
  between the mark and the connection.
- When writing the mark or its 5019 event fails, no connection opens.
  When the connection fails after the mark, with `peer-version-mismatch`
  or a `wb-guestroot` that doesn't answer, the mark stays.
- While the shell is open, the idle period doesn't end, a lend of the
  isolated slot waits, and `wb vm stop` and `wb vm suspend` from
  another terminal are refused with `vm-admin-open`.
- After the shell ends, the idle period's end and a lend stop the
  marked VM cold, and no saved state is written. A start of a marked
  VM with a saved state placed in the test deletes it, replaces the
  system disk, boots cold, and clears the mark.
- A host stop or a `wb-hostd` crash during an open shell leaves the
  mark. The next `wb-hostd` start writes the missing end event with
  the cause unknown.
- `wb vm admin` prints the warning of T19-pasted-login-code before the
  shell opens.
- An ask for `wb-guestroot`'s port from anything but the admin service
  is refused with `admin-port-unrequested`.
- In a guest, a project user can't bind `wb-guestroot`'s port while it
  restarts, and a connection from the guest's own CID is closed. The
  root shell opens while `wb-guestd` is stopped.
- Each use writes the two 5019 events, the end event with its cause.

**Status:** Draft
