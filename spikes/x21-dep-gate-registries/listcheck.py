#!/usr/bin/env python3
"""Throwaway (I76): which listed versions the full @v/list filter hides, and why.

Usage: listcheck.py <calibration point> <module>...
"""

import concurrent.futures as cf
import sys
import urllib.error
import urllib.request

POINT = int(sys.argv[1])


def get(u):
    with urllib.request.urlopen(u, timeout=60) as r:
        return r.read().decode()


def esc(p):
    return "".join("!" + c.lower() if c.isupper() else c for c in p)


for m in sys.argv[2:]:
    vs = get(f"https://proxy.golang.org/{esc(m)}/@v/list").split()

    def look(v, m=m):
        try:
            return v, int(get(f"https://sum.golang.org/lookup/{esc(m)}@{esc(v)}").splitlines()[0]), None
        except urllib.error.HTTPError as e:
            return v, None, f"HTTP {e.code} {e.read()[:150].decode(errors='replace').strip()}"

    with cf.ThreadPoolExecutor(16) as ex:
        for v, r, err in ex.map(look, vs):
            if r is None or r > POINT:
                print(m, v, r, err)
