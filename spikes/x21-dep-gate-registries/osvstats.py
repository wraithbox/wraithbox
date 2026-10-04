#!/usr/bin/env python3
"""Throwaway: OSV time per download from the proxy logs of the OSV runs."""

import glob
import json
import os

RES = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".scratch", "results")

for f in sorted(glob.glob(os.path.join(RES, "*.lock.refuse*.jsonl"))):
    name = os.path.basename(f)[:-6]
    if "osv" not in name and not name.endswith(("r1", "r2")):
        continue
    ev = [json.loads(l) for l in open(f) if l.strip()]
    dl = [e for e in ev if e["kind"] == "download"]
    osv = sorted(e.get("osv_ms", 0) for e in dl)
    vul = sum(1 for e in dl if e.get("vulns"))
    res = json.load(open(os.path.join(RES, name + ".result.json")))
    if not dl:
        continue
    print(f"{name:55} wall {res['seconds']:6.1f}s  downloads {len(dl):5}  osv median {osv[len(osv) // 2]:6.0f} ms  p95 {osv[int(len(osv) * .95)]:6.0f} ms  sum {sum(osv) / 1000:6.1f}s  with vulns {vul}")
