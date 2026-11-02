// Scope growth per V1 milestone per day, gate waits, merge waits.
// Usage: bun scope.mjs <datadir>  -> scope.csv, gates.csv, prints summaries
import { readFileSync, writeFileSync } from "node:fs";
const d = process.argv[2];
const I = JSON.parse(readFileSync(`${d}/raw/issues.json`));
const P = JSON.parse(readFileSync(`${d}/raw/prs.json`));
const ams = (t) => new Date(t).toLocaleDateString("sv-SE", { timeZone: "Europe/Amsterdam" });
const hours = (a, b) => (new Date(b) - new Date(a)) / 3.6e6;
const V1 = (m) => /^V1-M/.test(m ?? "");

// --- scope: replay milestone events
const ev = []; // {day, ms, kind: add|remove|close|reopen, issue}
const moved = [];
for (const i of I) {
  if (i.labels.nodes.some((l) => l.name === "run")) continue;
  let cur = null;
  const tl = i.timelineItems.nodes.filter((t) => /Milestoned|Demilestoned|Closed|Reopened/.test(t.__typename));
  for (const t of tl) {
    if (t.__typename === "MilestonedEvent") {
      if (cur && cur !== t.milestoneTitle) ev.push({ t: t.createdAt, ms: cur, kind: "move_out", issue: i.number });
      if (V1(cur) && t.milestoneTitle === "post-V1") moved.push({ issue: i.number, from: cur, at: t.createdAt });
      cur = t.milestoneTitle;
      ev.push({ t: t.createdAt, ms: cur, kind: hours(i.createdAt, t.createdAt) < 0.05 ? "add_new" : "add_existing", issue: i.number });
    } else if (t.__typename === "DemilestonedEvent") {
      if (cur === t.milestoneTitle) { ev.push({ t: t.createdAt, ms: cur, kind: "move_out", issue: i.number }); cur = null; }
    } else if (t.__typename === "ClosedEvent" && cur) ev.push({ t: t.createdAt, ms: cur, kind: "close", issue: i.number, reason: i.stateReason });
    else if (t.__typename === "ReopenedEvent" && cur) ev.push({ t: t.createdAt, ms: cur, kind: "reopen", issue: i.number });
  }
}
const days = [...new Set(ev.map((e) => ams(e.t)))].sort();
const mss = [...new Set(ev.map((e) => e.ms))].filter((m) => V1(m) || m === "post-V1").sort();
const rows = [];
for (const ms of mss) {
  let open = 0;
  for (const day of days) {
    const es = ev.filter((e) => e.ms === ms && ams(e.t) === day);
    const c = (k) => es.filter((e) => e.kind === k).length;
    const r = { day, milestone: ms, added_new: c("add_new"), added_existing: c("add_existing"), moved_out: c("move_out"), closed: c("close"), reopened: c("reopen") };
    open += r.added_new + r.added_existing - r.moved_out - r.closed + r.reopened;
    r.open_end_of_day = open;
    if (es.length) rows.push(r);
  }
}
const cols = Object.keys(rows[0]);
writeFileSync(`${d}/scope.csv`, [cols.join(","), ...rows.map((r) => cols.map((c) => r[c]).join(","))].join("\n") + "\n");
console.log("moved V1 -> post-V1:", moved.length, moved.map((m) => `#${m.issue}(${m.from.slice(3, 5)} ${ams(m.at)})`).join(" "));

// new V1 issues per day after 2026-10-16 (M2 start), excluding package filing days, by type
const newV1 = I.filter((i) => !i.labels.nodes.some((l) => l.name === "run")).filter((i) => {
  const first = i.timelineItems.nodes.find((t) => t.__typename === "MilestonedEvent");
  return first && V1(first.milestoneTitle) && i.createdAt >= "2026-10-16T17:00:00Z";
});
const byDay = {};
for (const i of newV1) {
  const day = ams(i.createdAt);
  const ls = i.labels.nodes.map((l) => l.name);
  const t = ls.includes("bug") ? "bug" : ls.includes("spike") ? "spike" : i.parent ? "followup" : "other";
  (byDay[day] ??= { bug: 0, spike: 0, followup: 0, other: 0, nums: [] })[t]++;
  byDay[day].nums.push(i.number);
}
console.log("\nnew issues into V1 milestones created after 2026-10-16 17:00Z, by day (bug/spike/followup-with-parent/other):");
for (const [k, v] of Object.entries(byDay).sort()) console.log(k, v.bug, v.spike, v.followup, v.other, v.nums.length > 12 ? `${v.nums[0]}..${v.nums.at(-1)}` : v.nums.join(" "));
const nonBulk = Object.values(byDay).filter((v) => v.nums.length < 15);
console.log("days:", Object.keys(byDay).length, "total new:", newV1.length, "excluding bulk filing days (>=15):", nonBulk.reduce((a, v) => a + v.nums.length, 0));
const fu = newV1.filter((i) => i.labels.nodes.some((l) => l.name === "bug") || i.parent);
console.log("bugs + follow-ups (have parent) into V1:", fu.length, "of which still open:", fu.filter((i) => i.state === "OPEN").length);

