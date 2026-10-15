# S06-04 - VMs and Placement

**Purpose:** The work VM and the isolated VM, Xcode in them, and which VM a
project's sessions run in.

## Design

### Two slots per guest OS

The **work VM** hosts every project whose
`placement` is `work`. The **isolated VM** hosts projects whose
`placement` is `isolated`, and sessions started with
`wb --isolated claude`.
macOS allows at most two running macOS guests, including any started
by other software: `wb-hostd` performs admission control and reports
a clear error when a slot is unavailable (NFR05-two-macos-vms). The
rules are in S06-07-slot-admission.

### Xcode in the session's VM

Xcode builds, `swift build` and
`swift test`, and signing to run locally run for a project user
without a GUI login session, under the Layer 2 profiles of
S13-guest-confinement (X06-guest-xcode). They run in the VM the
project's `placement` picks, and take no slot of their own.
X06-guest-xcode also ran iOS simulators and their tests for a
project user without a GUI session, but under the Layer 2 profiles
specified there they don't run (I196).

### No GUI login session

Every `xcodebuild test` of a macOS target,
a SwiftPM package's included, and launching a macOS app need a GUI
login session (X06-guest-xcode). Neither VM gives a project user
one: sealing refuses an image with automatic login or an
`/etc/kcpassword` (S06-02-image-build, "Sealing"). v1 doesn't offer macOS tests through
`xcodebuild` or launching a macOS app (B20-guest-xcode decision 1A,
I20). `swift test` is unaffected. When such a test fails because
`testmanagerd` can't reach a GUI session, `wb` replaces the error
with a message that names this limit (NFR06-explained-refusals).
How `wb` detects the failure, and which process does it, is I224. No
v1 work needs a console session, so S06-07-slot-admission has no rule
for one. I195 holds the console session design for after v1.

### Placement and configuration trust

`placement` (`work` or
`isolated`) is a project setting (S09-02-project-settings, "Settings contents"),
and `wb project place` changes it (S05-cli). `--isolated` overrides
it for one session. `config_trust` (`wb trust`) only decides whether
`.wraithbox/` is read, and never moves a project between VMs.

### New projects start in the work VM

A newly registered project
(FR02-any-repo) has `placement = work`, so it shares the grants of
every work-VM project with a session (T13-new-project-grants). For a
repository they don't trust, the user passes `--isolated` or sets
`placement = isolated`. The maintainer decided this on I42
(B42-trust-placement): projects in the work VM still run as separate
guest users.

### Changing placement

A new `placement` applies from the next
session start. `wb project place` is refused while the project has a
session, and the refusal names the session. A project user's home,
with its Claude Code state (FR13-claude-state) and commits not yet
pushed, is on the data disk of the VM it ran in (S06-05-disks). The host
can't move it by mounting either data disk
(SEC15-no-guest-disk-mount), and I232 decides whether and how a home
moves. Until I232 is decided and built, `wb project place` is refused
for a project that has a home on any VM's data disk. The refusal
names I232 and today's way to isolate such a project: `wb project rm`, then
`wb project place isolated` in the repository, which registers it
again with no home, at the cost of its Claude Code state and
approvals (NFR06-explained-refusals). So no home is left on the old VM,
exposed to its other projects (T05-cross-proj-clones), and no
session starts with an empty home without saying so. A new clone,
which has no home yet, can still be placed before its first session.

### Isolated slot to itself

Projects in one VM share its network
grants and credential bindings (S07-egress-gateway, "Enforced per
VM", T11-shared-vm-grants). A project whose `placement` is
`isolated` gets the isolated VM to itself, unless its settings mark
it shared (S09-02-project-settings, "Settings contents").
`wb-hostd` refuses a session start that would put another project,
or an `--isolated` session, in the isolated VM while such a project
has a session there, and refuses that project's session start while
another project has one there. The error names the other project
(NFR06-explained-refusals). Sessions started with `--isolated`, and
projects marked shared, may share the isolated VM with each other.
They then share their grants, and after guest root their clones
(T05-cross-proj-clones).

**Status:** Draft
