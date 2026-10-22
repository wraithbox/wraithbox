# R03 - Fix rounds and compaction

**Purpose:** When a wave lead should resume a builder for review
findings and when it should start a fresh one, and what Claude Code's
compaction and prompt cache settings do for builders, reviewers and
leads. Measured over the V1-M2 runs I307, I314, I323, I340, I367 and
waves 1 and 2 of I384 (2026-10-16 to 2026-10-22). Issue I386.

**Requirements:** none. This note is about the agent harness, not the
product.

## Method

- **Transcripts.** The session transcripts of the six run sessions
  under `~/.claude/projects/` for this repository: 48 builders, 46 code
  reviewers, 23 security reviewers and 18 wave leads. This note's own
  builder and its lead are left out. Short Python scripts read
  the `usage` of every API call, deduplicated by message ID, and the
  time stamps. A resume is a message from the lead that starts a new
  turn of a finished builder.
- **Round kinds.** A `must-fix` round follows a `needs changes` review,
  a fold round follows an approve. The lead's message says which.
  Background-task notices, rebase items and the three continuations
  after a turn limit are counted apart.
- **Warm or cold.** A call is cold when it reads less than half its
  context from the cache. The cache write and read counts come from
  the API's `usage` fields.
- **Cost.** In input-equivalent tokens: one base input token of the
  model is 1. For Opus 5.5 a 5-minute cache write is 1.25, a 1-hour
  cache write 2, a cache read 0.05 and an output token 5. Fable 5.1
  reads at 0.025 (Anthropic's pricing page). Every agent in the runs ran
  on Opus 5.5 except the security reviewers, which ran on Fable 5.1.
  1M input-equivalent tokens on Opus 5.5 is $4 at list price. The
  maintainer's plan usage is assumed to scale the same way, which the
  note doesn't check.
- **Next check.** The reviews and `re-checked:` comments on each pull
  request, from GitHub.
- **Options.** Claude Code's documentation at `code.claude.com/docs`,
  and the settings and version on the maintainer's Mac. Each claim
  below names its page.

## Results

### Fix rounds

| Run | Pull request | Round | Idle | Cache | Context | Calls | Cost | Minutes | Next check |
|---|---|---|---|---|---|---|---|---|---|
| I307 | PR308 | folds | 3 | warm | 36k | 11 | 31k | 3 | re-checked |
| I307 | PR310 | folds | 6 | cold | 50k | 9 | 86k | 3 | re-checked |
| I307 | PR311 | `must-fix` | 7 | cold | 132k | 31 | 414k | 5 | approve |
| I314 | PR315 | `must-fix` | 8 | cold | 90k | 28 | 292k | 6 | approve |
| I314 | PR315 | folds | 3 | warm | 122k | 5 | 34k | 3 | re-checked |
| I314 | PR316 | `must-fix` | 5 | warm | 107k | 21 | 155k | 4 | approve |
| I314 | PR317 | folds | 6 | cold | 113k | 21 | 294k | 5 | re-checked |
| I314 | PR319 | `must-fix` | 11 | cold | 152k | 33 | 520k | 8 | approve |
| I314 | PR319 | folds | 2 | warm | 194k | 7 | 73k | 3 | re-checked |
| I323 | PR324 | folds | 4 | warm | 98k | 15 | 99k | 4 | re-checked |
| I323 | PR325 | folds | 8 | cold | 192k | 15 | 401k | 5 | re-checked |
| I323 | PR326 | folds | 16 | cold | 465k | 33 | 1389k | 10 | re-checked |
| I323 | PR326 | `must-fix`, rebase builder | 12 | cold | 86k | 32 | 495k | 11 | approve |
| I323 | PR326 | folds, rebase builder | 1 | warm | 131k | 8 | 218k | 8 | re-checked |
| I323 | PR327 | folds | 4 | warm | 112k | 18 | 139k | 7 | re-checked |
| I323 | PR328 | `must-fix` | 11 | cold | 185k | 24 | 492k | 8 | approve |
| I323 | PR329 | folds | 16 | cold | 214k | 17 | 470k | 7 | re-checked |
| I323 | PR330 | `must-fix` | 18 | cold | 211k | 35 | 756k | 9 | approve |
| I323 | PR330 | folds | 5 | warm | 273k | 22 | 372k | 8 | re-checked |
| I323 | PR331 | folds | 13 | cold | 273k | 24 | 721k | 8 | re-checked |
| I340 | PR342 | `must-fix` | 8 | cold | 165k | 27 | 716k | 14 | approve |
| I340 | PR343 | folds | 13 | cold | 231k | 23 | 881k | 10 | re-checked |
| I340 | PR344 | `must-fix` | 10 | cold | 291k | 49 | 2107k | 31 | approve |
| I340 | PR344 | folds | 3 | warm | 363k | 16 | 1203k | 21 | re-checked |
| I340 | PR346 | folds | 6 | cold | 146k | 14 | 294k | 3 | re-checked |
| I340 | PR347 | folds | 11 | cold | 216k | 20 | 796k | 18 | re-checked |
| I340 | PR348 | `must-fix` | 8 | cold | 235k | 31 | 729k | 13 | approve |
| I340 | PR348 | folds | 3 | warm | 277k | 12 | 185k | 8 | re-checked |
| I340 | PR351 | folds | 12 | cold | 274k | 25 | 1066k | 18 | re-checked |
| I340 | PR352 | folds | 12 | cold | 550k | 34 | 2381k | 21 | re-checked |
| I340 | PR360 | `must-fix` | 12 | cold | 213k | 29 | 962k | 23 | approve |
| I367 | PR368 | folds | 5 | warm | 61k | 7 | 94k | 11 | re-checked |
| I367 | PR369 | `must-fix` | 13 | cold | 195k | 37 | 966k | 14 | approve |
| I367 | PR371 | folds | 6 | cold | 195k | 18 | 922k | 21 | re-checked |
| I367 | PR372 | folds | 17 | cold | 292k | 16 | 1327k | 14 | re-checked |
| I367 | PR373 | `must-fix` | 10 | cold | 469k | 29 | 1894k | 14 | approve |
| I367 | PR375 | `must-fix` | 24 | cold | 336k | 29 | 954k | 13 | approve |
| I367 | PR375 | folds | 2 | warm | 369k | 2 | 39k | 0 | re-checked |
| I367 | PR376 | folds | 17 | cold | 292k | 17 | 626k | 10 | re-checked |
| I367 | PR377 | folds | 3 | warm | 116k | 14 | 255k | 12 | re-checked |
| I384 | PR385 | folds | 9 | cold | 212k | 41 | 1340k | 25 | re-checked |
| I384 | PR387 | `must-fix` | 15 | cold | 108k | 28 | 535k | 15 | approve |

Idle is the minutes since the builder's last API call, context is the
builder's context at its first call of the round, and calls, cost
and minutes cover the round. PR360 was built in the I340 session the
day after the run, in an attended wave.

| | All 42 rounds |
|---|---|
| `must-fix` rounds, fold rounds | 15, 27 |
| Cold, warm | 29, 13 |
| Context at resume, median and largest | 195k, 550k |
| Rounds at 200k or more, under 100k | 20, 6 |
| Calls per round, median | 21.5 |
| Minutes per round, median | 9.5 |
| Cost, total and median | 27.7M, 508k |
| Share of all builder cost (81.0M) | 34% |

- **The cache.** Every round that came within 5 minutes of the
  builder's last call was warm, and every round after 6 minutes or more
  was cold. A cold round pays its whole context as a cache write.
  The lead sends most findings after both reviews are back, so 29 of
  the 42 rounds were cold.
- **Quality.** All 15 `must-fix` rounds were approved at the next
  review, and every fold round got a `re-checked:` comment without
  another round. Resumed builders at 300k to 550k did no worse than
  those under 150k. The data has no failed fix round to explain, so
  it doesn't show that a large context harms a fix.
- **Fresh builders.** No fix round went to a builder spawned for it.
  The closest is PR326: a builder spawned only to rebase it took the
  round 2 `must-fix` findings at 86k of context, and round 3 approved.
  Two fresh builders on an existing branch, one of them a fix for a
  failing `needs-vm` test, made their first edit after 23 and 26 calls,
  at 57k and 66k of context, for 117k and 127k of cost, in 2 to 4
  minutes.
- **Turn limits.** The builders of I161, I164 and I289 hit their
  200-turn limit and were resumed at once, warm, at 433k to 513k. The
  continuations cost 6.3M together. Each then had a cold review round
  at 465k to 550k (PR326, PR373 and PR352 above), which cost 5.7M
  together.

### Resume or start fresh: a cost model

Most of a fix round's cost is the context the builder carries. A
resumed round pays its context `C` once as a cache write (cold) or a
read (warm), then reads it again on each further call. A fresh builder
pays an orientation of about 120k and then reads a context of about
60k on each call. The work itself costs the same either way. Under
that model, reading and writing the context the builders already had
cost 18.4M of the 27.7M the 42 rounds cost.

| Rule | 5-minute cache, today | 1-hour cache |
|---|---|---|
| Always resume | 18.4M | 10.5M |
| Resume under 100k, else fresh | 7.5M | 8.8M |
| Resume under 150k, else fresh | 7.5M | 7.8M |
| Resume under 200k, else fresh | 8.9M | 7.6M |
| Always fresh | 7.8M | 9.7M |

With a fresh builder that orients at 80k instead of 60k, resuming under
150k costs 8.1M on today's cache. Of the limits tried, 80k to 200k, a
limit of 150k is within 0.2M of the cheapest under both cache
lifetimes. A fresh builder adds
2 to 4 minutes to a round that takes 9.5 minutes at the median.

The lead can see the size: a builder's completion notice has a
`<subagent_tokens>` field, which matched the builder's last context within
3k on the PR385 builder (213,072 against 210,279).

### Cold rewrites in every role

The cache also goes cold inside a builder's own run. 118 builder calls rewrote 23.9M tokens of context. All but five
of them, which rewrote 0.1M together, came after an idle gap of 5 to 25
minutes:

| Before the cold call | Rewrites | Tokens | Idle minutes, median |
|---|---|---|---|
| A CI watch (`ci-watch`, `gh run watch`) | 41 | 9.7M | 8 |
| A resume by the lead | 31 | 6.8M | 12 |
| Another Bash command | 22 | 3.8M | 7 |
| `mise run ci` | 9 | 1.5M | 9 |
| Edits, VM, Swift and docs tasks, other | 15 | 2.0M | 7 |

A one-hour cache turns each rewrite with a gap under 60 minutes into a
read, and makes every other cache write cost 2 instead of 1.25:

| Role | Cost | Cold rewrites | Saved by 1 hour | Extra writes | Net |
|---|---|---|---|---|---|
| Builder | 81.0M | 118, 23.9M | 28.6M | 6.3M | -22.3M (-28%) |
| Code reviewer | 11.9M | 30, 2.7M | 3.2M | 2.7M | -0.5M (-4%) |
| Wave lead | 9.4M | 71, 4.0M | 4.5M | 1.4M | -3.1M (-33%) |
| Security reviewer | 5.3M | 22, 0.8M | 1.0M | 1.9M | +0.9M |
| General-purpose agents | 3.8M | 17, 0.4M | 0.5M | 1.4M | +0.9M |

Every builder rewrite had a gap under 60 minutes. The lead's rewrites
follow its waits for agents, up to 127 minutes.

### Compaction in the runs

- **No subagent compacted.** None of the 491 subagent transcripts in
  this repository's project folders on 2026-10-22 has a
  `compact_boundary` record.
  The largest builder context was 587k. 20 of the 48 builders passed
  200k, 9 passed 300k and 3 passed 400k.
- **The maintainer's sessions compacted 14 times**, all by a typed
  `/compact`. `/compact` was typed 20 times, never with instructions.
  The compactions ran at 146k to 443k (median 260k), left 6.5k to 16k,
  and took 32 to 120 seconds. One was in a run, in I367 while the lead
  of wave 5 ran. Two were in the I340 session the day after that run,
  and the rest in attended sessions.
- **What happened next.** In the four compactions checked (the I367
  and I340 sessions, and an attended session on I379), the next ten
  tool calls read state
  back from GitHub, the run's `.scratch/` files or task output, and
  went on with the next step. None of them redid work. The other ten
  weren't checked.

So the runs don't show a builder, reviewer or lead losing something to
compaction, because none of them compacted.

## Claude Code's options

On the maintainer's Mac: Claude Code 2.1.302, `"model": "opus"` (Opus
5.5) and `"autoCompactEnabled": false` in `~/.claude/settings.json`.
Main sessions write their cache with the one-hour lifetime and
subagents with five minutes: in the I384 session the main conversation
wrote 235k tokens to the one-hour cache and none to the five-minute
one, and its subagents 6.0M to the five-minute cache and none to the
one-hour one.

