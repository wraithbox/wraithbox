# S15-01 - Rights

**Purpose:** Which rights Wraith Box takes on the host.

## Design

### No root or administrator rights at run time

The processes of
S04-architecture's table run as the user. The packet transport and
the userspace network stack replace the host networking features that
would need root. The image build never writes to a guest disk from
the host, so it doesn't need root either (X17-image-build).

### No extra host user accounts

Wraith Box's own processes are kept
apart by self-sandboxing (S04-03-self-sandboxing, "Each host daemon is self-sandboxed"), and projects by guest users (SEC08-proj-isolation).

### Hardening and entitlements

Every binary in the bundle is signed
with the hardened runtime (S10-tech-stack). On macOS, `wb-vmd` alone
has `com.apple.security.virtualization`, and no process has
`com.apple.vm.networking` (X25-vmd-sandbox). In a build signed by a
team, a process with store access gets the Keychain access groups of
the items it declares, and no other (S15-02-secret-store, "Keychain"). A new entitlement needs a change here first. Other
hosts use the equivalents in S12-platforms.

### The release check

`wb-release-check` (S04-architecture,
S14-claude-in-guest) doesn't declare a store item or an entitlement.
Its compiled-in profile allows only its inherited descriptors, and
denies files, the network, the secret store, and starting programs.
The Claude Code release key compiled into it is public.

### The downloads

`wb-download` (S17-install-and-upgrade) doesn't
declare a store item or an entitlement. Its compiled-in profile is
the network part of the `wb-proxyd` profile that
X23-sandboxed-daemons measured for I61, limited to TCP port 443:
name lookups, `com.apple.trustd.agent` and the metadata of the app
bundle's directory for certificate checks, the routing table
`sysctl` read that `getifaddrs` makes, and its inherited
descriptors. It denies files, the secret store and starting
programs. `wb-launcher` caps the size of the files it writes.
`wb-hostd` keeps no network.

### One-time prerequisites

`wb setup` checks the prerequisites of
S12-platforms, explains each missing one, and says who can fix it.
The user makes each change, and `wb setup` doesn't ask for an
administrator password. A prerequisite that needs administrator rights
on every run needs a change to this spec first.

**Status:** Draft
