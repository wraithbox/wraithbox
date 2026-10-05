#!/usr/bin/env python3
"""X03-network-path spike: summarize x03-netd logs.

  summarize.py lease <netd.jsonl>...   seconds from NIC hand-off to DHCP ACK
  summarize.py names <netd.jsonl>      every name the guest resolved, with
                                       count, types, first and last time
  summarize.py drops <netd.jsonl>      final counters
"""
import collections
import json
import sys


def lines(path):
    with open(path) as f:
        for line in f:
            line = line.strip()
            if line.startswith("{"):
                yield json.loads(line)


def lease(paths):
    for p in paths:
        fd = ack = None
        for j in lines(p):
            if j["ev"] == "nic-fd":
                fd = j["t"]
            if j["ev"] == "dhcp-out" and j["type"] == "ACK" and ack is None:
                ack = j["t"]
        print(f"{p}: nic hand-off to first ACK {ack - fd:.2f} s")


def names(path):
    seen = collections.OrderedDict()
    for j in lines(path):
        if j["ev"] != "dns":
            continue
        n = j["name"]
        s = seen.setdefault(n, {"count": 0, "types": set(), "first": j["t"], "last": j["t"], "rcodes": set()})
        s["count"] += 1
        s["types"].add(j["type"].removeprefix("Type"))
        s["rcodes"].add(j["rcode"].removeprefix("RCode"))
        s["last"] = j["t"]
    print(f"{len(seen)} distinct names, {sum(s['count'] for s in seen.values())} queries")
    print("first_s last_s count types rcodes name")
    for n, s in seen.items():
        print(f"{s['first']:8.1f} {s['last']:8.1f} {s['count']:5d} {','.join(sorted(s['types']))} {','.join(sorted(s['rcodes']))} {n}")


def drops(path):
    last = None
    for j in lines(path):
        if j["ev"] == "counters":
            last = j
    print(json.dumps(last, indent=1, sort_keys=True))


if __name__ == "__main__":
    cmd, args = sys.argv[1], sys.argv[2:]
    {"lease": lease, "names": lambda a: names(a[0]), "drops": lambda a: drops(a[0])}[cmd](args)
