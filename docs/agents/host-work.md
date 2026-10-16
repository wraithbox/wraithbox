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
  `lsimons` and `lsimons-bot` (`orchestration.md`, "Picking up an
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
  maintainer attends. `/wave` never picks it.

An issue can have both. Triage sets them ("Triage" in `planning.md`).
They decide what a run may merge without asking (`orchestration.md`,
"Runs"), so a missing label has to fail closed. A builder that finds
its work needs either label stops and adds it. It comments on the
issue what it needs and why, and hands back with the commands for the
attended step written out.

## What agents don't do on a host

Outside a `needs-host` issue, in an attended session:

- No host `xcodebuild`, `xcrun simctl`, or `xcode-select`. On
  2026-10-08 an `xcodebuild -showsdks` waited two hours on a signing
  dialog. Host Xcode setup (`sudo xcodebuild -runFirstLaunch`) is the
  maintainer's.
- No `security` command, no signing with an identity, no Keychain reads
  or writes. Ad-hoc signing of a spike binary with
  `codesign -f -s - --entitlements spikes/vm.entitlements .scratch/<path>`
  is allowed, with the path written out.
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
  `~/Library/Caches/wraithbox-spikes/<spike>/` (I120). The maintainer
  makes APFS clones (`cp -c`) of a bundle. An agent asks for a clone,
  with the target path, and never clones or deletes a bundle itself.
  Hand the builder the clone's literal path.
