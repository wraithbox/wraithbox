#!/usr/bin/env python3
"""Throwaway (I76): the checksum-database clock at scale.

Samples index.golang.org entries across the last 16 days, looks each one up
in sum.golang.org, and reports:

- how many entries had no record until this lookup created one (record
  number >= the tree size read just before), i.e. versions the proxy served
  but nobody had looked up in the checksum database;
- how well record order follows first-seen order for the rest;
- the calibration point "record number at now - 7 days" found from a window
  of index entries just before that time, taken as the minimum record of the
  window (safe: one prompt lookup in the window is enough), and how far the
  window's records spread;
- how many sampled entries the clock classifies differently from their
  first-seen time.

Usage: clock.py [per-slot-sample]  (writes results/clock.jsonl)
"""

import concurrent.futures as cf
import datetime as dt
import json
import os
import random
import sys
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "results")
NOW = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
MIN_AGE = dt.timedelta(days=7)
random.seed(76)


def get(url, timeout=60):
    req = urllib.request.Request(url, headers={"User-Agent": "wraithbox-x21-spike (I76)"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read().decode()


def esc(p):
    return "".join("!" + c.lower() if c.isupper() else c for c in p)


def ts(s):
    return dt.datetime.fromisoformat(s.replace("Z", "+00:00"))


def iso(t):
    return t.strftime("%Y-%m-%dT%H:%M:%S.%fZ")


def tree_size():
    return int(get("https://sum.golang.org/latest").splitlines()[1])


def index_since(t, limit=2000):
    return [json.loads(l) for l in get(f"https://index.golang.org/index?since={iso(t)}&limit={limit}").splitlines() if l]


def lookup(path, version):
    t0 = time.time()
    try:
        body = get(f"https://sum.golang.org/lookup/{esc(path)}@{esc(version)}")
        return int(body.splitlines()[0]), None, time.time() - t0
    except urllib.error.HTTPError as e:
        return None, f"HTTP {e.code}: {e.read()[:120].decode(errors='replace').strip()}", time.time() - t0
    except Exception as e:  # noqa: BLE001
        return None, str(e), time.time() - t0


def calibrate(at, window=dt.timedelta(minutes=10), margin=dt.timedelta(minutes=1), k=20):
    """Record number at time `at`: the minimum record of up to k entries first
    seen in [at - window, at - margin]. Returns (point, records, tree size before)."""
    entries = [e for e in index_since(at - window, 400) if ts(e["Timestamp"]) <= at - margin]
    if not entries:
        raise RuntimeError(f"no index entries before {at}")
    pick = entries[-k:]  # the ones closest to `at`
    before = tree_size()
    with cf.ThreadPoolExecutor(8) as ex:
        recs = list(ex.map(lambda e: lookup(e["Path"], e["Version"]), pick))
    nums = [r for r, err, _ in recs if r is not None]
    if not nums:
        raise RuntimeError("no record numbers in the calibration window")
    return min(nums), [(e["Timestamp"], r, r is not None and r >= before) for e, (r, _, _) in zip(pick, recs)], before


def main():
    per_slot = int(sys.argv[1]) if len(sys.argv) > 1 else 6
    os.makedirs(OUT, exist_ok=True)
    slots = [NOW - dt.timedelta(hours=h) for h in range(2, 16 * 24, 6)]
    sample = []
    for s in slots:
        entries = index_since(s, 300)
        for e in random.sample(entries, min(per_slot, len(entries))):
            sample.append(e)
    before = tree_size()
    t0 = time.time()
    with cf.ThreadPoolExecutor(8) as ex:
        res = list(ex.map(lambda e: lookup(e["Path"], e["Version"]), sample))
    took = time.time() - t0
    rows = []
    for e, (rec, err, sec) in zip(sample, res):
        rows.append({"first_seen": e["Timestamp"], "path": e["Path"], "version": e["Version"],
                     "record": rec, "err": err, "new": rec is not None and rec >= before, "sec": round(sec, 3)})
    with open(os.path.join(OUT, "clock.jsonl"), "w") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")

    ok = [r for r in rows if r["record"] is not None]
    new = [r for r in ok if r["new"]]
    errs = [r for r in rows if r["record"] is None]
    lat = sorted(r["sec"] for r in rows)
    print(f"now {iso(NOW)}; tree size before sampling {before}")
    print(f"sampled {len(rows)} index entries over 16 days in {took:.1f}s (8 at once); "
          f"lookup median {lat[len(lat)//2]*1000:.0f} ms, p95 {lat[int(len(lat)*.95)]*1000:.0f} ms")
    print(f"errors: {len(errs)}", sorted({r['err'][:60] for r in errs})[:6])
    print(f"no record until this lookup created one: {len(new)} of {len(ok)}")
    old_new = [r for r in new if NOW - ts(r["first_seen"]) >= MIN_AGE]
    print(f"  of those, first seen more than 7 days ago: {len(old_new)}")

    # Order: among prompt records, count pairs in time order whose records are inverted
    prompt = sorted([r for r in ok if not r["new"]], key=lambda r: r["first_seen"])
    inv = 0
    worst = dt.timedelta(0)
    for i, a in enumerate(prompt):
        for b in prompt[i + 1:]:
            if b["record"] < a["record"]:
                inv += 1
                worst = max(worst, ts(b["first_seen"]) - ts(a["first_seen"]))
    print(f"prompt records: {len(prompt)}; inverted pairs {inv}; widest inversion in first-seen time {worst}")

    # Calibration at now - 7 days, and its spread
    point, window, cbefore = calibrate(NOW - MIN_AGE)
    wr = [r for _, r, _ in window if r is not None]
    print(f"calibration at {iso(NOW - MIN_AGE)}: point {point}; window of {len(window)}: "
          f"min {min(wr)} median {sorted(wr)[len(wr)//2]} max {max(wr)}; created by this lookup {sum(1 for *_, n in window if n)}")
    # Repeat a few minutes apart to see stability
    for minutes in (30, 60):
        p2, w2, _ = calibrate(NOW - MIN_AGE - dt.timedelta(minutes=minutes))
        print(f"calibration at now - 7d - {minutes} min: point {p2} (difference {point - p2})")

    # Classification: first-seen vs clock
    disagree_strict = [r for r in prompt if NOW - ts(r["first_seen"]) >= MIN_AGE and r["record"] >= point]
    disagree_lenient = [r for r in ok if NOW - ts(r["first_seen"]) < MIN_AGE and r["record"] < point]
    print(f"first seen >= 7 days ago but young by the clock (prompt records): {len(disagree_strict)}")
    for r in disagree_strict[:5]:
        print("   ", r["first_seen"], r["record"], r["path"] + "@" + r["version"])
    print(f"first seen < 7 days ago but old by the clock: {len(disagree_lenient)}")
    for r in disagree_lenient[:5]:
        print("   ", r["first_seen"], r["record"], r["path"] + "@" + r["version"])
    rate = (tree_size() - point) / MIN_AGE.total_seconds()
    print(f"records added per second over the last 7 days: {rate:.2f}")


if __name__ == "__main__":
    main()