| Option | What it does | Source |
|---|---|---|
| Context window | Opus 4.7 and later run with a 1M window by default. | model-config, "Extended context" |
| Default threshold | Models with a native 1M window compact at about 967K. | model-config, "Default auto-compact thresholds" |
| `autoCompactEnabled` | Turns automatic compaction off or on. The documentation doesn't say whether it covers subagents. | settings-reference, index |
| `/autocompact`, `autoCompactWindow`, `--autocompact`, `CLAUDE_CODE_AUTO_COMPACT_WINDOW` | Set how full the window gets before compaction, 100K to 1M, per model, per launch or for every model. The variable takes precedence over the others. | model-config, "Set the auto-compact window" |
| `autoCompactWindow` in agent frontmatter | Compacts one subagent earlier, as a plain integer from 100000 to 1000000. Needs 2.1.296. The variable above overrides it. | sub-agents, "Set the auto-compact window for one subagent" |
| `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` | Applies to subagents as well. | sub-agents, "Auto-compaction" |
| `/compact <focus>` and a `# Compact instructions` section in `CLAUDE.md` | Tell the summary what to keep. | costs, "Manage context proactively" |
| `SessionStart` hook matching `compact` | Adds the hook's output to the compacted context. | context-window, "What survives compaction" |
| `PreCompact` hook, matcher `manual` or `auto` | Can block a compaction with `decision: "block"`. | hooks, decision control table |
| What survives | The project `CLAUDE.md` is re-read from disk. Up to five recently changed files are read again, and the rest is a summary. | context-window, "What survives compaction" |
| Compacting a subagent at a chosen point | None of the pages read documents a way. `/compact` is typed at the prompt, and the frontmatter has only `autoCompactWindow`. | sub-agents, frontmatter reference |
| Cost of a compaction | The summary request reads the history, from the cache when warm, uncached after the cache lifetime. | prompt-caching, "Compacting the conversation" |
| Subagent cache lifetime | Five minutes by default, also on a subscription. A resumed subagent can read the cache its first run warmed. | prompt-caching, "Subagents and the cache" |
| `experimental: cacheTtl: 1h` in agent frontmatter | One-hour cache for that subagent. Needs 2.1.248, and is ignored while the subscription draws on usage credits. | sub-agents, frontmatter reference, and prompt-caching, "Choose the TTL yourself" |
| `subagentPromptCacheTtl`, `CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL` | The same for every subagent, workflow and helper request, compaction included. | settings-reference, `subagentPromptCacheTtl` |

