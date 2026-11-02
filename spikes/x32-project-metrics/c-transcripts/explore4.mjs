import { readdirSync, statSync, existsSync, readFileSync } from "fs"; import { join } from "path";
const ROOT = "/Users/lsimons/.claude/projects/-Users-lsimons-git-wraithbox-wraithbox";
const T = {}; const t = k => T[k] = (T[k] || 0) + 1;
for (const s of readdirSync(ROOT)) {
  const sd = join(ROOT, s, "subagents"); if (!existsSync(sd)) continue;
  for (const e of readdirSync(sd)) { if (!e.endsWith(".jsonl")) continue;
    const meta = JSON.parse(readFileSync(join(sd, e.replace(".jsonl", ".meta.json")), "utf8"));
    t(`finished=${meta.finished} stopped=${meta.stoppedByUser}`);
    const lines = readFileSync(join(sd, e), "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l));
    for (const o of lines) { if (o.type === "attachment") t("att:" + o.attachment?.type); if (o.type === "system") t("sys:" + o.subtype); }
    const last = lines.filter(o => o.type === "user" || o.type === "assistant").at(-1);
    t(`last=${last?.type} sr=${last?.message?.stop_reason} blocks=${Array.isArray(last?.message?.content) ? last.message.content.map(b => b.type).join("+") : typeof last?.message?.content} finished=${meta.finished}`);
    const first = lines.find(o => o.type === "user");
    t(`firstUser ctype=${typeof first?.message?.content} hasAgentId=${!!first?.agentId}`);
  }
}
for (const [k, v] of Object.entries(T).sort((a, b) => b[1] - a[1])) console.log(v, k);