// --- gate waits: needs-host and ready-for-human label applied -> removed or closed
const g = [];
for (const i of I) {
  for (const lab of ["needs-host", "ready-for-human", "needs-vm"]) {
    const tl = i.timelineItems.nodes;
    const on = tl.find((t) => t.__typename === "LabeledEvent" && t.label?.name === lab);
    if (!on) continue;
    const off = tl.find((t) => t.__typename === "UnlabeledEvent" && t.label?.name === lab && t.createdAt > on.createdAt);
    const end = off?.createdAt ?? i.closedAt;
    g.push({ issue: i.number, label: lab, milestone: i.milestone?.title ?? "", applied: on.createdAt, resolved: end ?? "", how: off ? "unlabeled" : end ? "closed" : "open", wait_h: (hours(on.createdAt, end ?? "2026-11-02T08:00:00Z")).toFixed(1) });
  }
}
// merge waits: last approve verdict -> merged
const mw = [];
for (const p of P) {
  const appr = p.reviews.nodes.filter((r) => /Verdict: approve/.test(r.body)).map((r) => r.submittedAt).sort();
  const rc = p.comments.nodes.filter((c) => /^re-checked:/.test(c.body)).map((c) => c.createdAt).sort();
  const last = [appr.at(-1), rc.at(-1)].filter(Boolean).sort().at(-1);
  if (!last) continue;
  const iss = p.closingIssuesReferences.nodes.map((n) => I.find((i) => i.number === n.number)).filter(Boolean);
  const labs = iss.flatMap((i) => i.labels.nodes.map((l) => l.name));
  const spec = p.files.nodes.some((f) => /^docs\/(spec|briefs|spikes)|\.github|\.mise|\.claude|go\.mod|Package\.swift/.test(f.path));
  const gated = labs.some((l) => /needs-vm|needs-host|comp:wb-vmd/.test(l)) || spec;
  mw.push({ pr: p.number, gated, wait_h: hours(last, p.mergedAt), merged: p.mergedAt });
}
const cols2 = Object.keys(g[0]);
writeFileSync(`${d}/gates.csv`, [cols2.join(","), ...g.map((r) => cols2.map((c) => r[c]).join(","))].join("\n") + "\n");
writeFileSync(`${d}/merge-waits.csv`, ["pr,gated,wait_h,merged", ...mw.map((r) => [r.pr, r.gated, r.wait_h.toFixed(2), r.merged].join(","))].join("\n") + "\n");
const q = (a, p) => { const s = [...a].sort((x, y) => x - y); if (!s.length) return NaN; const k = (s.length - 1) * p; const f = Math.floor(k); return s[f] + (s[Math.min(f + 1, s.length - 1)] - s[f]) * (k - f); };
const st = (n, a) => console.log(n.padEnd(40), "n=" + a.length, "p50=" + q(a, .5).toFixed(1), "p75=" + q(a, .75).toFixed(1), "p90=" + q(a, .9).toFixed(1), "max=" + Math.max(...a).toFixed(1));
console.log();
for (const lab of ["needs-host", "ready-for-human", "needs-vm"]) {
  st(`${lab} applied->resolved h (resolved)`, g.filter((x) => x.label === lab && x.how !== "open").map((x) => +x.wait_h));
  console.log("   still open:", g.filter((x) => x.label === lab && x.how === "open").map((x) => `#${x.issue}:${(+x.wait_h / 24).toFixed(0)}d`).join(" "));
}
const since = (x) => x.merged >= "2026-10-16";
st("approve->merge h, gated PRs since 10-16", mw.filter((x) => x.gated && since(x)).map((x) => x.wait_h));
st("approve->merge h, other PRs since 10-16", mw.filter((x) => !x.gated && since(x)).map((x) => x.wait_h));
