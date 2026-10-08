#!/usr/bin/env python3
"""X04-tls-inspection (copied from X20-shared-homebrew): send one request to x17-guestd through `x04 serve`. Throwaway.

req.py <unix socket> run <script file> [timeout] [VAR=value ...]
req.py <unix socket> runs '<script text>' [timeout]
req.py <unix socket> put <local file> <guest path> <mode>
req.py <unix socket> hello | shutdown

VAR=value pairs are prepended to the script as shell assignments.
Prints the answer as one JSON line, and the script's stdout and stderr.
Appends request and answer to the file named by $X04_LOG, if set.
"""
import base64
import json
import os
import socket
import sys
import time


def main():
    sock, op = sys.argv[1], sys.argv[2]
    if op == "run" or op == "runs":
        script = open(sys.argv[3]).read() if op == "run" else sys.argv[3]
        timeout = int(sys.argv[4]) if len(sys.argv) > 4 else 3600
        pre = "".join("%s='%s'\n" % tuple(a.split("=", 1)) for a in sys.argv[5:])
        req = {"op": "run", "script": pre + script, "timeout": timeout}
        label = sys.argv[3] if op == "run" else "inline"
    elif op == "put":
        req = {"op": "put", "path": sys.argv[4], "mode": sys.argv[5],
               "b64": base64.b64encode(open(sys.argv[3], "rb").read()).decode()}
        label = sys.argv[4]
    else:
        req = {"op": op}
        label = op
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.connect(sock)
    t0 = time.time()
    s.sendall((json.dumps(req) + "\n").encode())
    buf = b""
    while not buf.endswith(b"\n"):
        chunk = s.recv(1 << 20)
        if not chunk:
            break
        buf += chunk
    secs = time.time() - t0
    resp = json.loads(buf) if buf.strip() else {"error": "no answer"}
    rec = {"label": label, "hostSeconds": round(secs, 1), "resp": resp,
           "at": time.strftime("%Y-%m-%dT%H:%M:%S")}
    if os.environ.get("X04_LOG"):
        with open(os.environ["X04_LOG"], "a") as f:
            f.write(json.dumps(rec) + "\n")
    print(json.dumps({k: v for k, v in resp.items() if k not in ("out", "err")}), "host %.1fs" % secs)
    if resp.get("out"):
        print(resp["out"], end="")
    if resp.get("err"):
        print("--- stderr\n" + resp["err"], end="")
    sys.exit(0 if resp.get("exit", 0) == 0 and "error" not in resp else 1)


main()
