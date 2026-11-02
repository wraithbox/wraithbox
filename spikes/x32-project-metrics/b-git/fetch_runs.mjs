import fs from 'node:fs';
import { execFileSync } from 'node:child_process';
import { RAW } from './lib.mjs';
let total = 0;
for (let p = 1; ; p++) {
  const f = `${RAW}/runs_${String(p).padStart(3, '0')}.json`;
  let r;
  if (fs.existsSync(f)) r = JSON.parse(fs.readFileSync(f));
  else { r = JSON.parse(execFileSync('gh', ['api', `repos/wraithbox/wraithbox/actions/runs?per_page=100&page=${p}`], { encoding: 'utf8', maxBuffer: 1 << 28 })); fs.writeFileSync(f, JSON.stringify(r)); }
  total += r.workflow_runs.length; if (r.workflow_runs.length < 100) break;
}
console.log(total);
