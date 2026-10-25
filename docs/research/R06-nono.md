# R06 - nono

**Purpose:** What nono is, how it relates to Wraith Box, which of its
threats and defenses Wraith Box lacks, and what to do about it. Read
at nono commit `11d9e6b8` (`v0.80.0-41-g11d9e6b8`, 41 commits after
0.80.0 of 2026-10-13, last commit 2026-10-23).

**Requirements:** SEC01-separate-kernel, SEC04-no-guest-secrets,
SEC05-default-deny, SEC08-proj-isolation, SEC10-audit,
SEC11-root-gains-nothing, NFR06-explained-refusals

## Sources

The maintainer's checkout at `~/git/oss/nono` was cloned with
`git clone --no-checkout` into `.scratch/src/nono/`, and read only
through `git -C <dir> show`, `ls-tree`, `grep` and `log`. No file of
the project was checked out, so its root `CLAUDE.md` and `AGENTS.md`,
and a nested `tool-sandbox-examples/kubernetes-with-approval/CLAUDE.md`,
never reached the disk. Nothing of the project was built or run.

Its root `CLAUDE.md` holds only `@AGENTS.md`. Its `AGENTS.md` gives
instructions to agents that contribute to nono: open an issue before
changing code, sign off commits for the DCO, and don't open a pull
request that breaks those rules. It also says that OpenClaw and Pi
coding agents "must not make changes or open PRs". None of these apply
to this note, and none were followed.

Facts from the history come from `git log` over the clone: 1864
commits, 114 tags from `v0.1.0` (2026-02-01) to `v0.80.0`, 516 tracked
files. Luke Hinds wrote 610 of the non-merge commits, the next author
132, and more than 100 people have commits. Facts about behavior come
from the docs named below, and where a finding depends on it, from the
code. This note didn't run nono, so every behavior is as documented or
as read in the code, not measured.

## What nono is

nono (`nolabs-ai/nono`, Apache-2.0) is a capability sandbox for AI
agents, by "the team that brought you Sigstore" (`README.md`). nono is
a Rust workspace: the library `crates/nono`, the CLI `crates/nono-cli`,
the proxy `crates/nono-proxy`, C bindings in `bindings/c`, and Python
bindings. It runs as the user from one binary and doesn't need root
(`README.md`), and its APIs are still stabilizing
before 1.0.

`nono run -- claude` starts a trusted supervisor as the user, which
applies a sandbox to its child and stays outside it
(`docs/cli/internals/security-model.mdx`):

- **The kernel sandbox.** On macOS a Seatbelt profile through the
  private `sandbox_init`, generated per run from the profile's grants
  (`crates/nono/src/sandbox/macos.rs`). On Linux Landlock, with seccomp
  user notification for brokered calls. WSL2 runs with fewer features.
- **The network proxy.** An HTTP proxy in the supervisor
  (`crates/nono-proxy`). It relays CONNECT without reading TLS, unless
  a rule needs the path or method. It can inject credentials as a
  reverse proxy: the child gets a placeholder token and a local URL,
  and the supervisor adds the real secret upstream, limited to the
  methods and paths of `--allow-endpoint`
  (`docs/cli/features/credential-injection.mdx`). Secrets come from the
  keyring, 1Password or Bitwarden. A second mode puts a secret in the
  child's environment.
- **Sandboxed tool execution.** PATH shims run named commands, such as
  `gh` or `git`, in their own sandbox with their own grants, and deny
  running them directly (`docs/cli/features/tool-sandbox.mdx`). A
  default `dangerous_commands` list blocks `rm`, `mv`, `npm`, `pip`,
  `brew`, `launchctl` and `sudo`, among others
  (`docs/cli/features/dangerous-command-blocking.mdx`,
  `crates/nono-cli/data/policy.json`).
- **Instruction file attestation.** A `trust-policy.json` names the
  publishers whose skills, hooks and instruction files are trusted. A
  file that the policy covers needs a Sigstore `.bundle` beside it, and
  is denied without one (`docs/cli/features/trust.mdx`).
