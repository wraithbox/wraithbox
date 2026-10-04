# X02 - Warm start

**Purpose:** How long a macOS guest takes to restore from saved state with a vsock device and a file-handle network attachment, and whether one saved state can be restored more than once.

Brief: B16-warm-start

## For review

- **Decides:** a saved state restores in about 4 s and can be restored
  any number of times, each time from a fresh APFS clone of the state,
  both disks and the auxiliary storage, under the conditions below,
  which S06-vm-lifecycle "Warm start" now states.
- **You are approving:** the answer "yes, with conditions", the
  measurements, and the S06-vm-lifecycle text. NFR01-startup fits for
  an idle guest. Whether it fits for a working guest waits on
  X18-vsock-handoff (I29) and the time-to-prompt benchmark of
  S11-verification-and-spikes (see "What it means for the specs").
- **Controls touched:** none. No code of ours ran in the guest. The
  host did not mount a guest disk (B44-data-disk-durability).
- **Assumed:** that restore, like save, fails while the host is locked
  (not tested). That a guest process listening on vsock answers the
  moment the guest kernel answers on the network (not measured, see
  "What was not measured").
- **Open decisions:**
  1. What an idle VM does when its idle period ends while the host is
     locked, since it can't be saved then. Recommended: it keeps
     running and is saved after the next unlock, so detached sessions
     survive (B46-session-lifecycle). S06-vm-lifecycle says so, pending
     this decision.
- **Brief:** B16-warm-start

## Question

From X00-index: measure time to restore a macOS guest from saved state
with a vsock device and a file-handle network attachment. Can a saved
state be reused (cloned with its disks) more than once?

## Answer

**Yes, with conditions.** The configuration that S06-vm-lifecycle
describes passes `validateSaveRestoreSupport`, saves in about 3 s at
4 GiB, and restores to a guest that answers on the network in 3.7 s
(p50) and 3.9 s (p95) when the state file is not in the host's page
cache. One saved state was restored 30 times from fresh clones. Every
restore worked that kept the saved identity while no other VM with
that identity ran (conditions 2 and 3). The conditions:

1. **A fresh clone for every restore.** Virtualization does not check
   that the disks match the state. A state restored a second time onto
   disks the guest had already run from for 10 s came up and ran
   without an error, so the guest's memory can be older than its disks
   and nothing reports it. A state is only restored onto the disks it
   was saved with, or onto APFS clones of them taken together with the
   state.
2. **The same identity.** A restore with a different machine
   identifier, or with a different MAC address on the network device,
   fails with "invalid argument".
3. **One running VM per identity during a restore.** A restore fails
   while another VM with the same machine identifier runs: 4 failures
   in 4 tries. Three of the tries were pairs of concurrent restores of
   one state, where one restore of each pair failed. The fourth was a
   restore next to a cold-booted VM with the same identifier. Cold
   boots of two clones that share an identifier both run, and clones
   of states with different identifiers restore side by side. The work
   VM and the isolated VM have their own identities, so this does not
   affect S06-vm-lifecycle's two slots.
4. **The host must be unlocked to save.** The save file is encrypted
   with a key that Virtualization creates in the Secure Enclave when it
   saves, with the access class "only while unlocked, only on this
   device". With the screen locked, the save failed with "permission
   denied", and the log shows the key creation refused
   (`errSecInteractionNotAllowed`). After an unlock, the same binary on
   the same bundle saved 21 times without an error. The error text and
   the lock state are in `results/save-locked-run.md` on the spike
   branch. That run's own output file was overwritten by the run after
   the unlock, so the error text there is transcribed from the session.
5. **Bound to the host.** Apple's header for
   `restoreMachineStateFromURL` says the file must have been written on
   the same host, and that a restore can fail after a host software
   update. A saved state can't be part of an image or move between
   machines. A restore that fails for one of these reasons deletes the
   state and cold boots. A host update gives "invalid argument"
   according to Apple's `VZVirtualMachine.h`, which this spike didn't
   test. An identifier in use gives the same error, as measured under
   condition 3. The host tells the two apart by whether a VM with
   that identifier runs.

## Measurements

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1. Guest: macOS 27.0.1
(26A434), installed from Apple's restore image and left at Setup
Assistant. 4 vCPU, a 64 GiB system disk and an 8 GiB data disk (both
sparse), `VZVirtioSocketDevice`, `VZFileHandleNetworkDeviceAttachment`
on a datagram socket pair, entropy, one display. 20 runs per row.

"Answering" is the guest kernel replying to an ICMPv6 echo that the
host writes into the file-handle attachment. The clock starts before
the `VZVirtualMachine` is created, so restore times include building
the configuration (7 to 15 ms) and `resume` (about 120 ms). "Fresh
clone" means the bundle was APFS-cloned just before the run, so its
files are not in the host page cache, which is the case after an idle
period.

