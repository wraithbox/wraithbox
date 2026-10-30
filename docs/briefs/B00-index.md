# B00 - Review briefs

**Purpose:** One page per decision, spike result or spec change waiting
for the maintainer, numbered after the GitHub issue it belongs to.

Each brief puts one decision on one page: what is being decided, a
diagram of the problem, the options with their cost, what each does to
the requirements, and the evidence. The record stays in its source (the
issue, the spike result, or the spec). A brief explains it.
`docs/agents/review-briefs.md` says what goes in a brief.

- A brief takes the number of its issue: B36-flow-attribution belongs
  to I36. A spike's brief takes the number of the spike's issue, and a
  spec change or pull request takes the number of the issue it closes.
- An issue has at most one brief. When it needs two, split the issue
  first.
- The issue links to its brief with a `Brief:` line in its body.

| ID | Description | Status |
|----|-------------|--------|
| B15-browser-login | Can Claude Code in the guest run on a browser login while the host keeps the real tokens? | Answered 2026-10-15: yes, with conditions. Decided 2026-10-15: 1A, not in V1, and 2A, the warning stays |
| B16-warm-start | How fast does a suspended VM come back, and can one saved state be restored more than once? | Answered 2026-10-04: yes, with conditions. Decided 2026-10-04: A, keep running while locked and save after unlock |
| B17-network-path | Does a macOS guest work on our userspace network stack, reaching only the proxy, and how fast? | Answered 2026-10-05: yes, with conditions. Decided 2026-10-06: 1A, link MTU 65535, and 2A, refuse the guest OS's own lookups quietly |
| B18-tls-inspection | Do the guest's tools accept the inspection CA? | Answered 2026-10-08: no. Decided 2026-10-10: A, accept it |
| B19-fs-benchmark | Is the data disk fast enough, and which disk settings does it use? | Answered 2026-10-05: no. Decided 2026-10-06: D, NFR02-fs-speed becomes a goal outside the release gate |
| B20-guest-xcode | Can Xcode builds, tests and simulators run for a project user without a GUI login? | Answered 2026-10-08: yes, with conditions. Decided 2026-10-12: 1A and 2A |
| B21-git-round-trip | Does the git round trip work, and what keeps the guest's pushes on its own branches? | Answered 2026-10-04. Decided 2026-10-04: A, Go filter plus `receive.hideRefs` |
| B22-data-disk | Can project homes on a data disk survive a new system disk? | Answered 2026-10-12: yes, with conditions. Decided 2026-10-12: 1A and 2A |
| B23-keychain-unsigned | How do development builds without a signing identity keep their Keychain items and their CA key? | Answered 2026-10-13: yes between updates, no across an update, and no permanent Secure Enclave key. Decided 2026-10-15: 1D (1A until X29-dev-signing) and 2A |
| B28-image-build | Can an unprivileged host build a macOS guest image without SSH or clicks? | Answered 2026-10-07: no. Decided 2026-10-07: 1A, changed, 2A, the build VM's own network, 3A, macOS 27 hosts, and 4, the secret store through `wb-guestadmin` |
| B29-vsock-handoff | Can wb-vmd hand guest connections to our Go daemons as descriptors, also across a restore? | Answered 2026-10-05: yes, with conditions. Decided 2026-10-06: A, the host opens every connection |
| B30-terminal-filter | Can wb filter Claude Code's terminal output without breaking it? | Answered 2026-10-04. Decided 2026-10-04: A, allowlist as measured, and H1 plus a URL list for hyperlinks |
| B31-shared-homebrew | Can projects in one guest share Homebrew without being able to change each other's tools? | Answered 2026-10-07: yes, with conditions. Decided 2026-10-08: no per-project prefix in v1 (A), and bottles only (A) |
| B32-dep-gate-registries | Can the dependency gate refuse young packages without breaking installs? | Answered 2026-10-04: yes, with conditions. Decided 2026-10-04: A, Homebrew ungated, and vulnerability threshold CRITICAL (I73) |
| B33-no-guest-credentials | Can the proxy drop every credential the guest sends without breaking its tools? | Answered 2026-10-05: yes, with conditions. Decided 2026-10-06: A, remove every guest credential and inject from the host, with the three open decisions as recommended |
| B34-sandboxed-daemons | Can the Go daemons confine themselves on macOS? | Answered 2026-10-04. Decided 2026-10-04: A, the `wb-launcher`, with the `wb-git` shim |
| B35-openshell-artifacts | Do the OpenShell parts the specs reuse exist as assumed? | Answered 2026-10-05: yes, with conditions. Decided 2026-10-06: 1 changed, built-in profiles checked in parts with GraphQL rules widened, 2A, the risk check in Go, and 3A, approvals bound by the boundary |
| B36-flow-attribution | Can projects sharing the work VM keep their network grants apart? | Decided 2026-10-04: A. The VM is the enforced unit |
| B37-terminal-boundary | Should the host terminal be a trust boundary with its own filter? | Decided 2026-10-04: A. Boundary plus allowlist filter, for now. Decided 2026-10-06: 1A, reset on exit, and 2C, the full filter on every stream |
| B38-port-forward-clipboard | How should guest dev servers and clipboard text cross to the host? | Decided 2026-10-04: D. Neither in V1 |
| B39-ca-rotation | Can an approval add an inspected host without breaking running tools? | Decided 2026-10-04: A. No per-set constraints, rotate with overlap |
| B40-learn-pass-modes | May learn mode and pass mode open holes in default-deny egress? | Decided 2026-10-04: A. Collect and refuse |
| B41-git-data-scope | Which git data may reach the guest, and how far does the host trust its own git? | Decided 2026-10-04: A. Export repository |
| B42-trust-placement | Where does a new project run, and what makes it the same project later? | Decided 2026-10-04: B. New projects start in the work VM |
| B43-vm-slot-admission | What gets one of the two macOS VM slots when everything wants one? | Decided 2026-10-04: A. The work VM stays, the isolated slot is lent out |
| B44-data-disk-durability | What may the host do with a guest's disks, and how much of the data disk may a crash lose? | Decided 2026-10-04: the mount rule becomes a SEC control, and A. Keep state and commits |
| B45-claude-in-guest | How does Claude Code run in the guest, and which of the user's settings go with it? | Decided 2026-10-04: per row, see the comment on I45 |
| B46-session-lifecycle | What happens to a running session when its terminal closes or its VM stops? | Decided 2026-10-04: B. Closing the terminal ends the session |
| B47-non-http-streams | What does the gateway do with a connection that isn't TLS or HTTP? | Decided 2026-10-04: A. Reset by default, raw TCP as a pass-class entry |
| B48-process-supervision | Who restarts the host processes, and what happens to running VMs when one fails? | Decided 2026-10-04: C. VMs stop with wb-hostd, for V1 |
| B49-install-upgrade | How is Wraith Box built, installed and upgraded, and how does an image build get its downloads? | Decided 2026-10-15: approved, with A for trusting the restore image and the other choices as written |
| B50-vm-test-infra | How do agents share the two macOS VM slots, and where do VM tests run? | Decided 2026-10-04: C. By hand for now, with VM builders run one at a time |
| B51-approval-flow | What happens between a blocked lookup and the user's answer? | Decided 2026-10-04: A. Hold the answer, then refuse uncached |
| B52-proto-contracts | How are the gRPC contracts between the processes written, generated, and checked? | Decided 2026-10-04: A. Commit generated code |
| B60-model-credential | Can Claude Code in the guest hold only a placeholder while the proxy adds the real subscription token? | Answered 2026-10-12: yes for a long-lived token. Decided 2026-10-12: A2, the model only with the event log denied by the host, and the long-lived token as a supported mode for V1 |
| B61-proxyd-keychain | Can a self-sandboxed wb-proxyd still use its Keychain items and Secure Enclave keys? | Answered 2026-10-15: yes |
| B64-vmd-sandbox | Can wb-vmd confine itself and still run a macOS VM? | Answered 2026-10-06. Decided 2026-10-12: 1A and 2A |
| B68-proxyd-stream-identity | How does wb-proxyd know which VM and name a stream belongs to, when wb-netd may be compromised? | Decided 2026-10-08: A |
| B74-pre-receive-check | Can Wraith Box refuse a bad push before any of it reaches the landing repository, and before it uses up the host's memory? | Answered 2026-10-05. Decided 2026-10-06: A, scanner and check, with fixed caps |
| B76-gosumdb-clock | Does the checksum database work as Go's clock for the dependency gate? | Answered 2026-10-08: yes. Decided 2026-10-12: A, with the values as written |
| B101-vsock-confinement | Can a project user take wb-guestd's place on the host-guest socket? | Answered 2026-10-06: yes, with conditions |
| B102-git-alloc-limit | Can git itself refuse a push that asks for too much memory, so the pack scanner gets simpler? | Answered 2026-10-06. Decided 2026-10-16: as recommended, with the wire-byte and per-push caps and `receive.maxInputSize` lowered from 1 GiB to 256 MiB |
| B145-least-privilege | Where are credentials held on the host, and who may reach them? | Decided 2026-10-07: A, the store, with declared access per process |
| B228-data-disk-followup | Does the host-made data volume stay unencrypted and give freed space back? | Answered 2026-10-26: yes, and yes with conditions. Decided 2026-10-26: keep `nobrowse` |
| B248-dev-signing | Can development builds signed by the developer's own team share Keychain items across an update? | Answered 2026-10-15: yes, with conditions. Decided 2026-10-15: A |

## Order to decide

Open decisions, most impact and risk first: what goes wrong, and for
whom, when one is decided badly or late. Take a brief off the list once
it is decided.

The maintainer decided B36-flow-attribution to B52-proto-contracts on
2026-10-04, B32-dep-gate-registries on 2026-10-04, and
B17-network-path, B19-fs-benchmark, B29-vsock-handoff,
B33-no-guest-credentials, B35-openshell-artifacts,
B37-terminal-boundary and B74-pre-receive-check on 2026-10-06,
B248-dev-signing on 2026-10-15, B102-git-alloc-limit on 2026-10-16,
and B228-data-disk-followup on 2026-10-26.

No decision is open.
