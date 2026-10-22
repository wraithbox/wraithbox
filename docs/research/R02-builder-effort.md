# R02 - Builder effort trial

**Purpose:** Whether builders should run at high effort instead of
medium. Reports the effort trial of `docs/agents/orchestration.md`
("Effort") over the V1-M2 runs I307, I314, I323, I340 and I367
(2026-10-16 to 2026-10-21), and recommends a setting.

**Requirements:** none. This note is about the agent harness, not the
product.

## The trial

`orchestration.md` set a builder's effort by the issue it builds:

| Work | Effort |
|---|---|
| Builder of a spike, or of security-sensitive code | high |
| Any other builder | high for an even issue number, medium for an odd one |
| Code reviewer | medium, high on a pull request that gets a security review |
| Security reviewer | Fable at high |

Security-sensitive code is the code that gets a security review
("A wave", step 4). Each wave report recorded, per pull request, the
builder's effort, the review rounds until approve, the `must-fix`
findings in the first review, the tokens of the builder and its
reviewers, the minutes from spawn to approve, and how often the
maintainer stepped in. On the first six code pull requests of V1-M2 a
second code reviewer at high also reviewed the same head.

The rule for the result, written before the trial: raise a role's
default when high cuts first-review `must-fix` findings by a third or
more, or saves half a review round per pull request, while tokens per
merged pull request rise by less than about a quarter. Otherwise keep
medium.

## Method

- **Records.** The `## Records` table and the wave report comments of
  each run issue.
- **Effort check.** The wave reports say that a lead can't see the
  effort an agent ran at, because the notifications don't show it. The
  `effort` that each lead passed when it spawned a builder or reviewer
  is in the session transcripts, and it matches the records for every
  pull request below. Nobody checked whether Claude Code applied it.
- **Size and labels.** Lines changed and the `comp:` labels from
  GitHub, and whether a security review was posted.
- **Groups.** Every odd-numbered issue built at high had a security
  review. The leads applied the rule as written. The comparison
  uses only the issues the parity rule decided: odd numbers at medium
  against even numbers at high, without a security review. Rebase
  items are counted apart.

## Results

### Medium against high, by parity

| Pull request | Issue | Effort | Lines | Rounds | `must-fix` 1st | Tokens | Minutes |
|---|---|---|---|---|---|---|---|
| PR308 | I169 | medium | 2 | 1 | 0 | 68k | 6 |
| PR309 | I217 | medium | 12 | 1 | 0 | 105k | 12 |
| PR310 | I243 | medium | 25 | 1 | 0 | 133k | 12 |
| PR368 | I345 | medium | 60 | 1 | 0 | 100k | 21 |
| PR377 | I341 | medium | 475 | 1 | 0 | 179k | 28 |
| PR346 | I301 | medium | 1186 | 1 | 0 | 239k | 40 |
| PR316 | I236 | high | 119 | 2 | 1 | 206k | 18 |
| PR371 | I362 | high | 138 | 1 | 0 | 249k | 80 |
| PR324 | I312 | high | 163 | 1 | 0 | 161k | 11 |
| PR327 | I162 | high | 459 | 1 | 0 | 294k | 21 |
| PR317 | I58 | high | 556 | 1 | 0 | 265k | 21 |
| PR315 | I120 | high | 651 | 2 | 0 | 275k | 25 |
| PR311 | I160 | high | 957 | 2 | 1 | 331k | 26 |
| PR342 | I300 | high | 1278 | 2 | 2 | 550k | 53 |

| | Medium (6) | High (8) |
|---|---|---|
| Review rounds per pull request | 1.0 | 1.5 |
| First-review `must-fix` per pull request | 0 | 0.5 |
| Tokens per pull request | 137k | 291k |
| Minutes to approve | 20 | 32 |
| Maintainer stepped in | 0 | 0 |

All six medium pull requests were approved in the first round, and
four of the eight high ones needed a second round.

### Size

The medium group got smaller issues by chance: its median is about 40
changed lines, against about 500 for the high group. Pull requests of
about the same size show the same difference:

