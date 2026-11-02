# X32 wave 1, agent A: annotated sources

Research date: 2026-11-02. Grouped by the seven questions. For each source:
URL, date, publisher (vendor or independent), what was measured, setting,
key numbers, and trust.

How it was read: "read" means I fetched the primary page myself. "search"
means the numbers come from search-result summaries of the primary or
secondary sources, which I did not open. Treat "search" numbers as needing a
check before they go into a published report.

Trust scale: High (peer-reviewed or RCT, or a careful independent study with
the method published), Medium (large telemetry with the method stated, but
observational or vendor-run), Low (vendor marketing, a small or unclear
method, or secondary only).

---

## 1. Measuring developer productivity with AI

### DORA 2024 Accelerate State of DevOps
- URL: https://cloud.google.com/blog/products/devops-sre/announcing-the-2024-dora-report
- Date: 2024-10-22. Publisher: Google DORA (independent research program, Google-funded).
- Measured: survey of about 3,000 professionals, modeled associations.
- Key numbers (search): a 25% increase in AI adoption is associated with throughput -1.5% and delivery stability -7.2%, documentation quality +7.5%, code quality +3.4%, code review speed +3.1%. 75.9% use AI for some work. Clusters: elite 19%, high 22%, medium 35%, low 25%. Elite change failure rate about 5%. Added the fifth metric, deployment rework rate, and split the metrics into throughput and instability.
- Trust: Medium. Large survey with good methods, but self-reported and correlational. DORA's explanation is larger batch sizes.

### DORA 2025 State of AI-assisted Software Development
- URL: https://cloud.google.com/blog/products/ai-machine-learning/announcing-the-2025-dora-report (read). Report: https://cloud.google.com/devops/state-of-devops
- Date: 2025-09-23. Publisher: Google DORA.
- Measured: survey of nearly 5,000 professionals plus 100+ hours of qualitative data.
- Key numbers: 90% use AI at work. Over 80% believe AI raised their productivity. 30% have little or no trust in AI code. AI adoption is now positively related to throughput and product performance, but still negatively related to delivery stability. The report attributes this to "increased change volume overwhelming weak controls" (tests, version control, feedback loops). It replaces the low-to-elite tiers with seven team profiles. Search adds: 59% report a positive effect on code quality.
- Trust: Medium. Same caveats as 2024. Main message: "AI is an amplifier".

### DORA AI Capabilities Model (2025)
- URL: https://services.google.com/fh/files/misc/2025_dora_ai_capabilities_model.pdf ; https://cloud.google.com/blog/products/ai-machine-learning/introducing-doras-inaugural-ai-capabilities-model
- Date: 2025-09. Publisher: Google DORA.
- Content (search): of 15 candidate capabilities, seven had a significant interaction with AI use. They are a clear AI stance, a healthy data ecosystem, AI-accessible internal data, strong version control practices, small batches, user-centric focus, and quality internal platforms. Without a user-centric focus, AI adoption can hurt team performance.
- Trust: Medium. Useful as a qualitative checklist, not as a number. The user-centric finding matters for question 7.

### SPACE framework
- URL: https://queue.acm.org/detail.cfm?id=3454124
- Date: 2021. Authors: Forsgren, Storey, Maddila, Zimmermann, Houck, Butler (GitHub, Microsoft, UVic). Independent and academic.
- Content (from prior knowledge, not fetched): productivity has five dimensions: Satisfaction and well-being, Performance, Activity, Communication and collaboration, Efficiency and flow. Use at least three dimensions, and never activity counts alone.
- Trust: High as a framework. It has no reference numbers.

### DevEx framework
- URL: https://queue.acm.org/detail.cfm?id=3595878
- Date: 2023. Authors: Noda, Storey, Forsgren, Greiler.
- Content (prior knowledge): three dimensions, feedback loops, cognitive load and flow state, measured with perceptual (survey) plus workflow (system) data.
- Trust: High as a framework. For a solo maintainer it supports a short self-report diary of flow, load and feedback-loop pain.

### DX AI-assisted engineering reports (Q4 2025, Q1 and Q2 2026)
- URLs: https://getdx.com/report/ai-assisted-engineering-q4-impact-report/ ; https://getdx.com/news/dx-releases-q2-2026-state-of-ai-impact-in-engineering-report/
- Date: Q4 2025 (data July to Oct 2025) and Q2 2026. Publisher: DX (vendor, sells developer-productivity measurement).
- Measured: 135k developers across about 425 orgs (Q4 2025). Telemetry for 85k of them, and time saved is self-reported.
- Key numbers (search): 3.6 h/week saved (Q4 2025), 3.9 h (Q1 2026), 4 to 6 h (Q2 2026). 22% of merged code AI-authored (Q4 2025), over 50% (Q2 2026). Median "TrueThroughput" went from 1.42 to 1.94 PRs per engineer per week over four quarters. Maintainability +3.8% and change confidence -6.1%.
- Trust: Low to Medium. Vendor, self-reported hours. PRs/engineer/week is a usable team baseline.

### METR RCT, early-2025 AI and experienced open-source developers
- URL: https://metr.org/blog/2025-07-10-early-2025-ai-experienced-os-dev-study/ ; paper https://metr.org/Early_2025_AI_Experienced_OS_Devs_Study-paper.pdf
- Date: 2025-07. Publisher: METR (independent nonprofit).
- Measured: 16 experienced developers, 246 tasks on their own mature repositories, randomized AI-allowed or AI-disallowed, mostly Cursor with Claude 3.5/3.7.
- Key numbers: 19% slower with AI (95% CI +2% to +39%). They expected 24% faster and afterwards believed 20% faster. Screen recordings showed less time actively coding and more time prompting, waiting and reviewing, plus more idle time. Fewer than 44% of AI generations were accepted, and about 9% of time went to reviewing and cleaning AI output (search).
- Trust: High for its setting. METR now labels it historical. Its main lasting lesson is that self-reported speedup is unreliable.

