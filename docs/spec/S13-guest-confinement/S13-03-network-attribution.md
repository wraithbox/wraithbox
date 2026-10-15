# S13-03 - Layer 3: Per-Program Network Attribution (after X14-flow-attribution)

**Purpose:** How a Network Extension in the guest labels each flow with the
program and user that opened it, and how rules match on the label.

## Design

A Network Extension system extension in the guest
(`NETransparentProxyProvider`) sees every TCP and UDP flow together
with the source process's audit token. For each flow it reports a
label to the host:

- executable path, signing identifier, team identifier, and code
  directory hash (cdhash). Apple Silicon runs only signed binaries, at
  least ad hoc signed. The cdhash identifies the file the way OpenShell's
  SHA-256 does.
- the user ID of the process from the same audit token, which names
  the project user and so the project. Parallel sessions of one
  project run as the same user. The label names a session only while
  its project has one session running.

`wb-netd` attaches the label to the stream it hands to `wb-proxyd`.
Rules then match on it (S09-policy-credentials-audit):

- **Matching.** A rule's `binaries` entry matches the program that
  opened the connection. Ancestry is not available from a Network
  Extension, so helpers must be listed themselves (for example `git` and
  `git-remote-https`).
- **Pinning.** The first cdhash seen for a path is recorded. A later
  connection from the same path with another cdhash is denied and
  audited, as in OpenShell.
- **Flows the extension does not see** (system daemons, a killed or
  unloaded extension) arrive unlabeled and only match rules with no
  program narrowing.

How labels reach the host (a header on each flow, or a side channel
over the host-guest socket keyed by the connection's addresses and
ports) is decided by spike X14-flow-attribution. Either way the host parses guest bytes,
and the parser gets a fuzz target (C03-policy-and-approvals).

**Apple prerequisites.** A Developer ID with the Network Extension
system extension entitlement. The extension is approved once in the
base image at build time (S06-vm-lifecycle). Production guests keep System
Integrity Protection on. That is expected to stop guest root from
replacing the extension, but not from killing it. X14-flow-attribution checks both.
Development guests may turn it off.

**Status:** Draft
