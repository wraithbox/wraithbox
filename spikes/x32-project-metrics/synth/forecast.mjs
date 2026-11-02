// X32 synthesis: top-down Monte Carlo forecast of V1 completion from historical throughput.
// Usage: bun forecast.mjs <data-dir>
// Reads e/calibration.csv (closed issues with lines and lane), e/remaining.csv (remaining
// nodes in package-issue units), c/timeline_by_day.csv (active days).
// Writes synth/forecast.json.
//
// Method: bootstrap one active day at a time from the active days 2026-10-16..2026-11-01
// (the build phase), counting work closed per lane in package-issue units (code lines of
// closed issues / median package-issue code lines). The vm lane and the rest burn down
// separately; V1 is done when both are. Remaining work grows by a factor g ~ U(1.0, 1.4)
// for follow-ups and unfiled-package size error. Active days become calendar days by
// drawing active days per week from a scenario.
import { readFileSync, writeFileSync } from "node:fs";

const dir = process.argv[2];
// "count" (default): history in the same units as e/remaining.csv (package issue = 1,
// spike = 0.7, bug/doc/chore/follow-up = 0.3). "lines": code lines / median package lines.
const MODE = process.argv[3] ?? "count";
const KIND_UNITS = { package: 1, spike: 0.7, bug: 0.3, doc: 0.3, chore: 0.3, followup: 0.3 };
const parse = (p) => {
  const text = readFileSync(`${dir}/${p}`, "utf8").trim();
  const rows = [];
  for (const line of text.split("\n")) {
    const cells = []; let cur = "", q = false;
    for (const ch of line) {
      if (ch === '"') q = !q; else if (ch === "," && !q) { cells.push(cur); cur = ""; } else cur += ch;
    }
    cells.push(cur); rows.push(cells);
  }
  const [head, ...body] = rows;
  return body.map((r) => Object.fromEntries(r.map((v, i) => [head[i], v])));
};

const calib = parse("e/calibration.csv");
const remaining = parse("e/remaining.csv");
const timeline = parse("c/timeline_by_day.csv");

const pkg = calib.filter((c) => c.kind === "package").map((c) => +c.code_lines).sort((a, b) => a - b);
const UNIT = pkg[Math.floor(pkg.length / 2)];
const amsDate = (iso) => new Date(new Date(iso).getTime() + 3600e3 * (iso < "2026-10-25T01:00:00Z" ? 2 : 1)).toISOString().slice(0, 10);

const buildDays = timeline.map((t) => t.date).filter((d) => d >= "2026-10-16" && d <= "2026-11-01");
const perDay = Object.fromEntries(buildDays.map((d) => [d, { vm: 0, nonvm: 0 }]));
for (const c of calib) {
  const d = amsDate(c.merged || c.closed);
  if (!perDay[d]) continue;
  const units = MODE === "lines" ? +c.code_lines / UNIT : (KIND_UNITS[c.kind] ?? 0.3);
  perDay[d][/^vm/.test(c.lane) ? "vm" : "nonvm"] += units;
}
const hist = Object.values(perDay);

const REM = { vm: 0, nonvm: 0 };
for (const n of remaining) REM[/^vm/.test(n.lane) ? "vm" : "nonvm"] += +n.size_factor || 0;

const scenarios = {
  less: [0, 2, 3, 3, 4],       // about half the attention, with some weeks off
  as_before: [5, 5, 6, 7],     // weeks 10-05 .. 10-26 as measured
  more: [6, 7, 7],
};
const rand = (xs) => xs[Math.floor(Math.random() * xs.length)];
const N = 20000;
const pct = (xs, p) => xs[Math.min(xs.length - 1, Math.floor(p * xs.length))];

const activeDays = [];
for (let i = 0; i < N; i++) {
  const g = 1 + 0.4 * Math.random();
  const need = { vm: REM.vm * g, nonvm: REM.nonvm * g };
  let vm = 0, nonvm = 0, days = 0;
  while ((vm < need.vm || nonvm < need.nonvm) && days < 2000) {
    const h = rand(hist); vm += h.vm; nonvm += h.nonvm; days++;
  }
  activeDays.push(days);
}
activeDays.sort((a, b) => a - b);

const start = new Date("2026-11-03T12:00:00Z");
const toDate = (calDays) => new Date(start.getTime() + calDays * 86400e3).toISOString().slice(0, 10);
const out = {
  mode: MODE, unit_lines: UNIT, build_days: hist.length,
  hist_units_per_day: {
    vm: +(hist.reduce((s, h) => s + h.vm, 0) / hist.length).toFixed(2),
    nonvm: +(hist.reduce((s, h) => s + h.nonvm, 0) / hist.length).toFixed(2),
  },
  remaining_units: { vm: +REM.vm.toFixed(1), nonvm: +REM.nonvm.toFixed(1) },
  active_days: { p15: pct(activeDays, 0.15), p50: pct(activeDays, 0.5), p85: pct(activeDays, 0.85), p95: pct(activeDays, 0.95) },
  calendar: {},
};
for (const [name, weeks] of Object.entries(scenarios)) {
  const cal = activeDays.map((need) => {
    let left = need, d = 0;
    while (left > 0 && d < 3650) { const a = rand(weeks); if (left <= a) { d += Math.ceil((left / Math.max(a, 1)) * 7); left = 0; } else { left -= a; d += 7; } }
    return d;
  }).sort((a, b) => a - b);
  out.calendar[name] = { p50: toDate(pct(cal, 0.5)), p85: toDate(pct(cal, 0.85)), p95: toDate(pct(cal, 0.95)) };
}
writeFileSync(`${dir}/synth/forecast-${MODE}.json`, JSON.stringify(out, null, 2));
console.log(JSON.stringify(out, null, 2));
