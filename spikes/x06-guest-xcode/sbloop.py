#!/usr/bin/env python3
"""X06-guest-xcode: one round of finding Xcode's Seatbelt exceptions. Throwaway.

sbloop.py <run label> <task> <how> [profile, default p1]

Copies guest/xcode.sb to /var/db/x06/xcode.sb, rebuilds the profiles, runs
the task under the profile, and prints the denials of the last 2 minutes,
leaving out daemons that are not the user's (they run under their own
sandboxes and log denials all the time).
"""
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
label, task, how = sys.argv[1:4]
prof = sys.argv[4] if len(sys.argv) > 4 else "p1"
req = [sys.executable, os.path.join(HERE, "req.py"), ".scratch/g.sock"]
log = os.path.join(HERE, "results", label + "-setup.jsonl")
subprocess.check_call(req + ["put", "--put", os.path.join(HERE, "guest", "xcode.sb"), "/var/db/x06/xcode.sb", "0644"], stdout=subprocess.DEVNULL)
subprocess.check_call(req + ["run", "--script", os.path.join(HERE, "guest", "profiles.sh"), "--log", log, "--label", "profiles"], stdout=subprocess.DEVNULL)
rc = subprocess.call([sys.executable, os.path.join(HERE, "task.py"), label, task, how, "--sandbox", "/var/db/x06/%s.sb" % prof])
out = subprocess.run(req + ["run", "--script", os.path.join(HERE, "guest", "denials.sh"),
                            "--log", log, "--label", "denials-%s-%s" % (task, how)], capture_output=True, text=True).stdout
NOISE = ("vfs.disk-space", "nfcd", "airportd", "apsd", "ASPCarryLog", "systemstats", "runningboardd", "mds ", "mdworker", "tty ")
print("=== denials")
print("\n".join(l for l in out.splitlines() if not any(n in l for n in NOISE))[:6000])
sys.exit(rc)
