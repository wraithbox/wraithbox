#!/usr/bin/env python3
"""Throwaway: summarize the matrix runs into one table."""

import glob
import json
import os
import sys

RES = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".scratch", "results")


def events(name):
    p = os.path.join(RES, name + ".jsonl")
    if not os.path.exists(p):
        return []
    return [json.loads(l) for l in open(p) if l.strip()]


def downloads(evs):
    return {(e["eco"], e["name"], e["version"]) for e in evs if e["kind"] == "download" and e.get("decision") in ("allow", "allow-would-refuse")}


def main():
    projects = json.load(open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "projects.json")))
    if "--other" in sys.argv:  # othereco.py runs: lock = cli/cli and bat, fresh = @latest / caret manifest
        projects = {"go": {"eco": "go"}, "cargo": {"eco": "cargo"}}
        for pid in projects:
            for kind in ("lock", "fresh"):
                for mode in ("off", "refuse", "filter"):
                    f = os.path.join(RES, f"{pid}.{kind}.{mode}.result.json")
                    if not os.path.exists(f) and os.path.exists(os.path.join(RES, f"{pid}.{kind}.{mode}.jsonl")):
                        ev = events(f"{pid}.{kind}.{mode}")
                        failed = any(e.get("decision") == "refuse" for e in ev)
                        json.dump({"rc": int(failed)}, open(f, "w"))
    print("| project | eco | install | off: rc, downloads, too young | refuse: rc, refused | filter: rc, versions hidden, backstop refusals, versions not in off run |")
    print("|---|---|---|---|---|---|")
    tot = {}
    for pid, p in projects.items():
        for kind in ("lock", "fresh"):
            row = [pid, p["eco"], kind]
            r = {}
            for mode in ("off", "refuse", "filter"):
                f = os.path.join(RES, f"{pid}.{kind}.{mode}.result.json")
                r[mode] = json.load(open(f)) if os.path.exists(f) else None
            if not r["off"]:
                continue
            off_ev = events(f"{pid}.{kind}.off")
            dl_off = downloads(off_ev)
            young = {(e["name"], e["version"]) for e in off_ev if e.get("decision") == "allow-would-refuse"}
            row.append(f"{r['off']['rc']}, {len(dl_off)}, {len(young)}")
            if r["refuse"]:
                ev = events(f"{pid}.{kind}.refuse")
                refused = [e for e in ev if e.get("decision") == "refuse"]
                row.append(f"{r['refuse']['rc']}, {len(refused)}")
            else:
                row.append("-")
            if r["filter"]:
                ev = events(f"{pid}.{kind}.filter")
                hidden = sum(e.get("removed", 0) for e in ev if e.get("decision") == "filter")
                back = [e for e in ev if e.get("decision") == "refuse"]
                dl_f = downloads(ev)
                older = len(dl_f - dl_off)  # name@version pairs the off run did not download
                row.append(f"{r['filter']['rc']}, {hidden}, {len(back)}, {older}")
            else:
                row.append("-")
            if "--times" in sys.argv:
                row.append(" / ".join(str(r[m].get("seconds", "?")) if r[m] else "-" for m in ("off", "refuse", "filter")))
            print("| " + " | ".join(row) + " |")
            for mode in ("off", "refuse", "filter"):
                if r[mode]:
                    k = (kind, mode)
                    tot.setdefault(k, [0, 0])
                    tot[k][0] += 1
                    tot[k][1] += r[mode]["rc"] != 0
    print()
    for (kind, mode), (n, fail) in sorted(tot.items()):
        print(f"{kind:5} {mode:6}: {fail}/{n} failed")


if __name__ == "__main__":
    main()
