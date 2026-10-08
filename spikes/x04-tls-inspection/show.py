#!/usr/bin/env python3
"""X04: print a matrix result file, one line per client. Throwaway.

show.py <matrix-*.jsonl> [client ...]   with clients named: also the output.
Relay rows for hosts in NOISE (system daemons that connect on their own
while a client runs) are left out.
"""
import json
import sys

NOISE = {"api.apple-cloudkit.com", "ocsp2.apple.com", "gateway.icloud.com", "configuration.apple.com"}
rows = [json.loads(line) for line in open(sys.argv[1])]
want = set(sys.argv[2:])
for r in rows:
    rel = [x for x in r["relay"] if x[0] not in NOISE]
    hosts = {}
    for sni, mode, res, err in rel:
        k = "%s:%s" % (sni, "ok" if res in ("inspected", "relayed") else (err.replace("remote error: tls: ", "alert:")[:40] or res))
        hosts[k] = hosts.get(k, 0) + 1
    print("%-28s %-8s exit=%-3s %6.1fs  %s" % (r["client"], r["cond"], r["exit"], r["secs"], " ".join("%s x%d" % kv for kv in hosts.items())))
    if r["client"] in want:
        print("    out: " + r["out"].replace("\n", "\n    out: "))
