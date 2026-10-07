#!/usr/bin/env python3
"""X17-image-build: build an x17-guestd request file. Throwaway.

mkreq.py <out.jsonl> <step>...
  hello | shutdown | run:<script file> | runpw:<script file>:<password file> | sleep:<seconds>
"""
import json
import sys

out = []
for step in sys.argv[2:]:
    if step.startswith("runpw:"):
        # runpw:<script>:<password file> prepends P='<password>'
        script, pwfile = step[6:].split(":")
        with open(pwfile) as f:
            pw = f.read().strip()
        with open(script) as f:
            out.append({"op": "run", "script": "P='%s'\n" % pw + f.read(), "timeout": 600})
    elif step.startswith("run:"):
        with open(step[4:]) as f:
            out.append({"op": "run", "script": f.read(), "timeout": 600})
    elif step.startswith("sleep:"):
        out.append({"op": "run", "script": "sleep " + step[6:]})
    else:
        out.append({"op": step})
with open(sys.argv[1], "w") as f:
    for r in out:
        f.write(json.dumps(r) + "\n")
