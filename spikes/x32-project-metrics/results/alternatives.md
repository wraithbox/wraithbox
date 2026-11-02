# F: what users of the alternatives get, and what V1 offers instead

Sources: R01-defenseclaw, R04-matchlock, R05-iron-proxy, R06-nono,
R07-egzo (read 2026-10-14 to 2026-10-25). The last column is my judgment.

## What they get that V1 won't have

| Tool | What its users get that V1 lacks | Where V1 is stronger | Would the maintainer pick V1 over it? |
|---|---|---|---|
| DefenseClaw (Cisco, 1.0, Apache-2.0) | Ships today. About fifteen agents. Hook mode with no VM: zero start-up cost, host files and IDE work as usual. Content inspection of prompts and tool calls (rule packs, CEL, Rego, an optional LLM judge). Linux hosts. Fleet and MDM management, SIEM exporters | A separate kernel. A macOS guest with Xcode. Default-deny egress with TLS inspection (hook mode has no network enforcement). Scoped repository writes. The host working tree is never written. Dependency gate | Yes for isolation. Hook mode is a different product (governance, not containment). Sandbox mode is OpenShell on a Linux libkrun MicroVM, which OpenShell calls experimental on a Mac |
| matchlock (MIT, 0.2.x, experimental) | Any agent in any OCI image. Fresh Linux microVM each run, fast. SDKs (Go, Python, TypeScript) and request and file hooks. Host directories mounted as a copy or live, so the IDE loop works | Default deny (matchlock is open by default). DNS answered by the host (matchlock relays to 8.8.8.8). Credentials only in named headers and paths. No host files in the guest. Persistent macOS with Xcode. Durable audit | Only if the work needs macOS or the stronger controls. For Linux toolchains, matchlock is lighter |
| iron-proxy (Apache-2.0 core, v0.54) | An egress proxy alone, for CI and containers. Credential transforms (SigV4, GCP, OAuth 2 grants, HMAC). MCP gateway, PostgreSQL proxy. OTLP audit | It's a component, not a sandbox: it doesn't confine the workload. V1 does | Not comparable. A reference for building `wb-proxyd` |
| nono (Apache-2.0, 0.80, about one tag every two days) | `nono run -- claude` with no VM: instant start, host files, host toolchain, IDE. Any agent. Rollback snapshots. Signed instruction files (Sigstore). Hash-chained audit. Terminal or webhook approvals. Credential injection through a reverse proxy | A separate kernel (nono's own model says to put a VM around it for hostile code). Default-deny egress (nono allows every host by default, private ranges included). The host `~/.claude` is never written (nono's Claude profile writes `~/.claude` and `~/.claude.json`, so an escaped agent can plant a hook) | V1 is the safer choice against an adversarial agent. nono is far cheaper to live with day to day, and its NEP-0007 draft moves toward a VM boundary too |
| egzo (AGPL, 0.10) | Many agents side by side, messaging each other, a web hub with terminals. Agents push to the forge with an injected token, so PRs work. Bypass permission mode by default. SSH to named servers with a proxy-held key | A separate kernel (egzo uses containers on a shared kernel). macOS guests. Scoped writes. A filtered terminal. Can't be vendored (AGPL) | Different audience (Linux, multi-agent teams). Untested on macOS |

## What V1 offers that none of them do

- A **macOS guest with a native toolchain**: Xcode builds, iOS simulators, Homebrew.
  None of the five offers this. It is V1's one clear functional distinction.
- A **separate kernel and default-deny, TLS-inspected egress together**, with
  credentials never in the guest and repository writes scoped per VM.
- **Work returns as a branch**, with risky paths flagged and the host working
  tree never written by the agent.
- A **dependency gate** (minimum age, OSV) at the proxy.
- A **filtered terminal stream** (S16). The others pass guest output through raw.

## Alternatives the notes don't cover

The research notes compare open source projects. They don't compare the
alternatives a Mac developer would most likely weigh V1 against:

- **Claude Code on the host with its own permission prompts and sandboxing
  options.** This is the status quo for the maintainer today.
- **A devcontainer or Docker-based Linux sandbox**, or a hand-made macOS VM
  (for example with Tart or UTM). The repository doesn't mention any of them.

Without that comparison, "would a user choose V1" can't be answered against
the real baseline. My unverified expectation: these alternatives are all cheaper
to live with than V1, and none gives V1's guarantees against an adversarial
agent on macOS.

## Is V1 different enough to choose?

On security, yes. No compared tool combines a separate kernel, a macOS
guest, host-side default deny with inspection, and a scoped git round trip.
On fitness, V1 asks for the most change in how a developer works: no live files,
no PRs from the agent, prompts for new hosts, cold starts, and an hour-long
first run. Its users would be developers who need macOS or Xcode in the sandbox
and accept that cost for containment. For Linux-toolchain work, the lighter
tools (matchlock, egzo, or nono inside a VM) give most of the protection with
less friction. The notes say each of those tools ships fast (DefenseClaw went
from 0.7 to 1.0 in four months, iron-proxy from v0.1 to v0.54 in seven, egzo
from first commit to 0.10 in three weeks). So by the time V1 ships, the gap
may be smaller than the notes describe, and I240 (re-review after V1) is the right check.
