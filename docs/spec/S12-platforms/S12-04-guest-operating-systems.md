# S12-04 - Guest Operating Systems

**Purpose:** What differs per guest operating system.

## Design

What differs per guest OS is confined to `wb-guestd` and to image
building:

| Concern | macOS guest | Linux guest | Windows guest |
|---|---|---|---|
| Image source | Apple restore image | Ubuntu LTS cloud or server image | user-supplied Windows 11 installation media |
| Unattended install | Wraith Box installer flow | automated install with a seed | unattended answer file |
| `wb-guestd` runs as | root LaunchDaemon | root systemd unit | SYSTEM service |
| Per-project users (`internal/platform/guestusers`) | local accounts, through `dscl` | local accounts | local accounts |
| PTY | Unix PTY | Unix PTY | ConPTY |
| Toolchain manifest (FR07-toolchain-manifest) | Brewfile | package list (apt) | package list (winget) |
| CA trust (S09-policy-credentials-audit) | trust files + toolchain variables. Not the system keychain, which refuses the CA without a user (X04-tls-inspection, I185) | system trust store + toolchain variables | machine certificate store + toolchain variables |
| Guest confinement (S13-guest-confinement) | Seatbelt profiles; Network Extension flow labels (X14-flow-attribution); Endpoint Security (X15-endpoint-security) | Landlock and seccomp, as in OpenShell's sandbox runtime (X16-openshell-linux) | not designed in V1 |

### Windows guests need a virtual TPM

Windows 11 requires a TPM 2.0 and Secure Boot. A host that can't
provide a virtual TPM to the guest can't run Windows guests (spikes
X11-windows-host, X13-windows-guests).

**Status:** Draft
