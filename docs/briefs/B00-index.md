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
| B36-flow-attribution | Can projects sharing the work VM keep their network grants apart? | Awaiting decision |
| B37-terminal-boundary | Should the host terminal be a trust boundary with its own filter? | Awaiting decision |
| B38-port-forward-clipboard | How should guest dev servers and clipboard text cross to the host? | Awaiting decision |
| B39-ca-rotation | Can an approval add an inspected host without breaking running tools? | Awaiting decision |
| B40-learn-pass-modes | May learn mode and pass mode open holes in default-deny egress? | Awaiting decision |
| B41-git-data-scope | Which git data may reach the guest, and how far does the host trust its own git? | Awaiting decision |
| B42-trust-placement | Where does a new project run, and what makes it the same project later? | Awaiting decision |
| B43-vm-slot-admission | What gets one of the two macOS VM slots when everything wants one? | Awaiting decision |
| B44-data-disk-durability | What may the host do with a guest's disks, and how much of the data disk may a crash lose? | Awaiting decision |
| B45-claude-in-guest | How does Claude Code run in the guest, and which of the user's settings go with it? | Awaiting decision |
| B46-session-lifecycle | What happens to a running session when its terminal closes or its VM stops? | Awaiting decision |
| B47-non-http-streams | What does the gateway do with a connection that isn't TLS or HTTP? | Awaiting decision |
| B48-process-supervision | Who restarts the host processes, and what happens to running VMs when one fails? | Awaiting decision |
| B49-install-upgrade | Which process downloads macOS restore images, and how are Wraith Box installs upgraded? | Awaiting decision |
| B50-vm-test-infra | How do agents share the two macOS VM slots, and where do VM tests run? | Awaiting decision |
| B51-approval-flow | What happens between a blocked lookup and the user's answer? | Awaiting decision |
| B52-proto-contracts | How are the gRPC contracts between the processes written, generated, and checked? | Awaiting decision |

## Order to decide

Open decisions, most impact and risk first: what goes wrong, and for
whom, when one is decided badly or late. Take a brief off the list once
it is decided.

1. B36-flow-attribution: without it, every project in the work VM can
   use every other project's credentials. It sets the meaning of
   "project" for B42-trust-placement and B51-approval-flow.
2. B40-learn-pass-modes: learn mode as specified opens egress to any
   host, a hole in SEC05-default-deny that no document records.
3. B41-git-data-scope: local branches never pushed anywhere, untracked files and
   tokens in remote URLs can reach the guest. The issue's fix doesn't
   stop a fetch by commit ID.
4. B42-trust-placement: decides whether a freshly cloned, possibly
   hostile repository starts next to your trusted projects.
5. B37-terminal-boundary: guest bytes reach the host terminal emulator
   unfiltered and can write the host clipboard.
6. B38-port-forward-clipboard: same-port forwarding sends host
   `localhost` cookies into the guest.
7. B45-claude-in-guest: carrying `~/.claude` in unfiltered puts MCP
   tokens and the sign-in session in the guest.
8. B44-data-disk-durability: one host crash can lose every project's
   Claude Code state, and no control stops the host mounting a guest
   disk.
9. B50-vm-test-infra: low risk, but the V1-M1-spikes-closed VM spikes
   collide or wait without it, so decide it early.
10. B39-ca-rotation: as specified, each approval of an inspected host
    breaks the running tools that talk to it.
11. B51-approval-flow: tools fail before the user answers, and guest
    daemons flood notifications.
12. B47-non-http-streams: behavior on allowed ports is whatever the
    code does.
13. B48-process-supervision: a guest can flood host daemons, and a
    `wb-hostd` restart kills running agents.
14. B46-session-lifecycle: closing the terminal either kills long
    tasks or leaves sessions holding the VM awake. B52-proto-contracts
    waits on it.
15. B52-proto-contracts: parallel builders invent their own contracts
    without it.
16. B49-install-upgrade: the issue's choice gives `wb-hostd` network
    access that S04-architecture denies it.
17. B43-vm-slot-admission: unexplained VM start failures, and image
    builds without a rule for which slot they take.
