import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
export const REPO = '/Users/lsimons/git/wraithbox/wraithbox';
export const HEAD = '6138336c';
export const OUT = '/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/b';
export const RAW = OUT + '/raw';
export const git = (args, opts = {}) => execFileSync('git', ['-C', REPO, ...args], { encoding: 'utf8', maxBuffer: 1 << 30, ...opts });
const fmt = new Intl.DateTimeFormat('en-CA', { timeZone: 'Europe/Amsterdam', year: 'numeric', month: '2-digit', day: '2-digit' });
export const day = (d) => fmt.format(new Date(d)); // YYYY-MM-DD in Amsterdam
export function weekStart(dayStr) { // Monday of the week, as YYYY-MM-DD
  const d = new Date(dayStr + 'T12:00:00Z');
  const dow = (d.getUTCDay() + 6) % 7;
  d.setUTCDate(d.getUTCDate() - dow);
  return d.toISOString().slice(0, 10);
}
export const weekOf = (iso) => weekStart(day(iso));
export const CATS = ['go-src','go-test','go-generated','swift-src','swift-test','spec-docs','agent-harness','doc-site','ci-config-scripts','lockfiles','other'];
export const SPEC_DIRS = ['docs/spec/','docs/requirements/','docs/threats/','docs/tests/','docs/spikes/','docs/versions/','docs/research/','docs/briefs/'];
let genSet = null;
export function generatedSet() {
  if (genSet) return genSet;
  const out = git(['grep', '-l', '-E', '^// Code generated .* DO NOT EDIT', HEAD, '--', '*.go'], { stdio: ['ignore','pipe','ignore'] }).split('\n').filter(Boolean);
  genSet = new Set(out.map((l) => l.replace(/^[^:]+:/, '')));
  return genSet;
}
export function category(p, gen = generatedSet()) {
  const base = p.split('/').pop();
  if (['bun.lock','go.sum','go.work.sum','Package.resolved','mise.lock','package-lock.json'].includes(base)) return 'lockfiles';
  if (SPEC_DIRS.some((d) => p.startsWith(d)) || p === 'docs/implementation-plan.md') return 'spec-docs';
  if (p.startsWith('docs/agents/') || p.startsWith('.claude/') || p === 'AGENTS.md' || p === 'CLAUDE.md') return 'agent-harness';
  if (p.startsWith('packages/wraithbox-doc/')) return 'doc-site';
  if (p.endsWith('.go')) {
    if (p.endsWith('.pb.go') || gen.has(p)) return 'go-generated';
    return p.endsWith('_test.go') ? 'go-test' : 'go-src';
  }
  if (p.endsWith('.swift')) return (/(^|\/)Tests\//.test(p) || /Tests?\.swift$/.test(p)) ? 'swift-test' : 'swift-src';
  if (p.startsWith('.github/') || p.startsWith('scripts/') || p.startsWith('.vale') || /^\.(mise|golangci|swiftlint|vale)/.test(base) || ['mise.toml','.mise.toml','cspell-words.txt','cspell.json','osv-scanner.toml','go.mod','go.work','.swift-format'].includes(base) || p.startsWith('.mise')) return 'ci-config-scripts';
  return 'other';
}
export const csv = (rows, cols) => [cols.join(','), ...rows.map((r) => cols.map((c) => { let v = r[c] ?? ''; v = String(v); return /[",\n]/.test(v) ? '"' + v.replace(/"/g, '""') + '"' : v; }).join(','))].join('\n') + '\n';
export const write = (name, data) => fs.writeFileSync(OUT + '/' + name, typeof data === 'string' ? data : JSON.stringify(data, null, 1));
