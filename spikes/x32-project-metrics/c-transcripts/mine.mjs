// X32 agent C: mine Claude Code transcripts into count-only data files.
// Never writes prompt or response text: only counts, lengths, timestamps, IDs.
import { readdirSync, statSync, existsSync, readFileSync, writeFileSync, mkdirSync } from "fs";
import { join, basename } from "path";

const ROOT = "/Users/lsimons/.claude/projects";
const OUT = "/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/c";
const EXCL = "6053c145-8d5f-4eef-a385-51984e6f586c";
mkdirSync(OUT, { recursive: true });

// ---------- prices (USD per MTok), platform.claude.com/docs/en/about-claude/pricing, fetched 2026-11-02 ----------
const PRICES = {
  "claude-opus-5-5": { in: 4, w5: 5, w1h: 8, hit: 0.2, out: 20, note: "published" },
  "claude-opus-5": { in: 5, w5: 6.25, w1h: 10, hit: 0.5, out: 25, note: "published" },
  "claude-opus-4-8": { in: 5, w5: 6.25, w1h: 10, hit: 0.5, out: 25, note: "published" },
  "claude-opus-4-7": { in: 5, w5: 6.25, w1h: 10, hit: 0.5, out: 25, note: "published" },
  "claude-fable-5-1": { in: 10, w5: 12.5, w1h: 20, hit: 0.25, out: 50, note: "published" },
  "claude-sonnet-5-5": { in: 2, w5: 2.5, w1h: 4, hit: 0.1, out: 10, note: "published" },
  "claude-sonnet-4-6": { in: 3, w5: 3.75, w1h: 6, hit: 0.3, out: 15, note: "published" },
  "claude-haiku-4-5-20251001": { in: 1, w5: 1.25, w1h: 2, hit: 0.1, out: 5, note: "published" },
};
const FAST_MULT = 2; // fast mode: Opus 5.5 $8/$40 = 2x standard
const US_GEO_MULT = 1.1;

