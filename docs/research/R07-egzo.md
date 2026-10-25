# R07 - egzo

**Purpose:** What egzo is and how it relates to Wraith Box: the
threats and defenses of egzo that Wraith Box lacks, and what to do
about them. Read at egzo commit `550e5d6` (five commits after the `0.10.0` tag of
2026-10-23, committed 2026-10-25).

**Requirements:** SEC01-separate-kernel, SEC03-no-host-exec,
SEC04-no-guest-secrets, SEC05-default-deny, SEC06-repo-writes,
SEC08-proj-isolation, SEC10-audit, NFR08-licensing,
T01-allowed-channels, T11-shared-vm-grants

## Sources and method

I cloned the maintainer's local checkout at `~/git/oss/egzo` with
`git clone --no-checkout` into `.scratch/src/egzo/`, and read it only
with `git ls-tree`, `git show HEAD:<path>`, `git grep` and `git log`.
I checked out no file of it, so nothing had to be removed. Its root
has a `CLAUDE.md` with rules for agents that work on egzo: failing
specs are its to-do list, how to test the hub, and why instance names
skip numbers. I read it as data and followed none of it. Its rules
are about egzo alone. No egzo build, test or binary ran.

Facts below come from those commands. `git log` gives the history:
233 commits from 2026-10-03 to 2026-10-25, 231 of them by Cedric
Veilleux under two addresses and two by a pin-bump bot.
`git tag --sort=creatordate` gives 16 tags, from `0.0.1-rc1` on
2026-10-08 to `0.10.0` on 2026-10-23. File citations are paths in
egzo's tree.

## What egzo is

egzo (`egzo-ai/egzo`, `AGPL-3.0-only`, copyright Neopeak Internet
Solutions inc.) calls itself "a CLI tool to sandbox coding agents and organize
your projects" (`README.md`). One YAML file, `egzo.yaml`, declares a
project's secret vaults, egress profiles, workspaces and agent
templates. `egzo project up` turns it into containers on a Docker or
Podman engine, and `egzo agent spawn` starts agent instances from the
templates. egzo doesn't run a daemon: the CLI reads its state back from the
engine (`DESIGN.md`, "Principles"). egzo is written in Go, with a
Svelte web app for the optional hub.

- **Agents** run Claude Code or OpenCode in Linux containers, one
  container and one internal network per instance, as the user's uid,
  with every capability dropped and `no-new-privileges`. gVisor is
  optional, as the engine's default runtime (`docs/security.md`). The
  README calls the project "in active development" and the CLI and web
  UI "stable enough for testing and non-production work". Linux hosts
  with Docker, rootless Podman or gVisor are tested. macOS and Windows,
  through Docker Desktop's Linux VM, are "untested"
  (`roadmap/windows-and-macos.md`).
- **A proxy sidecar** is each agent's only way out. It allows HTTPS on
  port 443 to the hosts of the agent's profile and refuses every
  non-public address. It relays allowed hosts as tunnels checked
  against the SNI, and terminates TLS with a project CA for hosts that
  get a secret. Secrets are read from `pass` or the environment by the
  CLI on the host and kept in the proxy's memory. The proxy either
  sets a header (`inject`) or swaps a placeholder the agent was given
  (`placeholder`). A second listener terminates SSH and connects to
  the servers a profile names with a key the proxy holds
  (`docs/sandboxing.md`).
- **A control sidecar** serves an MCP server to every agent, for
  messages between people and agents and for status reports
  (`docs/messaging.md`).
- **Git workspaces** are a base clone in an engine volume, with one
  worktree and one branch `egzo/<instance>` per agent. Agents push
  with a token the proxy injects for `github.com`
  (`docs/workspaces.md`).
- **The hub** is a separate container that shows the agents of one or
  more engines, with their terminals in a browser, OpenID Connect
  sign-in and roles per project (`docs/hub.md`,
  `docs/hub-access.md`).

Agents run in the harness's bypass permission mode by default: "the
sandbox, not the harness's permission prompts, is the security
boundary" (`docs/security.md`, `docs/harnesses.md`).

egzo's threat model is `docs/security.md`: what egzo enforces, and a
list of what it doesn't protect against. The `known-issues/`
directory adds accepted risks with their reasons and possible
mitigations, such as
`known-issues/git-base-clone-is-trusted-by-every-agent.md` and
`known-issues/a-secret-is-injected-in-every-request-to-its-hosts.md`.
`specs/` holds pytest suites that drive the real binary against a real
engine, among them containment specs that "assume the agent is
hostile".

## How it relates to Wraith Box

