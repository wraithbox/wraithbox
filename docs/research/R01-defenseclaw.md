# R01 - DefenseClaw

**Purpose:** What Cisco's DefenseClaw is, how it relates to Wraith Box,
which of its threats and defenses Wraith Box lacks, and what to do
about it. Read at DefenseClaw commit `039114a1` (1.0.x, October 2026).

**Requirements:** SEC03-no-host-exec, SEC05-default-deny,
SEC06-repo-writes, SEC10-audit, T01-allowed-channels,
T04-bad-approvals

## What DefenseClaw is

DefenseClaw (`cisco-ai-defense/defenseclaw`, Apache-2.0, first commit
March 2026, 1.0.0 on 2026-10-11) calls itself "security governance for
AI coding agents". It is a Python CLI, a Go gateway daemon on the host,
connector code per agent, policy packs and telemetry exporters. It has
two modes.

- **Hook mode**, the main product. DefenseClaw registers hooks in about
  fifteen agents, Claude Code, Codex, Cursor and Copilot CLI among
  them. Each hook posts the prompt or tool call to the gateway, which
  answers allow, deny or ask. The gateway decides with regex rule packs,
  CEL rules over parsed tool calls, Rego, an optional LLM judge and the
  optional Cisco AI Defense cloud API. The agent runs as the user, with
  no network enforcement ("Hook rules only. There is no network
  enforcement for a host-run agent", `segmentation.mdx`). The guardrail
  starts in `observe` mode, which logs and never blocks.
- **Sandbox mode.** DefenseClaw runs the agent inside NVIDIA OpenShell
  0.1.x: a container with Landlock and seccomp on Linux, a libkrun
  MicroVM on macOS, both with a Linux guest. Its own summary is
  "OpenShell owns isolation, DefenseClaw owns judgment"
  (`internal/openshell/doc.go`). DefenseClaw adds a host egress proxy,
  credential placeholders bound to the harness binaries, a workspace
  snapshot with an end-of-session review, and the hooks of hook mode
  inside the sandbox (`docs/SANDBOX.md`).

Most of the threat model work is about the `managed_enterprise`
deployment: keeping DefenseClaw's own services, hooks and policy intact
on a fleet machine against a standard user, the agent running as that
user, and a compromised gateway (`docs/ENTERPRISE-THREAT-MODEL.md`,
rows `B1` to `B21`, residual risks `R1` to `R34`). It doesn't model prompt
injection as a threat. Prompt injection detection is a guardrail
feature with published benchmarks instead.

## How it relates to Wraith Box

| | DefenseClaw | Wraith Box V1 |
|---|---|---|
| Agents | About fifteen | Claude Code (V1-02-claude-only) |
| Isolation | None in hook mode. OpenShell in sandbox mode | A macOS VM per trust level (SEC01-separate-kernel) |
| Guest | Linux | macOS, with Xcode (FR06-native-tools) |
| Egress | Hook rules only, or a proxy that relays TLS without reading it | Default deny with TLS inspection on the host (SEC05-default-deny) |
| Repository writes | Not scoped: the proxy can't see a push | Scoped per VM (SEC06-repo-writes) |
| Returned work | A live mount with undo, or a copy the user pulls | A branch in a landing repository (FR03-work-as-branch) |
| Agent actions | Inspected by hooks and rule packs | Not inspected |
| Audit | SQLite, OTLP, Splunk HEC, its own v8 schema | OCSF 1.8.0 JSONL (SEC10-audit) |
| Users | Fleets, MDM, an administrator | One developer on their own machine |

**Overlap.** DefenseClaw's sandbox mode, together with OpenShell, does
the job of `wb-netd`, `wb-proxyd` and the git round trip. On a Mac,
`defenseclaw sandbox run claudecode` competes directly with
`wb claude`. Wraith Box gives a macOS guest, scoped writes, the
dependency gate, and a host working tree the agent never writes.
DefenseClaw gives many agents, Linux hosts, content inspection and
fleet management, and ships today. OpenShell calls the MicroVM driver
it runs on a Mac experimental.

**Shared ancestry.** Both projects build on OpenShell artifacts.
Wraith Box uses OpenShell's network policy schema, its prover, its
supervisor middleware proto and its OCSF event classes
(S10-tech-stack). DefenseClaw uses a different part: the OpenShell
gateway's gRPC API through the Go SDK, and OpenShell's log lines, which
it parses even though it calls them "a display projection, not a wire
contract" (`internal/openshell/ocsf/ocsf.go`). It doesn't use the
middleware proto.

**No runtime abstraction.** DefenseClaw's sandbox code is OpenShell
code. The `openshell.Client` interface (`internal/openshell/client.go`)
names OpenShell's own concepts: sandboxes, provider profiles, policy
drafts and gateway settings. A second runtime would need a new client,
event parser and policy renderer. The per-agent artifacts
(`SandboxArtifacts` in `internal/gateway/connector/sandbox_artifacts.go`)
are generic in shape but assume `openshell sandbox create`.

**Where the two meet today.** Wraith Box's audit log uses OCSF, so a
SIEM that DefenseClaw exports to can read it too. A user who wants
DefenseClaw's hooks can install DefenseClaw in the guest and run its
gateway there, with no change to Wraith Box. Wraith Box would then
treat the hooks as one more program in the guest, with no effect on
its controls.

## Threats DefenseClaw covers that Wraith Box doesn't

- **Destructive writes through a granted channel.** DefenseClaw's
  opt-in rule packs refuse `terraform destroy`, `DROP DATABASE`, cloud
  bucket deletes and Kubernetes namespace deletes
  (`policies/guardrail-use-cases/`). Wraith Box's equivalent is the
  write grant of SEC06-repo-writes. The git hosting profile allows any
  `git-receive-pack` request to the repositories of the projects in the
  VM (S07-05-hosts-and-modes, "Modes, per host"). A guest with that grant
  can force-push over the default branch, and delete branches and tags,
  limited only by the hosting service's own branch protection. T00-index
  lists the user's repositories as an asset, and no accepted risk covers
  this. `wb-proxyd` inspects the push, so it can read each ref update
  and refuse deletions, or writes outside a branch prefix.
- **Cloud host services.** DefenseClaw's egress guard never reaches the
  Azure WireServer (`168.63.129.16`, a public address) or the
  deprecated IPv6 site-local range `fec0::/10` (`docs/SANDBOX.md`,
  "Decision order"). The upstream address list of S07-egress-gateway
  has neither. It already covers the Google Cloud and Oracle Cloud IPv6
  metadata addresses through `fc00::/7`, and the AWS one through
  `169.254.0.0/16`. This matters once a host is itself a cloud VM, such
  as an EC2 Mac instance, or an Azure Windows or Linux host
  (S12-platforms).
- **Known exfiltration services in an approval.** DefenseClaw embeds a
  blocklist of paste sites, anonymous file drops, webhook catchers,
  tunnels and anonymizers (`policies/sandbox/egress/blocklist.yaml`).
  Wraith Box asks the user about any unknown host
  (FR09-approve-unknown), and T04-bad-approvals accepts that the user
  may approve a bad one. An approval for `webhook.site` that says what
  the service is for would be harder to approve by accident.
- **Large uploads.** DefenseClaw reports an upload over 25 MiB to a
  destination it hasn't seen before (`docs/SANDBOX.md`, "Byte counts and
  large uploads"). T01-allowed-channels accepts data sent through
  allowed channels. Wraith Box already records the bytes of each
  connection (SEC10-audit). A finding built on those counts would show
  the user when a guest uses such a channel in bulk.
- **Risky returned files.** DefenseClaw's end-of-session review flags,
  with a severity, some paths that the list in S08-09-risky-changes, doesn't name: dev containers,
  package-manager configuration that runs code (`.yarnrc.yml` with
  `yarnPath`, `.pnpmfile.cjs`, `.npmrc`), Python tooling (`conftest.py`,
  `setup.py`), version-manager files that define tasks (`mise.toml`),
  IDE run configurations, and ignore rules changed so that they hide
  paths from the diff.

DefenseClaw's other threats don't apply to Wraith Box:

- Most are about hook integrity: an agent or user that removes or
  kills a hook, or starts the agent with another configuration root
  (`R1`, `R18`). Wraith Box doesn't enforce anything inside the guest.
- Others are about fleet administration: MDM precedence, enrollment,
  and the administrator's lifecycle. Wraith Box has one user, who is
  trusted (T00-index).
- Some cover a live mount of the host working tree, such as planted
  `.git` directories and undo. Wraith Box never writes the host working
  tree (FR03-work-as-branch).

## Detection and response

The NIST Cybersecurity Framework sorts security work into five
functions: Identify, Protect, Detect, Respond and Recover. Wraith Box
V1 is almost all Protect. It keeps the agent away from the host, its
secrets and its network, whatever the agent does inside the guest.
DefenseClaw is mostly Detect, with Respond through its approvals,
blocks and SIEM exports. Its Protect is weak where it has no sandbox.

Wraith Box assumes the guest is compromised, so a detector inside the
guest can never be a control: the agent that a prompt injection steers
can turn it off. DefenseClaw's own residual risks `R1` and `R18` show this
for hooks on the host. A detector is still useful as defense in depth
if nothing depends on it. It can stop an attack before the attacker
reaches the boundary, and it tells the user what was attempted.

Layers that could detect, from inside the guest outward:

- **Claude Code itself.** Its permission modes, `allow` and `deny`
  rules, and its own classifiers already stand between a prompt and a
  tool call. Wraith Box passes the user's permission mode through
  unchanged (S14-04-commands-and-endpoints, "Permission mode").
- **Hooks in the guest**, such as DefenseClaw's. They see the prompt,
  the tool call and its arguments, which no host component sees. Guest
  root can turn them off.
- **`wb-proxyd` on the host.** It reads every inspected request, so it
  can run content rules, such as known exfiltration services, secrets
  in a request body, or prompt injection markers in a response, without
  the guest being able to turn them off. Its findings would be
  Detection Finding (2004) events, as the existing ones are
  (S09-policy-credentials-audit).
- **The audit log and a SIEM.** Patterns across events, such as a burst
  of refusals or a large upload after a read of credentials, need the
  whole log.

Wraith Box hasn't decided which of these it relies on, what each
covers, or what the user does with a finding: Respond (stop the VM,
revoke a grant, quarantine a returned branch) and Recover (restore the
guest, roll back pushed work). Wraith Box's Identify part is small: its
assets and adversary are in T00-index, and it has no inventory of
agents, skills or MCP servers in the guest.

## Recommendations

- **Don't integrate with DefenseClaw now.** For DefenseClaw to run
  Claude Code in Wraith Box, Cisco would have to write a second sandbox
  runtime, or Wraith Box would have to serve OpenShell's gateway API.
  DefenseClaw's client of that API has 37 methods, and OpenShell is at
  release 0.1.x. DefenseClaw's in-guest hooks also need a host listener
  the guest can reach, one more surface for adversarial input
  (S04-architecture), for detection that guest root can turn off.
- **Keep integration possible.** Stay on OpenShell's schemas and on
  OCSF for audit events, so that DefenseClaw or another tool can read
  Wraith Box's policy and log later without a custom parser.
- **Re-review after V1.** The space changes monthly. DefenseClaw went
  from 0.7 to 1.0 in four months, and its sandbox mode is still "built
  in layers" (`docs/SANDBOX.md`, "Build status"). A re-review after V1
  (I240) updates this note and then files precise issues.

Candidates for that re-review, roughly in order of value:

- Scope pushes within a granted repository: refuse ref deletions and
  writes outside a branch prefix, or record the risk in T00-index. This
  reads guest-controlled pkt-lines, so it needs a fuzz target.
- Add the Azure WireServer address, `fec0::/10`, and any other cloud
  host-service address to the upstream address list of
  S07-egress-gateway.
- Show the category of a known exfiltration service in an approval
  prompt. DefenseClaw's list is Apache-2.0 and could be copied with its
  license notice.
- Compare the risky path list of S08-workspace-and-git with
  DefenseClaw's review list, and consider severity levels.
- Add a large-upload Detection Finding for a destination the VM hasn't
  sent to before.
- Decide the Detect, Respond and Recover parts of Wraith Box: which
  layer detects what, and what a finding makes Wraith Box or the user
  do. This belongs in a spec before any feature.

Not to adopt: an `observe` default, hooks that fail open, an LLM judge
as a control, and fleet management.

**Status:** Read 2026-10-14, re-review after V1 (I240)
