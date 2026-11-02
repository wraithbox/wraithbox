# X32 agent C: transcripts (saved by coordinator from the agent's hand-back, condensed)

Scope: 76 top-level sessions plus 705 subagents, 2026-10-03 10:47 to 2026-11-01 20:58 Amsterdam time. Session 6053c145 (this measurement) is excluded.

## Headline

- **Cost:** the transcripts give $2,297 API-equivalent. Claude Code's own cost-state gives $3,294 (Opus 5.5 $2,663, Fable 5.1 $618, other $13).
  - Use $3,294 as the total.
  - Breakdowns from the transcripts cover about 70% of the real figure. Sessions with subagents or WebSearch show 1.2–1.7 times more in cost-state, probably from classifier, web, compaction and title calls that aren't logged.
- **Tokens (deduplicated):** 38,890 calls.
  - Input 0.126 M, output 13.0 M.
  - Cache write 167.8 M, of which 5m 134.9 M and 1h 32.9 M.
  - Cache read 4,531 M.
- **Subagents by role:**

| Role | n | Cost $ | Calls | Compactions | Active h |
|---|---|---|---|---|---|
| builder | 170 | 1042 | 18812 | 48 | 75.2 |
| security-reviewer | 99 | 339 | 1704 | 0 | 16.9 |
| code-reviewer | 162 | 190 | 4649 | 0 | 20.5 |
| general-purpose | 59 | 120 | 2511 | 0 | 9.5 |
| wave-lead (own cost) | 35 | 73 | 1540 | 0 | 8.7 |
| skill:code-review | 139 | 51 | 1364 | 0 | 5.6 |

- A wave-lead subtree costs $27 at the median and $63 at the maximum. Fork, researcher, spike-builder, Explore and Plan agents are small.
- **AI time:** 91.5 h as a union across agents, 182.1 h of agent-hours. Sessionized at 5/15/30-minute gaps: 108/139/159 h. Up to 13 agents ran at once.
- **Human:**
  - 702 prompts with 20,525 words. Of these, 486 were typed, 92 were AskUserQuestion answers, 78 were slash commands and 46 were `!` shell inputs. There were also 10 interrupts.
  - Typed prompts: median 15 words, p75 34, maximum 948.
  - Prompt time sessionized at 5/15/30-minute gaps: 18.1/36.1/53.4 h.
  - Reading load: 1.64 M characters (275k words) of assistant text.
- **Days:** 25 of 30 days active, and the same 25 days had human prompts. 10 days had prompts on a weekday between 9:00 and 17:00.
- **Gaps:**
  - Thinking-gap candidates: 28, totaling 1,017 minutes.
  - Other gaps of 15–120 minutes: 25, totaling 1,005 minutes.
  - Breaks over 120 minutes: 28.

## Per week

| Week | Cost $ (transcript) | Output M | Calls | Subagents | AI union h | Agent h | Prompts | Words | Human days | Prompt h (15m) | Weekday 9–17 days | Thinking n/min |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 09-28 | 172 | 0.79 | 2853 | 36 | 7.3 | 14.4 | 111 | 3260 | 2 | 5.6 | 0 | 2/31 |
| 10-05 | 559 | 1.29 | 9212 | 166 | 20.2 | 45.8 | 134 | 3436 | 5 | 8.0 | 3 | 6/240 |
| 10-12 | 681 | 2.23 | 11989 | 233 | 26.6 | 54.7 | 229 | 9602 | 7 | 12.4 | 4 | 13/513 |
| 10-19 | 339 | 1.58 | 5231 | 97 | 15.0 | 24.1 | 100 | 1610 | 6 | 4.5 | 3 | 3/148 |
| 10-26 | 546 | 7.15 | 9605 | 173 | 22.3 | 43.0 | 128 | 2617 | 5 | 5.7 | 0 | 4/85 |

## Top issues by cost

| Issue | PR | Cost $ |
|---|---|---|
| I67 | PR191 | 57.3 |
| I289 | PR352 | 52.4 |
| I161 | PR326 | 47.8 |
| I476 | PR527 | 47.8 |
| I345 | PR368 | 46.0 |

156 issues are attributed. 84 subagents ($153) have no issue.

## Files

inventory.csv, tokens_by_{model,day,week,role,week_role}.csv, prices.csv, cost_state_by_{session,model}.csv, stats.json, issues.csv, wave_leads.csv, sessions_totals.csv, pr_branches.csv, prompts.csv, interrupts.csv, reading_load_by_day.csv, minutes.csv, timeline_by_day.csv, hour_of_week.csv, gaps.csv, gaps_by_day.csv.

## Doubts

- Transcripts undercount by about 30% (above).
- Turn-limit flags are an upper bound.
- Prompts typed into subagent conversations are not counted.
- Gaps are measured within a session only.
- Detecting that the assistant offered options is a regex guess.
- Activity minutes come from event timestamps, so a long tool call shows as idle.
- Deduplication was checked on file 65799dd0: 611 lines and 303 message IDs, and the raw count is about twice the deduplicated one.