### METR uplift update (second study)
- URL: https://metr.org/blog/2026-02-24-uplift-update/ (read)
- Date: 2026-02-24. Publisher: METR.
- Measured: from Aug 2025, 57 developers (10 returning, 47 new), 143 repositories, 800+ tasks, paid $50/h.
- Key numbers: returning developers -18% time (CI -38% to +9%), new developers -4% (CI -15% to +9%). Negative means a speedup, and neither result is significant. METR calls it "very weak evidence" and likely a lower bound, because developers refused to take part or withheld tasks they would not do without AI (30 to 50% withheld some). Time measurement broke down with concurrent agents, because developers switched to other work while agents ran. METR is changing the study design.
- Trust: High for honesty about limits, low as a point estimate. Relevant: wall-clock timing fails with parallel agents.

### METR transcript analysis (upper bound on time savings)
- URL: https://metr.org/notes/2026-02-17-exploratory-transcript-analysis-for-estimating-time-savings-from-coding-agents/ (read)
- Date: 2026-02-17. Author: Amy Deng, METR.
- Measured: 5,305 Claude Code transcripts from 7 METR staff in Jan 2026. Human time with AI is counted as the sum of 10-minute windows that contain at least one human-typed message, deduplicated across concurrent sessions. Time without AI is estimated by an LLM judge.
- Key numbers: time-savings factor about 1.5x to 13x across staff, with point estimates 2.1x to 11.6x. Higher concurrency goes with higher savings, and the top user averaged 2.3 main agents per active day. The judge's log correlation with human estimates is r = 0.83, usually within 2 to 3x. Every staff member's self-estimate was below the measured value. Lines of committed code explained about 46% of the variance in estimated daily task minutes for one person. 3.5 of 8 validation PRs were "task substitution", that is, low-value work that would otherwise not have been done.
- Trust: Medium. Explicitly an upper bound with a tiny sample, but it is the best published method for measuring human time with agents from transcripts. **Directly reusable for Wraith Box.**

### Cui, Demirer, Jaffe, Musolff, Peng, Salz: three field experiments
- URL: https://economics.mit.edu/sites/default/files/inline-files/draft_copilot_experiments.pdf ; Management Science, DOI 10.1287/mnsc.2025.00535
- Date: working paper 2024, journal 2025/2026. Independent academics plus Microsoft coauthors.
- Measured: RCTs of GitHub Copilot at Microsoft, Accenture and an anonymous Fortune 100 company, 4,867 developers.
- Key numbers (search): +26.08% (SE 10.3%) completed PRs per week, pooled. Larger gains for less experienced developers.
- Trust: High for completion-era tools in 2023. Does not cover agents.

### Google internal RCT (Paradis et al.)
- URL: https://arxiv.org/abs/2410.12944
- Date: 2024-10. Publisher: Google.
- Measured: 96 Google engineers, one enterprise C++ task, three AI features.
- Key numbers (search): about 21% faster (96 vs 114 min), with a wide CI.
- Trust: Medium to High. Lab task, internal tools, mid-2024.

### Stanford, Denisov-Blanch (100k and 120k developer studies)
- URLs: https://ai.engineer/talks/tbDDYKRFjhk-ai-developer-productivity (talk, 2025-07-23). Follow-up "120k devs" talk 2025-12-11.
- Publisher: Stanford researchers (academic, but talks, not peer-reviewed).
- Measured: Git history from 600+ companies, scored with a model for "functionality delivered", not commits.
- Key numbers (search): average gain 15 to 20%. Greenfield low-complexity 30 to 40%, greenfield high-complexity 10 to 15%, brownfield low-complexity 15 to 20%, brownfield high-complexity 0 to 10%. Gains are lower in less popular languages and larger codebases. Follow-up: median lift about 10%. In one case PRs went up 14%, quality went down 9%, and rework rose 2.5x.
- Trust: Medium. Large and thoughtful, but the method is only partly published. Useful for its greenfield versus brownfield split, because Wraith Box is greenfield, and its Go and Swift are reasonably popular languages.

### Faros AI "AI Productivity Paradox" (2025)
- URL: https://www.faros.ai/blog/ai-software-engineering (read)
- Date: 2025-07-23 (data to June 2025). Publisher: Faros AI (vendor, engineering analytics).
- Measured: telemetry from 10k+ developers on 1,255 teams. Compares each team's two lowest-adoption and two highest-adoption quarters with Spearman correlations.
- Key numbers: tasks +21%, PRs merged +98%, PR review time +91%, PR size +154%, bugs per developer +9%. No significant company-level correlation with DORA metrics.
- Trust: Medium. Vendor, but the method is stated. The correlations are weak (rho about 0.08 to 0.12).

### Faros AI "Acceleration Whiplash" (Q2 2026)
- URL: https://www.faros.ai/research/ai-acceleration-whiplash (read)
- Date: 2026 (Q2). Publisher: Faros AI.
- Measured: about 22k developers on 4k teams, high versus low adoption within the same orgs.
- Key numbers: epics completed +66.2%, task throughput +33.7%, PR merge rate +16.2%, deployments per week -11.7%. Median review time about 5x (search sources say +441.5%), and time to first review +156.6% (search). PR size +51%. Bugs per developer +54%, bugs per PR +28.7%, incidents per PR +243%, code churn +861%. Search sources also say 31% more PRs merge with no review, which I did not see on the page.
- Trust: Medium-Low. Vendor, and the headline numbers are dramatic. Direction agrees with Chen and Stratton and with DORA.

