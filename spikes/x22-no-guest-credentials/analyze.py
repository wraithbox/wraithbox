#!/usr/bin/env python3
"""Throwaway: the tables in results-tables.md, from results/*.jsonl and runs.jsonl."""
import collections
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
R = os.path.join(HERE, "results")
MODES = ["observe", "strip", "read-only", "issued", "inject", "inject-flow"]

# These ops set no placeholder, so their "+ph" runs are repeats.
NOPH = {"git-lfs", "oci-flow", "attacker", "swiftpm", "go-direct", "git-push-dry"}


def skip(label):
    return label.endswith("+ph") and label[:-3] in NOPH


runs = collections.OrderedDict()
for line in open(os.path.join(R, "runs.jsonl")):
    r = json.loads(line)
    if skip(r["op"]):
        continue
    runs[(r["op"], r["mode"])] = r
ops = []
for (op, _m) in runs:
    if op not in ops:
        ops.append(op)

print("## Client runs (exit status 0 = pass)\n")
print("| Operation | " + " | ".join(MODES) + " |")
print("|---" * (len(MODES) + 1) + "|")
for op in ops:
    cells = []
    for m in MODES:
        r = runs.get((op, m))
        cells.append("-" if r is None else ("pass" if r["rc"] == 0 else f"FAIL ({r['rc']})"))
    print(f"| {op} | " + " | ".join(cells) + " |")

print("\n## Attacker credentials: forwarded upstream?\n")
print("| Request | " + " | ".join(MODES) + " |")
print("|---" * (len(MODES) + 1) + "|")
rows = collections.OrderedDict()
for m in MODES:
    seen = collections.Counter()
    for line in open(os.path.join(R, f"{m}.jsonl")):
        e = json.loads(line)
        if e.get("label") != "attacker":
            continue
        key = f"{e['method']} {e['host']}{e['path']}"
        seen[key] += 1
        key = f"{key} #{seen[key]}" if seen[key] > 1 else key
        if e.get("refused"):
            cell = "refused (403)"
        elif not e.get("creds"):
            cell = f"none sent, {e['status']}"
        else:
            d = sorted({c["decision"] for c in e["creds"]})
            cell = "/".join(d) + (f", inject {e['injected']}" if e.get("injected") else "") + f", {e['status']}"
        rows.setdefault(key, {})[m] = cell
for k, v in rows.items():
    print(f"| `{k[:70]}` | " + " | ".join(v.get(m, "-") for m in MODES) + " |")

print("\n## Hosts, credentials and cookies seen with the rule off (observe)\n")
hosts = collections.OrderedDict()
for line in open(os.path.join(R, "observe.jsonl")):
    e = json.loads(line)
    if skip(e.get("label", "")):
        continue
    op = e.get("label", "").replace("+ph", " (placeholder)")
    if op.startswith("attacker"):
        continue
    h = hosts.setdefault((op, e["host"]), {"creds": set(), "setcookie": set(), "other": set(), "challenge": set(), "n": 0})
    h["n"] += 1
    for c in e.get("creds") or []:
        h["creds"].add(f"{c['header']} {c.get('scheme','')}".strip())
    for s in e.get("set_cookie") or []:
        h["setcookie"].add(s)
    for s in e.get("other_auth_headers") or []:
        h["other"].add(s)
    if e.get("www_auth"):
        h["challenge"].add(e["www_auth"].split(" ")[0])
print("| Operation | Host | Requests | Credential headers sent | Other key-like headers | Set-Cookie | 401 challenge |")
print("|---|---|---|---|---|---|---|")
for (op, host), h in hosts.items():
    j = lambda s: ", ".join(sorted(s)) or "-"
    print(f"| {op} | {host} | {h['n']} | {j(h['creds'])} | {j(h['other'])} | {j(h['setcookie'])} | {j(h['challenge'])} |")
cookies_sent = 0
for m in MODES:
    for line in open(os.path.join(R, f"{m}.jsonl")):
        e = json.loads(line)
        if e.get("label", "").startswith("attacker"):
            continue
        cookies_sent += sum(1 for c in e.get("creds") or [] if c["header"] == "Cookie")
print(f"\nCookie headers sent by any client in any mode (attacker script excluded): {cookies_sent}")
