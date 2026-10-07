#!/usr/bin/env python3
"""X17-image-build: print a boot result file readably. Throwaway."""
import json
import sys

for line in open(sys.argv[1]):
    r = json.loads(line)
    if "resp" not in r:
        print(r)
        continue
    resp = r["resp"]
    meta = {k: v for k, v in resp.items() if k not in ("out", "err")}
    first = r["req"].get("script", "").strip().splitlines()[:1] if isinstance(r["req"], dict) else []
    print("--- t=%.1fs %s %s" % (r["t"], first, meta))
    if resp.get("out"):
        print(resp["out"].rstrip())
    if resp.get("err"):
        print("stderr:", resp["err"].rstrip())
