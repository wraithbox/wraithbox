# S00 - Specs

**Purpose:** Every spec, in order. A spec decides how one part of
Wraith Box works and why.

| ID | Description | Status |
|----|-------------|--------|
| S01-spec-based-development | Spec-Based Development | Implemented |
| S02-toolchain | Toolchain | Active |
| S03-requirements | Requirements | Draft |
| S04-architecture | Architecture | Draft |
| S04-01-design-principles | Design Principles | Draft |
| S04-02-vm-provider | VM Provider and Streams | Draft |
| S04-03-self-sandboxing | Self-Sandboxing | Draft |
| S04-04-vm-processes | Per-VM Processes | Draft |
| S04-05-helper-programs | Helper Programs | Draft |
| S04-06-git-confinement | Git and the `wb-hostd` Profile | Draft |
| S04-07-supervision | Supervision | Draft |
| S04-08-records-and-bounds | Records, Versions and Bounds | Draft |
| S05-cli | Command Line Interface | Draft |
| S05-01-agent-commands | Agent Commands | Draft |
| S05-02-exit-status-and-signals | Exit Status and Signals | Draft |
| S05-03-management-commands | Management Commands | Draft |
| S05-04-naming | Naming | Draft |
| S06-vm-lifecycle | VM Lifecycle, Images and the Guest Agent | Draft |
| S06-01-image-layers | Image Layers and the Toolchain Prefix | Draft |
| S06-02-image-build | Image Build | Draft |
| S06-03-image-updates | Image Updates | Draft |
| S06-04-vms-and-placement | VMs and Placement | Draft |
| S06-05-disks | Disks | Draft |
| S06-06-devices-and-warm-start | Devices, Warm Start and Time | Draft |
| S06-07-slot-admission | Slot Admission | Draft |
| S06-08-projects-and-sessions | Projects and Sessions Inside a VM | Draft |
| S06-09-session-lifecycle | Session Lifecycle | Draft |
| S06-10-leftover-processes | Leftover Processes | Draft |
| S06-11-reconnect-and-recovery | Reconnect, Loss and Recovery | Draft |
| S06-12-guest-agent | Guest Agent | Draft |
| S07-egress-gateway | Egress Gateway | Draft |
| S07-01-per-vm-policy | Enforced per VM | Draft |
| S07-02-packet-path | Packet Path | Draft |
| S07-03-dns | DNS | Draft |
| S07-04-stream-hand-off | Stream Hand-off | Draft |
| S07-05-hosts-and-modes | Hosts and Modes | Draft |
| S07-06-requests-and-credentials | Requests and Credentials | Draft |
| S07-07-http-policy | HTTP Policy | Draft |
| S07-08-dependency-gate | Dependency Gate | Draft |
| S07-09-publish-time | Publish Time | Draft |
| S07-10-non-http-streams | Non-HTTP Streams | Draft |
| S07-11-approvals-and-learning | Approvals and Learning | Draft |
| S08-workspace-and-git | Workspace and Git Round Trip | Draft |
| S08-01-into-the-guest | Into the Guest | Draft |
| S08-02-export-repository | Export Repository | Draft |
| S08-03-pushes | Pushes | Draft |
| S08-04-push-checks | Push Checks | Draft |
| S08-05-push-limits | Push Limits | Draft |
| S08-06-landing-on-the-host | Landing on the Host | Draft |
| S08-07-repository-lifecycle | Repository Lifecycle | Draft |
| S08-08-host-git | Host Git | Draft |
| S08-09-risky-changes | Flagging Risky Changes | Draft |
| S08-10-wsl | WSL | Draft |
| S09-policy-credentials-audit | Policy, Credentials, Certificates and Audit | Draft |
| S09-01-policy-files | Policy Files | Draft |
| S09-02-project-settings | Project Settings | Draft |
| S09-03-boundary-check | Boundary Check | Draft |
| S09-04-credentials | Credentials | Draft |
| S09-05-ca-keys | CA Keys | Draft |
| S09-06-ca-certificates | CA Certificates | Draft |
| S09-07-guest-trust | Guest Trust | Draft |
| S09-08-approvals | Approvals | Draft |
| S09-09-approval-flow | Approval Flow | Draft |
| S09-10-audit | Audit | Draft |
| S10-tech-stack | Technology Choices | Draft |
| S11-verification-and-spikes | Verification | Draft |
| S12-platforms | Platforms | Draft |
| S12-01-platform-interfaces | Platform Interfaces | Draft |
| S12-02-languages-and-ci | Languages and CI | Draft |
| S12-03-host-prerequisites | Host Prerequisites | Draft |
| S12-04-guest-operating-systems | Guest Operating Systems | Draft |
| S12-05-wsl | WSL | Draft |
| S13-guest-confinement | Guest Confinement | Draft |
| S13-01-process-hardening | Layer 1: Process Hardening (v1) | Draft |
| S13-02-filesystem-profile | Layer 2: Seatbelt Filesystem Profile (v1) | Draft |
| S13-03-network-attribution | Layer 3: Per-Program Network Attribution (after X14-flow-attribution) | Draft |
| S13-04-endpoint-security | Layer 4: Endpoint Security (optional, after X15-endpoint-security) | Draft |
| S14-claude-in-guest | Claude Code in the Guest | Draft |
| S14-01-install | Install | Draft |
| S14-02-user-configuration | User Configuration | Draft |
| S14-03-configuration-filter | Configuration Filter | Draft |
| S14-04-commands-and-endpoints | Commands and Endpoints | Draft |
| S15-least-privilege | Least Privilege on the Host | Draft |
| S16-terminal-stream | Terminal Stream | Draft |
| S16-01-filter | Filter | Draft |
| S16-02-drops-and-links | Drops and Links | Draft |
| S16-03-around-the-stream | Around the Stream | Draft |
| S17-install-and-upgrade | Install, Downloads and Upgrades | Draft |
| S17-01-bundle | The Bundle | Draft |
| S17-02-setup | Install | Draft |
| S17-03-downloads | Downloads | Draft |
| S17-04-restore-and-release | Restore Image and Release | Draft |
| S17-05-upgrades-and-migrations | Upgrades and Migrations | Draft |

The other indexes: FR00-index, NFR00-index, SEC00-index, T00-index,
C00-index, X00-index, R00-index and V00-index.
