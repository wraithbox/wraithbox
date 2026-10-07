# T00 - Threat model

**Purpose:** Who Wraith Box defends against, what it protects, what it
trusts, and which risks it accepts rather than solves.

| ID | Description | Status |
|----|-------------|--------|
| T01-allowed-channels | Data through allowed channels | Accepted |
| T02-dns-names | Data in DNS names | Accepted |
| T03-hypervisor-escape | Hypervisor escape | Accepted |
| T04-bad-approvals | Bad approvals | Accepted |
| T05-cross-proj-clones | Other projects' clones after guest root | Accepted |
| T06-forged-labels | Forged program labels after guest root | Accepted |
| T07-ungated-sources | Dependencies from ungated sources | Accepted |
| T08-homebrew-ungated | Young Homebrew bottles | Accepted |
| T09-terminal-fingerprint | Host terminal facts in the guest | Accepted |
| T10-unnamed-credentials | Guest credentials in unnamed places | Accepted |
| T11-shared-vm-grants | Projects in one VM share network grants | Accepted |
| T12-shared-export | Sessions of a project share what they can fetch | Accepted |
| T13-new-project-grants | New projects share the work VM's grants | Accepted |
| T14-shared-proxyd | One proxy process serves both VMs | Accepted |

## Model

**Adversary.** Instructions or code running inside the guest with full
guest root: a prompt-injected or misaligned agent, a malicious dependency,
or a tool the agent runs. It can read everything in the guest, craft any
packet, and try every protocol on every interface it can see.

**Assets.** Host files; host credentials including the model credential;
the user's repositories and online accounts; other projects' code; the
integrity of the user's working tree and future host-side executions; the
user's attention (approval fatigue is an attack surface).

**Trusted.** Host kernel and the platform's hypervisor and virtualization
API (S12-platforms); the host user account, including the user's WSL
distributions on Windows; Wraith Box host binaries; the platform's
credential store and hardware key store (Keychain and Secure Enclave on
macOS); the operators of allowlisted services (as services, not their
content).

**Boundaries.** (1) the VM; (2) the host-guest socket RPC surface to
`wb-guestd` (vsock or the platform equivalent); (3) the virtual NIC
packet surface to `wb-netd`; (4) git objects returning to the host;
(5) user approvals; (6) the host terminal stream: the guest output
that `wb` writes to the user's terminal emulator, which acts on some
escape sequences on the host, and the replies the emulator sends back
(S05-cli, "Terminal stream"). What host programs such as `git log`
print about returned work after `wb land` is outside it
(T04-bad-approvals).

**Accepted residual risks**, documented rather than solved:

- **T01-allowed-channels: Data through allowed channels.** Data sent through allowed operations to allowed destinations,
  for example a push to an allowed repository that is public, or prompt
  content sent to the model provider. A host in pass mode is one of
  these channels: `wb-proxyd` relays its bytes without reading them,
  so no HTTP policy applies to what the guest sends it. Only the user
  can make a host a pass host, each one is audited, and a host with a
  credential binding or a built-in profile can't be one
  (S07-egress-gateway, "Modes, per host", and B40-learn-pass-modes).
  A pass host on a shared front end, such as a CDN, can carry in its
  encrypted bytes the name of any site behind that front end, in the
  inner `Host` header. That channel reaches past `wb-proxyd`'s check
  of the SNI. `wb-proxyd` parses every ClientHello the client sends
  to a pass host until the server's ServerHello, a second one after a
  HelloRetryRequest included. It resets a ClientHello with ECH or
  another SNI, and resets client data sent before that ServerHello.
  That closes the same channel through ECH (S07-egress-gateway,
  "Non-HTTP streams"). A raw TCP entry is a
  channel of the same kind: `wb-proxyd` relays its bytes to one named
  host and port without reading them, and it has the rules of a pass
  host (S07-egress-gateway, "Non-HTTP streams", and
  B47-non-http-streams). A name whose zone someone else controls can
  point any of these channels at a network on public addresses that
  the host reaches through a route other than its own links, such as
  a VPN. `wb-proxyd` refuses only the address ranges, interface
  addresses and on-link prefixes that S07-egress-gateway, "Upstream
  address", lists.
