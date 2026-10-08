#!/usr/bin/env python3
"""X06-guest-xcode: bisect the top-level forms of the safehouse profile's
lines 46..342 (the system runtime section) for the `xcodebuild test` hang.
Throwaway.

bisect2.py list            print the forms with their index
bisect2.py drop <i,j-k,..> run ios-test with the whole profile minus those
                           forms, plus (allow default). PASS or HANG.
"""
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
text = open(".scratch/sh-x06.sb").read().splitlines()


def forms(lines):
    """Top-level s-expressions as (start line, end line) pairs, 1-based."""
    out, depth, start, in_str = [], 0, None, False
    for n, line in enumerate(lines, 1):
        i = 0
        while i < len(line):
            c = line[i]
            if in_str:
                if c == "\\":
                    i += 1
                elif c == '"':
                    in_str = False
            elif c == ";":
                break
            elif c == '"':
                in_str = True
            elif c == "(":
                if depth == 0:
                    start = n
                depth += 1
            elif c == ")":
                depth -= 1
                if depth == 0:
                    out.append((start, n))
            i += 1
    return out


fs = [f for f in forms(text) if 46 <= f[0] <= 342]
if sys.argv[1] == "list":
    for i, (a, b) in enumerate(fs):
        print(i, a, b, text[a - 1].strip()[:110])
    sys.exit(0)
drop = set()
for part in sys.argv[2].split(","):
    a, _, b = part.partition("-")
    drop.update(range(int(a), int(b or a) + 1))
skip = set()
for i in drop:
    skip.update(range(fs[i][0], fs[i][1] + 1))
prof = [l for n, l in enumerate(text, 1) if n not in skip] + ["(allow default)"]
open(".scratch/bis.sb", "w").write("\n".join(prof) + "\n")
req = [sys.executable, os.path.join(HERE, "req.py"), ".scratch/g.sock"]
subprocess.check_call(req + ["put", "--put", ".scratch/bis.sb", "/var/db/x06/bis.sb", "0644"], stdout=subprocess.DEVNULL)
env = dict(os.environ, X06_LIMIT="120")
out = subprocess.run([sys.executable, os.path.join(HERE, "task.py"), "g3-bisect", "ios-test", "setuid", "--sandbox", "/var/db/x06/bis.sb"],
                     env=env, capture_output=True, text=True).stdout
print("drop %s: %s" % (sys.argv[2], "PASS" if "TEST SUCCEEDED" in out else "HANG/FAIL"))