- **Audit and rollback.** The supervisor writes each session's events
  to `audit-events.ndjson`, and at the end of the session a hash chain
  head, a Merkle root, a ledger entry and an optional DSSE signature.
  It hashes the main executable before it runs it. `--rollback` keeps
  content-addressed snapshots of the writable paths
  (`docs/cli/features/audit.mdx`, `atomic-rollbacks.mdx`).
- **Approvals.** A terminal, webhook or chained backend answers the
  requests the sandbox can't decide alone.
- **Profiles.** Built-in and registry profiles per agent, such as
  `nolabs-ai/claude`, and signed profile packs.

nono's own security model says it "does not create a separate kernel
or hardware boundary", and for hostile code it advises to "use a suitable container or microVM as the perimeter and use nono for
fine-grained control inside it" (`security-model.mdx`, lines 11 and
356).

## How it relates to Wraith Box

| | nono | Wraith Box V1 |
|---|---|---|
| Agents | Any, with profiles for Claude Code, Codex, OpenCode and OpenClaw (`docs/cli/clients/`) | Claude Code (V1-02-claude-only) |
| Isolation | Same kernel, as the user: Seatbelt or Landlock | A macOS VM per trust level (SEC01-separate-kernel) |
| Privileges | None beyond the user's | Host processes with sandboxes of their own (S04-03-self-sandboxing) |
| Egress | Open by default. Proxy denies link-local and cloud metadata names | Default deny, with TLS inspection on the host (SEC05-default-deny) |
| Credentials | Placeholders and a reverse proxy, or the real secret in the environment | Never in the guest (SEC04-no-guest-secrets) |
| Host Claude config | Read and written by the agent (`~/.claude`, `~/.claude.json`) | Copied in through an allowlist, never written back (S14-02-user-configuration) |
| Audit | Per-session NDJSON, hash chain and Merkle root at session end, optional signature | OCSF 1.8.0 JSONL on the host, written per event (S09-11-audit) |
| Recovery | Rollback snapshots of the writable paths | Work returns as a branch (FR03-work-as-branch). The VM is reset or rebuilt |
| Maturity | 0.80.0, about one tag every two days | Before V1 |

**Overlap.** Both keep an agent from the user's files, secrets and
network, and both put the decision and the secret in a process the
agent can't reach. nono's reverse proxy with placeholder tokens is the
same idea as `wb-proxyd` adding a credential the guest never sees
(S09-05-credentials). nono's proxy checks resolved addresses against
link-local ranges and connects to the address it checked
(`crates/nono/src/net_filter.rs`, `is_link_local`, lines 215 to 240),
as `wb-proxyd` does for its upstream address list
(S07-egress-gateway).

**Where they differ.** nono shares the host kernel, so an escape from
Seatbelt or Landlock is an escape to the user's account. That is the
attack SEC01-separate-kernel exists to stop. nono's defaults are open
where Wraith Box's are closed:

- The default network policy allows every host. The proxy denies only
  `169.254.0.0/16`, `fe80::/10` and their IPv4-mapped forms, and the
  names `metadata.google.internal`, `metadata.azure.internal` and
  `fd00:ec2::254` (`crates/nono-proxy/src/filter.rs`,
  `crates/nono-cli/data/network-policy.json`). Private ranges such as
  RFC1918 are allowed by design.
- On macOS in proxy mode with DNS enabled, the child resolves names
  through the system resolver: the profile allows `mDNSResponder`
  (`macos.rs`, lines 818 to 824). That leaves a DNS channel, which
  T02-dns-names describes and `wb-netd` closes.
- The Claude Code profile (`docs/cli/clients/claude.mdx`,
  `policy.json`, `claude_code_macos`) grants read and write on
  `~/.claude`, `~/.claude.json`, `~/.vscode` and the login keychain
  files. `--bypass-protection` lifts the keychain deny. nono installs a
  Claude Code plugin with hooks into the host's `~/.claude`, and turns
  `~/.claude.json` into a symlink.