- **T02-dns-names: Data in DNS names.** Data encoded in DNS names under allowed wildcards, bounded by a
  lookup budget per VM (S07-egress-gateway, "DNS").
- **T03-hypervisor-escape: Hypervisor escape.** Escape from the hypervisor.
- **T04-bad-approvals: Bad approvals.** A user approving a malicious request or merging a malicious
  change despite the flags.
- **T05-cross-proj-clones: Other projects' clones after guest root.** After a guest root escalation inside the shared work VM, read or
  write access to other projects' guest clones (code only; no secrets are
  present; changes still return only as reviewable branches). A
  repository the user doesn't trust runs in the isolated VM to avoid
  this, with `--isolated` or `placement = isolated`, but a new project
  starts in the work VM (T13-new-project-grants). The isolated VM
  doesn't isolate its tenants from each other: sessions started with
  `--isolated` and projects marked shared are co-tenants there, though
  they hold the repositories most likely to be hostile. They share
  their clones after guest root in the same way, and their network
  grants even without it (T11-shared-vm-grants). Only a project with
  `placement = isolated` that isn't marked shared has the VM to itself
  (S06-vm-lifecycle, "VMs"). The maintainer accepted this co-tenancy
  on I42 (2026-10-06, B42-trust-placement).
- **T06-forged-labels: Forged program labels after guest root.** After a guest root escalation, forged program identities in the
  guest confinement layer (S13-guest-confinement). Rules narrowed to named
  programs or to a project then act as the union of the VM's grants
  (T11-shared-vm-grants). The host floor (SEC05-default-deny,
  SEC06-repo-writes) still holds.
- **T07-ungated-sources: Dependencies from ungated sources.** The dependency gate
  (SEC07-dep-gate) covers the npm, PyPI, Go module proxy and crates.io
  hosts only. A dependency fetched from another allowed host skips the
  minimum age and the vulnerability check: Go with `GOPROXY=direct` or
  `GOPRIVATE` from a git host, `git+https:` and `github:` dependencies,
  archive downloads from a git host, cargo `git` dependencies, and
  registry mirrors the user approved (S07-egress-gateway, "Dependency
  gate").
- **T08-homebrew-ungated: Young Homebrew bottles.** Homebrew bottles from
  `homebrew/core` are not age-gated or checked against OSV. Homebrew has
  one version per formula and signed metadata, so the only option would
  be refusing young bottles, which fails about a third of popular
  formulae (X21-dep-gate-registries).
- **T09-terminal-fingerprint: Host terminal facts in the guest.** The
  guest learns the host terminal's name and version, its colors, the
  window size in cells and pixels, and the state of its modes, from the
  replies to the queries S05-cli passes, and from the `TERM`,
  `TERM_PROGRAM`, `TERM_PROGRAM_VERSION`, `COLORTERM`, `LANG`, `LC_ALL`,
  and `LC_CTYPE` variables that cross into the guest session. Claude Code needs them
  to choose its output. No passed query returns clipboard or file
  contents, or text the guest wrote.
- **T10-unnamed-credentials: Guest credentials in unnamed places.**
  `wb-proxyd` removes `Authorization`, `Proxy-Authorization` and
  `Cookie` on every inspected host. On the hosts of a built-in profile
  it removes the headers and refuses the query parameters and body
  fields the profile names. A
  guest's own credential somewhere else reaches the host: another
  header or parameter on a host without a built-in profile, a nested
  JSON field, a WebSocket `Sec-WebSocket-Protocol` value or first
  message, or a signed URL the guest made for its own storage bucket.
  The guest then acts as its own account there, within the methods
  policy allows there. An approval grants read methods only
  (S07-egress-gateway, "What an approval grants"). The user's
  credentials are never in the guest (SEC04-no-guest-secrets), and the
  git hosts of SEC06-repo-writes get the git hosting profile, a
  self-hosted one with every kind's names (S07-egress-gateway,
  X22-no-guest-credentials).