### Chen and Stratton (Harvard): "Artificial Intelligence in the Firm: Bottlenecks in Software Production"
- URL: secondary coverage https://complexdiscovery.com/harvard-researchers-find-ai-coding-agents-shifted-the-bottleneck-to-code-review/ , https://forkast.news/ai-coding-agents-write-30-more-code-and-resolve-zero-additional-issues-harvard-study-finds/ (paper is a Harvard job market paper, version 2026-08-04)
- Publisher: Harvard PhD candidates (independent, not peer-reviewed). Data from Jellyfish (vendor).
- Measured: 718 firms, 725k workers, Jan 2021 to Mar 2026, adoption of coding agents (Claude Code, Cursor, Devin).
- Key numbers (search): lines of code per worker +30% (about +1,494 on a baseline of about 5,017 per worker-month), commits +20%, PRs +23%. Jira issues and epics resolved show no significant change. Review time +49%, changes-requested share nearly doubled, comments per PR +35%.
- Trust: Medium-High. Best design among the observational studies (staggered adoption, many firms). The output-versus-delivery gap is the key caveat for velocity metrics.

### GitClear AI code quality 2025
- URL: https://www.gitclear.com/ai_assistant_code_quality_2025_research
- Date: 2025-02. Publisher: GitClear (vendor, code analytics).
- Measured: 211M changed lines, 2020 to 2024.
- Key numbers (search): duplicated blocks of 5+ lines up about 8x in 2024. Moved (refactored) lines fell from about 25% (2021) to under 10% (2024). Copy/paste went from 8.3% to 12.3%. Two-week churn went from 3.1% (2020) to 5.7% (2024).
- Trust: Medium-Low. Vendor. Its own pages disagree on some numbers, and the data does not separate AI from human code. It shows trends over time only.

### GitClear "The Maintainability Gap" 2026
- URL: https://www.gitclear.com/the_ai_code_quality_maintainability_gap (read)
- Date: 2026 (data to H1 2026). Publisher: GitClear.
- Measured: 623M changes, 2023 to 2026.
- Key numbers: block duplication +81%, copy/paste 9.4% of changed lines (2022) to 15.7% (H1 2026), moved code 21% (2022) to 3.8% (2026 YTD), error-masking constructs +47%, two-week churn +15% (no absolute value), function connectivity -35%. Heavy AI users gained about 25% velocity over their own past output.
- Trust: Low-Medium. Vendor, inconsistent units on the page, and time trends not tied to AI authorship.

### Uplevel Copilot study
- URL: https://www.cio.com/article/3540579/devs-gaining-little-if-anything-from-ai-coding-assistants.html
- Date: 2024-09. Publisher: Uplevel (vendor).
- Key numbers (search): about 800 developers (351 in the Copilot group), no change in PR cycle time or throughput, 41% more bugs.
- Trust: Low. The report is two pages, and groups were defined by access, not use. Critique: https://jasonstcyr.com/2024/10/09/does-github-copilot-actually-raise-bugs-in-code-by-41/

### CodeScene "Code for Machines, Not Just Humans" and AI-Ready Code
- URL: https://codescene.com/hubfs/whitepapers/AI-Ready-Code-How-Code-Health-Determines-AI-Performance.pdf
- Date: 2026-01 (v2 2026-03). Publisher: CodeScene (vendor). Authors Borg and Tornhill, with a peer-reviewed paper behind it.
- Key numbers (search): AI refactoring of 5,000 programs with six LLMs. Defect risk is at least 30% higher (later pages say 60%) when AI works on unhealthy code. The earlier "Code Red" study found up to 15x fewer defects in healthy code.
- Trust: Low-Medium. Vendor, and its numbers changed between versions. The direction (agents do worse in low-health code) is plausible.

### Anthropic, "How AI is transforming work at Anthropic"
- URL: https://www.anthropic.com/research/how-ai-is-transforming-work-at-anthropic (read)
- Date: 2025-12-02. Publisher: Anthropic (vendor, studying itself).
- Measured: survey of 132 engineers and researchers (Aug 2025), 53 interviews, about 200k internal Claude Code transcripts (Feb and Aug 2025).
- Key numbers: Claude used in 59% of work (28% a year before). Self-reported productivity +50% (+20% a year before). 14% report gains over 100%. 27% of Claude-assisted work would not otherwise have been done. Most respondents can fully delegate only 0 to 20% of their work. Merged PRs per engineer per day +67% after Claude Code adoption. Transcripts: consecutive tool calls 9.8 to 21.2, human turns 6.2 to 4.1, task complexity 3.2 to 3.8 (on a 1 to 5 scale), feature implementation 14% to 37% of transcripts.
- Trust: Medium-Low for the productivity numbers (self-report, vendor, privileged users). Medium for the transcript statistics.

### Anthropic, "How employment is changing at Anthropic"
- URL: https://www.anthropic.com/research/employment-at-anthropic (read)
- Date: 2026-10-16. Publisher: Anthropic Economics team.
- Measured: internal records, 192 agent adopters versus 74 non-adopters with fixed effects, session telemetry and surveys.
- Key numbers: commits per engineer about 5x the level of six months before. Agent work hours passed human hours in March 2026, and by Aug 2026 an individual's agents logged about 6x their handler's hours. Agent hours come from session telemetry excluding waits over 30 minutes, and human hours assume 40 h/week. About 80% of interactive Claude session time is spent waiting for human input. Meta-work (building agent tooling, supervising pipelines) went from 10% to 22% of interactive sessions. No difference in removal or revert rates between agent and human code.
- Trust: Medium. Vendor studying itself, non-random adoption (the authors say so). The method for agent-hours (exclude gaps over 30 min) is **directly reusable**.

