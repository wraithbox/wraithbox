import { readFileSync } from "fs";
const D = "/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/c/";
const load = f => { const [h, ...r] = readFileSync(D + f, "utf8").trim().split("\n").filter(l => !l.startsWith("#")); const H = h.split(","); return r.map(l => { const v = l.match(/("([^"]|"")*"|[^,]*)(,|$)/g).map(x => x.replace(/,$/, "").replace(/^"|"$/g, "")); return Object.fromEntries(H.map((k, i) => [k, isNaN(+v[i]) || v[i] === "" ? v[i] : +v[i]])); }); };
const week = d => { const x = new Date(d + "T00:00:00Z"); x.setUTCDate(x.getUTCDate() - ((x.getUTCDay() + 6) % 7)); return x.toISOString().slice(0, 10); };
const tl = load("timeline_by_day.csv"), pr = load("prompts.csv"), gp = load("gaps.csv"), inv = load("inventory.csv"), wk = load("tokens_by_week.csv"), rl = load("reading_load_by_day.csv"), wl = load("wave_leads.csv"), it = load("interrupts.csv");
const W = {};
const w = d => W[week(d)] ??= { ai_union_h: 0, agent_h: 0, human_prompts: 0, human_words: 0, human_days: 0, ai_days: 0, hs15_h: 0, thinking: 0, thinking_min: 0, breaks: 0, sessions: 0, subagents: 0, reading_kchars: 0, wd95: 0 };
for (const r of tl) { const x = w(r.date); x.ai_union_h += r.ai_union_min / 60; x.agent_h += r.agent_min / 60; x.hs15_h += r.human_sess15_min / 60; if (r.human_prompts > 0) x.human_days++; if (r.ai_union_min > 0) x.ai_days++; if (r.weekday_9to5_human) x.wd95++; }
for (const p of pr) { const x = w(p.date); x.human_prompts++; x.human_words += p.words; }
for (const g of gp) { const x = w(g.date); if (g.class === "thinking") { x.thinking++; x.thinking_min += g.gap_min; } if (g.class === "break") x.breaks++; }
for (const r of inv) { const x = w(r.start_local.slice(0, 10)); r.kind === "session" ? x.sessions++ : x.subagents++; }
for (const r of rl) w(r.date).reading_kchars += r.chars / 1000;
console.log("week|cost|out_M|cacheR_M|calls|sessions|subagents|ai_union_h|agent_h|prompts|words|human_days|hsess15_h|wd9-17days|thinking(n/min)|breaks|reading_kchars");
for (const k of Object.keys(W).sort()) { const x = W[k], t = wk.find(r => r.week_monday === k) || {}; console.log([k, t.cost_usd?.toFixed(0), (t.output / 1e6).toFixed(2), (t.cache_read / 1e6).toFixed(0), t.calls, x.sessions, x.subagents, x.ai_union_h.toFixed(1), x.agent_h.toFixed(1), x.human_prompts, x.human_words, x.human_days, x.hs15_h.toFixed(1), x.wd95, `${x.thinking}/${x.thinking_min.toFixed(0)}`, x.breaks, x.reading_kchars.toFixed(0)].join("|")); }
const sum = (a, f) => a.reduce((s, r) => s + (+r[f] || 0), 0);
console.log("totals ai_union_h", (sum(tl, "ai_union_min") / 60).toFixed(1), "agent_h", (sum(tl, "agent_min") / 60).toFixed(1), "prompts", pr.length, "words", sum(pr, "words"), "chars", sum(pr, "chars"), "human days", tl.filter(r => r.human_prompts > 0).length, "ai days", tl.filter(r => r.ai_union_min > 0).length, "hs5/15/30 h", ...[5, 15, 30].map(g => (sum(tl, `human_sess${g}_min`) / 60).toFixed(1)), "ai s5/15/30", ...[5, 15, 30].map(g => (sum(tl, `ai_sess${g}_min`) / 60).toFixed(1)), "interrupts", it.length, "reading chars", sum(rl, "chars"), "words", sum(rl, "words"));
const typed = pr.filter(p => p.category === "typed" || p.category === "queued"); const ws = typed.map(p => p.words).sort((a, b) => a - b);
console.log("typed n", typed.length, "median words", ws[ws.length >> 1], "p75", ws[Math.floor(ws.length * .75)], "max", ws.at(-1));
console.log("weekday 9-17 human days", tl.filter(r => r.weekday_9to5_human).length, "weekday 9-17 ai days", tl.filter(r => r.weekday_9to5_ai).length, "weekdays with any human", tl.filter(r => r.human_prompts && !["Sat", "Sun"].includes(r.dow)).length);
console.log("gaps", ["thinking", "other", "break"].map(c => `${c}:${gp.filter(g => g.class === c).length}/${sum(gp.filter(g => g.class === c), "gap_min").toFixed(0)}min`).join(" "));
const wlc = wl.map(r => r.subtree_cost_usd).sort((a, b) => a - b);
console.log("wave leads", wl.length, "subtree cost sum", sum(wl, "subtree_cost_usd").toFixed(0), "median", wlc[wlc.length >> 1]?.toFixed(1), "max", wlc.at(-1)?.toFixed(1), "own sum", sum(wl, "own_cost_usd").toFixed(0), "descendants", sum(wl, "descendants"));
const subs = inv.filter(r => r.kind === "subagent"); const roles = {};
for (const r of subs) { const x = roles[r.role] ??= { n: 0, unattr: 0, cost: 0, calls: 0, comp: 0, ge: 0, wall: 0 }; x.n++; if (r.issue === "") x.unattr++; x.cost += r.cost_usd; x.calls += r.api_calls; x.comp += r.compactions; if (r.calls_ge_max_turns === 1) x.ge++; x.wall += r.active_min; }
console.log("role|n|unattributed|cost|api_calls|compactions|calls>=maxTurns|active_h"); for (const [k, x] of Object.entries(roles).sort((a, b) => b[1].cost - a[1].cost)) console.log([k, x.n, x.unattr, x.cost.toFixed(0), x.calls, x.comp, x.ge, (x.wall / 60).toFixed(1)].join("|"));
const sess = inv.filter(r => r.kind === "session"); console.log("sessions", sess.length, "by role", JSON.stringify(sess.reduce((a, r) => (a[r.role] = (a[r.role] || 0) + 1, a), {})), "main compactions", sum(sess, "compactions"), "sessions with 0 prompts", sess.filter(r => !r.human_prompts).length);
console.log("first", inv[0].start_local, "last", inv.at(-1).end_local);