The last point matters most for Wraith Box's threat model. An agent
that can write `~/.claude` on the host can add a hook that runs, with
no sandbox, the next time the user starts Claude Code outside nono.
Wraith Box rules that out by keeping the guest off the host's files
(SEC02-no-host-fs-share), by copying in only an allowlist of the
user's configuration, without hooks (S14-02-user-configuration,
S14-03-configuration-filter), and by flagging a returned `.claude/` or
`CLAUDE.md` (S08-09-risky-changes).

**nono's case against a VM alone.** nono argues that inside a microVM
"everything is flat: an agent running in a microVM can freely reach
every asset in the box" (`security-model.mdx`, line 75). For Wraith
Box this is the job of S13-guest-confinement: a project user per
project, a Seatbelt profile that denies reads by default, and a denied
`AF_VSOCK` (S13-02-filesystem-profile, SEC08-proj-isolation). Wraith
Box treats that layer as defense in depth: the host holds every
control even when guest root is lost (SEC11-root-gains-nothing).

**Converging design.** `NEP-0007` (draft, created 2026-10-16, by Luke
Hinds) splits nono's supervisor across a VM boundary. Enforcement stays
in a Linux guest on Firecracker or Cloud Hypervisor. Approval
decisions, the audit ledger and credential injection move to a host
component over vsock, "so a full guest compromise does not yield
them". The caller's identity "comes from the transport (the per-VM
vsock socket), never from anything the guest sends". That is Wraith
Box's split (SEC04-no-guest-secrets, SEC09-host-policy, SEC10-audit),
reached from the other side. The details differ:

- In `NEP-0007` the host listens and the guest connects. Wraith Box's
  host doesn't register a vsock listener, and it opens every
  connection itself (S13-02-filesystem-profile, B29-vsock-handoff).
- `NEP-0007` warns that every clone of a VM snapshot starts with the
  same memory, so userspace random state, session IDs and request
  counters repeat across clones (observed on Agent Substrate). Wraith
  Box restores a saved state onto the disks it was saved with, deletes
  it once the VM has run on, and takes a fresh clone for each repeated
  restore (S06-06-devices-and-warm-start, "Disks and identity"). The
  host never takes identity from guest IDs. If warm start ever
  restores one state into many VMs, those guests start with the same
  userspace random state, and S06-06-devices-and-warm-start would have
  to say how a restored guest reseeds.

**Where they meet today.** Nowhere without a change. nono has no
macOS guest mode, and Wraith Box doesn't run a sandbox tool inside the
session other than its own profile.

**Shared ancestry.** Both build on Seatbelt on macOS. Wraith Box takes
its guest profile modules from agent-safehouse (S13-02-filesystem-profile,
"Source"). nono generates its own profile in Rust. Neither uses
OpenShell or OCSF in common: nono's audit format is its own.

## Threats nono covers that Wraith Box doesn't

Most of nono's threats are the ones a VM removes: the agent reading
the user's files, the user's keychain, or the host's processes. These
are covered by SEC01-separate-kernel, SEC02-no-host-fs-share and
SEC04-no-guest-secrets. The ones below are left after that.

**Unix-socket connects inside the guest.** nono's macOS docs point out
that Seatbelt treats `connect(2)` to a Unix socket as a
`network-outbound` operation, and file rules don't apply to it
(`docs/cli/internals/seatbelt.mdx`). When nono restricts the network,
its generator emits `(deny network*)` and then allows a connect only to
the socket paths of an explicit `--allow-unix-socket` grant. A file
grant on a directory doesn't allow connects to the sockets inside it
(`crates/nono/src/sandbox/macos.rs`, lines 830 to 850, and the test
near line 1779, after nono issues `#685` and `#696`). In its default
open network mode the generator emits `(allow network-outbound)`, which
allows every Unix-socket connect as well (`macos.rs`, line 900). The
S13-02-filesystem-profile network module "allows every outbound IP
connection". Whether it allows Unix-socket connects too isn't written
down, and no spike has checked it. If it does, a project user's
process can connect to a socket that another project user listens on,
although the file rules hide that user's home.