| | egzo | Wraith Box V1 |
|---|---|---|
| Users | A developer or a small team, through a CLI and an optional web hub | One developer, through `wb` (V1-05-cli-only) |
| Agents | Claude Code and OpenCode, many instances at once, which message each other | Claude Code (V1-02-claude-only), sessions per project |
| Isolation | A Linux container per agent, with optional gVisor. All agents share the engine's kernel | A macOS VM per trust level (SEC01-separate-kernel) |
| Hosts | Linux. macOS and Windows through Docker Desktop, untested | macOS (S12-platforms) |
| Permission mode | Bypass by default | The user's own (S14-04-commands-and-endpoints) |
| Egress | Default deny. HTTPS on port 443, tunneled by SNI unless a secret, `inspect` or ECH needs termination | Default deny with TLS inspection on the host (SEC05-default-deny) |
| Policy unit | One profile per agent template, checked by a token per instance | One union of grants per VM (T11-shared-vm-grants) |
| Credentials | `inject` in a header for every path of a host, or a placeholder swapped anywhere in a request | Injected in the binding's header on its paths only. A placeholder anywhere else is refused (S07-09-placeholder-binding) |
| SSH | To servers a profile names, with a key the proxy holds | Refused to forges. Elsewhere only through a raw TCP entry, with no key (S07-15-non-http-streams) |
| Returned work | The agent pushes to the remote with an injected token, or the user copies files out with `egzo workspace get` | A branch in a landing repository (FR03-work-as-branch), with risky paths flagged (SEC03-no-host-exec) |
| Repository writes | Whatever the token allows on `github.com` | Scoped per VM to the projects' repositories (SEC06-repo-writes) |
| Terminal | Raw pass-through, OSC 52 included (`DESIGN.md`, "Session backend") | Filtered by an allowlist (S16-terminal-stream) |
| Audit | JSON lines on the proxy's standard output, kept by the engine's log driver | OCSF 1.8.0 JSONL on the host (SEC10-audit) |

**Overlap.** Both projects run Claude Code behind a default-deny
egress proxy that the agent can't bypass, hold credentials outside
the agent, and keep the user's own files away from it. egzo's
`egzo agent spawn claude --attach` is the job of `wb claude`.

**Where they compete.** Little, today. egzo is for Linux hosts and
Linux toolchains, and its value is in running many agents side by side
and talking to them. Wraith Box is for one developer on a Mac who
needs macOS and Xcode in the sandbox (FR06-native-tools), and puts a
separate kernel between the agent and the host. A Mac user could run
egzo in Docker Desktop's Linux VM, which egzo hasn't tested. Then egzo's agents,
its sidecars and the engine all share that one Linux kernel.

**Shared ancestry.** None. egzo depends on the Docker client, the MCP
Go SDK and `golang.org/x/crypto` (`go.mod`), and on none of
OpenShell's artifacts or OCSF.

**Where the two meet today.** Nowhere. Running egzo inside a Wraith
Box guest needs containers in the guest, which V1 leaves out
(V1-04-no-containers). In the other direction, egzo has no macOS
guest to offer.

## Where Wraith Box's controls differ

Each row of the table where egzo is looser follows from a rule Wraith
Box already has. The ones below are those where egzo's own documents
explain the trade-off.

- **A placeholder can come back to the agent.** egzo swaps a
  placeholder in the URL path and query, in every header, and in the
  body of a request to a placeholder host. It "never looks at
  responses" (`DESIGN.md`, "Placeholders",
  `internal/proxy/placeholders.go`). Its own test
  `TestResponsesAreNeverSwapped` sends a placeholder in a query string
  to a server that echoes it, and expects the response to hold the
  real secret (`internal/proxy/placeholders_test.go`). So on a
  placeholder host that shows what it receives, such as a search
  field, a profile page or a form that echoes its input, the agent can
  read the secret back. `docs/security.md` says "the agent cannot read
  the secret". That holds for `inject`, not for `placeholder`.
  R05-iron-proxy found the same gap in iron-proxy's `replace` mode.
  Wraith Box refuses a placeholder anywhere but the header its binding
  injects (S07-09-placeholder-binding). egzo built placeholders for a
  password the agent types into a login form in a browser, which
  Wraith Box can't offer.
- **A secret goes with every path of its host.** egzo binds a secret
  to hosts only. The GitHub token goes with every request to
  `github.com`, the website included. egzo accepts this and suggests a
  path prefix as a later mitigation
  (`known-issues/a-secret-is-injected-in-every-request-to-its-hosts.md`).
  Wraith Box injects only on the paths its binding names
  (S07-08-requests-and-credentials).
