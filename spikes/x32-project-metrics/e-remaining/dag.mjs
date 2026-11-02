// Build the DAG of remaining V1 work: open issues in V1-M3..M7 plus unfiled plan packages.
// Usage: bun dag.mjs <datadir> -> remaining.csv, dag.json; prints critical path and a quick MC.
import { readFileSync, writeFileSync } from "node:fs";
import { PACKAGES } from "./plan.mjs";
const d = process.argv[2];
const I = JSON.parse(readFileSync(`${d}/raw/issues.json`));
const byNum = Object.fromEntries(I.map((i) => [i.number, i]));
const V1REM = ["V1-M3-network-floor", "V1-M7-image-build", "V1-M4-claude-end-to-end", "V1-M5-approvals", "V1-M6-release-gate"];
const pkgById = Object.fromEntries(PACKAGES.map((p) => [p.id, p]));

// Open issues folded into a not-yet-filed package node (plan "Issues" column).
const FOLD = { 70: "M4.5", 71: "M4.3", 231: "M4.2-vm", 135: "M4.12-vm", 474: "IMG.5", 193: "M5.7", 180: "M5.11-vm", 90: "M6.3", 196: "M6.3", 118: "M6.6", 148: "M6.7", 175: "M6.7", 318: "M6.2-vm" };
// Size factors in "package-issue units" (one closed V1-M2/M3 package issue = 1.0).
// Unfiled non-vm package: 2.6 issues (M2: 34 issues / 12 packages, M3: 29 / 11).
// Unfiled vm-only package: 1.5 vm issues (M2.5: 2, M2.6: 2 incl. #379, M3.12: 1 vm issue each).
// Mixed package: logic 2.0 non-vm units, then 1.0 vm unit.
const SIZE = { pkg: 2.6, "pkg-logic": 2.0, "pkg-vm": 1.5, "pkg-vmpart": 1.0, issue: 1.0, small: 0.3, spike: 0.7, gate: 0 };

const nodes = {};
const add = (n) => (nodes[n.id] = { deps: [], issues: [], attended: false, merge_gate: false, ...n });

// 1. Gate nodes (maintainer actions with no issue yet).
add({ id: "H4", title: "Host setup H4: wbtest account with a model token", ms: "M4", lane: "host", size: "gate", attended: true, note: "no issue filed; #214 (M1) shows wbtest exists" });
add({ id: "H5", title: "Host setup H5: attended notification permission", ms: "M5", lane: "host", size: "gate", attended: true, note: "no issue filed" });

// 2. Open issues in the remaining V1 milestones.
const open = I.filter((i) => i.state === "OPEN" && V1REM.includes(i.milestone?.title));
for (const i of open) {
  if (FOLD[i.number]) continue;
  const ls = i.labels.nodes.map((l) => l.name);
  const vm = ls.includes("needs-vm"), host = ls.includes("needs-host");
  const pkg = PACKAGES.find((p) => p.issues.includes(i.number));
  const small = ls.some((l) => /^(bug|chore|documentation)$/.test(l)) || (ls.includes("area:spec") && !ls.some((l) => /area:(go|swift)/.test(l)));
  const size = ls.includes("spike") ? "spike" : pkg && !small ? "issue" : small ? "small" : "issue";
  add({
    id: `#${i.number}`, title: i.title.slice(0, 90), ms: i.milestone.title.slice(3, 5), pkg: pkg?.id ?? "", lane: vm ? "vm" : "nonvm",
    size, attended: host || ls.includes("ready-for-human"),
    merge_gate: vm || host || ls.includes("comp:wb-vmd") || ls.includes("area:spec") || ls.includes("spike"),
    labels: ls.join(" "), assignee: i.assignees.nodes.map((a) => a.login).join(" "), issues: [i.number],
    deps: i.blockedBy.nodes.filter((b) => b.state === "OPEN").map((b) => `#${b.number}`),
  });
}

