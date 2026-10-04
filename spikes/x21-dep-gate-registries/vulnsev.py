#!/usr/bin/env python3
"""Throwaway: how many lockfile installs a HIGH vulnerability threshold would fail.

For each project's lockfile install (the off run's downloads), query OSV in
batch, fetch each vulnerability once, and take its severity from
database_specific.severity (GitHub advisories: LOW, MODERATE, HIGH,
CRITICAL). Records without it count as unknown.
"""

import concurrent.futures as cf
import json
import os
import urllib.request

RES = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".scratch", "results")
ECO = {"npm": "npm", "pypi": "PyPI", "Go": "Go", "crates.io": "crates.io"}


def post(url, body):
    req = urllib.request.Request(url, data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        return json.load(r)


vcache = {}


def vuln(i):
    if i not in vcache:
        with urllib.request.urlopen(f"https://api.osv.dev/v1/vulns/{i}", timeout=60) as r:
            vcache[i] = json.load(r)
    return vcache[i]


def sev(v):
    s = (v.get("database_specific") or {}).get("severity")
    return s.upper() if isinstance(s, str) else "UNKNOWN"


names = sorted({f.split(".")[0] for f in os.listdir(RES) if f.endswith(".lock.off.jsonl")})
for pid in names:
    ev = [json.loads(l) for l in open(os.path.join(RES, f"{pid}.lock.off.jsonl")) if l.strip()]
    pkgs = sorted({(e["eco"], e["name"], e["version"]) for e in ev if e["kind"] == "download" and e.get("name")})
    hits = {}
    for i in range(0, len(pkgs), 1000):
        chunk = pkgs[i:i + 1000]
        r = post("https://api.osv.dev/v1/querybatch", {"queries": [{"package": {"name": n, "ecosystem": ECO[e]}, "version": v} for e, n, v in chunk]})
        for p, res in zip(chunk, r["results"]):
            if res.get("vulns"):
                hits[p] = [x["id"] for x in res["vulns"]]
    ids = sorted({i for v in hits.values() for i in v})
    with cf.ThreadPoolExecutor(16) as ex:
        list(ex.map(vuln, ids))
    high = [p for p, v in hits.items() if any(sev(vuln(i)) in ("HIGH", "CRITICAL") for i in v)]
    unknown = [p for p, v in hits.items() if p not in high and any(sev(vuln(i)) == "UNKNOWN" for i in v)]
    crit = [p for p, v in hits.items() if any(sev(vuln(i)) == "CRITICAL" for i in v)]
    mal = [p for p, v in hits.items() if any(i.startswith("MAL-") for i in v)]
    cvss_only = [p for p in unknown if all((vuln(i).get("severity") or []) for i in hits[p] if sev(vuln(i)) == "UNKNOWN")]
    print(f"{pid:16} packages {len(pkgs):5}  with any vuln {len(hits):3}  HIGH or CRITICAL {len(high):3}  CRITICAL {len(crit):3}  MAL {len(mal)}  only unknown/lower-with-unknown {len(unknown):3} (of which with a CVSS vector {len(cvss_only)})  e.g. {[f'{n}@{v}' for _, n, v in high[:3]]}")
