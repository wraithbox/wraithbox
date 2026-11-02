// Structural exploration only: tallies shapes, never prints content.
import { readdirSync, statSync, existsSync } from "fs";
import { join } from "path";
const ROOT = "/Users/lsimons/.claude/projects";
const EXCL = "6053c145-8d5f-4eef-a385-51984e6f586c";
const files = [];
for (const d of readdirSync(ROOT).filter(d => d.startsWith("-Users-lsimons-git-wraithbox-wraithbox"))) {
  const walk = p => { for (const e of readdirSync(p)) { const q = join(p, e); const s = statSync(q); if (s.isDirectory()) { if (e !== "tool-results" && e !== "memory") walk(q); } else if (e.endsWith(".jsonl") && !q.includes(EXCL)) files.push(q); } };
  walk(join(ROOT, d));
}
const tally = {}; const t = (k) => tally[k] = (tally[k] || 0) + 1;
function shape(s) { if (typeof s !== "string") return "nonstr"; const m = s.match(/^\s*(<[a-z-]+>|\[Request interrupted[^\]]*\]|Caveat:|This session is being continued|\[Image)/); return m ? m[1].slice(0, 40) : "plain"; }
const usageDiff = { same: 0, diff: 0 };
for (const f of files) {
  const sub = f.includes("/subagents/");
  const seen = new Map();
  const text = await Bun.file(f).text();
  for (const line of text.split("\n")) {
    if (!line) continue; let o; try { o = JSON.parse(line); } catch { t("parseerr"); continue; }
    const P = sub ? "S" : "M";
    if (o.type === "system") t(`${P} system.${o.subtype}`);
    if (o.isCompactSummary) t(`${P} isCompactSummary`);
    if (o.type === "user") {
      if (o.toolUseResult !== undefined) { t(`${P} user.toolResult`); continue; }
      const c = o.message?.content;
      let sh = typeof c === "string" ? shape(c) : Array.isArray(c) ? "arr:" + c.map(b => b.type === "text" ? "text/" + shape(b.text) : b.type).join("+").slice(0, 80) : "?";
      t(`${P} user meta=${!!o.isMeta} origin=${o.origin?.kind} ps=${o.promptSource} to=${o.turnOrigin} ${sh}`);
    }
    if (o.type === "assistant") {
      t(`${P} model=${o.message?.model}`);
      const id = o.message?.id; const u = JSON.stringify(o.message?.usage);
      if (seen.has(id)) { seen.get(id) === u ? usageDiff.same++ : usageDiff.diff++; } seen.set(id, u);
      if (o.isApiErrorMessage) t(`${P} apiError`);
    }
  }
}
console.log(files.length, usageDiff);
for (const [k, v] of Object.entries(tally).sort((a, b) => b[1] - a[1])) console.log(v, k);