### Anthropic, "Measuring AI agent autonomy in practice"
- URL: https://www.anthropic.com/research/measuring-agent-autonomy (read)
- Date: 2026-02-18. Publisher: Anthropic.
- Key numbers: median Claude Code turn about 45 s. The 99.9th-percentile turn went from under 25 to over 45 min (Oct 2025 to Jan 2026). Full auto-approve in about 20% of sessions for new users and over 40% after 750 sessions. Interrupts in 5% of turns (new users) and 9% (experienced). Internally, success on the hardest tasks doubled and human interventions per session fell from 5.4 to 3.3 (Aug to Dec 2025). Top reasons humans interrupt: missing context or corrections 32%, slow or hanging 17%.
- Trust: Medium. Large telemetry, vendor. Gives baselines for interventions per session.

### Anthropic Economic Index (software)
- URLs: https://www.anthropic.com/research/anthropic-economic-index-january-2026-report ; https://www.anthropic.com/research/economic-index-june-2026-report ; https://www.anthropic.com/research/estimating-productivity-gains
- Dates: 2025-04 (software report), 2025-11-25 (productivity), 2026-01-15, 2026-06.
- Key numbers (search): 79% of Claude Code conversations are "automation" versus 49% on Claude.ai (2025 software report). Claude-estimated time per software request without AI is about 3.3 h. Estimated task-time reduction across conversations is 80%, which excludes the human's validation time outside the chat.
- Trust: Low-Medium for productivity (the model estimates its own counterfactual). Useful only as context.

### Stack Overflow Developer Survey 2025
- URL: https://stackoverflow.co/company/press/archive/stack-overflow-2025-developer-survey/
- Date: 2025-07-29. About 49k respondents.
- Key numbers (search): 84% use or plan to use AI. 46% distrust its accuracy and 33% trust it. 66% cite "almost right, but not quite". 45% say debugging AI code takes significant time.
- Trust: Medium (large self-selected survey).

---

## 2. Measuring AI effort (tokens, cost, sessions, agent-hours)

### Claude Code docs, "Manage costs effectively"
- URL: https://code.claude.com/docs/en/costs (read)
- Date: current (Nov 2026). Publisher: Anthropic (vendor docs).
- Key numbers: about $13 per developer per active day and $150 to 250 per developer per month across enterprise deployments. 90% of users stay below $30 per active day. Agent teams use about 7x the tokens of a standard session in plan mode. `/usage` shows API-equivalent cost at list price, with API duration versus wall duration. Example session: API 6m20s, wall 6h33m. OpenTelemetry export gives per-user tokens and cost.
- Trust: Medium. Vendor averages, but the only official per-day baseline.

### Artificial Analysis Coding Agent Index; Vantage
- URLs (search only): https://www.vantage.sh/blog/agentic-coding-costs ; summary in https://www.morphllm.com/ai-coding-costs
- Key numbers (search): a 50-turn session is about 1M input and 40k output tokens (25:1), $3 to 6 on Sonnet, with about 85% of cost from input. Artificial Analysis (Oct 2026): Claude Code with Sonnet 5.5 (max) averaged $14.19 and 27.7M tokens per benchmark task. An earlier Sept snapshot: $1.80, 4.6M tokens, 6.7 min per task. A gateway study puts the effective input price at about $0.30/M at a 92% cache hit rate.
- Trust: Low-Medium. Vendor and secondary sources, and benchmark tasks are not PRs.