- **Asset:** the separation between project users in one VM
  (SEC08-proj-isolation), and guest daemons that act for root.
- **Attack:** a process of project A connects to a Unix socket that a
  process of project B listens on under its own home, such as a
  language server or an SSH agent socket.
- **Already holds:** the host controls. Each project's network grants,
  git writes and credentials are checked on the host, and the guest's
  attribution label is untrusted (S07-01-per-vm-policy).
- **Fix:** S13-02-filesystem-profile, with a check in a guest. That
  check needs a macOS guest, so it is an open point here.

**Instruction files planted in a returned branch.** nono refuses to
load a skill, hook or instruction file whose Sigstore signature
doesn't match its trust policy. Wraith Box flags a returned
`.claude/`, `AGENTS.md` or `CLAUDE.md` (S08-09-risky-changes) and
filters what it copies in (S14-03-configuration-filter), but it has no
notion of a trusted publisher. That is a decision about the user's own
Claude Code configuration on the host, outside V1's scope, and the
flag is the right V1 answer.

**Threats that don't apply to Wraith Box:**

- The agent reading host secrets, the keychain or other projects on
  the host. The guest has none of them (SEC02-no-host-fs-share,
  SEC04-no-guest-secrets).
- The agent killing or tracing host processes. nono denies
  `process-info*` for other targets. A guest process can't name a host
  process (SEC01-separate-kernel).
- Destructive commands such as `rm -rf` on the user's files. nono's
  `dangerous_commands` list exists because the agent works on the real
  tree. Wraith Box's agent works on a clone, and its work returns as a
  branch (FR03-work-as-branch).
- Supervisor compromise as privilege escalation. By design, nono's
  supervisor doesn't hold a privilege the user lacks. A VM separates
  the guest from Wraith Box's host processes, which have sandboxes of
  their own (S04-03-self-sandboxing).

## Detection and response

By the five NIST Cybersecurity Framework functions:

| Function | nono | Wraith Box V1 |
|---|---|---|
| Identify | Prints the capabilities before a run. `nono why` explains a denial. Signed profile packs | Assets and adversary in T00-index |
| Protect | Seatbelt or Landlock, proxy, credential placeholders, attestation, tool sandboxes | A VM per trust level, default-deny egress, host credentials, scoped git writes |
| Detect | Audit log, denial diagnostics, executable hash | Detection Finding (2004) events, OCSF audit log (S09-11-audit) |
| Respond | Approvals, rate limits, ending the session | Approvals (S09-09-approvals), stopping the VM |
| Recover | Rollback snapshots | Work returns as a branch, `wb vm reset-data`, image rebuild |

nono covers more of Detect and Recover on the user's machine, because
its agent works on the user's real files. Wraith Box needs less
Recover there: the host tree isn't written.

nono's Detect features differ from Wraith Box's in these ways:

- **Its audit integrity is finalized at the end of the session.** The
  event log is appended during the run, but the hash chain head,
  Merkle root, ledger entry and signature are written when the session
  ends (`audit.mdx`, "Limits"). A session that the supervisor doesn't
  finish has events without a chain head. Wraith Box writes each event
  as it happens and stops the VM when a write fails (S09-11-audit). It
  has no hash chain, which matters less on one user's machine where
  the guest can't reach the log.
