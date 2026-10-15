# S04-05 - Helper Programs

**Purpose:** The programs that run once for an image build or a store operation:
`wb-guestadmin`, `wb-release-check`, `wb-download` and `wb-build-ssh`.

## Design

### The guest admin password has a process of its own

`wb-guestadmin` does the admin side of an image build, rotates
`wbadmin`'s password at each VM's first contact, and removes the
items of images and system disks that are gone, including the old
item when a VM's system disk is replaced (S15-least-privilege). It may touch the `wbadmin/*`
items in the secret store and nothing else. `wb-hostd` starts it
through `wb-launcher` for one run, and it exits when that run ends.
Its channels are pipes to the build's `wb-vmd` and `wb-build-ssh`,
whose read ends `wb-launcher` hands over at spawn, and one host-guest
connection to `wb-guestd` that `wb-hostd` has `wb-vmd` open for it.
It only writes to that connection, so it parses nothing from the
guest. Before it writes, it checks with `LOCAL_PEERPID` that the
connection's peer is Virtualization's service process, and refuses
any other descriptor. It doesn't talk to `wb-proxyd`. Its requests
carry only item names in the fixed format of S15-least-privilege,
which `wb-launcher` and `wb-guestadmin` both check. A compromised
`wb-hostd` can still keep a read end of a build pipe and so read
that build's password, because a pipe has no peer to check. That
password is burned at the build VM's first contact, and the residual
stays limited to builds. It runs apart from `wb-hostd` for least
privilege: `wb-hostd` runs the host-guest
socket handlers, and so never reads a credential.

### `wb-guestadmin`'s profile

`wb-guestadmin` confines itself at start the same way, from a
compiled-in profile (X23-sandboxed-daemons, X25-vmd-sandbox). The
profile allows the secret store service for its `wbadmin/*` items
and its inherited descriptors, and denies files, the network, and
starting programs. `wb-launcher` has one fixed entry per run kind
(`wb-guestadmin-build`, `wb-guestadmin-rotate`,
and `wb-guestadmin-cleanup`, which removes the items of images and
system disks that are gone), so a request
never chooses what it does.

### `wb-release-check`

`wb-release-check` checks a Claude Code release for an image build
(S14-claude-in-guest, "Install"). `wb-launcher` has one fixed
entry, `wb-release-check`, with no profile parameter. It confines
itself first thing, from a compiled-in profile that allows its
inherited descriptors (the manifest, the signature, the binary and
its result pipe) and denies files, the network, the secret store
and starting programs. Then it reads the requested version from the
pipe, runs the checks, and writes one fixed-format record to the
pipe: `pass` with the manifest's signed SHA-256 checksum for the
guest's platform, or the rule of the check
that failed. It exits after one check.

### `wb-download`

`wb-download` fetches what an image build needs
(S17-install-and-upgrade, "Downloads"). `wb-launcher` has two fixed
entries, `wb-download-restore` and `wb-download-claude`, each with
its hosts compiled into the program, so a request never names a
host or a URL. Its compiled-in profile is the network part of
the `wb-proxyd` profile that X23-sandboxed-daemons measured for
I61, limited to TCP port 443: name lookups, `com.apple.trustd.agent`
and the metadata of the app bundle's directory for certificate
checks, the routing table `sysctl` read that `getifaddrs` makes
for the upstream address rule, and its inherited descriptors. It
denies files, the secret
store and starting programs. `wb-launcher` sets each entry's file
size limit (`RLIMIT_FSIZE`) before it starts the program, so the
host bounds what a run writes.

### `wb-build-ssh`

`wb-build-ssh` is the second bounded exception. It is a fixed shim
in the bundle that takes no request field. It confines itself, then
runs the one program outside the bundle that the launcher allows,
the system `ssh` at its fixed platform path, with arguments and
environment compiled into the shim (S15-least-privilege, "The
build's SSH session"). Its only connection is to the build guest's
port 22, through `wb-netd` on the build VM's own network
(S06-vm-lifecycle, "Images").

**Status:** Draft
