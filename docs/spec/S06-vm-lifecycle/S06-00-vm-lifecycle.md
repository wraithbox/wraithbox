# S06 - VM Lifecycle, Images and the Guest Agent

**Purpose:** How guest images are built, how VMs are kept warm, how
projects and sessions map onto guests, and what `wb-guestd` does.

**Requirements:** FR02-any-repo, FR05-parallel-sessions to FR07-toolchain-manifest, FR11-port-forward to FR14-ephemeral, SEC01-separate-kernel, SEC02-no-host-fs-share, SEC08-proj-isolation, SEC11-root-gains-nothing, SEC13-bounded-resources, SEC15-no-guest-disk-mount, NFR01-startup to NFR05-two-macos-vms.

**Tests:** C09-guest-and-sessions, C11-host-daemons, C06-model-api

Briefs: B16-warm-start, B20-guest-xcode, B42-trust-placement,
B43-vm-slot-admission, B45-claude-in-guest, B46-session-lifecycle,
B60-model-credential

## Design

This spec describes macOS guests on a macOS host, the v1 target. The
structure (image layers, sealing, work and isolated VMs, one guest user
per project, worktree per session, the duties of `wb-guestd`) is the
same for every guest OS. What differs per guest and per host is listed
in S12-platforms.

### Pages

| Page | What it covers |
|---|---|
| S06-01-image-layers | The layers of a guest image, and the toolchain prefix that project layers install into |
| S06-02-image-build | `wb image build`: the build VM, provisioning, terminfo, sealing and storage |
| S06-03-image-updates | Replacing a VM's system disk, recreating project users, and the guest admin password per VM |
| S06-04-vms-and-placement | The work VM and the isolated VM, Xcode, and which VM a project runs in |
| S06-05-disks | The data disk: durability, and mounting in the guest |
| S06-06-devices-and-warm-start | A VM's devices and resources, saving and restoring it, and the guest clock |
| S06-07-slot-admission | How `wb-hostd` admits macOS VMs against the limit of two |
| S06-08-projects-and-sessions | Guest users, worktrees, conversation history and Claude Code in a VM |
| S06-09-session-lifecycle | A session's states, its start and end, deadlines and idle suspend |
| S06-10-leftover-processes | Results of the kill of a project user, and what `wb-hostd` does with them |
| S06-11-reconnect-and-recovery | Reconnecting after a dropped link, lost sessions and their recovery |
| S06-12-guest-agent | What `wb-guestd` does, and how it updates itself |
| S06-13-data-disk-format | How the host formats a new data disk, and the checks before a VM starts with it |

## Out of scope

Port forwarding (FR11-port-forward) and the clipboard (FR12-clipboard)
are out of scope for V1 (V1-08-no-forward-clipboard, decided on I38).
`wb-guestd` does not report listening ports or bridge the clipboard,
and nothing crosses the VM boundary for either. The analysis in
B38-port-forward-clipboard is the input for their design once they are
scheduled.

**Status:** Draft