A larger window changes when compaction runs, not what a resumed round
costs: that cost scales with the context the builder carries, at any
window size.

The settings weren't tried on a sample fix round. A builder can't spawn
agents, and an agent file's frontmatter applies to agents spawned after
the change.

## Limits

- 42 rounds by 35 builders. The model's orientation cost comes
  from two fresh builders, and its work cost is assumed equal for a
  fresh and a resumed builder. A fresh builder that re-reads more files,
  or that redoes a choice the review didn't question, costs more than
  the model says.
- No fix round failed, so the data can't tell whether a fresh builder
  fixes better or worse. The rule below rests on cost alone.
- The one-hour estimate replays the same calls with other prices. It
  assumes every call would have been the same.
- Costs are API list prices. The maintainer's plan may count usage
  differently.

## Recommendations

- **A one-hour cache for builders and wave leads.** Set
  `experimental: cacheTtl: 1h` in `.claude/agents/builder.md` and
  `wave-lead.md`. It would have saved about 28% of builder cost and
  33% of lead cost: their cold rewrites follow waits for CI, reviews and
  agents, all under 60 minutes for builders. Leave the reviewers and
  other agents at five minutes, where it saves little or costs more.
  The frontmatter is per agent, so `.claude/settings.json` stays as it
  is. After the change, a builder's transcript should show
  `ephemeral_1h_input_tokens` in its `usage`.
