"""X04 spike: Python HTTPS client. Throwaway.

python3 loop.py <requests|urllib> <url> [count] [interval s]
Each request uses a new connection.
"""

import json
import sys
import time

mode, url = sys.argv[1], sys.argv[2]
n = int(sys.argv[3]) if len(sys.argv) > 4 else 1
every = int(sys.argv[4]) if len(sys.argv) > 4 else 0
ok = False
for i in range(n):
    if i:
        time.sleep(every)
    out = {"t": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "i": i}
    try:
        if mode == "requests":
            import requests

            with requests.Session() as s:
                out["status"] = s.get(url, timeout=20).status_code
        else:
            import urllib.request

            with urllib.request.urlopen(url, timeout=20) as r:
                out["status"] = r.status
        ok = True
    except Exception as e:  # spike: record any failure
        out["err"] = "%s: %s" % (type(e).__name__, e)
        ok = False
    print(json.dumps(out), flush=True)
sys.exit(0 if ok else 1)
