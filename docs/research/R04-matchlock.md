# R04 - Matchlock

**Purpose:** What matchlock is, how it relates to Wraith Box, which of
its threats and defenses Wraith Box lacks, and what to do about it.
Read at matchlock commit `77e02f6` (0.2.17, July 2026).

**Requirements:** SEC02-no-host-fs-share, SEC04-no-guest-secrets,
SEC05-default-deny, SEC10-audit, SEC13-bounded-resources,
SEC15-no-guest-disk-mount

## What matchlock is

Matchlock (`jingkaihe/matchlock`, MIT, first commit February 2026,
0.2.17 on 2026-07-26) is "a CLI tool for running AI agents in
ephemeral microVMs - with network allowlisting, secret injection via
MITM proxy, and VM-level isolation". The README marks it experimental.
One author wrote 497 of its 516 commits, under two spellings of one name.

- **One Go program.** `matchlock run --image alpine:latest` pulls an
  OCI image, turns it into an ext4 root disk and boots it with
  matchlock's own Linux kernel: Firecracker on Linux hosts, and
  Virtualization.framework through the `Code-Hex/vz` bindings on macOS.
  A guest agent in the VM runs commands over vsock. A JSON-RPC mode
  (`matchlock rpc`) drives Go, Python and TypeScript SDKs, which embed
  it in other programs.
- **Network.** Without `--allow-host`, `--secret` or
  `--network-intercept`, a macOS guest gets Virtualization.framework's
  NAT with no policy at all (`pkg/vm/darwin/backend.go`). With any of
  them, a gVisor userspace stack on the host terminates the guest's
  frames (`pkg/net/stack_darwin.go`). Port 443 is always decrypted
  with a per-VM CA, port 80 is parsed as HTTP, and any other TCP port
  is relayed when its destination IP matches the allowlist. On Linux,
  nftables redirects ports 80 and 443 of a TAP device to the same
  proxy.
- **Secrets.** `--secret NAME@host` puts a placeholder in the guest's
  environment. The proxy swaps it for the secret in the headers and
  query string of requests to the hosts the flag lists, and refuses a
  request with the placeholder in a header or URL to any other host
  (`pkg/policy/engine.go`).
- **Files.** `-v host:guest` serves a host directory to the guest over
  a FUSE-over-vsock protocol (`pkg/vfs/server.go`). The default mode
  copies the directory to a snapshot first. `host_fs` serves the host
  directory itself, read-write.
- **Hooks.** SDK callers can attach rules to network requests and
  responses (block, or rewrite headers, query, path and body) and to
  file operations (block, rewrite a write, or run code after one).
  `docs/network-interception.md` and `docs/vfs-interception.md`
  describe them.

Matchlock has no threat model document. `adrs/001-local-image-build.md`
takes the stance Wraith Box takes: the VM is the security boundary,
and the seccomp filter and dropped capabilities in the guest are
defense in depth, not the isolation.

## How it relates to Wraith Box

| | Matchlock | Wraith Box V1 |
|---|---|---|
| Users | Programs, through SDKs, and the CLI | One developer, through `wb` |
| Agents | Any command in an OCI image | Claude Code (V1-02-claude-only) |
| Guest | Linux, from any OCI image, booted fresh each run | macOS with Xcode, persistent (FR06-native-tools) |
| Hosts | Linux with KVM, macOS on Apple Silicon | macOS (S12-platforms) |
| Egress | Open by default. With an allowlist: inspected HTTP and HTTPS, other TCP by IP | Default deny with TLS inspection on the host (SEC05-default-deny) |
| DNS | Every query relayed to `8.8.8.8` | Answered by `wb-netd` for allowed names only (S07-03-dns) |
| Credentials | Placeholder replaced in any header or the query string of a bound host | Injected in the binding's header only, on named paths (S07-09-placeholder-binding) |
| Host files | Host directories served to the guest, as a copy or live | None (SEC02-no-host-fs-share) |
| Returned work | The agent pushes with the user's token, or writes a live mount | A branch in a landing repository (FR03-work-as-branch) |
| Audit | In-memory events to the SDK caller, dropped when the buffer is full | OCSF 1.8.0 JSONL on the host (SEC10-audit) |

