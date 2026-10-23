# R00 - Research notes

**Purpose:** Background research that informs the design without
deciding it: surveys of prior art, measurements, and reading notes.

Where a spike (X00-index) answers a yes/no question with a prototype,
and a spec (S00-index) decides, a research note reports what is out
there and what it tells us. Each note is
`docs/research/R<NN>-<slug>.md`. Run `mise run doc:index` to list a
new one here.

| ID | Description | Status |
|----|-------------|--------|
| R01-defenseclaw | DefenseClaw | Read 2026-10-14, re-review after V1 (I240) |
| R02-builder-effort | Builder effort trial | Measured 2026-10-22 over 32 pull requests of V1-M2. Decided 2026-10-22: builders and code reviewers at medium |
| R03-fix-rounds-compaction | Fix rounds and compaction | Measured 2026-10-22 over 42 fix rounds of V1-M2. PR389 applies the recommendations in commit 28ab7e2, and merging it accepts them. Follow-up 2026-10-22: I392 turns compaction on, with builders at 200k, and is closed after the check that follows the merge |
| R04-matchlock | Matchlock | Read 2026-10-23, no follow-up issues |
| R05-iron-proxy | iron-proxy | Read 2026-10-23, re-review after V1 (no issue yet) |