// 3. Unfiled packages (or the unfiled core of a partly filed one).
const UNFILED = {
  "IMG.1": ["nonvm", "pkg"], "IMG.2": ["vm", "pkg-vm"], "IMG.3": ["vm", "pkg-vm"], "IMG.4": ["vm", "pkg-vm", true], "IMG.5": ["vm", "pkg-vm"],
  "IMG.6": ["vm", "pkg-vm"], "IMG.7": ["mixed"], "IMG.8": ["vm", "pkg-vm"],
  "M4.1": ["nonvm", "pkg"], "M4.2": ["mixed"], "M4.3": ["nonvm", "pkg"], "M4.4": ["nonvm", "pkg"], "M4.5": ["nonvm", "pkg"], "M4.6": ["nonvm", "pkg"],
  "M4.7": ["vm", "pkg-vm"], "M4.8": ["mixed"], "M4.9": ["mixed"], "M4.10": ["nonvm", "pkg"], "M4.11": ["nonvm", "pkg"], "M4.12": ["vm", "pkg-vm", true],
  "M5.1": ["nonvm", "pkg"], "M5.2": ["nonvm", "pkg"], "M5.3": ["nonvm", "pkg"], "M5.4": ["nonvm", "pkg", true], "M5.5": ["nonvm", "pkg"], "M5.6": ["nonvm", "pkg"],
  "M5.7": ["nonvm", "pkg"], "M5.8": ["nonvm", "pkg"], "M5.9": ["nonvm", "pkg"], "M5.10": ["nonvm", "pkg"], "M5.11": ["mixed"], "M5.12": ["vm", "pkg-vm", true],
  "M6.1": ["nonvm", "pkg"], "M6.2": ["mixed"], "M6.3": ["vm", "pkg-vm"], "M6.4": ["vm", "pkg-vm"], "M6.5": ["nonvm", "pkg"], "M6.6": ["nonvm", "pkg", true],
  "M6.7": ["vm", "pkg-vm"], "M6.8": ["vm", "pkg-vm", true],
};
const foldedInto = (id) => Object.entries(FOLD).filter(([, v]) => v === id).map(([k]) => +k);
for (const [id, [kind, size, att]] of Object.entries(UNFILED)) {
  const p = pkgById[id];
  const base = { title: `plan ${id} (unfiled)`, ms: p.ms, pkg: id, attended: !!att, merge_gate: kind !== "nonvm" || !!att };
  if (kind === "mixed") {
    add({ ...base, id, lane: "nonvm", size: "pkg-logic", issues: foldedInto(id), merge_gate: false });
    add({ ...base, id: `${id}-vm`, title: `plan ${id} VM part (unfiled)`, lane: "vm", size: "pkg-vmpart", deps: [id], issues: foldedInto(`${id}-vm`), merge_gate: true });
  } else add({ ...base, id, lane: kind, size, issues: foldedInto(id) });
}
// M4.12 split: first prompt on the fixture bundle (vm, host, H4), then C06 end to end after the rest of M4 and IMG.7.
add({ id: "M4.12-vm", title: "plan M4.12 C06 end to end on a sealed image (unfiled)", ms: "M4", pkg: "M4.12", lane: "vm", size: "pkg-vmpart", attended: true, merge_gate: true, issues: foldedInto("M4.12-vm") });

