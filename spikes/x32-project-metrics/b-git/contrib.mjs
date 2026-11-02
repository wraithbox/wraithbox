import fs from 'node:fs';
import { RAW, OUT, weekStart, csv, write } from './lib.mjs';
const cc = JSON.parse(fs.readFileSync(RAW + '/contributions.json')).data.user.contributionsCollection;
const la = new Intl.DateTimeFormat('en-CA', { timeZone: 'America/Los_Angeles', year: 'numeric', month: '2-digit', day: '2-digit' });
const ld = (iso) => la.format(new Date(iso));
const D = {};
for (const w of cc.contributionCalendar.weeks) for (const d of w.contributionDays) D[d.date] = { day: d.date, total: d.contributionCount, wb_commits: 0, wb_issues: 0, wb_prs: 0, wb_reviews: 0 };
const isWB = (n) => n.startsWith('wraithbox/');
for (const r of cc.commitContributionsByRepository) if (isWB(r.repository.nameWithOwner)) for (const n of r.contributions.nodes) { const d = n.occurredAt.slice(0, 10); if (D[d]) D[d].wb_commits += n.commitCount; }
// issues/PRs/reviews: the API truncates at 100 nodes per repo, so use full repo data (author lsimons) instead
const rd = (re, key) => fs.readdirSync(RAW).filter((f) => re.test(f)).flatMap((f) => JSON.parse(fs.readFileSync(`${RAW}/${f}`)).data.repository[key].nodes);
for (const i of rd(/^issues_/, 'issues')) if (i.author?.login === 'lsimons' && D[ld(i.createdAt)]) D[ld(i.createdAt)].wb_issues++;
for (const p of rd(/^prs_/, 'pullRequests')) { if (p.author?.login === 'lsimons' && D[ld(p.createdAt)]) D[ld(p.createdAt)].wb_prs++; for (const r of p.reviews.nodes) if (r.author?.login === 'lsimons' && D[ld(r.createdAt)]) D[ld(r.createdAt)].wb_reviews++; }
const rows = Object.values(D).sort((a, b) => a.day.localeCompare(b.day)).map((r) => { const wb = r.wb_commits + r.wb_issues + r.wb_prs + r.wb_reviews; const wbc = Math.min(wb, r.total); return { ...r, wraithbox: wbc, wraithbox_raw_sum: wb, other: r.total - wbc, wraithbox_share_pct: r.total ? (100 * wbc / r.total).toFixed(1) : '' }; });
write('contributions_daily.csv', csv(rows, Object.keys(rows[0])));
const W = {}; for (const r of rows) { const w = (W[weekStart(r.day)] ??= { week: weekStart(r.day), total: 0, wraithbox: 0, other: 0 }); w.total += r.total; w.wraithbox += r.wraithbox; w.other += r.other; }
const wr = Object.values(W).map((w) => ({ ...w, wraithbox_share_pct: w.total ? (100 * w.wraithbox / w.total).toFixed(1) : '' }));
write('contributions_weekly.csv', csv(wr, Object.keys(wr[0])));
console.table(wr);