- **Pushes aren't scoped.** An agent with the `github` service pushes
  to any repository its token may write, and a deleted branch or a
  force-push goes to the remote directly. egzo's answer is a
  fine-grained token. Wraith Box scopes writes per VM
  (SEC06-repo-writes) and returns work as a branch that the user
  fetches with `wb land`. Data sent through an allowed host is a risk
  both accept: egzo in `docs/security.md`, "What it does not protect
  against", and Wraith Box in T01-allowed-channels.
- **The kernel is shared.** egzo treats gVisor as an option and the
  engine as trusted, and says "a kernel escape from a normal container
  is possible in principle" (`docs/security.md`). Wraith Box has no
  same-kernel mode (SEC01-separate-kernel).
- **The terminal is raw.** `egzo agent attach` passes "all byte values
  both ways", and keeps OSC 52 clipboard writes and OSC 8 links on
  purpose (`DESIGN.md`, "Session backend"). So an agent can write the
  user's clipboard through an attached terminal. Wraith Box drops both
  (S16-02-drops-and-links). egzo does filter the text it prints in its
  own tables (`internal/termsafe/termsafe.go`), as Wraith Box does for
  push summaries (S08-03-pushes).
- **The upstream address list is shorter.** egzo's proxy refuses
  loopback, private, link-local, CGNAT, documentation and reserved
  ranges, and `64:ff9b:1::/48` (`internal/proxy/limits.go`, `isPublic`).
  The list doesn't name the well-known NAT64 prefix `64:ff9b::/96`,
  6to4 or Teredo, which S07-07-hosts-and-modes, "Upstream address",
  names. The Azure WireServer `168.63.129.16` is refused only for SSH
  (`internal/proxy/ssh_upstream.go`), and Wraith Box doesn't list it
  at all (R01-defenseclaw).

On ECH the two projects reached the same answer on their own. A proxy
can't tell a GREASE ECH extension from a real one, so egzo terminates
TLS for any hello with ECH instead of tunneling it (`DESIGN.md`,
"Egress proxy and TLS"). Wraith Box inspects by default, and resets
such a hello on a pass host (S07-16-tls-and-tunnels).

## Threats egzo covers that Wraith Box doesn't

- **SSH credentials for operations work.** egzo lets an agent run
  `ssh prod` to a server its profile names, without holding the key.
  The proxy terminates the agent's SSH with an identity derived from
  the instance's token, connects to the server with the profile's key,
  fixes the remote user, and refuses a server whose host key isn't the
  pinned one. It refuses port, agent and `X11` forwarding and every
  subsystem but `sftp`, and audits each session and command
  (`docs/sandboxing.md`, "ssh to servers", `internal/proxy/ssh*.go`).
  In Wraith Box an SSH key is a host secret (SEC04-no-guest-secrets).
  The only path to a server is a raw TCP entry (S07-15-non-http-streams),
  so a user who wants Claude Code to read a server's logs has to copy
  a key into the guest, which breaks the control by hand. The asset is
  the user's server accounts, which T00-index lists among "online
  accounts". The fix would be an SSH binding in `wb-proxyd`
  (S07-15-non-http-streams, S09-05-credentials). It reads the guest's
  SSH handshake and channel requests, so it needs a fuzz target. Such work
  is outside V1, which is about development work in a repository.
- **Per-agent policy on one machine.** Each egzo instance has its own
  token, and the proxy applies that instance's profile. So a reviewer
  agent and a deploying agent of one project get different egress, and
  the container boundary keeps their tokens apart. In Wraith Box,
  sessions in one VM share the VM's grants (T11-shared-vm-grants), and
  a guest program's identity is a label that guest root can forge
  (T06-forged-labels). Wraith Box's answer is another VM, through
  `placement = isolated` (SEC08-proj-isolation). That is coarser, and
  the boundary between the two is a kernel.
  Nothing to adopt: a finer unit inside one VM would rest on a label.

egzo's other risks don't apply to Wraith Box, or Wraith Box already
holds them:

- **Agents that message each other.** egzo stops a message from
  forging its origin and bounds loops, open requests and depth
  (`docs/security.md`, "Messages"). What a message says still reaches
  the recipient as text, so a prompt injection can pass from an agent
  with little access to one with more. Wraith Box has no channel
  between sessions.
- **A shared base clone.** Every agent that lists a git workspace has
  the base clone's `.git` mounted read-write. One agent can rewrite
  another's branch, or plant a `filter` or `core.sshCommand` setting
  that runs in the next agent's prep container with that agent's
  token (`known-issues/git-base-clone-is-trusted-by-every-agent.md`).
  Wraith Box's host never runs git with configuration the guest wrote:
  pushes go into `landing.git` with hooks off
  (S08-03-pushes, S08-08-host-git). Clones of projects in one VM are
  kept apart by guest users, and T05-cross-proj-clones accepts the
  rest.
