import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import { git, REPO, OUT, day, weekStart, category, CATS, csv, write, generatedSet } from './lib.mjs';
const rows = fs.readFileSync(OUT + '/commits.csv', 'utf8').split('\n').slice(1).filter(Boolean);
// use parse via simple JSON? re-derive from git log instead
const log = git(['log', '--format=%H %cI %aI', '6138336c']).trim().split('\n').map((l) => l.split(' '));
// log is newest first. snapshot per week: newest commit whose author-date day <= Sunday; use first match walking newest->oldest
const first = weekStart('2026-10-03');
const weeks = []; for (let w = first; w <= '2026-11-02'; ) { weeks.push(w); const d = new Date(w + 'T12:00:00Z'); d.setUTCDate(d.getUTCDate() + 7); w = d.toISOString().slice(0, 10); }
const snaps = [];
for (const w of weeks) {
  const sun = new Date(w + 'T12:00:00Z'); sun.setUTCDate(sun.getUTCDate() + 6); const s = sun.toISOString().slice(0, 10);
  const c = log.find(([h, cd]) => day(cd) <= s); // newest commit landed by end of week
  if (c) snaps.push({ label: w + (s > '2026-11-02' ? ' (HEAD)' : ''), week: w, sha: c[0] });
}
snaps.push({ label: 'HEAD', week: 'HEAD', sha: git(['rev-parse', '6138336c']).trim() });
const gen = generatedSet();
const cache = new Map();
function stats(oid, path) {
  const k = oid; if (cache.has(k)) return cache.get(k);
  const r = spawnSync('git', ['-C', REPO, 'cat-file', 'blob', oid], { maxBuffer: 1 << 28 });
  const buf = r.stdout; let loc = 0; if (buf.includes(0)) { cache.set(k, null); return null; }
  const t = buf.toString('utf8'); loc = t.split('\n').length - (t.endsWith('\n') ? 1 : 0);
  const o = { loc, gofn: 0, gomethod: 0, gotype: 0, swtype: 0, swfunc: 0 };
  if (path.endsWith('.go')) {
    o.gofn = (t.match(/^func [A-Za-z_]/gm) || []).length;
    o.gomethod = (t.match(/^func \(/gm) || []).length;
    o.gotype = (t.match(/^\s*type [A-Za-z_]\w*/gm) || []).length;
  } else if (path.endsWith('.swift')) {
    o.swtype = (t.match(/^\s*(?:[a-z]+\s+)*(?:struct|class|enum|protocol|actor|extension)\s+[A-Za-z_]/gm) || []).length;
    o.swfunc = (t.match(/^\s*(?:@\w+\s+)*(?:[a-z]+\s+)*func\s+[A-Za-z_`]/gm) || []).length;
  }
  cache.set(k, o); return o;
}
const out = [];
for (const s of snaps) {
  const lines = git(['ls-tree', '-r', '-z', s.sha]).split('\0').filter(Boolean);
  const row = { snapshot: s.label, week: s.week, sha: s.sha.slice(0, 8), files_total: lines.length, go_packages: 0, go_funcs: 0, go_methods: 0, go_types: 0, swift_types: 0, swift_funcs: 0, spec_files: 0 };
  for (const k of CATS) { row['files_' + k] = 0; row['loc_' + k] = 0; }
  const pk = new Set();
  for (const l of lines) {
    const [meta, p] = l.split('\t'); const oid = meta.split(' ')[2];
    const cat = category(p, gen); row['files_' + cat]++;
    if (/\.(png|jpe?g|gif|ico|icns|woff2?|pdf|svg)$/i.test(p)) continue;
    if (p.startsWith('docs/spec/') && p.endsWith('.md')) row.spec_files++;
    const st = (cat === 'lockfiles') ? { loc: 0 } : stats(oid, p); // lockfile LOC not counted
    if (!st) continue;
    row['loc_' + cat] += st.loc;
    if (cat === 'go-src' || cat === 'go-test') { pk.add(p.replace(/\/[^/]*$/, '')); row.go_funcs += st.gofn; row.go_methods += st.gomethod; row.go_types += st.gotype; }
    if (cat === 'swift-src' || cat === 'swift-test') { row.swift_types += st.swtype; row.swift_funcs += st.swfunc; }
  }
  row.go_packages = pk.size; out.push(row);
  console.error(s.label, row.files_total);
}
const cols = Object.keys(out[0]);
write('weekly_snapshots.csv', csv(out, cols));