| Medium | Lines | Tokens | Rounds, `must-fix` | High | Lines | Tokens | Rounds, `must-fix` |
|---|---|---|---|---|---|---|---|
| PR346 | 1186 | 239k | 1, 0 | PR342 | 1278 | 550k | 2, 2 |
| PR377 | 475 | 179k | 1, 0 | PR327 | 459 | 294k | 1, 0 |
| PR377 | 475 | 179k | 1, 0 | PR317 | 556 | 265k | 1, 0 |
| PR368 | 60 | 100k | 1, 0 | PR324 | 163 | 161k | 1, 0 |

PR346 and PR342 are the closest pair: both change the terminal filter
of `wb` (`comp:wb`), by about the same amount. At a matched size, high
used 1.5 to 2.3 times the tokens. Tokens per changed line differ by
only about 15% over the whole groups, but a pull request costs about
70k to 100k tokens before it changes a line, which raises the
per-line cost of the small medium pull requests.

### Security-sensitive pull requests

All 17 security-sensitive pull requests had a builder at high, so the
trial has no comparison for them. They had 0.76 first-review
`must-fix` findings and 1.5 rounds per pull request, at 661k tokens
each. They used about three quarters of the tokens of all the pull
requests in the records.

PR325 (I159, the first protos) is the one exception. Its builder ran
at medium, and the lead added a security review because `guest.proto`
is the host-guest boundary. About 5,000 changed lines got 0 `must-fix`
findings and were approved in one round.

High effort didn't stop defects from reaching `main` either. PR380 and
PR382 fixed the guest link after PR376 (I285) merged, which had a
builder and a code reviewer at high and a security review.

### The second code reviewer

A second code reviewer at high ran on PR309, PR310, PR311, PR315,
PR317 and PR319. All 15 of its unique findings held. On PR315
it found `must-fix` findings that the first reviewer missed: the spike
cache path followed `$HOME`, and the cleanup emptied a volume mounted
inside a target. The others were
folds and drops. On PR319 it missed the `must-fix` that the first
reviewer, also at high, found.

This shows that a second, independent review finds things. It doesn't
separate the effect of the effort from that of a second pass, so it
isn't evidence for a higher code reviewer default. A code review at
medium cost about 50k tokens, and one at high 100k to 190k.

### Rebases

Rebase builders ran at high for PR373, PR375 and PR376 and at medium
for PR329 and PR330. Each used 28k to 65k tokens, with no difference
between the two levels.

## Limits

- Six and eight pull requests are few. Zero against four `must-fix`
  findings could be chance, but none of the measures favors high.
- Builders at high may write more code and tests for the same issue,
  which gives the reviewer more to find. The
  records can't tell this apart from worse code.
- The reviewer decides what is `must-fix`. The code reviewers of both
  groups ran at medium, so they judged both groups alike.
- Some token counts in the records are the lead's estimates (`~`).
- The records don't cover the effort of the dispatcher, the wave lead
  or attended sessions, so they say nothing about the levels
  above high.

## Recommendations

- **Builders at medium.** High failed both conditions of the rule: it
  had more first-review `must-fix` findings and more rounds, not
  fewer, and about twice the tokens. End the parity split. Every
  builder that isn't a spike or security-sensitive runs at medium,
  rebases included.
- **Security-sensitive builders stay at high for now.** The trial has
  no comparison for them, and the one medium build, PR325, went well.
  Most of the tokens go there, so the next trial is the parity split for these builders, with the code reviewer
  at high and the security review unchanged, because the reviews are
  the control.
- **Reviewers unchanged.** The code reviewer stays at medium, and at
  high on a pull request that gets a security review. The security
  reviewer stays at Fable, high.
- **Records.** Keep the table per pull request. A lead fills it from
  the notifications it already gets, and the milestone review and the
  next trial read it.

## Decision

The maintainer decided on 2026-10-22, and `orchestration.md`
("Effort") now says:

- Builders run at medium, spikes and security-sensitive work included.
- Code reviewers run at medium, also on pull requests that get a
  security review.
- Security reviewers, wave leads and the maintainer's own sessions run
  at high. Claude Code's default stays at high.
- The per-PR records and the second code reviewer end with the trial.
  No further effort trials are planned. After the current wave the next
  harness experiment counts the valid findings of Claude Security.

**Status:** Measured 2026-10-22 over 32 pull requests of V1-M2. Decided 2026-10-22: builders and code reviewers at medium
