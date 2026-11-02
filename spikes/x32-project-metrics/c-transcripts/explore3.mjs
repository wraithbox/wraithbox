import { readdirSync, statSync, existsSync, readFileSync } from "fs"; import { join } from "path";
const ROOT = "/Users/lsimons/.claude/projects/-Users-lsimons-git-wraithbox-wraithbox";
const types = {}, keys = {}, names = {}, other = {}; let nometa = 0, n = 0, depth = {};
for (const s of readdirSync(ROOT)) {
  const sd = join(ROOT, s, "subagents"); if (!existsSync(sd)) continue;
  const walk = (p) => { for (const e of readdirSync(p)) { const q = join(p, e); if (statSync(q).isDirectory()) { other["dir:" + e] = (other["dir:" + e] || 0) + 1; walk(q); continue; }
    if (e.endsWith(".jsonl")) { n++; names[e.replace(/[0-9a-f]{8,}.*/, "")] = (names[e.replace(/[0-9a-f]{8,}.*/, "")] || 0) + 1;
      const m = q.replace(/\.jsonl$/, ".meta.json"); if (!existsSync(m)) { nometa++; continue; }
      const j = JSON.parse(readFileSync(m, "utf8")); types[j.agentType] = (types[j.agentType] || 0) + 1; for (const k of Object.keys(j)) keys[k] = (keys[k] || 0) + 1; depth[j.spawnDepth] = (depth[j.spawnDepth] || 0) + 1;
    } else other[e.replace(/^.*?\./, ".")] = (other[e.replace(/^.*?\./, ".")] || 0) + 1; } };
  walk(sd);
}
console.log({ n, nometa, types, keys, names, other, depth });
