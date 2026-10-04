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