// ---------- time helpers ----------
const fmt = new Intl.DateTimeFormat("en-CA", { timeZone: "Europe/Amsterdam", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", weekday: "short", hour12: false });
const minCache = new Map();
function local(epochMin) {
  let v = minCache.get(epochMin);
  if (v) return v;
  const p = Object.fromEntries(fmt.formatToParts(new Date(epochMin * 60000)).map(x => [x.type, x.value]));
  const hour = p.hour === "24" ? 0 : +p.hour;
  const dow = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"].indexOf(p.weekday);
  const date = `${p.year}-${p.month}-${p.day}`;
  const d = new Date(date + "T00:00:00Z"); d.setUTCDate(d.getUTCDate() - dow);
  v = { date, hour, minute: +p.minute, dow, week: d.toISOString().slice(0, 10), hm: `${p.hour === "24" ? "00" : p.hour}:${p.minute}` };
  minCache.set(epochMin, v);
  return v;
}
const emin = ts => Math.floor(Date.parse(ts) / 60000);
const words = s => (s.trim().match(/\S+/g) || []).length;

// ---------- discover files ----------
const mains = [], subs = [];
for (const d of readdirSync(ROOT).filter(d => d.startsWith("-Users-lsimons-git-wraithbox-wraithbox"))) {
  const pd = join(ROOT, d);
  for (const e of readdirSync(pd)) {
    const q = join(pd, e);
    if (e.endsWith(".jsonl")) { if (!e.includes(EXCL)) mains.push({ path: q, id: e.replace(".jsonl", ""), dir: d }); continue; }
    if (e === EXCL || e === "memory" || !statSync(q).isDirectory()) continue;
    const sd = join(q, "subagents");
    if (!existsSync(sd)) continue;
    for (const f of readdirSync(sd)) {
      if (!f.endsWith(".jsonl")) continue;
      const meta = JSON.parse(readFileSync(join(sd, f.replace(".jsonl", ".meta.json")), "utf8"));
      const fsk = join(sd, f.replace(".jsonl", ".forked-skill.json"));
      const skill = existsSync(fsk) ? JSON.parse(readFileSync(fsk, "utf8")).skillName : null;
      subs.push({ path: join(sd, f), id: f.replace(/^agent-/, "").replace(".jsonl", ""), session: e, meta, skill });
    }
  }
}
subs.sort((a, b) => a.meta.spawnDepth - b.meta.spawnDepth);
const MAXTURNS = { builder: 200, researcher: 200, "spike-builder": 200, "code-reviewer": 80, "security-reviewer": 150, "wave-lead": 300 };

// ---------- global state ----------
const seenMsg = new Map(); // message.id -> file id that owns it
const msgs = new Map(); // message.id -> {file, model, ts, usage, rids:Set}
const ridToMsg = new Map();
let crossFileDupLines = 0, crossFileDupMsgs = new Set();
const seenUuid = new Set();
const inventory = [];
const fileMinutes = new Map(); // file id -> Set(epochMin)
const aiEventsByMin = new Map(); // epochMin -> count of events
const prompts = [], interrupts = [], gaps = [];
const readingByDay = {};
const agentToolResults = new Map(); // toolUseId -> {limitHit:boolean}
const fileInfo = new Map();
let dedupSample = null;
const speedGeo = {};

function issueFrom(text, src) {
  if (!text) return null;
  let m = text.match(/(?:pull request|PR)\s*#?(\d{2,4})\b/i);
  if (m) return { n: +m[1], kind: "pr", src };
  m = text.match(/\bissues?\s*#?(\d{2,4})\b/i) || text.match(/#(\d{2,4})\b/);
  if (m) return { n: +m[1], kind: "issue", src };
  return null;
}
function issueFromBranch(b, src) {
  if (!b || b === "main" || b === "HEAD" || /^worktree-agent-/.test(b)) return null;
  const m = b.match(/(?:^|[\/-])(\d{2,4})(?:-|$)/);
  return m ? { n: +m[1], kind: "branch", src } : null;
}
function textOf(c) {
  if (typeof c === "string") return c;
  if (Array.isArray(c)) return c.filter(b => b.type === "text").map(b => b.text).join("\n");
  return "";
}
const asksOrOffers = t => {
  const s = (t || "").trim();
  if (!s) return false;
  if (/\?\s*\**\s*$/.test(s)) return true;
  const tail = s.slice(-1500);
  return /(^|\n)\s*(\d+[.)]|[a-d][.)]|\*\*Option|Option [A-D1-9]|- \*\*\(?[A-D1-9]\)?)\s+\S/m.test(tail) && /\b(option|choose|prefer|which|want me|should I|shall I|or)\b/i.test(tail);
};

async function processFile(f, isMain) {
  const id = f.id;
  const info = { id, isMain, models: new Set(), apiCalls: 0, compactions: 0, start: null, end: null, lines: 0, gitBranches: new Set(), firstPrompt: null, typed: 0, endsOnToolUse: false, apiErrors: 0 };
  const mins = new Set();
  const askIds = new Set();
  let lastAsstTs = null, lastAsstText = "", lastAsk = false;
  let rawUsage = { in: 0, out: 0, cw: 0, cr: 0 }, lastKind = null;
  const text = await Bun.file(f.path).text();
  for (const line of text.split("\n")) {
    if (!line) continue;
    let o; try { o = JSON.parse(line); } catch { continue; }
    info.lines++;
    if (o.type === "system" && o.subtype === "compact_boundary") info.compactions++;
    if (o.gitBranch) info.gitBranches.add(o.gitBranch);
    if (!o.timestamp) continue;
    const ts = o.timestamp; const m = emin(ts);
    if (o.uuid && seenUuid.has(o.uuid)) { crossFileDupLines++; continue; }
    if (o.uuid) seenUuid.add(o.uuid);
    info.start ??= ts; if (ts > (info.end || "")) info.end = ts;

    if (o.type === "assistant") {
      const msg = o.message || {};
      const mid = msg.id;
      if (mid && seenMsg.has(mid) && seenMsg.get(mid) !== id) { crossFileDupMsgs.add(mid); continue; }
      if (o.isApiErrorMessage) info.apiErrors++;
      mins.add(m); aiEventsByMin.set(m, (aiEventsByMin.get(m) || 0) + 1);
      info.asstLines = (info.asstLines || 0) + 1;
      lastKind = Array.isArray(msg.content) ? msg.content.map(b => b.type).join("+") : "text";
      if (msg.model && msg.model !== "<synthetic>") info.models.add(msg.model);
      const u = msg.usage;
      if (u) { rawUsage.in += u.input_tokens || 0; rawUsage.out += u.output_tokens || 0; rawUsage.cw += u.cache_creation_input_tokens || 0; rawUsage.cr += u.cache_read_input_tokens || 0; }
      if (mid && msg.model !== "<synthetic>") {
        seenMsg.set(mid, id);
        let r = msgs.get(mid);
        if (!r) { r = { file: id, model: msg.model, ts, usage: u, rids: new Set() }; msgs.set(mid, r); info.apiCalls++; }
        r.usage = u || r.usage; r.ts = ts;
        if (o.requestId) { r.rids.add(o.requestId); if (!ridToMsg.has(o.requestId)) ridToMsg.set(o.requestId, new Set()); ridToMsg.get(o.requestId).add(mid); }
        const k = `${u?.speed}/${u?.inference_geo}`; speedGeo[k] = (speedGeo[k] || 0) + 1;
      }
      if (Array.isArray(msg.content)) for (const b of msg.content) {
        if (b.type === "tool_use" && b.name === "AskUserQuestion") { askIds.add(b.id); lastAsk = true; }
        if (b.type === "text" && b.text) {
          lastAsstText = b.text; lastAsk = false;
          if (isMain) { const d = local(m).date; readingByDay[d] ??= { chars: 0, words: 0, blocks: 0 }; readingByDay[d].chars += b.text.length; readingByDay[d].words += words(b.text); readingByDay[d].blocks++; }
        }
      }
      lastAsstTs = ts;
      continue;
    }
    if (o.type !== "user") continue;
    const c = o.message?.content;
    // tool results = AI activity; also AskUserQuestion answers and Agent tool results
    const blocks = Array.isArray(c) ? c : [];
    const trs = blocks.filter(b => b.type === "tool_result");
    if (o.toolUseResult !== undefined || trs.length) {
      mins.add(m); aiEventsByMin.set(m, (aiEventsByMin.get(m) || 0) + 1);
      lastKind = "tool_result";
      for (const b of trs) {
        const t = typeof b.content === "string" ? b.content : Array.isArray(b.content) ? b.content.filter(x => x.type === "text").map(x => x.text).join("\n") : "";
        if (/max(imum)?[ _-]?turns|turn limit/i.test(t.slice(0, 2000)) || /max(imum)?[ _-]?turns|turn limit/i.test(t.slice(-2000))) agentToolResults.set(b.tool_use_id, true);
        if (isMain && askIds.has(b.tool_use_id)) {
          const ans = o.toolUseResult?.answers;
          const s = ans && typeof ans === "object" ? Object.values(ans).map(String).join(" ") : t;
          addPrompt(f, ts, "ask-answer", s, { lastAsstTs, lastAsstText, lastAsk: true });
        }
      }
      continue;
    }
    if (!isMain) {
      if (!info.firstPrompt && typeof c === "string") info.firstPrompt = c;
      else if (!info.firstPrompt && Array.isArray(c) && !o.isMeta) info.firstPrompt = textOf(c);
      continue;
    }
    if (o.isMeta || o.isCompactSummary) continue;
    const s = textOf(c);
    if (/^\s*\[Request interrupted by user/.test(s)) { interrupts.push({ ts, session: id, forTool: /for tool use/.test(s) }); continue; }
    let cat = null, body = s;
    if (o.origin?.kind === "human") {
      if (/^\s*<command-message>/.test(s)) { cat = "slash-skill"; const a = s.match(/<command-name>([^<]*)<\/command-name>[\s\S]*?(?:<command-args>([\s\S]*?)<\/command-args>)?/); body = ((a?.[1] || "") + " " + (a?.[2] || "")).trim(); }
      else cat = o.promptSource === "queued" ? "queued" : "typed";
    } else if (o.origin === undefined && /^\s*<command-name>/.test(s)) {
      cat = "slash-builtin"; const a = s.match(/<command-name>([^<]*)<\/command-name>[\s\S]*?(?:<command-args>([\s\S]*?)<\/command-args>)?/); body = ((a?.[1] || "") + " " + (a?.[2] || "")).trim();
    } else if (o.origin === undefined && /^\s*<bash-input>/.test(s)) { cat = "bash"; body = s.replace(/<\/?bash-input>/g, ""); }
    if (!cat) continue;
    info.typed++;
    addPrompt(f, ts, cat, body, { lastAsstTs, lastAsstText, lastAsk });
  }
  info.endsOnToolUse = lastKind === "tool_use" || lastKind?.endsWith("+tool_use") || false;
  if (!dedupSample && info.apiCalls > 200 && isMain) {
    const d = { in: 0, out: 0, cw: 0, cr: 0 };
    for (const [mid, r] of msgs) if (r.file === id && r.usage) { d.in += r.usage.input_tokens || 0; d.out += r.usage.output_tokens || 0; d.cw += r.usage.cache_creation_input_tokens || 0; d.cr += r.usage.cache_read_input_tokens || 0; }
    dedupSample = { file: id, assistantLines: info.asstLines, raw: rawUsage, dedup: d, distinctMessages: info.apiCalls };
  }
  fileMinutes.set(id, mins);
  fileInfo.set(id, info);
  return info;
}

function addPrompt(f, ts, cat, body, ctx) {
  const p = { ts, session: f.id, cat, chars: body.length, words: words(body) };
  if (ctx.lastAsstTs) {
    p.gapMin = (Date.parse(ts) - Date.parse(ctx.lastAsstTs)) / 60000;
    p.prevAsked = ctx.lastAsk || asksOrOffers(ctx.lastAsstText);
  }
  prompts.push(p);
}

for (const f of mains.sort((a, b) => a.id.localeCompare(b.id))) await processFile(f, true);
for (const f of subs) await processFile(f, false);

// ---------- cost ----------
const unpriced = {};
function cost(model, u) {
  const p = PRICES[model];
  if (!p || !u) { if (u) unpriced[model] = (unpriced[model] || 0) + 1; return 0; }
  const cc = u.cache_creation;
  const w5 = cc ? cc.ephemeral_5m_input_tokens || 0 : u.cache_creation_input_tokens || 0;
  const w1 = cc ? cc.ephemeral_1h_input_tokens || 0 : 0;
  let c = ((u.input_tokens || 0) * p.in + w5 * p.w5 + w1 * p.w1h + (u.cache_read_input_tokens || 0) * p.hit + (u.output_tokens || 0) * p.out) / 1e6;
  if (u.speed === "fast") c *= FAST_MULT;
  if (u.inference_geo === "us") c *= US_GEO_MULT;
  return c;
}
const zero = () => ({ calls: 0, input: 0, output: 0, cache_write: 0, cache_write_5m: 0, cache_write_1h: 0, cache_read: 0, cost_usd: 0 });
function add(acc, u, c) {
  acc.calls++; acc.input += u.input_tokens || 0; acc.output += u.output_tokens || 0;
  acc.cache_write += u.cache_creation_input_tokens || 0; acc.cache_write_5m += u.cache_creation?.ephemeral_5m_input_tokens || 0; acc.cache_write_1h += u.cache_creation?.ephemeral_1h_input_tokens || 0;
  acc.cache_read += u.cache_read_input_tokens || 0; acc.cost_usd += c;
}

// ---------- roles, parents, issues ----------
const subById = new Map(subs.map(s => [s.id, s]));
const roleOf = id => {
  const s = subById.get(id);
  if (!s) { const mf = mains.find(m => m.id === id); return mf && mf.dir.includes("worktrees") ? (fileInfo.get(id).typed ? "interactive-worktree" : "sdk") : "interactive"; }
  if (s.skill) return `skill:${s.skill}`;
  return s.meta.agentType;
};
const parentOf = id => { const s = subById.get(id); if (!s) return ""; return s.meta.parentAgentId && subById.has(s.meta.parentAgentId) ? s.meta.parentAgentId : s.session; };
const issueOf = new Map();
// PR number -> issue number (closing reference, else the number in the head branch); from gh pr list
const prMap = new Map();
for (const l of readFileSync(join(OUT, "pr_branches.csv"), "utf8").split("\n").filter(Boolean)) {
  const [n, br, closes] = l.split(",").map(x => x.replace(/"/g, ""));
  const iss = closes ? +closes.split(" ")[0] : (br.match(/(?:^|[\/-])(\d{2,4})(?:-|$)/) || [])[1];
  prMap.set(+n, iss ? +iss : null);
}
const resolve = iss => { if (!iss) return iss; if (prMap.has(iss.n)) return { ...iss, pr: iss.n, n: prMap.get(iss.n) ?? iss.n }; return iss; };
for (const s of subs) {
  const info = fileInfo.get(s.id);
  let iss = issueFromBranch(s.meta.worktreeBranch, "meta.worktreeBranch");
  for (const b of info.gitBranches) iss ??= issueFromBranch(b, "gitBranch");
  const fp = issueFrom(info.firstPrompt, "firstPrompt");
  const dp = issueFrom(s.meta.description, "description");
  // reviewers: prefer the PR number named in the prompt over the builder branch they check out
  if (/review/.test(roleOf(s.id)) && (dp?.kind === "pr" || fp?.kind === "pr")) iss = dp?.kind === "pr" ? dp : fp;
  iss ??= dp ?? fp;
  if (roleOf(s.id) === "wave-lead") iss = null;
  // inherit from parent agent (forks of a builder, skills run by a reviewer)
  if (!iss && s.meta.parentAgentId && issueOf.get(s.meta.parentAgentId)) iss = { ...issueOf.get(s.meta.parentAgentId), src: "parent:" + issueOf.get(s.meta.parentAgentId).src };
  if (iss) issueOf.set(s.id, resolve(iss));
}

// ---------- aggregates ----------
const byModel = {}, byDay = {}, byWeek = {}, byRole = {}, byWeekRole = {}, byFile = {}, total = zero();
for (const [mid, r] of msgs) {
  if (!r.usage) continue;
  const c = cost(r.model, r.usage);
  const lt = local(emin(r.ts)); const role = roleOf(r.file);
  for (const [acc, k] of [[byModel, r.model], [byDay, lt.date], [byWeek, lt.week], [byRole, role], [byWeekRole, lt.week + "|" + role], [byFile, r.file]]) add(acc[k] ??= zero(), r.usage, c);
  add(total, r.usage, c);
}
const csv = (rows, cols) => [cols.join(","), ...rows.map(r => cols.map(c => { const v = r[c]; return v === undefined || v === null ? "" : typeof v === "number" ? (Number.isInteger(v) ? v : v.toFixed(4)) : /[,"\n]/.test(String(v)) ? `"${String(v).replace(/"/g, '""')}"` : v; }).join(","))].join("\n") + "\n";
const TCOLS = ["calls", "input", "output", "cache_write", "cache_write_5m", "cache_write_1h", "cache_read", "cost_usd"];
const dump = (name, obj, key) => writeFileSync(join(OUT, name), csv(Object.entries(obj).sort().map(([k, v]) => ({ [key]: k, ...v })), [key, ...TCOLS]));
dump("tokens_by_model.csv", byModel, "model");
dump("tokens_by_day.csv", byDay, "date");
dump("tokens_by_week.csv", byWeek, "week_monday");
dump("tokens_by_role.csv", byRole, "role");
writeFileSync(join(OUT, "tokens_by_week_role.csv"), csv(Object.entries(byWeekRole).sort().map(([k, v]) => ({ week_monday: k.split("|")[0], role: k.split("|")[1], ...v })), ["week_monday", "role", ...TCOLS]));
writeFileSync(join(OUT, "prices.csv"), csv(Object.entries(PRICES).map(([m, p]) => ({ model: m, ...p })), ["model", "in", "w5", "w1h", "hit", "out", "note"]).replace(/\n$/, "\n") +
  "# USD per million tokens. Source: https://platform.claude.com/docs/en/about-claude/pricing (fetched 2026-11-02). Fast mode x2 (Opus 5.5 $8/$40), inference_geo=us x1.1. API-equivalent only, not money spent.\n");

// ---------- inventory ----------
const rows = [];
for (const [id, info] of fileInfo) {
  const s = subById.get(id); const role = roleOf(id); const t = byFile[id] || zero();
  const mt = MAXTURNS[s?.meta.agentType];
  rows.push({
    id, kind: s ? "subagent" : "session", session: s ? s.session : id, parent: parentOf(id), depth: s ? s.meta.spawnDepth : 0, role,
    start_local: info.start ? `${local(emin(info.start)).date} ${local(emin(info.start)).hm}` : "", end_local: info.end ? `${local(emin(info.end)).date} ${local(emin(info.end)).hm}` : "",
    start_utc: info.start, end_utc: info.end, wall_min: info.start ? Math.round((Date.parse(info.end) - Date.parse(info.start)) / 60000) : 0,
    active_min: fileMinutes.get(id).size, models: [...info.models].join(" "), api_calls: info.apiCalls, human_prompts: s ? "" : info.typed,
    compactions: info.compactions, max_turns: mt ?? "", calls_ge_max_turns: mt ? (info.apiCalls >= mt - 2 ? 1 : 0) : "",
    parent_reports_turn_limit: s?.meta.toolUseId ? (agentToolResults.get(s.meta.toolUseId) ? 1 : 0) : "",
    ends_on_tool_use: info.endsOnToolUse ? 1 : 0, finished_meta: s?.meta.finished ? 1 : "", stopped_by_user: s?.meta.stoppedByUser ? 1 : "",
    api_errors: info.apiErrors, issue: issueOf.get(id)?.n ?? "", via_pr: issueOf.get(id)?.pr ?? "", issue_kind: issueOf.get(id)?.kind ?? "", issue_src: issueOf.get(id)?.src ?? "",
    ...Object.fromEntries(TCOLS.map(c => [c, t[c]])),
  });
}
rows.sort((a, b) => (a.start_utc || "").localeCompare(b.start_utc || ""));
writeFileSync(join(OUT, "inventory.csv"), csv(rows, Object.keys(rows[0])));

// ---------- issues ----------
const byIssue = {};
for (const r of rows) {
  if (r.kind !== "subagent" || r.issue === "") continue;
  const a = byIssue[r.issue] ??= { issue: r.issue, prs: new Set(), kinds: new Set(), agents: 0, roles: {}, first: r.start_local, last: r.end_local, ...zero() };
  a.kinds.add(r.issue_kind); if (r.via_pr !== "") a.prs.add(r.via_pr); a.agents++; a.roles[r.role] = (a.roles[r.role] || 0) + 1;
  if (r.end_local > a.last) a.last = r.end_local;
  for (const c of TCOLS) a[c] += r[c];
}
const issueRows = Object.values(byIssue).map(a => ({ ...a, kinds: [...a.kinds].join(" "), prs: [...a.prs].join(" "), roles: Object.entries(a.roles).map(([k, v]) => `${k}:${v}`).join(" "), total_tokens: a.input + a.output + a.cache_write + a.cache_read })).sort((a, b) => b.cost_usd - a.cost_usd);
writeFileSync(join(OUT, "issues.csv"), csv(issueRows, ["issue", "prs", "kinds", "agents", "roles", "first", "last", "total_tokens", ...TCOLS]));

// ---------- wave leads ----------
const children = new Map();
for (const r of rows) if (r.kind === "subagent") { if (!children.has(r.parent)) children.set(r.parent, []); children.get(r.parent).push(r); }
const subtree = id => { const out = []; for (const c of children.get(id) || []) { out.push(c); out.push(...subtree(c.id)); } return out; };
const waveRows = rows.filter(r => r.role === "wave-lead").map(r => {
  const st = subtree(r.id); const sum = zero();
  for (const x of [r, ...st]) for (const c of TCOLS) sum[c] += x[c];
  return { wave_lead: r.id, session: r.session, start_local: r.start_local, end_local: r.end_local, wall_min: r.wall_min, own_api_calls: r.api_calls, own_cost_usd: r.cost_usd, descendants: st.length,
    issues: [...new Set(st.map(x => x.issue).filter(x => x !== ""))].join(" "), compactions: r.compactions, ...Object.fromEntries(TCOLS.map(c => ["subtree_" + c, sum[c]])) };
});
writeFileSync(join(OUT, "wave_leads.csv"), csv(waveRows, Object.keys(waveRows[0] || { wave_lead: 1 })));
// sessions: own + all subagents
const sessRows = rows.filter(r => r.kind === "session").map(r => {
  const sub = rows.filter(x => x.session === r.id && x.kind === "subagent"); const sum = zero();
  for (const x of [r, ...sub]) for (const c of TCOLS) sum[c] += x[c];
  return { session: r.id, role: r.role, start_local: r.start_local, end_local: r.end_local, human_prompts: r.human_prompts, subagents: sub.length, wave_leads: sub.filter(x => x.role === "wave-lead").length, own_cost_usd: r.cost_usd, ...Object.fromEntries(TCOLS.map(c => ["total_" + c, sum[c]])) };
});
writeFileSync(join(OUT, "sessions_totals.csv"), csv(sessRows, Object.keys(sessRows[0])));

// ---------- prompts & gaps ----------
prompts.sort((a, b) => a.ts.localeCompare(b.ts));
const wordList = prompts.filter(p => ["typed", "queued", "ask-answer"].includes(p.cat)).map(p => p.words).sort((a, b) => a - b);
const q75 = wordList[Math.floor(wordList.length * 0.75)] ?? 0;
writeFileSync(join(OUT, "prompts.csv"), csv(prompts.map(p => { const l = local(emin(p.ts)); return { ts_utc: p.ts, date: l.date, time_local: l.hm, session: p.session, category: p.cat, chars: p.chars, words: p.words, gap_from_assistant_min: p.gapMin ?? "", prev_assistant_asked: p.prevAsked === undefined ? "" : p.prevAsked ? 1 : 0 }; }), ["ts_utc", "date", "time_local", "session", "category", "chars", "words", "gap_from_assistant_min", "prev_assistant_asked"]));
writeFileSync(join(OUT, "interrupts.csv"), csv(interrupts.map(i => ({ ts_utc: i.ts, date: local(emin(i.ts)).date, session: i.session, for_tool_use: i.forTool ? 1 : 0 })), ["ts_utc", "date", "session", "for_tool_use"]));
const gapRows = [], gapDay = {};
for (const p of prompts) {
  if (p.gapMin === undefined || p.gapMin < 15 || p.cat === "bash" || p.cat === "slash-builtin") continue;
  const d = local(emin(p.ts)).date; const g = gapDay[d] ??= { date: d, thinking_n: 0, thinking_min: 0, other_15_120_n: 0, other_15_120_min: 0, breaks_n: 0, breaks_min: 0 };
  let cls;
  if (p.gapMin > 120) { cls = "break"; g.breaks_n++; g.breaks_min += p.gapMin; }
  else if (p.words >= q75 || p.prevAsked) { cls = "thinking"; g.thinking_n++; g.thinking_min += p.gapMin; }
  else { cls = "other"; g.other_15_120_n++; g.other_15_120_min += p.gapMin; }
  gapRows.push({ ts_utc: p.ts, date: d, session: p.session, gap_min: p.gapMin, next_words: p.words, long_next: p.words >= q75 ? 1 : 0, prev_asked: p.prevAsked ? 1 : 0, class: cls });
}
writeFileSync(join(OUT, "gaps.csv"), csv(gapRows, ["ts_utc", "date", "session", "gap_min", "next_words", "long_next", "prev_asked", "class"]));
writeFileSync(join(OUT, "gaps_by_day.csv"), csv(Object.values(gapDay).sort((a, b) => a.date.localeCompare(b.date)), ["date", "thinking_n", "thinking_min", "other_15_120_n", "other_15_120_min", "breaks_n", "breaks_min"]));
writeFileSync(join(OUT, "reading_load_by_day.csv"), csv(Object.entries(readingByDay).sort().map(([d, v]) => ({ date: d, ...v })), ["date", "chars", "words", "blocks"]));

// ---------- timeline ----------
const conc = new Map(), mainConc = new Map();
for (const [id, ms] of fileMinutes) for (const m of ms) { conc.set(m, (conc.get(m) || 0) + 1); if (!subById.has(id)) mainConc.set(m, (mainConc.get(m) || 0) + 1); }
const humanMin = new Map();
for (const p of prompts) { const m = emin(p.ts); humanMin.set(m, (humanMin.get(m) || 0) + 1); }
const allMins = [...new Set([...conc.keys(), ...humanMin.keys()])].sort((a, b) => a - b);
writeFileSync(join(OUT, "minutes.csv"), csv(allMins.map(m => { const l = local(m); return { epoch_min: m, date: l.date, time_local: l.hm, agents_active: conc.get(m) || 0, main_sessions_active: mainConc.get(m) || 0, ai_events: aiEventsByMin.get(m) || 0, human_prompts: humanMin.get(m) || 0 }; }), ["epoch_min", "date", "time_local", "agents_active", "main_sessions_active", "ai_events", "human_prompts"]));
function sessionize(sorted, gap) { // returns [{start,end}] in epoch minutes
  const out = []; for (const m of sorted) { const l = out.at(-1); if (l && m - l.end <= gap) l.end = m; else out.push({ start: m, end: m }); } return out;
}
const day = {};
const D = d => day[d] ??= { date: d, dow: "", ai_union_min: 0, agent_min: 0, max_concurrent: 0, avg_concurrent: 0, human_prompt_min: 0, human_prompts: 0, ai_sess5_min: 0, ai_sess15_min: 0, ai_sess30_min: 0, human_sess5_min: 0, human_sess15_min: 0, human_sess30_min: 0, ai_9to5_min: 0, human_9to5_prompts: 0, weekday_9to5_ai: 0, weekday_9to5_human: 0 };
for (const [m, n] of conc) {
  const l = local(m); const r = D(l.date); r.dow = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"][l.dow];
  r.ai_union_min++; r.agent_min += n; r.max_concurrent = Math.max(r.max_concurrent, n);
  if (l.hour >= 9 && l.hour < 17) { r.ai_9to5_min++; if (l.dow < 5) r.weekday_9to5_ai = 1; }
}
for (const [m, n] of humanMin) {
  const l = local(m); const r = D(l.date); r.dow = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"][l.dow];
  r.human_prompt_min++; r.human_prompts += n;
  if (l.hour >= 9 && l.hour < 17) { r.human_9to5_prompts += n; if (l.dow < 5) r.weekday_9to5_human = 1; }
}
for (const g of [5, 15, 30]) {
  // each minute inside a session span counts on its own local day, so spans across midnight split
  for (const s of sessionize([...conc.keys()].sort((a, b) => a - b), g)) for (let m = s.start; m <= s.end; m++) D(local(m).date)[`ai_sess${g}_min`]++;
  for (const s of sessionize([...humanMin.keys()].sort((a, b) => a - b), g)) for (let m = s.start; m <= s.end; m++) D(local(m).date)[`human_sess${g}_min`]++;
}
for (const r of Object.values(day)) r.avg_concurrent = r.ai_union_min ? r.agent_min / r.ai_union_min : 0;
writeFileSync(join(OUT, "timeline_by_day.csv"), csv(Object.values(day).sort((a, b) => a.date.localeCompare(b.date)), Object.keys(D("0000-00-00")).filter(() => true)).replace(/\n0000-00-00[^\n]*/, ""));
const how = Array.from({ length: 7 * 24 }, (_, i) => ({ dow: ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"][Math.floor(i / 24)], hour: i % 24, ai_events: 0, ai_union_min: 0, agent_min: 0, human_prompts: 0 }));
for (const [m, n] of conc) { const l = local(m); const h = how[l.dow * 24 + l.hour]; h.ai_union_min++; h.agent_min += n; h.ai_events += aiEventsByMin.get(m) || 0; }
for (const [m, n] of humanMin) { const l = local(m); how[l.dow * 24 + l.hour].human_prompts += n; }
writeFileSync(join(OUT, "hour_of_week.csv"), csv(how, ["dow", "hour", "ai_events", "ai_union_min", "agent_min", "human_prompts"]));

// ---------- dedup check + stats for summary ----------
let multiRid = 0, ridMulti = 0;
for (const r of msgs.values()) if (r.rids.size > 1) multiRid++;
for (const s of ridToMsg.values()) if (s.size > 1) ridMulti++;
const stats = { files: { mains: mains.length, subs: subs.length }, total, distinctMessages: msgs.size, multiRid, ridMulti, crossFileDupLines, crossFileDupMsgs: crossFileDupMsgs.size, dedupSample, speedGeo, unpriced, q75, promptCats: prompts.reduce((a, p) => (a[p.cat] = (a[p.cat] || 0) + 1, a), {}), interrupts: interrupts.length };
writeFileSync(join(OUT, "stats.json"), JSON.stringify(stats, null, 1));
console.log(JSON.stringify(stats, null, 1));
