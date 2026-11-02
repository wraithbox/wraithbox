import fs from 'node:fs';
import { OUT, CATS, csv, write } from './lib.mjs';
const commits = JSON.parse(fs.readFileSync(OUT + '/commits.json'));
const W = {};
for (const c of commits) { const w = (W[c.week] ??= { week: c.week, commits: 0, commits_with_ab: 0, commits_dependabot: 0 }); w.commits++; if (c.assisted_by) w.commits_with_ab++; if (/dependabot/.test(c.author)) w.commits_dependabot++; for (const k of CATS) { w['add_' + k] = (w['add_' + k] || 0) + c['add_' + k]; w['del_' + k] = (w['del_' + k] || 0) + c['del_' + k]; } }
const rows = Object.values(W).sort((a, b) => a.week.localeCompare(b.week));
write('commits_weekly.csv', csv(rows, Object.keys(rows[0])));
const T = {}; for (const r of rows) for (const k of Object.keys(r)) if (k !== 'week') T[k] = (T[k] || 0) + r[k];
console.log(T);
const md = (r) => `| ${r.week} | ${r.commits} | ${CATS.slice(0, 9).map((k) => r['add_' + k] + '/' + r['del_' + k]).join(' | ')} |`;
console.log(rows.map(md).join('\n'));
const s = fs.readFileSync(OUT + '/weekly_snapshots.csv', 'utf8').split('\n'); const h = s[0].split(',');
const last = s[s.length - 2].split(','); const o = {}; h.forEach((k, i) => (o[k] = last[i])); console.log(o);
const ma = fs.readFileSync(OUT + '/maintainer_actions_daily.csv', 'utf8').trim().split('\n'); const mh = ma[0].split(',');
const MW = {}; for (const l of ma.slice(1)) { const r = l.split(','); const d = r[0]; const { weekStart } = await import('./lib.mjs'); const w = weekStart(d); const x = (MW[w] ??= {}); mh.slice(1).forEach((k, i) => (x[k] = (x[k] || 0) + +r[i + 1])); }
console.table(MW);