### Cost per merged PR or per KLOC
- **No independent published figure found.** The search found no study that divides token cost by merged PRs or KLOC for agentic development. The nearest are:
  - Uvik SDD benchmark (see section 6): $2.35 to 4.23 per merged ticket and about 270k to 330k build-phase tokens per ticket (median), for 45-minute Python tickets. Vendor, low trust.
  - Cloudflare vinext (2026-02-24, https://blog.cloudflare.com/vinext): one engineer, about one week, about $1,100 in tokens, 800+ sessions, 94% of the Next.js 16 API, 1,700+ unit and 380 E2E tests. No LOC in the sources I saw. Vendor PR, and security flaws were found soon after (https://cybernews.com/security/hackers-find-critical-flaws-in-cloudflares-nextjs-alternative/).
  - Ladybird LibJS port (2026-02-23, https://ladybird.org/posts/adopting-rust/): about 25k lines of Rust in about 2 weeks by one expert directing Claude Code and Codex, with zero regressions on test262 plus its own tests. A port with a byte-for-byte oracle, which is not comparable with new design work. No cost given.
  - Gas Town (Yegge, 2026, https://steve-yegge.medium.com/gas-town-from-clown-show-to-v1-0-c239d9a407ec ; https://www.dolthub.com/blog/2026-01-15-a-day-in-gas-town/): about $100/hour of tokens with 12 to 30 parallel agents, about 10x a normal session. Several $200/month Max accounts. "100% vibe coded."
  - Subscriptions hide cost. Quesma (https://quesma.com/blog/claude-code-pricing-for-enterprise/) finds Max plans worth 12 to 40x their price at API rates. Report API-equivalent cost and cash cost separately.

### METR time horizons
- URL: https://metr.org/time-horizons/ (read). Opus 4.6 post: https://x.com/METR_Evals/status/2024923422867030027
- Dates: page updated 2026-09-08, "no longer actively updated". Opus 4.6 measured 2026-02-20, Mythos Preview 2026-05-08.
- Key numbers: Claude Opus 4.6 50% horizon about 14.5 h (95% CI 6 to 98 h), noisy because the suite is near saturation. Claude Mythos Preview (early) at least 16 h (CI 8.5 to 55 h) (search). "Measurements above 16 hrs are unreliable." Doubling time: about 7 months long-run, about 4 months (88 to 130 days) on 2024+ fits (search). The 80% horizons are much shorter (Opus 4.6 about 1 h 10 m under TH1.1, search). METR tasks are cleaner than real work, and performance drops on "messier" tasks and with holistic scoring.
- Trust: High for the method, low for precision at the top end. For Wraith Box, the 80% horizon and the messiness caveat matter more than the 50% headline.

---

## 3. Inferring human effort from activity logs

### WakaTime
- URLs: https://wakatime.com/faq ; https://wakatime.com/developers/
- Content (search): heartbeats from the editor are joined into durations when gaps are at most the "keystroke timeout", default 15 min, settable from 5 to 240 min. Time after the last heartbeat is not counted. Public leaderboards always use 15 min. Users say 15 min captures typing time, not working time.
- Trust: Medium. A de facto industry convention, not validated research.

### git-hours style commit sessionization
- URLs: https://github.com/kimmobrunfeldt/git-hours ; https://github.com/Pierre-Demessence/git-hours ; https://github.com/luigitni/git-estimate
- Content (search): defaults are a 120 min max gap within a session plus 30 to 120 min credited for the first commit of a session. git-estimate uses an 8 h gap and credits 2 h. Vendor claims of "70 to 80% of billable time recovered" have no published validation.
- Trust: Low. **No peer-reviewed validation of commit-gap thresholds was found.**

### METR 10-minute window method
- See section 1. Counts 10-minute windows with at least one human-typed message, deduplicated across concurrent sessions. This is the best published method for human time in agentic work, and is better than commit gaps for Wraith Box because transcripts exist.

### Anthropic agent-hours method
- See section 1 (employment report). Agent-hours come from session telemetry excluding waits over 30 min. About 80% of interactive session wall time is spent waiting on the human, so session wall time is neither human time nor agent time.

### Program comprehension share of developer time
- Minelli, Mocci, Lanza, ICPC 2015: https://robertominelli.com/assets/downloads/publications/Mine2015b.pdf . 18 developers, 740 sessions, about 200 h of IDE events. About 70% of time on program comprehension and about 5% on editing (the editing share is from memory of the paper, verify).
- Xia, Bao, Lo, Xing, Hassan, Li, TSE 2018: https://xin-xia.github.io/publication/TSE17.pdf . 78 professionals, 3,148 working hours, all applications. About 58% of time on comprehension. Seniors spend less.
- Trust: High (peer-reviewed field studies), but they predate AI. Implication: keyboard and prompt activity understates human effort, because thinking, reading and reviewing time leaves few events.

### Human time supervising agents
- METR 2025 RCT screen recordings (above): more time prompting, waiting, reviewing and idle, about 9% on reviewing and cleaning AI output.
- Anthropic autonomy (above): interventions per session 5.4 to 3.3, and interrupts on 5 to 9% of turns.
- Anthropic employment (above): about 80% of interactive session time waiting on the human, and meta-work 22% of sessions.
- "The Work Behind Delegation" (arXiv 2609.24234, 2026): qualitative framework built on Sheridan's supervisory control. No numbers seen.
- Chen and Stratton: review time +49%, and 14% more workers pulled into review (search).
- BairesDev Dev Barometer Q3 2026 (search, vendor survey of 705): "nearly 80% spend most of their week reviewing". Low trust.
- Bain 2026 figures quoted in secondary sources (21% more tasks, 91% more review time) **look copied from Faros 2025**. Don't cite them without checking.
- **Gap:** I found no study that measures the hours a single maintainer spends on prompting, reviewing and deciding when supervising parallel autonomous agents. Wraith Box's own measurement would be new data.

---

## 4. Forecasting

### Vacanti, "When Will It Be Done?" (2020) and practitioner summaries
- URLs: https://www.lucasfcosta.com/blog/monte-carlo-forecasts ; https://medium.com/asos-techblog/the-full-monte-901d721b8532
- Content (search): Monte Carlo from historical daily throughput of finished items, with 10k+ runs, reported as percentiles (50/70/85/95). The assumption is that "the future looks roughly like the past". ASOS back-test: the 85th-percentile forecast was right 90% of the time (361/400), the 70th 84%, the 50th 68%.
- Trust: Medium-High (widely used, back-tested in one org).

### Magennis / Focused Objective
- URLs: https://github.com/FocusedObjective/FocusedObjective.Resources ; via https://sep.com/blog/software-forecasting-my-process/
- Content (search): the throughput forecaster spreadsheet samples historical throughput, takes backlog size plus a **split rate** (low and high, 1.0 to 2.0 or more) for scope growth, and runs 500+ simulations. Sample guidance: the chance that the next sample lands in the range seen so far is (n-1)/(n+1), so 7 samples give 75%, 11 give 83% and 20 give 90%.
- Trust: Medium-High as practice. The split rate is the knob for "discovered work".

### Calendar days and zero days
- Practitioner consensus (https://kiniabulatov.com/2025/09/16/monte-carlo-simulation-forecasting-throughput-and-project-timelines/ ; https://github.com/destacey/Wayd/issues/888): sample **calendar days including zero-throughput days**, so weekends and absences carry into the forecast. Prateek Singh's 2021 back-test (cited there) found simple random sampling beats Markov or weekday-matched sampling.
- **Part-time and intermittent capacity: no source addresses it directly.** Inference: either sample calendar days (and assume future attention looks like past attention), or model two layers, an attention calendar (on and off days) and throughput per active day. The two-layer model lets you run scenarios such as "maintainer has 2 days a week" against "a holiday month".

### Flyvbjerg reference class and base rates
- Sources: Flyvbjerg and Gardner, *How Big Things Get Done* (2023), Appendix A. Flyvbjerg and Budzier, HBR 2011 "Why Your IT Project May Be Riskier Than You Think" (https://arxiv.org/pdf/1304.0265). JMIS 2022.
- Key numbers (search): IT projects average a 73% cost overrun. 18% are in the tail (over 50% overrun), and those average 447%. In HBR 2011 (1,471 projects), the average cost overrun was 27%, and one in six was a "black swan" with about 200% cost and about 70% schedule overrun. JMIS 2022 (5,000+ IT projects): median overrun 0, mean 80%. Only 8.5% of all projects hit cost and time, and 0.5% hit cost, time and benefits.
- Trust: High for large organizational IT projects. **Not a matching reference class** for a solo, AI-agent, greenfield developer tool. Use it to justify fat-tailed (P85/P95) reporting, not as a multiplier.

### Forecasting with AI agents
- No method source found. Relevant evidence:
  - LinearB 2026 benchmarks (via https://www.flowverify.co/blog/ai-code-review-bottleneck-2026-data, vendor, search): a "review-led" versus "coding-led" regime. Once agent output passes review capacity, the backlog compounds. AI PRs merge within 30 days 32.7% of the time against 84.4% for unassisted PRs.
  - CircleCI 2026 State of Software Delivery (28M workflows, vendor): feature-branch throughput +15% but main-branch throughput -7% for the median team.
  - Chen and Stratton: code +30%, issues resolved flat.
  - Implication: forecast from **done, human-accepted items** (merged and verified against acceptance), and treat human review and decision capacity as the constraint (theory of constraints). Agent capacity is close to elastic and is not the bottleneck.

### Critical path
- No AI-specific source found. Standard practice (Magennis's book *Forecasting and Simulating Software Development Projects*, critical chain): throughput Monte Carlo assumes independent, interchangeable items. Items that only the maintainer can do (needs-host, decisions, integration testing), and chains of blocked items, break that assumption. Forecast them as a separate serial queue, and take the max of (parallel agent work, serial human chain).

---

## 5. Quality measures and leading indicators

### Change failure rate and rework (DORA)
- See DORA 2024: elite CFR about 5% (the high cluster is about 20% in 2024, odd but published). Deployment rework rate was added in 2024. For a pre-release project, map "deployment" to "merge to main" and "failure" to "reverted or follow-up fix within N days".

### Post-merge follow-up fixes on agent PRs
- Takerngsaksiri, Duong, Barnett, "Who Finishes the Job?", arXiv 2609.26847 (read abstract), 2026-09-22.
- Measured: 6,774 merged agent PRs (Codex, Copilot, Devin, Cursor, Claude Code) in AIDev-pop (repos with 500+ stars) versus 5,044 human PRs from the same repos.
- Key numbers: odds of a verified follow-up fix are **1.62x** higher for agent merges. 69.6% of fixes to agent merges come from the same agent. LLM-judge agreement with humans K = 0.78.
- Trust: High-Medium (academic, method described). A good method template for Wraith Box: link each merged PR to later fix PRs.

### Survival of agent code
- Rahman and Shihab, "Will It Survive?", arXiv 2601.16809 (read abstract), 2026-01.
- Measured: 201 OSS projects, 200k+ code units, agent versus human.
- Key numbers: agent code is modified **less** (hazard ratio 0.842, modification rate 15.8 points lower). Corrective modifications are slightly higher (26.3% vs 23.0%). The effect is small (V = 0.116), and variation between agents exceeds the agent-human gap.
- An MSR 2026 study (via blog, https://codex.danielvaughan.com/2026/05/01/agent-code-churn-research-codex-cli-durability-patterns/, not verified) of 110k PRs reports the opposite: agent code is rewritten faster.
- Trust: Medium. **The evidence on churn conflicts.** Don't present churn as settled.

### Agent PR merge rates (AIDev dataset studies)
- arXiv 2607.21832, 2601.15195, 2602.08915, 2602.00164, 2601.17627 (search).
- Key numbers: human PRs merge about 85%, agent PRs 64 to 68% overall. Claude Code ranges from 57 to 84% depending on the subset, with only 115 to 459 Claude Code PRs in the sets.
- Trust: Medium. Public OSS PRs from agents to strangers' repos, not a maintainer's own agent fleet.

### Review findings per PR
- CodeRabbit "State of AI vs Human Code Generation", https://www.coderabbit.ai/blog/state-of-ai-vs-human-code-generation-report, 2025-12-17. Vendor (sells AI review).
- Key numbers (search): 470 OSS PRs. AI co-authored 10.83 issues per PR against human 6.45 (1.7x). Logic and correctness issues +75%, performance issues about 8x, readability 3.15x. The AI or human label is uncertain.
- Trust: Low-Medium.

### Static-analysis issue density of LLM code
- Sonar "Coding Personalities of Leading LLMs" (https://www.sonarsource.com/the-coding-personalities-of-leading-llms.pdf, 2025-08) and model reviews (https://www.sonarsource.com/blog/claude-opus-5-5-an-evaluation/, 2026-09). Vendor.
- Key numbers (search): Claude Sonnet 4 per KLOC: 1.14 bugs, 0.38 vulnerabilities, 17.96 smells. Opus 4.6: 18.33 issues per KLOC. Opus 4.7: 24.10 issues per KLOC (0.80 bugs, 0.29 vulnerabilities). Opus 5: 20.52. Opus 5.5: 16.46 issues per KLOC. Over 90% of issues are code smells. Java benchmark tasks only.
- Trust: Medium-Low (vendor, Java tasks). A rough per-KLOC reference for SonarQube-style issue density.

### Security of LLM code
- Veracode GenAI Code Security reports: https://www.veracode.com/blog/genai-code-security-report/ (2025-07), https://www.veracode.com/blog/spring-2026-genai-code-security/ (2026-03), Oct 2026 press release (search). Vendor.
- Key numbers: 45% of tasks introduced an OWASP Top 10 flaw (2025). Security pass rate was flat at 45 to 55% while syntax pass rates rose to 95%. 44% (mid-2026) and 38% (Oct 2026, 18 models). No security-specific prompting.
- Trust: Medium-Low. Narrow CWE set, prompt-level tests, not agentic work with reviews.

### Defect density baselines
- McConnell, *Code Complete* 2nd ed. (2004), citing older studies: industry average 15 to 50 errors per KLOC of delivered code. Microsoft applications 10 to 20 per KLOC in in-house testing and 0.5 per KLOC released. Cleanroom 3 per KLOC in testing and 0.1 released.
- Capers Jones: about 5 defects per function point created, 85% average defect removal efficiency (DRE), and over 95% DRE counts as high quality.
- Trust: Medium. Old (1990s data), definitions vary. Use only for order of magnitude.

### Test coverage
- Ivanković, Petrović, Just, Fraser, "Code coverage at Google", ESEC/FSE 2019, https://research.google/pubs/code-coverage-at-google/ . Guidelines from the Google Testing Blog, "Code Coverage Best Practices" (2020): 60% acceptable, 75% commendable, 90% exemplary (search, attribution to the blog post rather than the paper is likely).
- Inozemtseva and Holmes, ICSE 2014, "Coverage Is Not Strongly Correlated with Test Suite Effectiveness" (from knowledge): correlation is low to moderate once suite size is controlled.
- Trust: High. Coverage is a floor, not evidence of quality.

### Mutation testing
- Petrović, Ivanković, Fraser, Just, "Practical Mutation Testing at Scale: A View from Google", TSE 2022, https://arxiv.org/abs/2102.11378 .
- Key numbers (search): incremental, diff-based mutants surfaced in review. 17M mutants over 760k changes, with 2M surfaced to 24k developers. In related work, 38% of all surfaced mutants and 60% of productive mutants were resolved by code or test changes. Full mutation adequacy is "neither practical nor desirable".
- Trust: High. Suggests running mutation testing on the security-critical packages (netd, proxyd), with go-mutesting or similar tools, as a test-strength indicator that AI-written tests cannot easily game.

### Flaky tests
- Micco, Google Testing Blog, 2016-05-27 (https://testing.googleblog.com/2016/05/flaky-tests-at-google-and-how-we.html): about 1.5% of all test runs flaky, and about 16% of tests have some flakiness. 84% of pass-to-fail transitions involved a flaky test. 2017 follow-up (search): 0.5% small, 1.6% medium and 14% large tests.
- Lam et al. 2019 (Microsoft): 4.6% of test cases flaky. Parry et al. EASE 2025: 75% of flaky tests fail in clusters.
- Trust: High (Google, Microsoft), but old for Google.

### Coupling and cohesion
- No new 2024 to 2026 benchmark source found. Classic metrics (afferent and efferent coupling, instability I = Ce/(Ca+Ce), Martin's main-sequence distance, LCOM) apply per Go package. CodeScene's Code Health and GitClear's "function connectivity" (-35%) are vendor proxies. GitClear's moved-code decline is a proxy for missing refactoring.
- Trust: n/a. **Gap.**

### Requirements traceability
- Rempel and Mäder, "Preventing Defects: The Impact of Requirements Traceability Completeness on Software Quality", TSE 43(8), 2017, DOI 10.1109/TSE.2016.2622264 (search).
- Finding: more complete traceability goes with significantly fewer defects. The sample, about 24 Apache-style projects, is from memory, so verify it.
- Trust: High (TSE). Supports measuring the share of SEC/FR requirements with a linked test (C*) and code reference.

### Dependency freshness
- Cox, Bouwers, van Eekelen, Visser, "Measuring Dependency Freshness in Software Systems", ICSE 2015, https://ericbouwers.github.io/papers/icse15.pdf . The libyear tool (https://libyear.com) is derived from it. Singlebrook aims to keep apps below 10 libyears.
- Trust: Medium. A simple metric, and outdated dependencies correlate with vulnerabilities in the paper.

### Anthropic internal quality comparison
- Employment report (above): no difference in removed or reverted lines between agent and human code at Anthropic. Vendor.

---

## 6. Spec-driven and agentic development practice

### Böckeler, "Understanding Spec-Driven-Development: Kiro, spec-kit, and Tessl"
- URL: https://martinfowler.com/articles/exploring-gen-ai/sdd-3-tools.html (read). Date: 2025-10-15. Thoughtworks (independent of the tools).
- Content: three levels, spec-first, spec-anchored and spec-as-source. Spec-kit was heavy to review ("I'd rather review code than all these markdown files"), and the agent ignored notes and duplicated classes. Kiro turned a small bug into 4 user stories with 16 acceptance criteria. Tessl's regeneration was non-deterministic. Agents don't reliably follow large instruction sets.
- Trust: Medium-High (careful practitioner, qualitative).

### Uvik SDD benchmark (Q4 2026)
- URL: https://uvik.net/spec-driven-development-benchmark/ (read). Date: 2026-10. Vendor (staff augmentation, Claude partner).
- Measured: 50 brownfield Python tickets, five arms, blind review, a ±10-point noise band.
- Key numbers: merged of 50 were no-spec 36 (72%), Spec Kit 40, Kiro 41, BMAD 41 and OpenSpec 42. Review defects per merged ticket were 0.86 with no spec and 0.39 to 0.57 with a spec. Cost per merged ticket $2.35 to 4.23 and median time 29 to 55 min. The spec phase took 28 to 44% of tokens. Benefits were concentrated in feature tickets and tickets touching 3+ files.
- Trust: Low. A vendor single run with differences inside its own noise band, model names I could not verify, and a page dated before its run. Directional at best.

### Hashimoto, "My AI Adoption Journey"
- URL: https://mitchellh.com/writing/my-ai-adoption-journey . Date: 2026-02-05.
- Content (search): six steps, ending in "engineer the harness" (fix AGENTS.md and tools after every agent mistake) and "always have an agent running". He reproduced his manual commits with agents ("did the work twice") to calibrate. Context switching is expensive, so the human controls interruptions. **No effort or quality numbers.**
- Trust: Medium (expert practitioner, anecdotal).

### Ronacher (lucumr.pocoo.org)
- "Agentic Coding Recommendations" (2025-06-12): about $100/month Max plan, Sonnet, Go works well, and tools are exposed through a Makefile. "A Year of Vibes" (2025-12-22): mostly hands-off Claude Code, Amp and Pi. "The Coming Loop" (2026-06-23, search): long uninterrupted agent runs "produce worse code than what we were producing last autumn" because there is less human in the loop. **No effort or quality numbers.**
- Trust: Medium. A quality warning from an expert about high autonomy.

### Kent Beck, "Augmented Coding: Beyond the Vibes"
- URL: https://newsletter.kentbeck.com/p/augmented-coding-beyond-the-vibes . Date: 2025-06.
- Content (search): a B+ tree library in Rust and Python over about 4 weeks of part-time work, restarted twice. TDD system prompt. Good on correctness and performance, "not so good about the code quality". The agent cheated by deleting or disabling tests and faking implementations ("Genie Wants to Leap", 2025-05).
- Trust: Medium. Directly relevant: test integrity must be checked, not assumed.

### Simon Willison, "Agentic Engineering Patterns"
- URL: https://simonwillison.net/2026/Feb/23/agentic-engineering-patterns/ . Date: 2026-02-23 and later.
- Content (search): "writing code is cheap now". Red/green TDD. Agents build explanations to fight "cognitive debt". 2026-09 note: agents "make software engineering even harder". **No effort numbers found.**
- Trust: Medium.

### Yegge, Gas Town and Beads
- See section 2. Cost about $100/hour of agents and "100% vibe coded". Built for throughput, not correctness: "some work gets lost". The opposite of Wraith Box's model of firm human control.
- Trust: Low as evidence, but a useful contrasting reference point.

### Cloudflare vinext; Ladybird LibJS
- See section 2. Both are cases where an **existing test suite or reference implementation served as the spec**. Vinext shipped with security holes despite 2k+ tests, a caution that conformance tests are not security review.

### Anthropic, "measuring agent autonomy" and the internal study
- See section 1. Most engineers fully delegate only 0 to 20% of work, and supervision is the binding concern ("paradox of supervision").

---

## 7. "Done to spec but useless": fitness for use

### ISO/IEC 25010:2023 and 25019:2023
- URLs: https://quality.arc42.org/articles/iso-25010-update-2023 ; https://www.iso.org/standard/78176.html
- Content (search): the product quality model has nine characteristics, with usability renamed **interaction capability** and safety added. Quality in use (effectiveness, efficiency, satisfaction, freedom from risk, context coverage) moved to ISO/IEC 25019. Interaction capability is "a prerequisite for usability".
- Trust: High (standard). Gives vocabulary to separate conformance (product quality) from fitness (quality in use).

### Nielsen and Landauer, five-user testing
- URL: https://www.nngroup.com/articles/why-you-only-need-to-test-with-5-users/ (2000). Model from 1993.
- Content: about 85% of problems found with 5 users, assuming each problem has about a 31% chance of being hit per user. Iterate over 3+ rounds. Subtle problems need more users.
- Trust: High, with known limits.

### Cognitive and heuristic walkthroughs for developer tools
- Polson et al. 1992 (cognitive walkthrough). "Usefulness and usability of heuristic walkthroughs for evaluating domain-specific developer tools in industry", Information and Software Technology 2023, https://www.sciencedirect.com/science/article/pii/S0950584923000745 : adequate for developer tools, even early in development.
- arXiv 2512.03568, "Synthetic Cognitive Walkthrough": LLMs performing walkthroughs. Unproven.
- Trust: Medium-High. A walkthrough of the first-run journey (install, `wb claude`, egress denied, approve a host, push a branch) is cheap and fits a solo maintainer.

### Dogfooding formalized
- arXiv 2608.09939, "How to Dogfood Your AI Chat Agent" (2026): manual dogfooding is essential but unscalable and has expert blind spots. Proposes personas, goal types and gates.
- Trust: Medium. The idea of persona and goal journeys transfers.

### Spec conformance does not mean fitness (practitioner evidence)
- Tests written by the same agent mirror the agent's reading of the spec. Fixes: holdout or acceptance tests written separately, and role separation between test writer and coder. Sources (blogs, low trust): https://newsletter.agentbuild.ai/p/why-ai-written-tests-pass-but-still ; https://traianowelcome.substack.com/p/how-an-ai-coding-agents-can-pass . The SWE-CI benchmark (Alibaba, via blog) found agents broke existing behavior in about half of maintenance iterations.
- DORA AI Capabilities: without a user-centric focus, AI adoption can hurt performance.
- Kent Beck: agents delete or disable tests to pass.
- Trust: Low to Medium individually, consistent together.

---

## Searched for and not found
- An independent cost per merged PR or per KLOC for agentic development.
- A validated idle-gap threshold for estimating human effort from commits or events (only conventions: WakaTime 15 min, git-hours 120 min, METR 10-min windows, Anthropic 30-min exclusion).
- Forecasting guidance for part-time or intermittent capacity, or for AI-agent projects.
- Effort or quality numbers from Willison, Ronacher, Hashimoto or Beck for a whole solo project.
- A 2024 to 2026 coupling or cohesion benchmark.
- A reference class of solo, AI-heavy greenfield projects with known outcomes.
