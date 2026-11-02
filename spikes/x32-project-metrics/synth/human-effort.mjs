// X32 synthesis: inferred human effort per day, as low / mid / high.
// Usage: bun human-effort.mjs <data-dir>
// Reads c/timeline_by_day.csv, c/gaps.csv, c/prompts.csv, c/reading_load_by_day.csv,
// synth/decisions.txt. Writes synth/human_effort_daily.csv, synth/human_effort.json.
//
// Model (assumptions from the maintainer, 2026-11-02, I543):
// - No AI activity on a day means no human effort on it.
// - Effort shows as prompting sessions, reading, thinking gaps and decisions.
// - Weekdays 09:00-17:00 Amsterdam get a discount: attention is split with the day job.
import { readFileSync, writeFileSync } from "node:fs";

const dir = process.argv[2];
const csv = (p) => {
  const [head, ...rows] = readFileSync(`${dir}/${p}`, "utf8").trim().split("\n");
  const keys = head.split(",");
  return rows.map((r) => Object.fromEntries(r.split(",").map((v, i) => [keys[i], v])));
};

const timeline = csv("c/timeline_by_day.csv");
const gaps = csv("c/gaps.csv");
const prompts = csv("c/prompts.csv");
const reading = Object.fromEntries(csv("c/reading_load_by_day.csv").map((r) => [r.date, +r.words]));
const decisions = {};
for (const line of readFileSync(`${dir}/synth/decisions.txt`, "utf8").trim().split("\n")) {
  const date = line.split(" ")[1];
  decisions[date] = (decisions[date] ?? 0) + 1;
}

const gapMin = (date, cls) =>
  gaps.filter((g) => g.date === date && g.class === cls).reduce((s, g) => s + +g.gap_min, 0);

const isWorkHours = (p) => {
  const dow = new Date(`${p.date}T12:00:00Z`).getUTCDay();
  const hour = +p.time_local.split(":")[0];
  return dow >= 1 && dow <= 5 && hour >= 9 && hour < 17;
};

const READ_WPM = 250; // skimming speed for agent output
const days = timeline.map((t) => {
  const dayPrompts = prompts.filter((p) => p.date === t.date);
  const f9 = dayPrompts.length ? dayPrompts.filter(isWorkHours).length / dayPrompts.length : 0;
  const D = decisions[t.date] ?? 0;
  const T = gapMin(t.date, "thinking");
  const O = gapMin(t.date, "other");
  const readMin = (reading[t.date] ?? 0) / READ_WPM;
  const s5 = +t.human_sess5_min, s15 = +t.human_sess15_min, s30 = +t.human_sess30_min;
  const low = (1 - 0.5 * f9) * s5 + 5 * D;
  const mid = Math.max((1 - 0.5 * f9) * s15, readMin) + 0.5 * T + 10 * D;
  const high = Math.max((1 - 0.25 * f9) * s30, readMin) + T + 0.5 * O + 20 * D;
  return {
    date: t.date, dow: t.dow, prompts: dayPrompts.length, work_hours_share: +f9.toFixed(2),
    decisions: D, thinking_min: Math.round(T), other_gap_min: Math.round(O), read_min: Math.round(readMin),
    ai_union_min: +t.ai_union_min, agent_min: +t.agent_min,
    low_h: +(low / 60).toFixed(2), mid_h: +(mid / 60).toFixed(2), high_h: +(high / 60).toFixed(2),
  };
});

const weekOf = (date) => {
  const d = new Date(`${date}T12:00:00Z`);
  d.setUTCDate(d.getUTCDate() - ((d.getUTCDay() + 6) % 7));
  return d.toISOString().slice(0, 10);
};
const weeks = {};
for (const d of days) {
  const w = (weeks[weekOf(d.date)] ??= { days: 0, low_h: 0, mid_h: 0, high_h: 0, ai_union_h: 0, agent_h: 0, decisions: 0 });
  w.days += 1; w.low_h += d.low_h; w.mid_h += d.mid_h; w.high_h += d.high_h;
  w.ai_union_h += d.ai_union_min / 60; w.agent_h += d.agent_min / 60; w.decisions += d.decisions;
}
for (const w of Object.values(weeks)) for (const k of Object.keys(w)) w[k] = +w[k].toFixed(1);

const pearson = (xs, ys) => {
  const n = xs.length, mx = xs.reduce((a, b) => a + b) / n, my = ys.reduce((a, b) => a + b) / n;
  let sxy = 0, sxx = 0, syy = 0;
  for (let i = 0; i < n; i++) { sxy += (xs[i] - mx) * (ys[i] - my); sxx += (xs[i] - mx) ** 2; syy += (ys[i] - my) ** 2; }
  return +(sxy / Math.sqrt(sxx * syy)).toFixed(2);
};
const sum = (k) => +days.reduce((s, d) => s + d[k], 0).toFixed(1);
const out = {
  active_days: days.length,
  total_h: { low: sum("low_h"), mid: sum("mid_h"), high: sum("high_h") },
  ai_union_h: +(sum("ai_union_min") / 60).toFixed(1), agent_h: +(sum("agent_min") / 60).toFixed(1),
  decisions: sum("decisions"),
  corr_mid_vs_ai_union: pearson(days.map((d) => d.mid_h), days.map((d) => d.ai_union_min)),
  corr_decisions_vs_mid: pearson(days.map((d) => d.decisions), days.map((d) => d.mid_h)),
  corr_decisions_vs_prompt_sess15: pearson(days.map((d) => d.decisions), timeline.map((t) => +t.human_sess15_min)),
  corr_ai_union_vs_prompt_sess15: pearson(days.map((d) => d.ai_union_min), timeline.map((t) => +t.human_sess15_min)),
  weeks,
};
const keys = Object.keys(days[0]);
writeFileSync(`${dir}/synth/human_effort_daily.csv`, [keys.join(","), ...days.map((d) => keys.map((k) => d[k]).join(","))].join("\n") + "\n");
writeFileSync(`${dir}/synth/human_effort.json`, JSON.stringify(out, null, 2));
console.log(JSON.stringify(out, null, 2));
