import fs from 'node:fs';
import { git, OUT, day, weekStart, category, csv, write, generatedSet } from './lib.mjs';
const commits = JSON.parse(fs.readFileSync(OUT + '/commits.json'));
const files = git(['ls-tree', '-r', '--name-only', '6138336c']).split('\n').filter(Boolean);
const gen = generatedSet();
const W = {};
const wk = (w) => (W[w] ??= { week: w, commits: 0, feat: 0, fix: 0, test_commits: 0, revert: 0, added_src_test: 0, added_go_swift_src: 0, added_go_swift_test: 0, surviving_src_test: 0, surviving_src: 0, surviving_test: 0 });
for (const c of commits) {
  const w = wk(c.week); w.commits++;
  if (c.type === 'feat') w.feat++; if (c.type === 'fix') w.fix++; if (c.type === 'test') w.test_commits++;
  w.revert += c.is_revert;
  const s = c['add_go-src'] + c['add_swift-src'], t = c['add_go-test'] + c['add_swift-test'];
  w.added_go_swift_src += s; w.added_go_swift_test += t; w.added_src_test += s + t;
}
let total = 0;
for (const p of files) {
  const cat = category(p, gen);
  if (!['go-src', 'go-test', 'swift-src', 'swift-test'].includes(cat)) continue;
  const out = git(['blame', '--line-porcelain', '6138336c', '--', p]);
  for (const m of out.matchAll(/^author-time (\d+)$/gm)) {
    const w = wk(weekStart(day(new Date(+m[1] * 1000)))); total++;
    w.surviving_src_test++; cat.endsWith('src') ? w.surviving_src++ : w.surviving_test++;
  }
}
const rows = Object.values(W).sort((a, b) => a.week.localeCompare(b.week));
for (const r of rows) r.survival_pct = r.added_src_test ? (100 * r.surviving_src_test / r.added_src_test).toFixed(1) : '';
write('line_survival_weekly.csv', csv(rows, Object.keys(rows[0])));
console.log(total); console.table(rows);
