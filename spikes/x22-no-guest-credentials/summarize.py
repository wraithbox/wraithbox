#!/usr/bin/env python3
"""Throwaway: per op, the hosts it reached, credential headers sent,
cookies set, auth challenges, and server-issued tokens. usage: summarize.py mode"""
import collections
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
mode = sys.argv[1]
ops = collections.OrderedDict()
for line in open(os.path.join(HERE, "results", f"{mode}.jsonl")):
    e = json.loads(line)
    o = ops.setdefault(e.get("label", ""), collections.OrderedDict())
    h = o.setdefault(e["host"], collections.Counter())
    h[f"n"] += 1
    h[f"status {e['status']}"] += 1
    if e.get("refused"):
        h["REFUSED " + e["refused"][:40]] += 1
    for c in e.get("creds") or []:
        h[f"cred {c['header']} {c.get('scheme','')} {c['hash']} {c['decision']} {c.get('why','')} {e['method']}"] += 1
    for k in e.get("other_auth_headers") or []:
        h["header " + k] += 1
    if e.get("injected"):
        h["inject " + e["injected"]] += 1
    if e.get("www_auth"):
        h["challenge " + e["www_auth"][:90]] += 1
    for s in e.get("set_cookie") or []:
        h["set-cookie " + s] += 1
    if e.get("token_issued"):
        h["token issued"] += 1
    for l in e.get("lfs_action_headers") or []:
        h["lfs " + l] += 1
    if e.get("location"):
        h["redirect " + e["location"][:80]] += 1
    if e.get("err"):
        h["err " + e["err"][:80]] += 1
    for k in e.get("query_keys") or []:
        if any(s in k.lower() for s in ("token", "sig", "key", "auth", "cred", "x-amz", "jwt")):
            h["query " + k] += 1
for op, hosts in ops.items():
    print(f"== {op}")
    for host, c in hosts.items():
        print(f"  {host}: " + "; ".join(f"{k} x{v}" if not k.startswith("status") and k != "n" else f"{k}:{v}" for k, v in c.items()))