// 4. Plan edges: package dep -> all remaining nodes of that package.
const nodesOfPkg = (pid) => Object.values(nodes).filter((n) => n.pkg === pid || n.id === pid || n.id === `${pid}-vm`);
// A non-vm node waits for the logic part of a mixed dep package; a vm node waits for all of it.
const tail = (pid, lane) => nodesOfPkg(pid).filter((n) => lane === "vm" || !(n.id === `${pid}-vm` && nodes[pid])).map((n) => n.id);
const extra = { "X01-253": ["#253"], "X30": ["#262"], "SECRET-SCANNER": ["#306"], H4: ["H4"], H5: ["H5"] };
for (const p of PACKAGES) {
  const mine = nodesOfPkg(p.id).filter((n) => !(n.id === `${p.id}-vm` && nodes[p.id] && UNFILED[p.id]?.[0] === "mixed"));
  // Filed issues keep only their GitHub blockedBy edges (finer than the plan); plan edges apply to unfiled nodes.
  for (const n of mine.filter((m) => !m.id.startsWith("#"))) {
    const depIds = p.deps.flatMap((dp) => extra[dp] ?? (pkgById[dp] ? tail(dp, n.lane) : []));
    for (const dId of depIds) if (dId !== n.id && !n.deps.includes(dId)) n.deps.push(dId);
  }
}
// Within-package order for filed packages is already in blockedBy. Specific extra edges:
nodes["M4.12-vm"].deps.push("M4.12", ...["M4.1", "M4.2-vm", "M4.3", "M4.4", "M4.5", "M4.7", "M4.8-vm", "M4.9-vm", "M4.11", "#500", "#502"], "IMG.7-vm");
nodes["M4.10"].deps.push("#500", "#504", "#514", "#253");
nodes["IMG.2"].deps.push("#448"); // X31-admin-channel answers the admin path IMG.4 builds; keep it ahead of the image lane
nodes["IMG.4"].deps.push("#448");
for (const s of ["#150", "#194", "#224", "#463"]) if (nodes[s]) for (const t of ["IMG.2"]) nodes[t].deps.push(s);
if (nodes["#194"]) nodes["IMG.8"].deps.push("#194");
for (const s of ["#72", "#128", "#137"]) nodes["M4.1"].deps.push(s);
nodes["M4.5"].deps.push("#137");
nodes["#490"].deps.push("M5.3");
// Remove deps to nodes that don't exist (completed packages) and dedupe.
for (const n of Object.values(nodes)) n.deps = [...new Set(n.deps)].filter((x) => nodes[x] && x !== n.id);

// 5. Cycle check + topo order.
const order = [], seen = {}, stack = {};
const visit = (id) => { if (seen[id]) return; if (stack[id]) throw new Error("cycle at " + id); stack[id] = 1; for (const x of nodes[id].deps) visit(x); stack[id] = 0; seen[id] = 1; order.push(id); };
Object.keys(nodes).forEach(visit);

// 6. Calibration samples (working days per package-issue unit), from calibration.csv.
const cal = readFileSync(`${d}/calibration.csv`, "utf8").trim().split("\n").map((l) => l.split(","));
const hdr = cal.shift();
const C = cal.map((r) => Object.fromEntries(hdr.map((k, i) => [k, r[i]])));
const pk = C.filter((r) => r.kind === "package");
const vmS = pk.filter((r) => r.lane.startsWith("vm")).map((r) => +r.start_to_merge_h);
const nvS = pk.filter((r) => !r.lane.startsWith("vm")).map((r) => +r.start_to_merge_h);
const smS = C.filter((r) => /bug|chore|doc|followup/.test(r.kind)).map((r) => +r.start_to_merge_h);

