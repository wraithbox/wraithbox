#!/usr/bin/env python3
"""Throwaway: how long OSV lookups take for every package in a lockfile.

single-seq:  one /v1/query per package, one at a time.
single-16:   one /v1/query per package, 16 at a time (npm fetches in parallel).
batch:       /v1/querybatch, 1000 queries per request.
Each query asks for one exact version, so the answer is the vulns affecting it.
"""

import concurrent.futures as cf
import json
import os
import sys
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
INPUTS = os.path.join(HERE, "..", "..", ".scratch", "inputs")


def pkgs(project):
    lock = json.load(open(os.path.join(INPUTS, project, "package-lock.json")))
    out = set()
    for path, p in lock["packages"].items():
        if not path or "version" not in p or p.get("link"):
            continue
        name = p.get("name") or path.split("node_modules/")[-1]
        out.add((name, p["version"]))
    return sorted(out)


def post(url, body):
    req = urllib.request.Request(url, data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        return json.load(r)


def q(nv):
    t = time.time()
    r = post("https://api.osv.dev/v1/query", {"package": {"name": nv[0], "ecosystem": "npm"}, "version": nv[1]})
    return time.time() - t, len(r.get("vulns", []))


def main(project):
    ps = pkgs(project)
    print(f"{project}: {len(ps)} packages")
    t0 = time.time()
    lat = []
    hits = 0
    for nv in ps:
        dt, n = q(nv)
        lat.append(dt)
        hits += n > 0
    seq = time.time() - t0
    lat.sort()
    print(f"single-seq: {seq:.1f}s total, per query median {1000 * lat[len(lat) // 2]:.0f} ms, p95 {1000 * lat[int(len(lat) * .95)]:.0f} ms; packages with vulns: {hits}")
    t0 = time.time()
    with cf.ThreadPoolExecutor(16) as ex:
        list(ex.map(q, ps))
    print(f"single-16: {time.time() - t0:.1f}s total")
    t0 = time.time()
    ids = 0
    for i in range(0, len(ps), 1000):
        chunk = ps[i:i + 1000]
        r = post("https://api.osv.dev/v1/querybatch", {"queries": [{"package": {"name": n, "ecosystem": "npm"}, "version": v} for n, v in chunk]})
        ids += sum(len(x.get("vulns", [])) for x in r["results"])
    print(f"batch: {time.time() - t0:.1f}s total for {len(ps)} queries ({(len(ps) + 999) // 1000} requests); vuln ids returned: {ids} (severity needs one /v1/vulns/<id> each)")


if __name__ == "__main__":
    main(sys.argv[1])
