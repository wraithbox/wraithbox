import { git, day, weekOf, category, CATS, csv, write } from './lib.mjs';
const SEP = '\x01', F = '\x02', E = '\x03';
const raw = git(['log', '-M', '--numstat', `--format=${SEP}%H${F}%aI${F}%cI${F}%an${F}%P${F}%s${F}%b${E}`, 'HEAD'.replace('HEAD','6138336c')]);
const commits = [];
for (const chunk of raw.split(SEP).slice(1)) {
  const [head, rest] = chunk.split(E);
  const [sha, ad, cd, an, parents, subject, body] = head.split(F);
  const m = /^(\w+)(?:\(([^)]*)\))?(!)?:/.exec(subject);
  const ab = /^Assisted-by:\s*(.+)$/mi.exec(body);
  const c = { sha: sha.slice(0, 8), author_date: ad, commit_date: cd, day: day(ad), week: weekOf(ad), author: an, parents: parents.split(' ').filter(Boolean).length,
    subject, type: m ? m[1] : '', scope: m?.[2] ?? '', assisted_by: ab ? ab[1].trim() : '', model: ab ? ab[1].trim().replace(/^[^:]*:/, '') : '',
    is_revert: /^revert/i.test(subject) || /^This reverts commit/m.test(body) ? 1 : 0, files: 0 };
  for (const k of CATS) { c['add_' + k] = 0; c['del_' + k] = 0; }
  for (const line of (rest || '').split('\n').filter(Boolean)) {
    const [a, d, pth] = line.split('\t');
    if (pth === undefined) continue;
    let p = pth;
    if (p.includes('=>')) p = p.replace(/\{([^}]*) => ([^}]*)\}/, '$2').replace(/\/\//g, '/'); // brace rename
    if (p.includes(' => ')) p = p.split(' => ')[1];
    const cat = category(p);
    c.files++;
    if (a !== '-') { c['add_' + cat] += +a; c['del_' + cat] += +d; }
  }
  commits.push(c);
}
commits.reverse();
const cols = ['sha','author_date','commit_date','day','week','author','parents','type','scope','assisted_by','model','is_revert','files', ...CATS.flatMap((k) => ['add_' + k, 'del_' + k]), 'subject'];
write('commits.csv', csv(commits, cols));
const days = {};
for (const c of commits) {
  const d = (days[c.day] ??= { day: c.day, commits: 0, with_assisted_by: 0, without_assisted_by: 0 });
  d.commits++; c.assisted_by ? d.with_assisted_by++ : d.without_assisted_by++;
  for (const k of CATS) { d['add_' + k] = (d['add_' + k] || 0) + c['add_' + k]; d['del_' + k] = (d['del_' + k] || 0) + c['del_' + k]; }
}
const dcols = ['day','commits','with_assisted_by','without_assisted_by', ...CATS.flatMap((k) => ['add_' + k, 'del_' + k])];
write('commits_per_day.csv', csv(Object.values(days).sort((a, b) => a.day.localeCompare(b.day)), dcols));
write('commits_without_assisted_by.csv', csv(commits.filter((c) => !c.assisted_by), ['sha','author_date','author','subject']));
const models = {}; for (const c of commits) models[c.model || '(none)'] = (models[c.model || '(none)'] || 0) + 1;
write('commit_models.json', models);
console.log(commits.length, models, commits.filter((c) => !c.assisted_by).length, 'reverts', commits.filter(c=>c.is_revert).length);
write('commits.json', commits);