- **T11-shared-vm-grants: Projects in one VM share network grants.**
  The host sees one guest address per VM, and can't tell which
  project user, session or program opened a connection. So it enforces
  network policy, credential bindings and approvals per VM, as the
  union of those of the projects with a session in the VM
  (S07-egress-gateway, "Enforced per VM"). This holds in any VM: the
  work VM, and the isolated VM when `--isolated` sessions or shared
  projects run there together. Any process in the VM, without guest
  root, can use what every project with a session there was granted:
  - its credential bindings and its write grants;
  - its dependency-gate overrides, which apply VM-wide;
  - the hosts approved for it, and session approvals, which hold for
    the VM until the sessions running at approval end.

  The projects also share the VM's availability. One project can use
  up the wildcard budget, the approval request rate and pending cap,
  and the detection-finding rate limit for the others, and the
  strictest limit any of them sets applies to all. Per-project and
  per-session rules narrow the union only by a label the guest
  reports, which guest root can forge (T06-forged-labels), and until
  X14-flow-attribution delivers labels nothing narrows it. Credentials
  still never enter the guest (SEC04-no-guest-secrets), the floor of
  SEC05-default-deny holds for the whole VM, and returned work still
  lands per project and session (S08-workspace-and-git). Conflicting
  modes and bindings refuse the joining session, and the join notice
  says what becomes reachable. A project whose grants must stay apart
  is set to `placement = isolated`, and `wb-hostd` gives it the
  isolated VM to itself (S06-vm-lifecycle, "VMs"). With
  NFR05-two-macos-vms, one such project runs at a time, and
  `--isolated` sessions wait meanwhile. The maintainer accepted this
  on I36.
- **T12-shared-export: Sessions of a project share what they can
  fetch.** A project's `export.git` holds the refs that any live session
  of the project selected (S08-workspace-and-git, "Export repository").
  Over protocol v2 `upload-pack` serves any object it holds to a client
  that names its ID, and `uploadpack.hideRefs` only trims the list of
  refs it offers (X07-git-round-trip). So a session's guest can fetch
  the start commits and selected refs of the project's other live
  sessions, also when the user selected them for that other session
  only. A guest also keeps every object it fetched earlier in the
  project user's clone, after the ref left the selection and
  `export.git` was pruned. Pruning stops only later fetches. Everything
  here is the user's own repository content, selected for a session of
  the same project, and the project's sessions share one guest user.
  Other branches, tags, notes and the stash never reach `export.git`,
  and other projects have their own (SEC04-no-guest-secrets). Proposed
  in PR123 (B41-git-data-scope).
- **T13-new-project-grants: New projects share the work VM's grants.**
  A newly registered project has `placement = work`
  (S06-vm-lifecycle, "VMs"). So the first session in a freshly cloned
  repository, the likeliest place for a prompt injection, runs in the
  work VM. Any process in it can use the network grants, credential
  bindings and approvals of every work-VM project with a session
  running alongside it (T11-shared-vm-grants), and after guest root
  their clones (T05-cross-proj-clones). The user avoids this for a
  repository they don't trust by passing `--isolated`, or by running
  `wb project place isolated` before the first session (S05-cli).
  Credentials still never enter the guest (SEC04-no-guest-secrets),
  and the floor of SEC05-default-deny holds. The maintainer accepted
  this on I42 (B42-trust-placement), because projects in the work VM
  still run as separate guest users.
- **T14-shared-proxyd: One proxy process serves both VMs.** One
  `wb-proxyd` serves the work VM and the isolated VM
  (S04-architecture, "Processes"). A guest that finds a way to crash
  it takes the other VM's network down too, until `wb-hostd` restarts
  it, and until the next session start once `wb-hostd` gives up after five exits in ten
  minutes (S04-architecture, "Restarting a daemon"). The other VM
  also shares `wb-proxyd`'s blocked event channel
  (`event-channel-blocked`) and the secret store prompts its restarts
  cause. This costs availability, not egress: while `wb-proxyd` is
  down or blocked, both guests get resets and refusals. Proposed in PR171 (B48-process-supervision).