const out = {
  generated: "2026-11-02", source: "main 6138336c, gh GraphQL 2026-11-02",
  units: "size factors are in package-issue units; one unit's duration is sampled from duration_samples_h of the node's lane",
  size_factors: SIZE,
  duration_samples_h: { vm_issue: vmS, nonvm_issue: nvS, small: smS },
  active_hours_per_day_note: "start_to_merge_h is wall-clock from claim (or PR open) to merge; a run day covers about 8-12 such hours",
  gate_wait_samples_h: {
    attended_needs_host: readFileSync(`${d}/gates.csv`, "utf8").trim().split("\n").slice(1).map((l) => l.split(",")).filter((r) => r[1] === "needs-host" && r[5] !== "open").map((r) => +r[6]),
    ready_for_human: readFileSync(`${d}/gates.csv`, "utf8").trim().split("\n").slice(1).map((l) => l.split(",")).filter((r) => r[1] === "ready-for-human" && r[5] !== "open").map((r) => +r[6]),
    merge_after_approve_gated: readFileSync(`${d}/merge-waits.csv`, "utf8").trim().split("\n").slice(1).map((l) => l.split(",")).filter((r) => r[1] === "true" && r[3] >= "2026-10-16").map((r) => +r[2]),
  },
  resources: { vm: { capacity: 1, note: "one VM builder at a time (orchestration.md, host-work.md)" }, nonvm: { capacity: 3, note: "3-4 builders per wave; observed in-flight p50 1, p90 4" }, maintainer: { note: "attended nodes need the maintainer present; needs-host is never run by /wave" } },
  nodes: order.map((id) => { const n = nodes[id]; return { id, title: n.title, milestone: n.ms, package: n.pkg ?? "", lane: n.lane, size_class: n.size, size_factor: SIZE[n.size], attended: n.attended, merge_gate: n.merge_gate, issues: n.issues, deps: n.deps }; }),
};
writeFileSync(`${d}/dag.json`, JSON.stringify(out, null, 1));
const cols = ["id", "milestone", "package", "lane", "size_class", "size_factor", "attended", "merge_gate", "deps", "issues", "labels", "assignee", "title"];
writeFileSync(`${d}/remaining.csv`, [cols.join(","), ...order.map((id) => { const n = nodes[id]; return [id, n.ms, n.pkg ?? "", n.lane, n.size, SIZE[n.size], n.attended, n.merge_gate, n.deps.join(" "), (n.issues ?? []).map((x) => "#" + x).join(" "), n.labels ?? "", n.assignee ?? "", `"${(n.title ?? "").replace(/"/g, "'")}"`].join(","); })].join("\n") + "\n");

// 7. Summaries.
const N = Object.values(nodes);
const tally = {};
for (const n of N) { const k = `${n.ms} ${n.lane}`; tally[k] ??= { nodes: 0, units: 0, attended: 0 }; tally[k].nodes++; tally[k].units += SIZE[n.size]; if (n.attended) tally[k].attended++; }
console.log("milestone lane: nodes units attended");
for (const [k, v] of Object.entries(tally).sort()) console.log(k.padEnd(12), v.nodes, v.units.toFixed(1), v.attended);
const tot = (f) => N.filter(f).reduce((a, n) => a + SIZE[n.size], 0);
console.log("total nodes", N.length, "units", tot(() => true).toFixed(1), "vm units", tot((n) => n.lane === "vm").toFixed(1), "nonvm units", tot((n) => n.lane === "nonvm").toFixed(1), "attended nodes", N.filter((n) => n.attended).length);

// Longest path with point durations in working days: vm unit 1.2 d, nonvm unit 1.0 d, small 0.3 d, attended +1 d.
const DUR = (n) => SIZE[n.size] * (n.lane === "vm" ? 1.2 : 1.0) + (n.attended ? 1.0 : 0);
const best = {}, prev = {};
for (const id of order) { const n = nodes[id]; let b = 0, p = null; for (const x of n.deps) if (best[x] > b) { b = best[x]; p = x; } best[id] = b + DUR(n); prev[id] = p; }
let end = order.reduce((a, b) => (best[a] > best[b] ? a : b));
const path = []; for (let x = end; x; x = prev[x]) path.unshift(x);
console.log("\nlongest dependency path (no lane limits):", best[end].toFixed(1), "days,", path.length, "nodes, attended", path.filter((x) => nodes[x].attended).length);
console.log(path.map((x) => `${x}[${nodes[x].lane}${nodes[x].attended ? ",A" : ""} ${DUR(nodes[x]).toFixed(1)}]`).join(" -> "));
writeFileSync(`${d}/raw/path.json`, JSON.stringify({ path, best: best[end] }));

