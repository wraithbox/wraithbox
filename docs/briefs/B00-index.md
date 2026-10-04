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
| B16-warm-start | How fast does a suspended VM come back, and can one saved state be restored more than once? | Answered 2026-10-04: yes, with conditions. Decided 2026-10-04: A, keep running while locked and save after unlock |
| B21-git-round-trip | Does the git round trip work, and what keeps the guest's pushes on its own branches? | Answered 2026-10-04. Decided 2026-10-04: A, Go filter plus `receive.hideRefs` |
| B29-vsock-handoff | Can wb-vmd hand guest connections to our Go daemons as descriptors, also across a restore? | Answered 2026-10-05: yes, with conditions |
| B30-terminal-filter | Can wb filter Claude Code's terminal output without breaking it? | Answered 2026-10-04. Decided 2026-10-04: A, allowlist as measured, and H1 plus a URL list for hyperlinks |
| B32-dep-gate-registries | Can the dependency gate refuse young packages without breaking installs? | Answered 2026-10-04: yes, with conditions. Decided 2026-10-04: A, Homebrew ungated, and vulnerability threshold CRITICAL (I73) |
| B34-sandboxed-daemons | Can the Go daemons confine themselves on macOS? | Answered 2026-10-04. Decided 2026-10-04: A, the `wb-launcher`, with the `wb-git` shim |
| B36-flow-attribution | Can projects sharing the work VM keep their network grants apart? | Decided 2026-10-04: A. The VM is the enforced unit |
| B37-terminal-boundary | Should the host terminal be a trust boundary with its own filter? | Decided 2026-10-04: A. Boundary plus allowlist filter, for now |
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
| B49-install-upgrade | Which process downloads macOS restore images, and how are Wraith Box installs upgraded? | Decided 2026-10-04: a dedicated download program that wb-hostd invokes |
| B50-vm-test-infra | How do agents share the two macOS VM slots, and where do VM tests run? | Decided 2026-10-04: C. By hand for now, with VM builders run one at a time |
| B51-approval-flow | What happens between a blocked lookup and the user's answer? | Decided 2026-10-04: A. Hold the answer, then refuse uncached |
| B52-proto-contracts | How are the gRPC contracts between the processes written, generated, and checked? | Decided 2026-10-04: A. Commit generated code |

## Order to decide

Open decisions, most impact and risk first: what goes wrong, and for
whom, when one is decided badly or late. Take a brief off the list once
it is decided.

The maintainer decided B36-flow-attribution to B52-proto-contracts on
2026-10-04.

1. B32-dep-gate-registries: the dependency gate as S07-egress-gateway
   specified it fails every fresh install. Approving the spike result
   accepts metadata filtering in front of the download refusal, and
   Homebrew outside the gate. Its vulnerability threshold is I73.
