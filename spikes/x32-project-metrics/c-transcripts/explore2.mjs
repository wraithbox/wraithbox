import { readdirSync, statSync } from "fs"; import { join } from "path";
const ROOT = "/Users/lsimons/.claude/projects"; const EXCL = "6053c145-8d5f-4eef-a385-51984e6f586c";
const mains = [];
for (const d of readdirSync(ROOT).filter(d => d.startsWith("-Users-lsimons-git-wraithbox-wraithbox")))
  for (const e of readdirSync(join(ROOT, d))) if (e.endsWith(".jsonl") && !e.includes(EXCL)) mains.push(join(ROOT, d, e));
for (const f of mains) {
  const ep = {}; let first = null, last = null, nPrompt = 0, ver = new Set(), odd = [];
  let prev = null;
  for (const line of (await Bun.file(f).text()).split("\n")) {
    if (!line) continue; const o = JSON.parse(line);
    if (o.entrypoint) ep[o.entrypoint] = 1; if (o.version) ver.add(o.version);
    if (o.timestamp) { first ??= o.timestamp; last = o.timestamp; }
    if (o.type === "user" && o.toolUseResult === undefined && !o.isMeta) {
      const c = o.message?.content; const s = typeof c === "string" ? c : "";
      if (o.origin?.kind === "human") nPrompt++;
      else if (typeof c === "string" && !/^\s*</.test(s) && !s.startsWith("This session is being")) odd.push(`${o.version}|prev=${prev}|len=${s.length}`);
      const m = s.match(/<command-name>([^<]{0,40})<\/command-name>/); if (m) odd.push(`cmd:${m[1]} origin=${o.origin?.kind} startsWith=${s.slice(1, 12)}`);
    }
    if (o.type !== "attachment" && o.type !== "progress") prev = o.type + (o.subtype ? "." + o.subtype : "");
  }
  console.log(f.split("/").slice(-2).join("/").slice(-60), Object.keys(ep).join(","), [...ver].join(","), first, last, nPrompt, odd.length ? odd.join(" ; ") : "");
}