- **The hub and the engine socket.** Whoever holds the engine socket
  or is a hub operator controls every agent. Wraith Box has one local
  user and no web UI.
- **Files copied out.** `egzo workspace get` copies an agent's files
  to the host with no check. Wraith Box returns work only as a branch,
  and flags risky paths (SEC03-no-host-exec, S08-09-risky-changes).

## Detection and response

In the NIST Cybersecurity Framework functions that R01-defenseclaw
uses, egzo's controls are Protect, like Wraith Box's, with a
few parts in the other functions.

- **Identify.** `egzo proxy rules` and `egzo project check --resolved`
  print what each agent may reach and which credentials go where.
  `egzo ps` and the hub list every agent.
- **Detect.** The proxy logs one JSON event per step of a connection,
  with a reason code from a fixed list, bytes and duration on close,
  method and path on a terminated host, and each SSH command
  (`docs/sandboxing.md`, "See what happened"). Claude Code hooks in the
  agent report whether it is working, idle or blocked
  (`internal/harness/claudecode.go`), for the operator's view rather
  than for security. Like a hook of R01-defenseclaw, the agent could
  turn them off.
- **Respond.** `egzo agent rm`, a hub operator who disables a person
  and closes their terminals, and `egzo project rotate-ca`.
- **Recover.** An agent's committed work stays on its branch in the
  base clone after `agent rm`.

The audit log is the proxy container's standard output, so the
engine's log driver decides how long the log lasts
(`known-issues/container-logging-follows-the-engine-default.md`).
A Docker engine left at its default keeps it unbounded, and a bounded
one can rotate the oldest events away. Wraith Box writes its audit log
to a file on the host (SEC10-audit).

The SSH audit adds a case to the open question of R01-defenseclaw,
which layer detects what: with an SSH binding, `wb-proxyd` would see
each command the guest runs on a server, as it sees each request on
an inspected host. egzo has no detector beyond its log, and adds
nothing else to that question.

## License

egzo is `AGPL-3.0-only`, a copyleft license, and every source file
starts with the SPDX header. S10-tech-stack and NFR08-licensing allow
permissive licenses only. So Wraith Box can't use egzo's code as a
dependency, and shouldn't copy its code, tests, lists or text either.
Ideas from its documents can be written into Wraith Box's specs in
Wraith Box's own words, and built from those specs.

## Recommendations

- **Don't integrate with egzo.** It has no contract another program
  could serve, its isolation is a container on a shared kernel, which
  SEC01-separate-kernel rules out, and its guests are Linux. Running
  it inside a guest needs containers, which V1 leaves out.
- **Don't use or vendor any part of it.** The license rules that out
  (NFR08-licensing).
- **Use its documents as review material.** Its known issues are
  candid write-ups of trade-offs Wraith Box made the other way. When
  V1-M3-network-floor builds `wb-proxyd`, the echoed placeholder
  (`TestResponsesAreNeverSwapped`) and the GREASE ECH hello are cases
  for the tests of C05-credentials-and-store and C02-streams-and-tls.
  Write those tests from Wraith Box's specs, not from egzo's code.
- **No new issue.** I240 already covers "any comparison note added
  before the review starts", so the re-review after V1 reads egzo
  again with the other notes and updates this one. egzo went from its
  first commit to `0.10.0` in three weeks, so much of this note may
  be out of date by then.

Candidates for that re-review, roughly in order of value:

- An SSH binding in `wb-proxyd` for operations work, with a pinned
  host key and a fixed remote user. It would refuse forwarding and
  audit each command. It would change S07-15-non-http-streams and
  S09-05-credentials, keep SEC04-no-guest-secrets, and read guest SSH
  bytes, so it needs a fuzz target. Decide first whether operations
  work is in Wraith Box's scope at all.
- The Azure WireServer in the upstream address list of
  S07-07-hosts-and-modes, already an I240 candidate from
  R01-defenseclaw. egzo's SSH dialer is one more project that refuses
  it.

Not to adopt: bypass mode by default (S14-04-commands-and-endpoints
rejects it), a placeholder swapped anywhere in a request, a secret
bound to every path of a host, pushes with an agent-held token to the
whole forge, a shared kernel as the boundary, a raw terminal with
clipboard writes, an `env` secret backend, and agent-to-agent
messages.

**Status:** Read 2026-10-25, re-review after V1 (I240)
