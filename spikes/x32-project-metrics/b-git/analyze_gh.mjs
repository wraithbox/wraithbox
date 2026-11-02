import fs from 'node:fs';
import { RAW, OUT, day, weekStart, csv, write } from './lib.mjs';
const rd = (re, key) => fs.readdirSync(RAW).filter((f) => re.test(f)).sort().flatMap((f) => JSON.parse(fs.readFileSync(`${RAW}/${f}`)).data.repository[key].nodes);
const issues = rd(/^issues_/, 'issues'), prs = rd(/^prs_/, 'pullRequests');
const runs = fs.readdirSync(RAW).filter((f) => /^runs_/.test(f)).sort().flatMap((f) => JSON.parse(fs.readFileSync(`${RAW}/${f}`)).workflow_runs);
const wkd = (iso) => weekStart(day(iso));
const hasAB = (b) => /Assisted-by/i.test(b || '') ? 1 : 0;
const isBot = (l) => /dependabot|\[bot\]/i.test(l || '');
const logins = {}; const seen = (kind, l) => { const k = kind + ':' + (l ?? '(deleted)'); logins[k] = (logins[k] || 0) + 1; };

// ---- comments / reviews
function verdict(body) {
  const ms = [...(body || '').matchAll(/^[\s>*#_]*Verdict\**\s*:\**\s*(.*)$/gim)];
  if (!ms.length) return { v: '', mf: '' };
  const t = ms[ms.length - 1][1].toLowerCase();
  const v = t.startsWith('approve') ? 'approve' : /^(needs changes|request ?changes|changes)/.test(t) ? 'needs-changes' : 'other';
  let mf = 0; const lines = body.split('\n'); let inMF = false;
  for (const l of lines) {
    if (/^#{1,4}\s*must-?fix\b/i.test(l)) { inMF = true; continue; }
    if (/^#{1,4}\s/.test(l)) inMF = false;
    if (inMF && /^\s*(\d+\.|[-*])\s/.test(l)) mf++;
    else if (!inMF && /^\s*(\d+\.|[-*])\s*\**must-?fix\b/i.test(l) && !/fixed/i.test(l)) mf++;
  }
  return { v, mf };
}
const cm = [];
for (const [kind, arr] of [['issue', issues], ['pr', prs]]) for (const n of arr) {
  for (const c of n.comments.nodes) cm.push({ parent_kind: kind, parent: n.number, type: 'comment', author: c.author?.login, state: '', created: c.createdAt, day: day(c.createdAt), len: c.body.length, assisted_by: hasAB(c.body), ...(() => { const x = verdict(c.body); return { verdict: x.v, must_fix_est: x.mf }; })() });
  for (const r of n.reviews?.nodes ?? []) cm.push({ parent_kind: kind, parent: n.number, type: 'review', author: r.author?.login, state: r.state, created: r.createdAt, day: day(r.createdAt), len: (r.body || '').length, assisted_by: hasAB(r.body), inline_comments: r.comments.totalCount, ...(() => { const x = verdict(r.body); return { verdict: x.v, must_fix_est: x.mf }; })() });
}
for (const c of cm) seen(c.type, c.author);
write('comments_and_reviews.csv', csv(cm, ['parent_kind', 'parent', 'type', 'author', 'state', 'created', 'day', 'len', 'assisted_by', 'inline_comments', 'verdict', 'must_fix_est']));

// ---- issues
const irows = issues.map((i) => {
  const ce = i.timelineItems.nodes[0]; const closer = ce?.closer;
  return { number: i.number, title: i.title, created: i.createdAt, closed: i.closedAt || '', state: i.state, state_reason: i.stateReason || '', author: i.author?.login, labels: i.labels.nodes.map((l) => l.name).join('|'), milestone: i.milestone?.title || '', assignees: i.assignees.nodes.map((a) => a.login).join('|'), comments: i.comments.totalCount,
    closed_by_actor: ce?.actor?.login || '', closed_via: !ce ? '' : closer?.__typename === 'PullRequest' ? 'pr' : closer?.__typename === 'Commit' ? 'commit' : 'manual', closer_pr: closer?.number || '' };
});
for (const r of irows) seen('issue_author', r.author);
write('issues.csv', csv(irows, Object.keys(irows[0])));

// ---- PRs
const prows = prs.map((p) => {
  const cl = new Set(p.closingIssuesReferences.nodes.map((n) => n.number));
  for (const m of (p.body || '').matchAll(/\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s*:?\s+(?:wraithbox\/wraithbox)?#(\d+)/gi)) cl.add(+m[1]);
  const vs = cm.filter((c) => c.parent_kind === 'pr' && c.parent === p.number && c.verdict);
  return { number: p.number, title: p.title, state: p.state, author: p.author?.login, created: p.createdAt, merged: p.mergedAt || '', closed: p.closedAt || '', merged_by: p.mergedBy?.login || '', additions: p.additions, deletions: p.deletions, changed_files: p.changedFiles, commits: p.commits.totalCount, labels: p.labels.nodes.map((l) => l.name).join('|'), closes_issues: [...cl].sort((a, b) => a - b).join('|'),
    reviews: p.reviews.totalCount, review_inline_comments: p.reviews.nodes.reduce((s, r) => s + r.comments.totalCount, 0), comments: p.comments.nodes.length, review_rounds_with_verdict: vs.length, verdicts: vs.map((x) => x.verdict).join('|'), must_fix_est_total: vs.reduce((s, x) => s + x.must_fix_est, 0),
    hours_to_merge: p.mergedAt ? ((new Date(p.mergedAt) - new Date(p.createdAt)) / 36e5).toFixed(2) : '' };
});
for (const r of prows) { seen('pr_author', r.author); if (r.merged_by) seen('merged_by', r.merged_by); }
write('prs.csv', csv(prows, Object.keys(prows[0])));

// ---- milestone burn-up
const days = []; for (let d = new Date('2026-10-03T12:00:00Z'); d <= new Date('2026-11-02T12:00:00Z'); d.setUTCDate(d.getUTCDate() + 1)) days.push(d.toISOString().slice(0, 10));
const ms = [...new Set(irows.map((i) => i.milestone || '(none)'))].sort();
const bu = [];
for (const m of ms) for (const d of days) {
  const mine = irows.filter((i) => (i.milestone || '(none)') === m);
  const created = mine.filter((i) => day(i.created) <= d).length, closed = mine.filter((i) => i.closed && day(i.closed) <= d).length;
  bu.push({ milestone: m, day: d, issues_created_cum: created, issues_closed_cum: closed, open: created - closed });
}
write('milestone_burnup_daily.csv', csv(bu, Object.keys(bu[0])));

// ---- CI
const rr = runs.map((r) => ({ id: r.id, workflow: r.name, event: r.event, head_sha: r.head_sha.slice(0, 8), head_branch: r.head_branch, conclusion: r.conclusion || r.status, run_attempt: r.run_attempt, created: r.created_at, run_started: r.run_started_at, updated: r.updated_at, duration_s: Math.round((new Date(r.updated_at) - new Date(r.run_started_at || r.created_at)) / 1000), week: wkd(r.created_at), day: day(r.created_at), actor: r.actor?.login }));
const grp = {}; for (const r of rr) (grp[r.workflow + '|' + r.head_sha] ??= []).push(r);
const flakyRetry = new Set(rr.filter((r) => r.run_attempt > 1).map((r) => r.id));
const flakyFS = new Set();
for (const g of Object.values(grp)) { g.sort((a, b) => a.created.localeCompare(b.created)); const fi = g.findIndex((r) => r.conclusion === 'failure'); if (fi >= 0 && g.slice(fi + 1).some((r) => r.conclusion === 'success')) g.filter((r) => r.conclusion === 'failure').forEach((r) => flakyFS.add(r.id)); }
for (const r of rr) { r.flaky_rerun = flakyRetry.has(r.id) ? 1 : 0; r.flaky_fail_then_pass = flakyFS.has(r.id) ? 1 : 0; }
write('ci_runs.csv', csv(rr, Object.keys(rr[0])));
const cw = {};
for (const r of rr) { const w = (cw[r.week] ??= { week: r.week, runs: 0, success: 0, failure: 0, cancelled: 0, other: 0, minutes: 0, rerun_runs: 0, fail_then_pass_runs: 0 }); w.runs++; if (r.conclusion === 'success') w.success++; else if (r.conclusion === 'failure') w.failure++; else if (r.conclusion === 'cancelled') w.cancelled++; else w.other++; if (r.conclusion === "success" || r.conclusion === "failure") w.minutes += r.duration_s / 60; w.rerun_runs += r.flaky_rerun; w.fail_then_pass_runs += r.flaky_fail_then_pass; }
const cwr = Object.values(cw).sort((a, b) => a.week.localeCompare(b.week)).map((w) => ({ ...w, minutes: w.minutes.toFixed(0), failure_rate_pct: (100 * w.failure / Math.max(1, w.success + w.failure)).toFixed(1), flaky_rate_pct: (100 * (w.rerun_runs + w.fail_then_pass_runs) / Math.max(1, w.runs)).toFixed(1) }));
write('ci_weekly.csv', csv(cwr, Object.keys(cwr[0])));
write('ci_flaky_runs.csv', csv(rr.filter((r) => r.flaky_rerun || r.flaky_fail_then_pass), Object.keys(rr[0])));
const byWf = {}; for (const r of rr) { const w = (byWf[r.workflow] ??= { workflow: r.workflow, runs: 0, failure: 0, success: 0, cancelled: 0 }); w.runs++; if (r.conclusion in w) w[r.conclusion]++; }
write('ci_by_workflow.csv', csv(Object.values(byWf), ['workflow', 'runs', 'success', 'failure', 'cancelled']));

// ---- maintainer actions per day
const commits = JSON.parse(fs.readFileSync(OUT + '/commits.json'));
const md = {}; for (const d of days) md[d] = { day: d, merges: 0, human_reviews_approved_or_changes_requested: 0, reviews_no_assisted_by: 0, comments_no_assisted_by: 0, issues_closed_manual: 0, issues_closed_by_pr: 0, issues_closed_by_commit: 0, commits_no_assisted_by_nonbot: 0, commits_total: 0 };
const M = (d) => md[d];
for (const p of prows) if (p.merged && M(day(p.merged))) M(day(p.merged)).merges++;
for (const c of cm) { const m = M(c.day); if (!m || isBot(c.author)) continue; if (c.type === 'review' && !c.assisted_by) { m.reviews_no_assisted_by++; if (['APPROVED', 'CHANGES_REQUESTED'].includes(c.state)) m.human_reviews_approved_or_changes_requested++; } if (c.type === 'comment' && !c.assisted_by) m.comments_no_assisted_by++; }
for (const i of irows) if (i.closed && M(day(i.closed))) { const m = M(day(i.closed)); if (i.closed_via === 'manual' || i.closed_via === '') m.issues_closed_manual++; else if (i.closed_via === 'pr') m.issues_closed_by_pr++; else m.issues_closed_by_commit++; }
for (const c of commits) { const m = M(c.day); if (!m) continue; m.commits_total++; if (!c.assisted_by && !isBot(c.author)) m.commits_no_assisted_by_nonbot++; }
const mdr = Object.values(md); write('maintainer_actions_daily.csv', csv(mdr, Object.keys(mdr[0])));
write('logins.json', logins);

// ---- weekly github rollup
const wk = {}; const W = (w) => (wk[w] ??= { week: w, issues_created: 0, issues_closed: 0, issues_closed_not_planned: 0, prs_created: 0, prs_merged: 0, pr_median_hours_to_merge: '', pr_additions: 0, pr_deletions: 0, review_verdicts_approve: 0, review_verdicts_needs_changes: 0, must_fix_est: 0, merges: 0, comments_no_ab: 0, reviews_no_ab: 0, issue_closed_manual: 0, commits_no_ab_nonbot: 0, _h: [] });
for (const i of irows) { W(wkd(i.created)).issues_created++; if (i.closed) { const w = W(wkd(i.closed)); w.issues_closed++; if (i.state_reason === 'NOT_PLANNED') w.issues_closed_not_planned++; if (i.closed_via !== 'pr' && i.closed_via !== 'commit') w.issue_closed_manual++; } }
for (const p of prows) { W(wkd(p.created)).prs_created++; if (p.merged) { const w = W(wkd(p.merged)); w.prs_merged++; w.merges++; w.pr_additions += p.additions; w.pr_deletions += p.deletions; w._h.push(+p.hours_to_merge); } }
for (const c of cm) { const w = W(wkd(c.created)); if (c.verdict === 'approve') w.review_verdicts_approve++; if (c.verdict === 'needs-changes') { w.review_verdicts_needs_changes++; w.must_fix_est += c.must_fix_est; } if (!isBot(c.author) && !c.assisted_by) { c.type === 'comment' ? w.comments_no_ab++ : w.reviews_no_ab++; } }
for (const c of commits) if (!c.assisted_by && !isBot(c.author)) W(c.week).commits_no_ab_nonbot++;
const wr = Object.values(wk).sort((a, b) => a.week.localeCompare(b.week)).filter((w) => w.week >= '2026-09-28').map((w) => { const h = w._h.sort((a, b) => a - b); w.pr_median_hours_to_merge = h.length ? h[Math.floor(h.length / 2)].toFixed(2) : ''; delete w._h; return w; });
write('github_weekly.csv', csv(wr, Object.keys(wr[0])));
console.log(logins); console.table(cwr); console.table(wr.map(({ week, issues_created, issues_closed, prs_merged, pr_median_hours_to_merge, review_verdicts_approve, review_verdicts_needs_changes, must_fix_est, comments_no_ab, issue_closed_manual }) => ({ week, issues_created, issues_closed, prs_merged, pr_median_hours_to_merge, review_verdicts_approve, review_verdicts_needs_changes, must_fix_est, comments_no_ab, issue_closed_manual })));
