#!/usr/bin/env python3
"""Throwaway: `npm ci` with the OSV lookup overlapped with the download."""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import harness  # noqa: E402

harness.PROXY = os.path.join(harness.SCRATCH, "x21proxy-go")
os.environ["X21_OSV_OVERLAP"] = "1"
for pid in sys.argv[1].split(","):
    for rep in ("1", "2"):
        harness.run_one(pid, "lock", "refuse", "single", "", "overlap-r" + rep)