- **Resume a builder under 150k, else start a fresh one.** The lead
  reads `<subagent_tokens>` from the builder's last notice. Under
  150k it resumes the builder. At 150k or more it stops it and spawns a
  fresh builder on the same branch, with the pull request, the posted
  reviews and the same findings message as its brief, plus "read the
  pull request's Choices made, and don't revisit a choice the review
  didn't question". On the 42 rounds this rule costs 7.5M against 18.4M
  today, and 7.8M against 10.5M with the one-hour cache.
- **Keep resuming after a turn limit.** A builder that stops at its
  turn limit has work in its context that isn't in a commit or a pull
  request yet. Its three continuations were warm.
- **No compaction setting.** No builder, reviewer or lead compacted,
  and the largest builder stopped at 587k, below the 967K default. A
  lower `autoCompactWindow` would summarize builders in the middle of
  their work, with an effect the runs can't measure, and the fix-round
  rule already caps the largest resumed contexts. Look again when a
  builder passes about 800k or a subagent transcript has a
  `compact_boundary` record. With `autoCompactEnabled` off, if that
  setting covers subagents, a builder that reaches the 1M window stops
  with a context-limit error instead of compacting.

**Status:** Measured 2026-10-22 over 42 fix rounds of V1-M2. Awaits the maintainer's decision
