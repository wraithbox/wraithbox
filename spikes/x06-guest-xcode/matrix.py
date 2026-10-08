#!/usr/bin/env python3
"""X06-guest-xcode: the final run of every task, in every mode. Throwaway.

matrix.py <run label>    runs the list below in order, appends every answer
                         to results/<label>-tasks.jsonl, and writes
                         results/<label>-matrix.txt (task, mode, exit, time).
Each xcodebuild run is killed after 300 s (X06_LIMIT).
"""
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
label = sys.argv[1]
TASKS = ["env", "spm", "mac-build", "mac-test", "mac-uitest", "pkg-xcb-test", "ios-build",
         "ios-device-build", "sign", "sim", "ios-test", "ios-uitest"]
RUNS = [(t, how, None) for how in ("setuid", "asuser") for t in TASKS]
RUNS += [(t, "setuid", "p0") for t in ("spm", "mac-build", "ios-build", "sign", "sim")]
RUNS += [(t, "setuid", "p1") for t in ("spm", "mac-build", "ios-build", "sign", "sim", "ios-test", "escape")]
RUNS += [(t, "setuid", "p2") for t in ("ios-test", "ios-uitest", "escape")]
if len(sys.argv) > 2:
    RUNS = [r for r in RUNS if r[0] in sys.argv[2:]]

req = [sys.executable, os.path.join(HERE, "req.py"), ".scratch/g.sock"]
for f in ("xcode.sb", "xcode-simtest.sb"):
    subprocess.check_call(req + ["put", "--put", os.path.join(HERE, "guest", f), "/var/db/x06/" + f, "0644"], stdout=subprocess.DEVNULL)
subprocess.check_call(req + ["run", "--script", os.path.join(HERE, "guest", "profiles.sh")], stdout=subprocess.DEVNULL)

env = dict(os.environ, X06_LIMIT="300")
rows = []
for task, how, prof in RUNS:
    cmd = [sys.executable, os.path.join(HERE, "task.py"), label, task, how]
    if prof:
        cmd += ["--sandbox", "/var/db/x06/%s.sb" % prof]
    subprocess.run(cmd, env=env, capture_output=True, text=True)
    with open(os.path.join(HERE, "results", label + "-tasks.jsonl")) as f:
        last = json.loads(f.readlines()[-1])
    out = last["resp"].get("out", "")
    results = [l for l in out.splitlines() if l.startswith("RESULT")]
    row = "%-17s %-7s %-4s exit=%-4s %5.0fs  %s" % (task, how, prof or "-", last["resp"].get("exit"), last["t"], " | ".join(results)[:200])
    print(row, flush=True)
    rows.append(row)
with open(os.path.join(HERE, "results", label + "-matrix.txt"), "a") as f:
    f.write("\n".join(rows) + "\n")
