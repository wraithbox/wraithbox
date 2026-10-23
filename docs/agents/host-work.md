# Work on the maintainer's Mac

Most work runs on any Mac with the repository checked out. Some work
needs a macOS guest, or the host itself: its Keychain, a signing
identity, the `wbtest` user, `sudo`, or a dialog only the maintainer can
answer. This page says which machine does what, how the labels mark it,
and what agents must not do on a host.

## Two machines

| Machine | Runs | Labels it takes |
|---|---|---|
| The maintainer's personal Mac | everything, attended or not | any, `needs-vm` and `needs-host` included |
| The maintainer's work laptop | code, specs, docs, CI chores | issues with neither `needs-vm` nor `needs-host` |

The work laptop doesn't run spike VMs, test users, Keychain
experiments or long VM test loads. One run at a time per machine.

The reference machine for performance and resource defaults is a 2022
M2 MacBook Air with 16 GB of memory and a 512 GB disk (NFR01-startup).
Check vCPU, memory and disk defaults against it. The performance
numbers in NFR00-index are goals, and no fixed benchmark rig comes
before Wraith Box works end to end.

### Setting up a second machine

- `gh auth status` shows `lsimons`. Agents trust only comments by
  `lsimons` and `lsimons-bot` (`working.md`, "Picking up an
  issue").
- A git identity for this repository, and the attribution lines from
  `AGENTS.md`.
- `mise trust && mise install`, then `mise run install` and
  `mise run ci`.
- Xcode for `swift build` and `swift test`. Host `xcodebuild` tests are
  `needs-host` work.
- The same Claude Code effort and auto-mode settings as the personal Mac.
- Claude Code's memory is per machine. Handoffs go in issues and run
  issues, never in memory.

## The labels

- **`needs-vm`:** the work boots a macOS guest. It runs on the personal
  Mac, one such builder at a time (`orchestration.md`, "A wave", step
  2), and macOS allows two guests per host (NFR05-two-macos-vms).
- **`needs-host`:** the work touches the host outside a worktree:
  Keychain items, team or identity signing, the `wbtest` user, `sudo`,
  TCC or signing dialogs, host Xcode tools, or a VM bundle or image
  outside `.scratch/`. It runs on the personal Mac in a session the
  maintainer attends. `/wave` never picks it. Removing a spike's
  bundles with `mise run spike:clean` doesn't make work `needs-host`
  (`spikes.md`, "VM bundles"), and neither do test bundles
  made through `mise run vm:*` ("Gotchas"). Booting one is `needs-vm`
  work.

A run merges an issue's pull request without the maintainer only when
the issue has neither label, nor `comp:wb-vmd` (`orchestration.md`,
"Runs").

An issue can have both. Triage sets them ("Triage" in `planning.md`).
They decide what a run may merge without asking (`orchestration.md`,
"Runs"), so a missing label has to fail closed. A builder that finds
its work needs either label stops and adds it. It comments on the
issue what it needs and why, and hands back with the commands for the
attended step written out.

## What agents don't do on a host

Outside a `needs-host` issue, in an attended session:

- No host `xcodebuild`, `xcrun simctl`, or `xcode-select`. They can
  wait for hours on a signing or Keychain dialog. Host Xcode setup
  (`sudo xcodebuild -runFirstLaunch`) is the maintainer's.
- The one exception is the `xcodebuild` in the bundle build, run only
  through `mise run app:bundle`, `build` or `ci`, without `WB_TEAM_ID`.
  Never run `xcodebuild` or `scripts/app-bundle.sh` directly. That
  build signs nothing in Xcode (`CODE_SIGNING_ALLOWED=NO`), and signs
  the bundle ad-hoc (S17-01-bundle). It writes its build output under
  `packages/wraithbox-swift/.build/`. It makes the guest tools disk
  image there, and attaches it read-only for its check. Outside the
  worktree, Xcode registers the built app with Launch Services and
  with the execution policy (`RegisterWithLaunchServices`,
  `RegisterExecutionPolicyException`), and may write to
  `~/Library/Developer/Xcode/DerivedData`: `xcodebuild` takes
  `-derivedDataPath` only with a scheme.
  Next to the project it writes `WraithBox.xcodeproj/project.xcworkspace`,
  which git ignores.
- No `WB_TEAM_ID` in an agent's environment, and no
  `mise run app:install`, which replaces `~/Applications/Wraith Box.app`.
- No `security` command, no signing with an identity, no Keychain reads
  or writes. Ad-hoc signing of a spike binary with
  `codesign -f -s - --entitlements spikes/vm.entitlements .scratch/<path>`
  is allowed, with the path written out. So is the same signing of
  `wb-dev-bundle` that `mise run vm:build` does.
- No `sudo`, no `su`, nothing run as `wbtest`.
- Never run a command for an agent that the permission system refused
  it. Report the refusal, and the maintainer decides.

## Gotchas

- **Signed binaries:** overwriting a signed binary in place with `cp`
  gets it killed at launch. Remove it first, then copy.
- **`wbtest`:** it can't read the maintainer's home, so stage its files
  in `/Users/Shared/<spike>/`. A `sudo -u wbtest` session needs
  `security unlock-keychain` first, and a Keychain item it reads needs
  `-T /usr/bin/security` on its access list. The maintainer runs these.
- **Logs:** `log` in zsh is a shell builtin. Use `/usr/bin/log`. On
  macOS 27, sandbox violations come from `sandboxd`
  (`eventMessage CONTAINS "Sandbox: <process>("`), and only some are
  reported.
- **VM bundles:** spike bundles are under
  `~/Library/Caches/wraithbox-spikes/x<NN>/` (`spikes.md`,
  "VM bundles"). Test bundles for `needs-vm` work go in the same cache,
  and agents and the maintainer make, clone, provision, stop and remove
  them only through `mise run vm:*`:

  ```bash
  mise run vm:install dev/base restore-images/<image>.ipsw  # never booted
  mise run vm:clone dev/base i<n>/guest                    # cp -c, APFS clone
  mise run vm:provision i<n>/guest [minutes]               # first boot: SSH user, Remote Login, NAT
  mise run vm:stop i<n>/guest
  mise run vm:clean i<n>
  ```

  The tasks run `wb-dev-bundle`, dev-only test tooling that `vm:build`
  signs ad hoc with `spikes/vm.entitlements` (S10-tech-stack, "Test
  tooling"). Paths are absolute in the cache or relative to it, and
  spelled byte for byte as `ls` shows them: APFS ignores case, so the
  tool refuses any other spelling. It refuses a path outside the cache,
  a symbolic link, `restore-images/` as a bundle, a change to a
  directory with a `KEEP` file or under one, a removal of a tree with a
  `KEEP` file anywhere in it, and a clone, provision or removal of a
  bundle whose VM runs. It logs
  each step and the rule of each refusal.
- **Provisioning:** `vm:provision` boots a bundle that `vm:install` made
  and that never booted, such as a fresh clone of it. macOS creates the
  user `wbdev` with a random password, which the tool writes to
  `ssh-password` in the bundle, and turns on Remote Login on Apple's NAT
  network. Only test guests in the spike cache get this. Product guests
  never do. The guest's address is its lease in `/var/db/dhcpd_leases`,
  for the MAC address the task prints. The task runs until the guest
  shuts down, `vm:stop`, or the time limit (60 minutes by default), so
  run it in the background.
- **Slots:** a clone of a `vm:install` bundle has the file names
  `wb-vmd` opens (S06-05-disks, "Bundle files"). An older spike bundle
  has `.img` names, which have to be renamed when its clone goes to a
  slot's bundle path. The needs-vm test of `wb-vmd` (`VMBootTests`) does
  that rename.
  Agents remove spike bundles with `mise run spike:clean x<NN>` or
  `mise run vm:clean`, never with `rm`.
