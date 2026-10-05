#!/usr/bin/env python3
# X05-fs-benchmark spike: medians of results/*-bench.jsonl, and each guest
# configuration's throughput as a share of the host's (host median time /
# guest median time; NFR02-fs-speed wants >= 0.80). The host baseline is
# every warm host run in results/host-bench.jsonl. Throwaway.
import glob
import json
import statistics
import sys

WL = ["npm-ci", "git-status", "go-incr", "go-clean", "fsync", "seqwrite"]
NFR = ["npm-ci", "git-status", "go-incr"]


def load(paths):
    rows = []
    for p in paths:
        for line in open(p):
            r = json.loads(line)
            if "workload" in r and r.get("mode") in ("warm", "cold"):
                rows.append(r)
    return rows


def med(rows, label_pred, wl, mode):
    xs = [r["seconds"] for r in rows if label_pred(r["label"]) and r["workload"] == wl and r["mode"] == mode]
    return (statistics.median(xs), len(xs)) if xs else (None, 0)


d = sys.argv[1] if len(sys.argv) > 1 else "results"
host = load([f"{d}/host-bench.jsonl"])
guest_files = sorted(p for p in glob.glob(f"{d}/*-bench.jsonl") if not p.endswith("host-bench.jsonl"))
guest = load(guest_files)
labels = sorted({r["label"] for r in guest})

print("Host (warm, all runs between guest configurations):")
hbase = {}
for wl in WL:
    m, n = med(host, lambda l: True, wl, "warm")
    hbase[wl] = m
    if m is not None:
        xs = sorted(r["seconds"] for r in host if r["workload"] == wl and r["mode"] == "warm")
        print(f"  {wl:11s} p50 {m:7.3f} s  min {xs[0]:7.3f}  max {xs[-1]:7.3f}  n={n}")

for mode in ("warm", "cold"):
    print(f"\nGuest p50 seconds, {mode} (share of host warm throughput in brackets):")
    print("  " + "label".ljust(26) + "".join(w.rjust(17) for w in WL))
    for lab in labels:
        cells = []
        for wl in WL:
            m, n = med(guest, lambda l: l == lab, wl, mode)
            if m is None:
                cells.append("-".rjust(17))
            else:
                share = hbase[wl] / m if hbase.get(wl) else None
                cells.append((f"{m:.3f} [{share:.2f}]" if share else f"{m:.3f}").rjust(17))
        print("  " + lab.ljust(26) + "".join(cells))