| Measurement | 4 GiB p50 | 4 GiB p95 | 8 GiB p50 | 8 GiB p95 |
|---|---|---|---|---|
| Restore to answering, fresh clone | 3.71 s | 3.92 s | 4.11 s | 4.58 s |
| Restore to answering, state just saved | 1.97 s | 2.02 s | 2.39 s | 2.47 s |
| Cold boot to answering, fresh clone | 5.04 s | 5.38 s | | |
| Cold boot to answering, same files | 4.76 s | 5.08 s | 5.02 s | 5.92 s |
| `pause` | 7 ms | 13 ms | 7 ms | 16 ms |
| `saveMachineStateTo` | 2.94 s | 3.20 s | 4.78 s | 5.16 s |
| Saved state size | 1.41 GB | 1.41 GB | 1.41 GB | 1.41 GB |

- The state file holds the memory the guest has used, not the memory
  it was given: 1.41 GB at both 4 and 8 GiB for an idle guest. A guest
  running Claude Code and builds writes a larger file and restores
  more slowly.
- The network is back the moment the guest runs: the first echo reply
  comes within 11 ms of `resume`, on a new socket pair that the host
  passes in the configuration of the restored VM.
- A cold boot first answers at about 5 s, the moment the guest's DHCP
  client also starts. That is early in boot: the rest of the guest's
  startup still runs after it. A restore saves about 1.3 s at that
  point, and the guest it returns has finished starting up.
- Concurrent restores of clones of two different states (4 GiB each)
  took about 5 s each.
- Installing macOS from the restore image took 256 s. The system disk
  then used 26 GiB.

## What was not measured

**A guest process answering on vsock.** Nothing in a stock guest
listens on vsock: 60 s after boot, connects to all 65535 ports were
refused. Getting a listener into the guest means writing it to the
guest's disk from the host, which B44-data-disk-durability allows only
before the image's first boot, or going through Setup Assistant by
hand, which needs the maintainer.

This spike wrote such a host-side injector: `guest/inject.sh` on
`spike/x02-warm-start` at commit 2abe903. It was meant to attach the
guest's system disk image with `hdiutil attach`, mount the guest's
Data volume with `diskutil mount`, and copy the probe and a
LaunchDaemon into it. The permission check of the agent's session blocked it before
it ran, and commit 4979ba1 removed it. No guest disk was mounted.

X17-image-build
answers how `wb-guestd` gets into the guest, and X18-vsock-handoff
already tests vsock connections across save and restore (its step 4),
so the time from restore to `wb-guestd` answering is measured there.

A vsock connect from the host to an unbound port is refused within
30 ms of `start`, before the guest kernel has booted. The refusal comes
from the host side of the device, so it says nothing about the guest.

**Restore while the host is locked**, and restore after a host restart
or a host update. Restore while locked is assumed to fail as saving
does, measured since in X18-vsock-handoff. NFR01-startup already excludes the first boot after a host
restart.

**A working guest.** Every state here came from a guest idle at Setup
Assistant, with 1.41 GB of memory in use. Reading the state file is
most of the restore time (1.97 s with the file in the host's memory,
3.71 s without), so a guest running Claude Code and builds, with more
memory in use, restores more slowly.

## What it means for the specs

- **S06-vm-lifecycle "Warm start"** now states the conditions above,
  and what a failed restore does: a permanent failure (another host, a
  host update) deletes the state and cold boots, and a temporary one
  (identifier in use, locked host) keeps the state (changed in this
  pull request).
- **NFR01-startup** fits for an idle guest, and its text is unchanged.
  A suspended VM has 7 s to the Claude prompt. The restore of an idle
  guest takes 3.7 s to 4.1 s (p50) or 3.9 s to 4.6 s (p95), which
  leaves about 2.5 s for the vsock connection, `wb-guestd`, and
  starting Claude Code. X18-vsock-handoff (I29) measures the time
  from restore to `wb-guestd` answering on vsock, in its save and
  restore step. The time-to-prompt benchmark of
  S11-verification-and-spikes measures the restore of a working
  guest's state. If either one breaks the
  budget, NFR01-startup or the warm start design changes then.
- **NFR03-footprint**: each suspended VM keeps a state file of about the
  guest memory in use (1.4 GB idle) next to its disks.
- **S04-architecture**: the VM bundle already holds "disks, machine
  identity, saved state".

## Spike code

Branch `spike/x02-warm-start`, at
[aadb90a](https://github.com/wraithbox/wraithbox/tree/aadb90accc5e59822262431c3248fb00c3ef136a/spikes/x02-warm-start):
a Swift command-line tool that installs, boots, saves, and restores the
VM, the clone and series scripts, and the raw results in `results/`
with their p50 and p95 in `results/stats.txt`.

**Status:** Answered 2026-10-04: yes, with conditions. Decided 2026-10-04 (I16): an idle VM keeps running while the host is locked and is saved after unlock
