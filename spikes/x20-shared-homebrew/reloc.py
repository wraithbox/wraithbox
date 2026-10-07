#!/usr/bin/env python3
"""X20-shared-homebrew: which bottles pour outside /opt/homebrew. Throwaway.

reloc.py <formula.json> <bottle tag> <formula>...

Reads Homebrew's formula API (https://formulae.brew.sh/api/formula.json),
walks the runtime dependency closure of the named formulae, and for each
formula prints the bottle's cellar for the tag:
  :any_skip_relocation  pours anywhere, no path rewriting
  :any                  pours anywhere, Homebrew rewrites paths at pour
  /opt/homebrew/Cellar  pours only into /opt/homebrew; elsewhere brew
                        builds from source
  none                  no bottle for the tag; built from source everywhere
"""
import json
import sys

data = {f["name"]: f for f in json.load(open(sys.argv[1]))}
tag = sys.argv[2]
want = sys.argv[3:]

closure, stack = [], list(want)
while stack:
    n = stack.pop()
    if n in closure:
        continue
    f = data.get(n)
    if f is None:
        print("unknown formula", n, file=sys.stderr)
        continue
    closure.append(n)
    stack.extend(f.get("dependencies", []))

counts = {}
for n in sorted(closure):
    files = data[n].get("bottle", {}).get("stable", {}).get("files", {})
    cellar = files.get(tag, files.get("all", {})).get("cellar", "none")
    kind = cellar if cellar.startswith(":") or cellar == "none" else "fixed-prefix"
    counts[kind] = counts.get(kind, 0) + 1
    top = "*" if n in want else " "
    print("%s %-28s %-12s %s" % (top, n, data[n]["versions"]["stable"], cellar))
print("formulae in closure:", len(closure), counts)
