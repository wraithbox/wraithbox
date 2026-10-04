"""X02-warm-start: print each result line of the given JSONL files compactly."""

import json
import sys

for path in sys.argv[1:]:
    print("--", path.rsplit("/", 1)[-1])
    for line in open(path):
        r = json.loads(line)
        if "error" in r:
            print("  ERROR", r.get("bundle"), r.get("opts"), r["error"][:100], round(r.get("seconds", 0), 2))
            continue
        print("  ", {k: (round(v, 3) if isinstance(v, float) else v) for k, v in r.items() if not k.startswith("vsock")})
