# X32-project-metrics spike code

Throwaway measurement scripts for X32-project-metrics (#543). They were written for speed, and none of the project gates apply. The answer and the report are in `docs/spikes/X32-project-metrics.md` and `docs/research/R09-v1-progress-and-quality.md` on main.

- `b-git/`: git and GitHub history (commits, LOC, line survival, issues, PRs, CI, contributions).
- `c-transcripts/`: Claude Code transcripts (deduplicated tokens, cost, agent time, prompts, gaps).
- `d-structure/`: specs, traceability, coverage, fuzz targets, imports, complexity, doc comments, dependencies, interfaces.
- `e-remaining/`: remaining V1 work, calibration, scope growth, the DAG and critical path.
- `f-fitness/`: read-only CLI probe for the fitness-for-use review.
- `synth/`: the human-effort model (`human-effort.mjs`) and the throughput Monte Carlo (`forecast.mjs <data> count|lines`).
- `results/`: the agents' summaries and the aggregate outputs that the report uses.

Paths are hard-coded to the maintainer's machine. Raw data, including anything taken from transcripts and the cached GitHub responses, stays in `~/git/lsimons/lsimons-tmp/wraithbox-metrics/data/` and is not committed. Run order: b, c, d and e in any order, then `synth/human-effort.mjs` and `synth/forecast.mjs`.