- **Denials are explained to the agent.** nono's Claude Code plugin
  has `PostToolUse` and `PostToolUseFailure` hooks that tell Claude a
  failure came from the sandbox and that `nono why` explains it
  (`claude.mdx`). Wraith Box's refusals reach the guest as a 403 or 502
  that names the rule, or as a TCP reset (S07-egress-gateway,
  NFR06-explained-refusals). A 403 body reaches Claude as text, and a
  reset has no text. A hook in the guest image could turn a failed
  tool call into "the host refused this, run `wb` on the host to see
  why". Guest root can turn the hook off, so it only helps the agent
  and doesn't enforce anything.

The layers that could detect, from inside the guest outward, are the
ones R01-defenseclaw named: Claude Code's own permission modes and
rules, hooks in the guest, `wb-proxyd` on the host, and the audit log
with a SIEM. nono adds one more inside the guest, a capability sandbox
around the agent, which guest root can also turn off. Which layer
detects what, and what a finding makes the user do, is still
undecided (R01-defenseclaw, "Detection and response").

## License

nono is Apache-2.0, which S10-tech-stack allows. Wraith Box could copy
data with the license notice: the host groups of
`crates/nono-cli/data/network-policy.json`, the credential paths of
`policy.json`, or the `dangerous_commands` list. This note didn't
check the licenses of nono's Rust dependencies, which matters only if
Wraith Box ever ships nono itself. `NEP-0007`'s approval wire format
could become an integration contract, but it is a draft.

## Recommendations

- **Don't integrate with nono, and don't use it as a component.**
  Claude Code doesn't run on the host, and Wraith Box's host processes
  have sandboxes of their own (S04-03-self-sandboxing). In the guest,
  nono overlaps S13-guest-confinement, which Wraith Box generates on
  the host and checks with spikes, and its defaults open what Wraith
  Box keeps closed.
- **Keep nono inside the guest possible.** A user may want it as extra
  defense in depth. Wraith Box needs nothing for that, but nobody has
  tried it: a second `sandbox_init` inside a process that
  S13-02-filesystem-profile already confines may be refused. Testing
  it needs a macOS guest, so it is an open point.
- **Keep a host decider possible.** Later, for Linux guests
  (S12-platforms), a decider like the one in `NEP-0007` is the
  interface where nono and `wb-hostd` could meet, with Wraith Box's
  rule that the host dials and never listens.
- **Re-review after V1.** nono tags about every two days, and
  `NEP-0007` may land. A re-review after V1 (I240) updates this note
  and then files precise issues.

Candidates for that re-review, roughly in order of value:

- Check, in a guest, whether S13-02-filesystem-profile lets a project
  user connect to another project user's Unix socket. If it does,
  allow Unix-socket connects only to named paths, such as
  `wb-guestd`'s socket, as nono's generator does
  (S13-02-filesystem-profile, SEC08-proj-isolation).
- Add a hook to the guest image that tells Claude a failed tool call
  was a host refusal and where to read why (S14-claude-in-guest,
  NFR06-explained-refusals). Write it so that nothing depends on it.
- Compare nono's per-command credentials and endpoint rules, such as
  a `gh` that alone gets the GitHub token, with what
  S13-03-network-attribution and X14-flow-attribution can attribute
  per program from the host.
- Name the guest image build, and the Claude Code version and SHA-256
  that S14-01-install checked, in the VM start record of
  S09-11-audit. nono hashes the executable it starts. Wraith Box knows
  it from the host side, which the guest can't change.
- Revisit trust in instruction files when Wraith Box carries in user
  configuration beyond V1's allowlist (S14-02-user-configuration).

Not to adopt:

- A same-kernel sandbox as the boundary (SEC01-separate-kernel).
- Open default egress, uninspected CONNECT, and allowed private ranges
  (SEC05-default-deny).
- A credential in the agent's environment (SEC04-no-guest-secrets).
- Grants on the keychain, or writes to the host's `~/.claude`
  (SEC02-no-host-fs-share).
- A command deny list as a control. A renamed or rebuilt binary passes
  it, and guest root controls the guest's `PATH`.
- Audit integrity that exists only once the session ends.

**Status:** Read 2026-10-25, re-review after V1 (I240)
