# Wave 1 agent cost

Taken from the coordinator's task notifications on 2026-11-02 (session 6053c145). Tokens are the `subagent_tokens` figure that Claude Code reports, and durations are wall-clock. Agents B and D ran on Sonnet 5.5 and the rest on Opus 5.5. F ran at high effort and the rest at medium.

| Agent | Tokens | Duration |
|---|---|---|
| A, web research | 177,581 | 12.5 min |
| B, git and GitHub | 76,993 | 6.4 min |
| C, transcripts | 122,577 | 9.0 min |
| D, specs and structure | 135,467 | 7.9 min |
| E, remaining work | 133,615 | 10.2 min |
| F, fitness for use | 210,404 | 11.4 min |
| **Total** | **856,637** | 6 to 13 min, in parallel |
| Reviewer of PR545 | 128,403 | 6.6 min |
