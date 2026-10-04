#!/usr/bin/env python3
"""Throwaway harness for X21-dep-gate-registries.

Starts the spike proxy in a mode, runs one install of one project through
it in a fresh temp dir (outside the worktree), and records the result.
The throwaway CA is trusted only per process (NODE_EXTRA_CA_CERTS,
SSL_CERT_FILE, PIP_CERT) and deleted afterwards.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
SCRATCH = os.path.join(ROOT, ".scratch")
PROXY = os.path.join(SCRATCH, "x21proxy")
INPUTS = os.path.join(SCRATCH, "inputs")
RESULTS = os.path.join(SCRATCH, "results")
PY = shutil.which("python3")

# project id -> (ecosystem, repo, ref, files)
PROJECTS = json.load(open(os.path.join(HERE, "projects.json")))


def fetch_inputs():
    os.makedirs(INPUTS, exist_ok=True)
    for pid, p in PROJECTS.items():
        d = os.path.join(INPUTS, pid)
        os.makedirs(d, exist_ok=True)
        for src, f in p["files"]:
            dst = os.path.join(d, f)
            if os.path.exists(dst):
                continue
            url = f"https://raw.githubusercontent.com/{p['repo']}/{p['ref']}/{src}"
            print("fetch", url)
            with urllib.request.urlopen(url) as r, open(dst, "wb") as out:
                out.write(r.read())


def start_proxy(mode, logfile, ca, osv="off", osv_cache="", now=""):
    args = [PROXY, "-mode", mode, "-ca-out", ca, "-log", logfile, "-osv", osv]
    if osv_cache:
        args += ["-osv-cache", osv_cache]
    if now:
        args += ["-now", now]
    if os.environ.get("X21_OSV_OVERLAP"):
        args += ["-osv-overlap"]
    p = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    addr = p.stdout.readline().strip()
    return p, addr


def env_for(addr, ca, tmp):
    e = {k: v for k, v in os.environ.items() if not k.lower().endswith("_proxy")}
    proxy = f"http://{addr}"
    e.update({
        "HTTPS_PROXY": proxy, "https_proxy": proxy, "HTTP_PROXY": proxy, "http_proxy": proxy,
        "NO_PROXY": "", "no_proxy": "",
        # npm
        "npm_config_proxy": proxy, "npm_config_https_proxy": proxy,
        "npm_config_cache": os.path.join(tmp, "npm-cache"),
        "npm_config_userconfig": os.path.join(tmp, "npmrc"),
        "npm_config_registry": "https://registry.npmjs.org/",
        "npm_config_update_notifier": "false",
        "NODE_EXTRA_CA_CERTS": ca,
        # uv
        "UV_CACHE_DIR": os.path.join(tmp, "uv-cache"),
        "UV_NO_CONFIG": "1", "UV_PYTHON_DOWNLOADS": "never", "UV_PYTHON": PY,
        "SSL_CERT_FILE": ca,
        "UV_HTTP_TIMEOUT": "120",
        # pip
        "PIP_CERT": ca, "PIP_NO_CACHE_DIR": "1", "PIP_CONFIG_FILE": os.devnull,
        "PIP_DISABLE_PIP_VERSION_CHECK": "1",
        # go
        "GOPROXY": "https://proxy.golang.org", "GOFLAGS": "-mod=mod",
        "GOMODCACHE": os.path.join(tmp, "gomod"), "GOPATH": os.path.join(tmp, "gopath"),
        "GONOSUMDB": "", "GOSUMDB": "sum.golang.org",
        # cargo
        "CARGO_HOME": os.path.join(tmp, "cargo-home"),
        "CARGO_HTTP_CAINFO": ca,
    })
    return e


def commands(p, kind):
    eco = p["eco"]
    if eco == "npm":
        base = ["npm", "--ignore-scripts", "--no-audit", "--no-fund", "--loglevel=error"]
        return [base + (["ci"] if kind == "lock" else ["install"])]
    if eco == "uv":
        c = ["uv", "sync", "--no-install-project", "--all-groups"]
        if p.get("extras"):
            c += ["--all-extras"]
        return [c + (["--frozen"] if kind == "lock" else [])]
    if eco == "pip":
        req = "requirements.txt" if kind == "lock" else "requirements.in"
        return [[PY, "-m", "venv", ".venv"], [".venv/bin/python", "-m", "pip", "install", "-r", req]]
    raise ValueError(eco)


def prepare(p, pid, kind, tmp):
    src = os.path.join(INPUTS, pid)
    work = os.path.join(tmp, "work")
    os.makedirs(work)
    for f in os.listdir(src):
        shutil.copy(os.path.join(src, f), work)
    if kind == "fresh":
        for lock in ("package-lock.json", "uv.lock"):
            if os.path.exists(os.path.join(work, lock)):
                os.remove(os.path.join(work, lock))
    if p["eco"] == "uv" and kind == "fresh":
        # Without a lockfile uv needs the project's version, and a dynamic
        # version needs the project's source, which isn't copied.
        pp = os.path.join(work, "pyproject.toml")
        s = open(pp).read()
        s = s.replace('dynamic = ["version"]', 'version = "0.0.0"')
        open(pp, "w").write(s)
    if p["eco"] == "pip":
        # Drop editable/local lines (`-e .`): only registry packages are measured.
        txt = os.path.join(work, "requirements.txt")
        keep = [l for l in open(txt) if not l.strip().startswith("-e")]
        open(txt, "w").write("".join(keep))
        inp = os.path.join(work, "requirements.in")
        if os.path.exists(inp):
            keep = [l for l in open(inp) if not l.strip().startswith(("-e", "-c", "-r"))]
            open(inp, "w").write("".join(keep))
        else:
            # strip pins: requirements.in is requirements.txt without ==versions
            lines = []
            for line in open(txt):
                line = line.split("#")[0].strip()
                if not line or line.startswith("-"):
                    continue
                lines.append(line.split("==")[0].split(";")[0].strip())
            open(inp, "w").write("\n".join(lines) + "\n")
    return work


def run_one(pid, kind, mode, osv="off", osv_cache="", tag=""):
    p = PROJECTS[pid]
    os.makedirs(RESULTS, exist_ok=True)
    name = f"{pid}.{kind}.{mode}" + (f".osv-{osv}" if osv != "off" else "") + (f".{tag}" if tag else "")
    logfile = os.path.join(RESULTS, name + ".jsonl")
    if os.path.exists(logfile):
        os.remove(logfile)
    tmp = tempfile.mkdtemp(prefix="x21-")
    ca = os.path.join(tmp, "ca.pem")
    proc, addr = start_proxy(mode, logfile, ca, osv, osv_cache)
    try:
        work = prepare(p, pid, kind, tmp)
        env = env_for(addr, ca, tmp)
        t0 = time.time()
        rc = 0
        out = ""
        for c in commands(p, kind):
            r = subprocess.run(c, cwd=work, env=env, capture_output=True, text=True, timeout=1800)
            out += r.stdout[-4000:] + r.stderr[-6000:]
            rc = r.returncode
            if rc != 0:
                break
        dt = time.time() - t0
        if osv_cache:
            try:
                urllib.request.build_opener(urllib.request.ProxyHandler({})).open(f"http://{addr}/x21/save-osv-cache").read()
            except Exception:
                pass
    finally:
        proc.terminate()
        proc.wait()
        shutil.rmtree(tmp, ignore_errors=True)  # removes installs, caches, and the CA
    res = {"project": pid, "kind": kind, "mode": mode, "osv": osv, "tag": tag, "rc": rc, "seconds": round(dt, 1), "tail": out[-1500:]}
    json.dump(res, open(os.path.join(RESULTS, name + ".result.json"), "w"), indent=1)
    print(json.dumps({k: v for k, v in res.items() if k != "tail"}), flush=True)
    return res


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "fetch":
        fetch_inputs()
    elif cmd == "run":
        pid, kind, mode = sys.argv[2:5]
        osv = sys.argv[5] if len(sys.argv) > 5 else "off"
        cache = sys.argv[6] if len(sys.argv) > 6 else ""
        tag = sys.argv[7] if len(sys.argv) > 7 else ""
        run_one(pid, kind, mode, osv, cache, tag)
    elif cmd == "show":
        print(json.load(open(os.path.join(RESULTS, sys.argv[2] + ".result.json")))["tail"])
    elif cmd == "matrix":
        pids = sys.argv[2].split(",") if len(sys.argv) > 2 else list(PROJECTS)
        for pid in pids:
            for kind in ("lock", "fresh"):
                for mode in ("off", "refuse", "filter"):
                    run_one(pid, kind, mode)
