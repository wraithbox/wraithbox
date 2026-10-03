# Wraith Box

Run Claude Code inside an isolated virtual machine, with one command:

```bash
cd ~/src/my-project
wb claude                      # instead of: claude
wb claude --resume             # everything after `claude` goes to claude unchanged
wb --isolated claude           # wb's own flags go before the command
wb diff <session>              # review the work that came back
wb land <session>              # fetch it into your repo as a branch
```

`wb` is the single entry point, `gh`-style: `wb [wb flags] <command> [args]`.

> **Status: design phase.** Nothing works yet. The design lives in
> [`docs/spec/`](docs/spec/); the code under `packages/` is a compiling
> skeleton.

## Why

Coding agents are most useful when they can run tools without asking for
permission at every step. Doing that directly on your Mac hands the agent
everything you can reach: your files, credentials, other repositories and
accounts. A single prompt injection in a README, an issue or a dependency
is enough to turn that access against you.

Wraith Box assumes the agent may be adversarial and capable, and enforces
every control that matters **outside** the sandbox:

- **Separate kernel.** Claude Code and every tool it runs live in a guest
  VM with native tooling for its OS. First target: macOS guests on Macs
  (Apple Virtualization framework) with Homebrew, Xcode command line
  tools, optionally Xcode.
- **No host files.** Nothing from your machine is mounted into the guest. The
  project arrives as a git clone; the agent's work comes back as a git
  branch you review and land. Changes to files your machine or CI might
  execute (hooks, build scripts, workflows) are flagged.
- **No secrets in the guest.** Not even the model credential. The guest
  holds placeholders; a host-side proxy strips whatever the guest sends
  and injects the real credential, so a token smuggled in by an attacker
  is never used.
- **Default-deny network.** Every packet from the guest goes to a
  userspace network stack on the host. Only allowlisted hostnames
  resolve, connections go only through a policy-enforcing proxy, pushes
  are limited to the project's own repositories, and fresh or vulnerable
  package versions are refused.
- **Fast.** A warm VM and a git clone on guest-native storage aim for a
  prompt in a few seconds and near-native filesystem speed.

## How it fits together

```
wb ──▶ wb-hostd (Go) ── vsock ──────────▶ guest VM ── project users ── claude + tools
          └─ wb-vmd (native: Swift on macOS)   │ runs the VM
       wb-netd (Go, gVisor) ◀──────── virtual NIC
          └──▶ wb-proxyd (Go): policy · credentials · dependency gate ──▶ allowlisted Internet
```

Start reading at [003 - Requirements and Threat Model](docs/spec/003-requirements.md),
then [004 - Architecture](docs/spec/004-architecture.md).

| Spec | Topic |
|---|---|
| [003](docs/spec/003-requirements.md) | Requirements and threat model |
| [004](docs/spec/004-architecture.md) | Architecture: processes and boundaries |
| [005](docs/spec/005-cli.md) | The `wb` command |
| [006](docs/spec/006-vm-lifecycle.md) | Images, VMs, guest users, guest agent |
| [007](docs/spec/007-egress-gateway.md) | Network stack, DNS, proxy, dependency gate |
| [008](docs/spec/008-workspace-and-git.md) | Workspace and the git round trip |
| [009](docs/spec/009-policy-credentials-audit.md) | Policy, credentials, certificates, audit |
| [010](docs/spec/010-tech-stack.md) | Languages, libraries, packaging |
| [011](docs/spec/011-verification-and-spikes.md) | Verification and open questions |
| [012](docs/spec/012-platforms.md) | Host and guest platforms, WSL |

## Platforms

| Host ↓ / Guest → | macOS | Linux (Ubuntu LTS) | Windows 11 |
|---|---|---|---|
| macOS 15+, Apple Silicon | **first target** | later | later |
| Windows 11 Home / Pro (and `wb` inside WSL) | — | later | later |
| Linux (Ubuntu LTS) | — | later | later |

No root or administrator rights and no extra user accounts on the host
at run time. Most code is cross-platform Go; platform-native code (Swift
on macOS) is kept to small, separate components. See
[spec 012](docs/spec/012-platforms.md).

## Development

Go (built and tested on macOS, Linux and Windows) and Swift (macOS),
plus an Astro Starlight documentation site published at
[wraithbox.nl](https://wraithbox.nl). Toolchains and tasks are managed
by [`mise`](https://mise.jdx.dev/); Swift comes from Xcode.

```bash
mise trust && mise install   # pinned toolchains
mise run install             # docs site dependencies
mise run ci                  # lint + typecheck + test + build (offline)
mise run vuln                # dependency vulnerability scan (network)
mise run doc:dev             # docs site dev server
```

```
packages/
  wraithbox-go/      wb, wb-hostd, wb-netd, wb-proxyd, wb-guestd, internal/platform
  wraithbox-swift/   wb-vmd for macOS and the WraithBoxVM library
  wraithbox-doc/     documentation site
docs/spec/           specifications
```

See [AGENTS.md](AGENTS.md) for conventions and [CONTRIBUTING.md](CONTRIBUTING.md)
for the contribution process.

## License

Apache-2.0. See [LICENSE](./LICENSE).
