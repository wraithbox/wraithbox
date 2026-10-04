#!/usr/bin/env python3
"""p50 and p95 of the restore and cold series. X18-vsock-handoff, throwaway."""
import json
import sys


def pct(xs, p):
    xs = sorted(xs)
    if not xs:
        return float("nan")
    k = (len(xs) - 1) * p / 100
    lo = int(k)
    hi = min(lo + 1, len(xs) - 1)
    return xs[lo] + (xs[hi] - xs[lo]) * (k - lo)


def main():
    for path in sys.argv[1:]:
        rows = [json.loads(line) for line in open(path) if line.strip()]
        ok = [r for r in rows if "error" not in r]
        print(f"{path}: {len(ok)} of {len(rows)} runs without error")
        keys = ["configMs", "restoreSeconds", "resumeMs", "resumedSeconds",
                "connectSeconds", "grpcSeconds", "netSeconds",
                "coldConnectSeconds", "coldGrpcSeconds", "connectTries", "tries"]
        for k in keys:
            xs = [r[k] for r in ok if isinstance(r.get(k), (int, float))]
            if xs:
                print(f"  {k:20s} n={len(xs):2d} p50={pct(xs, 50):9.3f} p95={pct(xs, 95):9.3f} min={min(xs):9.3f} max={max(xs):9.3f}")
        hs = [r["grpc"]["healthMs"] for r in ok if isinstance(r.get("grpc"), dict) and "healthMs" in r["grpc"]]
        if hs:
            print(f"  {'grpc.healthMs':20s} n={len(hs):2d} p50={pct(hs, 50):9.3f} p95={pct(hs, 95):9.3f} min={min(hs):9.3f} max={max(hs):9.3f}")


main()
