#!/usr/bin/env python3
"""Throwaway: is the checksum-database record number a usable clock for Go?

For entries first seen by the module proxy at known times (index.golang.org),
look up their record number in sum.golang.org. If record numbers grow with
first-seen time, one calibration point ("record number at now - 7 days")
tells whether any module version is younger than 7 days, with one sumdb
lookup that the go command makes anyway.
"""

import datetime as dt
import json
import urllib.request

NOW = dt.datetime.now(dt.timezone.utc)


def get(url):
    with urllib.request.urlopen(url, timeout=60) as r:
        return r.read().decode()


def esc(p):
    return "".join("!" + c.lower() if c.isupper() else c for c in p)


rows = []
for days in [14, 10, 8, 7.5, 7, 6.5, 6, 4, 2, 1, 0.5, 0.1]:
    since = (NOW - dt.timedelta(days=days)).strftime("%Y-%m-%dT%H:%M:%SZ")
    entries = [json.loads(l) for l in get(f"https://index.golang.org/index?since={since}&limit=20").splitlines() if l]
    for e in entries[:3]:
        try:
            rec = get(f"https://sum.golang.org/lookup/{esc(e['Path'])}@{esc(e['Version'])}").splitlines()[0]
            rows.append((e["Timestamp"], int(rec), e["Path"] + "@" + e["Version"]))
        except Exception as ex:
            rows.append((e["Timestamp"], None, f"{e['Path']}@{e['Version']} {ex}"))
latest = get("https://sum.golang.org/latest").splitlines()
print("tree size now:", latest[1])
prev = None
mono = True
for t, rec, name in rows:
    print(t, rec, name)
    if rec is not None and prev is not None and rec < prev:
        mono = False
    if rec is not None:
        prev = rec
print("record numbers increase with first-seen time:", mono)