**Overlap.** Both put an agent in a VM, terminate its network on the
host, inspect TLS with a CA of their own, and keep credentials on the
host behind placeholders. The `examples/claude-code/` helper runs
Claude Code in a matchlock VM against a GitHub repository, which is
the job of `wb claude`. Their users differ, though. Matchlock is a
sandbox library for running generated code in throwaway Linux VMs. Wraith Box is a persistent macOS workstation for one
developer's Claude Code sessions. Matchlock competes with Wraith Box
only for a user who wants a Linux toolchain and accepts its defaults.

**Shared ancestry.** Both attach a socket pair to the VM as its
network device on macOS, and run gVisor's `pkg/tcpip`
on the host in promiscuous and spoofing mode (S07-02-packet-path,
X03-network-path). Matchlock has no link filter in front of the stack,
so gVisor answers ARP for every address, as B17-network-path found.
Its Linux guest gets a static address on the kernel command line,
so it doesn't hit the address conflict that made Wraith Box add the filter. Matchlock uses none of
the OpenShell artifacts or OCSF, so it doesn't share a schema with
Wraith Box.

**No integration point.** Matchlock's VM backend interface
(`pkg/vm/backend.go`) is a Go interface inside its own process, with no
contract another program could serve. Its Linux backend needs
`cap_net_admin` and `cap_net_raw` on the binary, membership of the
`kvm` and `netdev` groups and a TAP device (`cmd/matchlock/setup_linux.go`).
X10-linux-hypervisor rules out each of those for Wraith Box, and so does
SEC12-least-privilege.

## Where Wraith Box's controls differ

Matchlock is useful as a worked example of the threats Wraith Box's
specs already name. Each item below is a rule of Wraith Box that
matchlock doesn't have, read from matchlock's code and not tested.

- **Default open egress.** Without an allowlist, a macOS guest has
  NAT to the internet, the LAN and the host. With interception forced
  and no hosts, an empty allowlist "means allow all hosts"
  (`docs/network-interception.md`). The `claude-danger` example
  allows `*` unless the user passes `--allowed-host`.
- **DNS.** The gVisor stack relays every UDP query on port 53 to
  `8.8.8.8` or `8.8.4.4`, whatever the allowlist says. That is the DNS
  tunnel that SEC05-default-deny forbids. Wraith Box answers only
  allowed names and bounds lookups under a wildcard
  (T02-dns-names).
- **Upstream addresses.** `isPrivateIP` resolves the name, checks only
  the first address, treats a failed lookup as public, and the dial
  resolves the name again. Its ranges leave out CGNAT, `0.0.0.0/8`,
  NAT64 and the host's own addresses. Wraith Box checks every address
  against a longer list and connects only to the address it checked
  (S07-07-hosts-and-modes, "Upstream address").
- **SNI and `Host`.** On port 443, the allowlist, the hook rules and
  the secret's host all use the SNI. The `Host` header of each request
  on that connection isn't compared with it. Where the allowed host
  shares a front end that routes by `Host`, such as some CDNs, a
  request can reach another site behind it with the secret injected.
  Wraith Box requires `Host` or `:authority` to equal the stream's
  name (S07-08-requests-and-credentials).
- **Where a secret goes.** Matchlock replaces the placeholder in every
  header and in the query string of a bound host. A response that
  echoes a header or the query, such as an error page or a redirect,
  brings the secret itself into the guest. The `claude-danger` example
  adds the user's subscription access token to every request to
  `api.anthropic.com` and `platform.claude.com`, on every path. Wraith
  Box injects in the binding's header only, on the paths
  X01-model-credential saw Claude Code use, and removes `Set-Cookie`
  from the responses of a bound host (S07-08-requests-and-credentials).
- **Repository writes.** The `claude-code` example binds the user's
  GitHub token to `github.com` and `api.github.com` and starts Claude
  Code with `--dangerously-skip-permissions`. The guest can then do
  whatever the token allows, in any repository the user can reach.
  Matchlock doesn't scope repository writes (SEC06-repo-writes).
- **Host files.** `pkg/vfs/realfs.go` joins the guest's path to the
  mount root and calls `os.Stat` and `os.OpenFile`, which follow
  symbolic links on the host. The guest can't create a link through
  the protocol (the server answers `ENOSYS`), but a link already in
  the mounted tree is followed for reads and writes. The default copy
  mode keeps links as links, so an absolute link to `~/.ssh` in a
  repository opens the host's `~/.ssh` to the guest. SEC02-no-host-fs-share
  avoids the whole class.
