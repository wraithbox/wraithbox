#!/usr/bin/env python3
"""Throwaway: `npm ci` wall time with OSV lookups in the proxy.

Per project, in order: no OSV, one query per download, coalesced querybatch,
then querybatch with a persistent cache twice (cold, then warm).
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import harness  # noqa: E402

for pid in sys.argv[1].split(","):
    cache = os.path.join(harness.SCRATCH, f"osv-cache-{pid}.json")
    if os.path.exists(cache):
        os.remove(cache)
    for rep in ("1", "2"):
        harness.run_one(pid, "lock", "refuse", "off", "", "r" + rep)
        harness.run_one(pid, "lock", "refuse", "single", "", "r" + rep)
        harness.run_one(pid, "lock", "refuse", "batch", "", "r" + rep)
    harness.run_one(pid, "lock", "refuse", "batch", cache, "cache-cold")
    harness.run_one(pid, "lock", "refuse", "batch", cache, "cache-warm")
