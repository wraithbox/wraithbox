#!/usr/bin/env python3
"""X06-guest-xcode: send one request to x06-guestd through `x06 serve`. Throwaway.

req.py <socket> <op> [--script F] [--user U] [--how setuid|asuser] [--pty]
       [--sandbox GUESTPATH] [--work GUESTPATH] [--env K=V]... [--timeout S]
       [--put LOCAL GUESTPATH MODE] [--log results/x.jsonl] [--label L]

Prints the answer's metadata, then its output. With --log, appends
{"label", "t", "req" (script text included), "resp"} as one JSON line.
"""
import argparse
import base64
import json
import socket
import sys
import time

p = argparse.ArgumentParser()
p.add_argument("sock")
p.add_argument("op")
p.add_argument("--script")
p.add_argument("--user")
p.add_argument("--how")
p.add_argument("--pty", action="store_true")
p.add_argument("--sandbox")
p.add_argument("--work")
p.add_argument("--env", action="append", default=[])
p.add_argument("--timeout", type=int)
p.add_argument("--put", nargs=3)
p.add_argument("--log")
p.add_argument("--label", default="")
a = p.parse_args()

r = {"op": a.op}
if a.script:
    r["script"] = open(a.script).read()
for k in ("user", "how", "sandbox", "work", "timeout"):
    if getattr(a, k):
        r[k] = getattr(a, k)
if a.pty:
    r["pty"] = True
if a.env:
    r["env"] = dict(e.split("=", 1) for e in a.env)
if a.put:
    r["path"] = a.put[1]
    r["mode"] = a.put[2]
    r["b64"] = base64.b64encode(open(a.put[0], "rb").read()).decode()

s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect(a.sock)
t0 = time.time()
s.sendall((json.dumps(r) + "\n").encode())
buf = b""
while not buf.endswith(b"\n"):
    chunk = s.recv(1 << 16)
    if not chunk:
        break
    buf += chunk
s.close()
dt = time.time() - t0
try:
    resp = json.loads(buf)
except ValueError:
    print("bad answer:", buf[:500], file=sys.stderr)
    sys.exit(1)
meta = {k: v for k, v in resp.items() if k not in ("out", "err")}
print("--- %s %.1fs %s" % (a.label, dt, meta))
if resp.get("out"):
    print(resp["out"].rstrip())
if resp.get("err"):
    print("stderr:", resp["err"].rstrip())
if a.log:
    logged = dict(r)
    if "b64" in logged:
        logged["b64"] = "<%d bytes>" % len(logged["b64"])
    with open(a.log, "a") as f:
        f.write(json.dumps({"label": a.label, "t": dt, "req": logged, "resp": resp}) + "\n")
sys.exit(0 if resp.get("exit", 0) == 0 and "error" not in resp else 1)