// List schedule with vm capacity 1 and nonvm capacity 3 (point estimates).
function schedule(dur, capNon = 3, gateDelay = () => 0) {
  const fin = {}, start = {};
  const free = { vm: [0], nonvm: Array(capNon).fill(0) };
  const ready = (id) => Math.max(0, ...nodes[id].deps.map((x) => fin[x]));
  const pending = new Set(order);
  while (pending.size) {
    // pick the pending node whose deps are done, earliest ready time, then by order index (plan order)
    let pick = null, pr = Infinity;
    for (const id of pending) { if (nodes[id].deps.some((x) => fin[x] === undefined)) continue; const r = ready(id); if (r < pr) { pr = r; pick = id; } }
    const n = nodes[pick];
    const res = n.size === "gate" ? null : free[n.lane];
    let s = pr;
    if (res) { res.sort((a, b) => a - b); s = Math.max(pr, res[0]); }
    const f = s + dur(n) + (n.attended ? gateDelay(n) : 0);
    if (res) res[0] = s + dur(n); // the maintainer wait does not hold the lane
    start[pick] = s; fin[pick] = f; pending.delete(pick);
  }
  return { fin, start, end: Math.max(...Object.values(fin)) };
}
const det = schedule((n) => SIZE[n.size] * (n.lane === "vm" ? 1.2 : 1.0), 3, () => 1.0);
console.log("\nlist schedule, point estimates (vm 1.2 d/unit, nonvm 1.0 d/unit, 3 non-vm builders, attended +1 d):", det.end.toFixed(1), "working days");
const vmBusy = N.filter((n) => n.lane === "vm").reduce((a, n) => a + SIZE[n.size] * 1.2, 0);
console.log("vm lane busy days:", vmBusy.toFixed(1), " vm lane idle (waiting on deps):", (det.end - vmBusy).toFixed(1));
// Quick MC. Unit durations: calibrated wall-clock samples rescaled so their mean equals the
// throughput-derived active days per unit (vm lane 13 items in 14 active days 10-16..11-01 -> ~1.1 d;
// non-vm 33 package issues + 37 small items in 14 active days at ~2 builders -> ~0.7 d).
const pickR = (a) => a[Math.floor(Math.random() * a.length)];
const mean = (a) => a.reduce((x, y) => x + y, 0) / a.length;
const scale = { vm: 1.1 / mean(vmS), nonvm: 0.7 / mean(nvS), small: 0.25 / mean(smS) };
const gH = out.gate_wait_samples_h.attended_needs_host.concat(out.gate_wait_samples_h.ready_for_human);
out.unit_day_scale = { ...scale, note: "days = sample_h * scale; vm mean 1.1 d, nonvm mean 0.7 d, small mean 0.25 d per unit" };
writeFileSync(`${d}/dag.json`, JSON.stringify(out, null, 1));
function mc(capNon, gateScale, sizeMul = 1, runs = 2000) {
  const res = [];
  for (let k = 0; k < runs; k++) {
    const s = schedule((n) => {
      if (n.size === "gate") return 0;
      const [src, sc] = n.size === "small" ? [smS, scale.small] : n.lane === "vm" ? [vmS, scale.vm] : [nvS, scale.nonvm];
      const u = SIZE[n.size] * sizeMul; const whole = Math.max(1, Math.round(u)); let t = 0;
      for (let j = 0; j < whole; j++) t += Math.max(0.1, pickR(src) * sc);
      return t * (u / whole);
    }, capNon, () => (pickR(gH) / 24) * gateScale);
    res.push(s.end);
  }
  res.sort((a, b) => a - b);
  return `p10 ${res[runs / 10 | 0].toFixed(0)} p50 ${res[runs / 2].toFixed(0)} p80 ${res[runs * 0.8 | 0].toFixed(0)} p90 ${res[runs * 0.9 | 0].toFixed(0)}`;
}
console.log("quick MC, 3 non-vm builders, gate waits as measured (calendar days counted as active):", mc(3, 1));
console.log("quick MC, 2 non-vm builders, gate waits as measured:", mc(2, 1));
console.log("quick MC, 2 non-vm builders, gate waits x0.5 (only active days count):", mc(2, 0.5));
console.log("quick MC, 3 builders, sizes x1.3 (scope growth):", mc(3, 1, 1.3));
console.log("quick MC, 2 builders, sizes x1.3, gates x1:", mc(2, 1, 1.3));
writeFileSync(`${d}/raw/schedule.json`, JSON.stringify(det));
