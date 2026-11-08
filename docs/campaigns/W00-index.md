# W00 - Campaign reports

**Purpose:** One page per campaign, numbered after its campaign issue:
what the campaign put on its train, what it spent, and what waits for
the maintainer.

A campaign runs waves unattended within a budget and merges into a
train branch, and the maintainer reviews the train as a whole
(`docs/agents/campaigns.md`). `scripts/campaign.mjs` renders each
report, and it reaches `main` and this site when its train merges.

- A report takes the number of its campaign issue: `W700` belongs to `I700`,
  and its train is `train/700`.
- `Running` reports are on a train that hasn't merged. A report on
  `main` says what ended its campaign.

| ID | Description | Status |
|----|-------------|--------|
| W604-network-floor-mac | Campaign 604: network floor, lane mac | Running |