- **Guest-written disks.** `matchlock build` keeps a BuildKit cache
  disk that guests write, and runs `e2fsck -fy` and `resize2fs` on it
  on the host to grow it (`cmd/matchlock/cmd_build.go`). It then
  imports the image tarball the build guest wrote. SEC15-no-guest-disk-mount
  forbids the first, and S06-02-image-build never parses the build
  guest's output.
- **Bounds.** The VFS server reads a 4-byte length from the guest and
  allocates that many bytes, and a read allocates the size the guest
  asks for, up to 4 GiB each. The repository has no fuzz targets.
  SEC13-bounded-resources and S04-08-records-and-bounds cap both.
- **Audit.** Network and file events go to a channel of 100 and are
  dropped when it is full. They reach only an SDK caller, and the CLI
  keeps none. Byte counts come from `Content-Length`, so a chunked
  upload counts as zero. SEC10-audit asks for a persisted log of every
  decision.

## Threats matchlock covers that Wraith Box doesn't

None. Matchlock's defenses are a subset of Wraith Box's: VM isolation,
an allowlist, TLS inspection and placeholders. It has no threat model
that names a risk Wraith Box's T00-index lacks. Its in-guest
restrictions (dropped capabilities, a seccomp filter, a PID namespace
per command) protect its guest agent from the command it runs. Wraith
Box gets that from running `wb-guestd` as root and each project as its
own user, and doesn't rely on it (SEC11-root-gains-nothing).

Matchlock has some features that Wraith Box has deferred or
not designed:

- **Host-side subscription login.** `examples/claude-danger/` reads
  the host's Claude Code OAuth credentials, refreshes the access token
  on the host, writes the new tokens back, and injects the access
  token with a network hook. The guest only sees a placeholder API
  key. This is close to the design of I257, which S09-05-credentials and
  S14-04-commands-and-endpoints defer until after V1. The example
  shows the refresh works from outside Claude Code. It also shows
  the risk of injecting on every path, which I257 has to avoid by
  keeping the binding on the paths X01-model-credential lists.
- **Request and response rules.** Rules with a phase (before or
  after), an action (allow, block or mutate) and matchers for host,
  method and path, plus callbacks in the SDK process
  (`pkg/policy/network_hooks.go`). Rewrites of a `text/event-stream`
  response apply to each `data:` line, so they keep the stream's
  framing. This rule model would fit the content rules in
  `wb-proxyd` that R01-defenseclaw lists as a detection layer.
- **Throwaway VMs.** A matchlock VM boots from an image "in under a
  second", by its README, and is deleted after the command unless
  `--rm=false` is given. `--no-network` starts a VM with no network
  device. Wraith Box's ephemeral sessions
  (FR14-ephemeral) have no milestone.
- **Port forwarding.** `matchlock port-forward` binds host loopback by
  default and relays plain TCP for the ports the user names. That is
  option B of B38-port-forward-clipboard, which the brief says weakens
  SEC04-no-guest-secrets through cookies on `localhost`. Port
  forwarding is out of V1 (V1-08-no-forward-clipboard).

## Detection and response

In the NIST Cybersecurity Framework functions that R01-defenseclaw
uses, matchlock is almost all Protect, like Wraith Box. Its Detect is
the event stream to the SDK caller, which a program could watch, but
it drops events under load and keeps none. Its Respond is
`matchlock allow-list delete` on a running VM, and `matchlock kill`.
It adds nothing to the open question R01-defenseclaw raised: which
layer detects what, and what a finding makes Wraith Box or the user
do.

## Recommendations

- **Don't integrate with matchlock.** It has no contract another
  program could serve or call, and its Linux backend needs host
  privileges that SEC12-least-privilege forbids.
- **Don't take its egress or file designs.** The differences above
  each follow a rule Wraith Box already has. Matchlock is a reminder
  of why those rules are there, and a source of review cases for
  `wb-netd` and `wb-proxyd`: DNS outside the allowlist, a checked
  address that the dial doesn't use, a `Host` that differs from the
  SNI, and a placeholder echoed back in a response.
- **Use the subscription example when I257 is designed.** It shows a
  working host-side refresh, and what to avoid in the binding.
- **Keep matchlock in mind for Linux guests.** When
  X10-linux-hypervisor and X16-openshell-linux run, matchlock is a
  data point for booting OCI images quickly on both hosts. It doesn't
  meet their constraints as it is.
- **No follow-up issues.** None of this needs a spec change. If the
  maintainer wants matchlock looked at again, the re-review after V1
  (I240) can include it.

**Status:** Read 2026-10-23, no follow-up issues
