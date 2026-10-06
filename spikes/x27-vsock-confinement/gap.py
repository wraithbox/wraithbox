#!/usr/bin/env python3
"""X27-vsock-confinement: the window in which nothing (or an impostor)
answered on the guest port, from the host's poll lines. Throwaway.

For each poll run (lines from i=0 to the "done" line) it prints who
answered over time as runs of the same answer: the banner, or the error.
The gap is the time between the last answer of the killed process and the
first answer of the next one.
"""
import json
import sys


def runs(path):
    cur = []
    for line in open(path):
        line = line.strip()
        if not line.startswith("{"):
            continue
        r = json.loads(line)
        if r.get("cmd") != "poll":
            continue
        if r.get("done"):
            yield cur
            cur = []
            continue
        cur.append(r)
    if cur:
        yield cur


def who(r):
    b = r.get("banner")
    if b is not None:
        return b if b else "connected, no banner"
    e = r.get("error", "?")
    return "error: " + e.split("\"")[0][:60]


for path in sys.argv[1:]:
    print(path)
    gaps = []
    for n, rs in enumerate(runs(path), 1):
        segs = []
        for r in rs:
            w = who(r)
            if segs and segs[-1][0] == w:
                segs[-1][2] = r["wall"]
                segs[-1][3] += 1
            else:
                segs.append([w, r["wall"], r["wall"], 1])
        print(f"  run {n}: {len(rs)} attempts")
        for w, a, b, c in segs:
            print(f"    {a:.3f} .. {b:.3f}  ({(b - a) * 1000:8.1f} ms, {c:5d} x)  {w}")
        answers = [s for s in segs if s[0].startswith("guestd")]
        if len(answers) >= 2:
            gaps.append((answers[1][1] - answers[0][2]) * 1000)
    if gaps:
        gaps.sort()
        print(f"  gaps between guestd answers (ms): {', '.join(f'{g:.1f}' for g in gaps)}")
