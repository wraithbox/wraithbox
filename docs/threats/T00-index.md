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
| T09-unnamed-credentials | Guest credentials in unnamed places | Accepted |

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
  content sent to the model provider.
- **T02-dns-names: Data in DNS names.** Data encoded in DNS names under allowed wildcards, bounded by a
  per-session lookup budget.
- **T03-hypervisor-escape: Hypervisor escape.** Escape from the hypervisor.
- **T04-bad-approvals: Bad approvals.** A user approving a malicious request or merging a malicious
  change despite the flags.
- **T05-cross-proj-clones: Other projects' clones after guest root.** After a guest root escalation inside the shared work VM, read or
  write access to other projects' guest clones (code only; no secrets are
  present; changes still return only as reviewable branches). Untrusted
  repositories use the isolated VM to avoid this.
- **T06-forged-labels: Forged program labels after guest root.** After a guest root escalation, forged program identities in the
  guest confinement layer (S13-guest-confinement). Rules narrowed to named
  programs then act as the union of the project's grants. The host
  floor (SEC05-default-deny, SEC06-repo-writes) still holds.
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
- **T09-unnamed-credentials: Guest credentials in unnamed places.**
  `wb-proxyd` removes `Authorization`, `Proxy-Authorization` and
  `Cookie` on every inspected host, and the headers and query
  parameters a built-in profile names on its hosts. On an allowed host
  without a built-in profile, a guest's own credential in another
  header or parameter, such as an API key in the query string, is
  forwarded, and the guest acts as its own account there. It never
  carries one of the user's credentials (SEC04-no-guest-secrets), and
  the git hosts of SEC06-repo-writes have built-in profiles
  (S07-egress-gateway, X22-no-guest-credentials).
