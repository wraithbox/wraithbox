// Calibration: closed V1-M2/M3/M7 issues closed by merged PRs.
// Usage: bun calibrate.mjs <datadir>   (reads raw/issues.json, raw/prs.json; writes calibration.csv)
import { readFileSync, writeFileSync } from "node:fs";
import { pkgOfIssue } from "./plan.mjs";
const d = process.argv[2];
const I = JSON.parse(readFileSync(`${d}/raw/issues.json`));
const P = JSON.parse(readFileSync(`${d}/raw/prs.json`));
const prBy = Object.fromEntries(P.map((p) => [p.number, p]));

const GEN = [/internal\/gen\//, /WraithBoxRPC\//, /\.pb\.(go|swift)$/, /\.grpc\.swift$/, /(go\.sum|bun\.lock|Package\.resolved|mise\.lock)$/, /src\/data\/.*\.json$/];
export function classify(path) {
  if (GEN.some((r) => r.test(path))) return "gen";
  if (/_test\.go$|\/Tests\/|testdata\//.test(path)) return "test";
  if (/\.(go|swift|c|h|m|proto|sh|mjs|ts)$/.test(path) || /\.sb$/.test(path)) return "src";
  if (/\.(md|mdx)$/.test(path)) return "docs";
  return "other";
}
export function prLines(p) {
  const r = { src: 0, test: 0, docs: 0, gen: 0, other: 0 };
  for (const f of p.files.nodes) r[classify(f.path)] += f.additions + f.deletions;
  return r;
}
const ams = (t) => new Date(t).toLocaleDateString("sv-SE", { timeZone: "Europe/Amsterdam" });
const hours = (a, b) => (new Date(b) - new Date(a)) / 3.6e6;
function distinctDays(a, b) {
  const s = new Set();
  for (let t = new Date(a); t <= new Date(b); t = new Date(+t + 3.6e6)) s.add(ams(t));
  s.add(ams(b));
  return s.size;
}
function lane(labels, pkg) {
  const vm = labels.includes("needs-vm"), host = labels.includes("needs-host");
  if (vm && host) return "vm+host";
  if (vm) return "vm";
  if (host) return "host";
  if (pkg?.lane?.startsWith("mac")) return "mac";
  return "any";
}
function kind(i, labels, pkg) {
  if (labels.includes("spike")) return "spike";
  if (pkg) return "package";
  if (labels.includes("bug")) return "bug";
  if (labels.includes("chore")) return "chore";
  if (labels.includes("documentation") || labels.includes("area:spec") && !labels.includes("area:go")) return "doc";
  return "followup";
}
function reviewRounds(p) {
  let max = 0, code = 0, sec = 0;
  for (const r of p.reviews.nodes) {
    const m = r.body.match(/^(Code|Security) review, round (\d+)/m);
    if (m) { max = Math.max(max, +m[2]); if (m[1] === "Code") code++; else sec++; }
  }
  return { max, code, sec };
}
const rows = [];
const ms = /V1-M2|V1-M3|V1-M7/;
for (const i of I) {
  if (i.state !== "CLOSED" || !ms.test(i.milestone?.title ?? "")) continue;
  const prs = i.closedByPullRequestsReferences.nodes.filter((x) => x.merged).map((x) => prBy[x.number]).filter(Boolean);
  if (!prs.length) continue;
  const labels = i.labels.nodes.map((l) => l.name);
  const pkg = pkgOfIssue(i.number);
  const claims = [
    ...i.timelineItems.nodes.filter((t) => t.__typename === "AssignedEvent").map((t) => t.createdAt),
    ...i.comments.nodes.filter((c) => /^Claimed/m.test(c.body)).map((c) => c.createdAt),
  ].sort();
  const claim = claims[0] ?? null;
  const lines = { src: 0, test: 0, docs: 0, gen: 0, other: 0 };
  let nIssues = 1, rr = { max: 0, code: 0, sec: 0 }, prOpen = prs[0].createdAt, merged = prs[0].mergedAt;
  for (const p of prs) {
    const share = p.closingIssuesReferences.nodes.length || 1;
    nIssues = Math.max(nIssues, share);
    const l = prLines(p);
    for (const k in l) lines[k] += Math.round(l[k] / share);
    const r = reviewRounds(p);
    rr = { max: Math.max(rr.max, r.max), code: rr.code + r.code, sec: rr.sec + r.sec };
    if (p.createdAt < prOpen) prOpen = p.createdAt;
    if (p.mergedAt > merged) merged = p.mergedAt;
  }
  const start = claim && claim < prOpen ? claim : prOpen;
  rows.push({
    issue: i.number, milestone: i.milestone.title.slice(0, 5), package: pkg?.id ?? "", plan_lane: (pkg?.lane ?? "").replace(/,/g, "/"),
    lane: lane(labels, pkg), kind: kind(i, labels, pkg), prs: prs.map((p) => p.number).join(" "), issues_per_pr: nIssues,
    code_lines: lines.src + lines.test, src_lines: lines.src, test_lines: lines.test, docs_lines: lines.docs, gen_lines: lines.gen, other_lines: lines.other,
    plan_size_est: pkg ? "300-1500" : "",
    created: i.createdAt, claim: claim ?? "", pr_open: prOpen, merged, closed: i.closedAt,
    claim_to_merge_h: claim ? hours(claim, merged).toFixed(1) : "",
    start_to_merge_h: hours(start, merged).toFixed(1),
    start_to_merge_days: distinctDays(start, merged),
    create_to_close_d: (hours(i.createdAt, i.closedAt) / 24).toFixed(2),
    review_rounds: rr.max, code_reviews: rr.code, sec_reviews: rr.sec,
    title: i.title.replace(/[",]/g, " ").slice(0, 80),
  });
}
const cols = Object.keys(rows[0]);
writeFileSync(`${d}/calibration.csv`, [cols.join(","), ...rows.map((r) => cols.map((c) => r[c]).join(","))].join("\n") + "\n");
console.error("rows", rows.length);

// Summary statistics
const q = (a, p) => { const s = [...a].sort((x, y) => x - y); if (!s.length) return NaN; const k = (s.length - 1) * p; const f = Math.floor(k); return s[f] + (s[Math.min(f + 1, s.length - 1)] - s[f]) * (k - f); };
const stat = (name, a) => console.log(name.padEnd(36), "n=" + a.length, "p10=" + q(a, .1).toFixed(1), "p50=" + q(a, .5).toFixed(1), "p90=" + q(a, .9).toFixed(1), "mean=" + (a.reduce((x, y) => x + y, 0) / a.length).toFixed(1), "max=" + Math.max(...a).toFixed(1));
const groups = {};
for (const r of rows) {
  const lk = r.lane.startsWith("vm") ? "vm" : r.lane === "host" ? "host" : "nonvm";
  for (const g of [`${r.kind}`, `${r.kind}/${lk}`]) (groups[g] ??= []).push(r);
}
for (const [g, rs] of Object.entries(groups).sort()) {
  stat(`code_lines ${g}`, rs.map((r) => r.code_lines));
  stat(`start_to_merge_h ${g}`, rs.map((r) => +r.start_to_merge_h));
  stat(`start_to_merge_days ${g}`, rs.map((r) => r.start_to_merge_days));
  stat(`review_rounds ${g}`, rs.map((r) => r.review_rounds));
}
const pk = rows.filter((r) => r.kind === "package");
console.log("packages within 300-1500 code lines:", pk.filter((r) => r.code_lines >= 300 && r.code_lines <= 1500).length, "/", pk.length, " >1500:", pk.filter((r) => r.code_lines > 1500).length, " >3000:", pk.filter((r) => r.code_lines > 3000).length);
console.log("src only: >1500:", pk.filter((r) => r.src_lines > 1500).length, " p50 src", q(pk.map((r) => r.src_lines), .5), " p50 test", q(pk.map((r) => r.test_lines), .5));
// per plan package (sum issues)
const byPkg = {};
for (const r of pk) { const b = (byPkg[r.package] ??= { code: 0, src: 0, n: 0, lanes: new Set(), start: r.claim || r.pr_open, end: r.merged }); b.code += r.code_lines; b.src += r.src_lines; b.n++; b.lanes.add(r.lane); const s = r.claim || r.pr_open; if (s < b.start) b.start = s; if (r.merged > b.end) b.end = r.merged; }
console.log("\nper plan package: id issues code src lanes span_days");
for (const [k, b] of Object.entries(byPkg).sort()) console.log(k, b.n, b.code, b.src, [...b.lanes].join("/"), (hours(b.start, b.end) / 24).toFixed(1));
// concurrency: packages in flight per hour (start->merge), all PR-closed issues
const iv = rows.map((r) => [new Date(r.claim && r.claim < r.pr_open ? r.claim : r.pr_open), new Date(r.merged), r.lane.startsWith("vm")]);
const t0 = Math.min(...iv.map((x) => +x[0])), t1 = Math.max(...iv.map((x) => +x[1]));
const conc = [], concVm = [];
for (let t = t0; t <= t1; t += 3.6e6) { const a = iv.filter((x) => +x[0] <= t && t < +x[1]); if (a.length) { conc.push(a.length); concVm.push(a.filter((x) => x[2]).length); } }
stat("in-flight (hours with >=1 open)", conc);
stat("in-flight vm (same hours)", concVm);
console.log("hours with >1 vm item in flight:", concVm.filter((x) => x > 1).length, "of", concVm.filter((x) => x > 0).length);
