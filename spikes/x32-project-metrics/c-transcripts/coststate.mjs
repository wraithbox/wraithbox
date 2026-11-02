// Claude Code's own per-process cost records (type "cost-state") in main sessions: they include
// requests the transcripts do not log (auto-mode classifier, WebSearch/WebFetch, compaction, titles).
import { readdirSync, writeFileSync } from "fs"; import { join } from "path";
const ROOT = "/Users/lsimons/.claude/projects", OUT = "/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/c", EXCL = "6053c145-8d5f-4eef-a385-51984e6f586c";
const rows = [], byModel = {};
for (const d of readdirSync(ROOT).filter(d => d.startsWith("-Users-lsimons-git-wraithbox-wraithbox")))
  for (const e of readdirSync(join(ROOT, d))) {
    if (!e.endsWith(".jsonl") || e.includes(EXCL)) continue;
    const segs = []; let cur = null;
    for (const line of (await Bun.file(join(ROOT, d, e)).text()).split("\n")) {
      if (!line.includes('"cost-state"')) continue; const o = JSON.parse(line); if (o.type !== "cost-state") continue;
      if (!cur || o.totalCostUSD < cur.totalCostUSD || o.startTime !== cur.startTime) { if (cur) segs.push(cur); }
      cur = o;
    }
    if (cur) segs.push(cur);
    // segments: a drop in cost or a new startTime = new process; keep the last record of each segment
    const dedupSegs = []; for (const s of segs) { const l = dedupSegs.at(-1); if (l && l.startTime === s.startTime) dedupSegs[dedupSegs.length - 1] = s; else dedupSegs.push(s); }
    let total = 0;
    for (const s of dedupSegs) { total += s.totalCostUSD; for (const [m, u] of Object.entries(s.modelUsage || {})) { const a = byModel[m] ??= { model: m, input: 0, output: 0, cache_write: 0, cache_read: 0, web_search: 0, cost_usd: 0 }; a.input += u.inputTokens; a.output += u.outputTokens; a.cache_write += u.cacheCreationInputTokens; a.cache_read += u.cacheReadInputTokens; a.web_search += u.webSearchRequests || 0; a.cost_usd += u.costUSD; } }
    rows.push({ session: e.replace(".jsonl", ""), segments: dedupSegs.length, records: segs.length, cost_usd: total, api_duration_min: dedupSegs.reduce((a, s) => a + (s.totalAPIDuration || 0), 0) / 60000, lines_added: dedupSegs.reduce((a, s) => a + (s.totalLinesAdded || 0), 0), lines_removed: dedupSegs.reduce((a, s) => a + (s.totalLinesRemoved || 0), 0) });
  }
const csv = (r, c) => [c.join(","), ...r.map(x => c.map(k => typeof x[k] === "number" && !Number.isInteger(x[k]) ? x[k].toFixed(4) : x[k]).join(","))].join("\n") + "\n";
writeFileSync(join(OUT, "cost_state_by_session.csv"), csv(rows, Object.keys(rows[0])));
writeFileSync(join(OUT, "cost_state_by_model.csv"), csv(Object.values(byModel), Object.keys(Object.values(byModel)[0])));
console.log(rows.reduce((a, r) => a + r.cost_usd, 0), rows.filter(r => r.segments > 1).map(r => r.session.slice(0, 8) + ":" + r.segments).join(" "));
console.log(Object.values(byModel));
