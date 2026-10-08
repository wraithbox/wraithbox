#!/usr/bin/env python3
"""X06-guest-xcode: find which part of the safehouse profile makes
`xcodebuild test` on a simulator hang, even with (allow default) after it.
Throwaway.

bisect.py <first line> <last line>
  keeps lines 1..45 (version and definitions) and <first>..<last> of the
  rendered profile, appends (allow default), and runs ios-test under it
  with a 120 s limit. Prints PASS or HANG.
"""
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
lo, hi = int(sys.argv[1]), int(sys.argv[2])
lines = open(".scratch/sh-x06.sb").read().splitlines()
prof = lines[:45] + lines[lo - 1:hi] + ["(allow default)"]
open(".scratch/bis.sb", "w").write("\n".join(prof) + "\n")
req = [sys.executable, os.path.join(HERE, "req.py"), ".scratch/g.sock"]
subprocess.check_call(req + ["put", "--put", ".scratch/bis.sb", "/var/db/x06/bis.sb", "0644"], stdout=subprocess.DEVNULL)
env = dict(os.environ, X06_LIMIT="120")
out = subprocess.run([sys.executable, os.path.join(HERE, "task.py"), "g3-bisect", "ios-test", "setuid", "--sandbox", "/var/db/x06/bis.sb"],
                     env=env, capture_output=True, text=True).stdout
ok = "TEST SUCCEEDED" in out
print("%d-%d %s" % (lo, hi, "PASS" if ok else "HANG/FAIL"))
if not ok:
    print("\n".join(l for l in out.splitlines() if "RESULT" in l or "error" in l.lower())[:800])
