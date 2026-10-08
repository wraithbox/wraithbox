#!/usr/bin/env python3
"""Throwaway (I76): Go installs through the spike proxy with the
checksum-database clock (-go-clock sumdb) and GOSUMDB on.

The go command reaches sum.golang.org through the proxy
(GOPROXY=http://<addr>/goproxy serves /sumdb/sum.golang.org/...), so every
download is checked against the signed checksum database as usual, and the
gate's own lookups run in the proxy.

Usage:
  goclock.py installs [modes]       lockfile and fresh installs
  goclock.py unreachable            index.golang.org unreachable: fail closed
  goclock.py lists [full|lazy]      cost of filtering @v/list
  goclock.py pseudo N               go get of N pseudo-versions of old commits
  goclock.py baseline               lockfile installs straight from proxy.golang.org, no gate
"""

import json
import os
import random
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
PROXY = os.path.join(ROOT, ".scratch", "x21proxy-go")
RESULTS = os.path.join(HERE, "results")
RAW = "https://raw.githubusercontent.com/{repo}/{ref}/{f}"

# go.mod + go.sum at pinned commits (2026-10-08, cli/cli as in othereco.py)
LOCK = {
    "cli/cli": "6fc1c29d5477bfe71da7af290eb481c0df7811f1",
    "junegunn/fzf": "b1be3a8be1b833ce5b92fbbac11637643d60a046",
    "caddyserver/caddy": "4f45ded75556f6645075ddb8724a6d79cc1e5870",
    "prometheus/node_exporter": "1271bc244266457fd225dcef9b8a33641b582c64",
    "gohugoio/hugo": "ac62c804c329bcbd74975c4753d9b6c76d89bf19",
}
GO_FRESH = ["github.com/aws/aws-sdk-go-v2/service/s3@latest", "golang.org/x/net@latest",
            "google.golang.org/grpc@latest", "github.com/spf13/cobra@latest", "k8s.io/client-go@latest"]
LIST_MODULES = ["github.com/aws/aws-sdk-go", "github.com/aws/aws-sdk-go-v2/service/s3", "k8s.io/client-go",
                "google.golang.org/grpc", "github.com/spf13/cobra", "golang.org/x/net",
                "github.com/hashicorp/terraform", "github.com/prometheus/client_golang"]
# Repositories for old-commit pseudo-versions: (module, GitHub repo)
PSEUDO_REPOS = [("github.com/spf13/cobra", "spf13/cobra"), ("github.com/gorilla/mux", "gorilla/mux"),
                ("github.com/sirupsen/logrus", "sirupsen/logrus"), ("github.com/stretchr/testify", "stretchr/testify"),
                ("github.com/urfave/cli", "urfave/cli"), ("github.com/BurntSushi/toml", "BurntSushi/toml")]


def start(mode, log, extra=()):
    args = [PROXY, "-mode", mode, "-log", log, "-ca-out", os.path.join(os.path.dirname(log), "ca.pem"),
            "-go-clock", "sumdb", *extra]
    p = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    return p, p.stdout.readline().strip()


def stats(addr):
    return urllib.request.build_opener(urllib.request.ProxyHandler({})).open(f"http://{addr}/x21/clock-stats").read().decode().strip()


def go_env(addr, tmp):
    e = {k: v for k, v in os.environ.items() if not k.lower().endswith("_proxy") and not k.startswith("GO")}
    e.update({"GOPROXY": f"http://{addr}/goproxy", "GOSUMDB": "sum.golang.org", "GONOSUMDB": "", "GOPRIVATE": "",
              "GOFLAGS": "-mod=mod", "GOTOOLCHAIN": "local", "GOMODCACHE": os.path.join(tmp, "gomod"),
              "GOPATH": os.path.join(tmp, "gopath"), "GOCACHE": os.path.join(tmp, "gocache")})
    return e


def events(log):
    return [json.loads(l) for l in open(log) if l.strip()]


def run(name, mode, setup, cmds, extra=()):
    tmp = tempfile.mkdtemp(prefix="x21-i76-")
    os.makedirs(RESULTS, exist_ok=True)
    log = os.path.join(RESULTS, f"i76.{name}.{mode}.jsonl")
    if os.path.exists(log):
        os.remove(log)
    p, addr = start(mode, log, extra)
    try:
        work = os.path.join(tmp, "work")
        os.makedirs(work)
        setup(work)
        env = go_env(addr, tmp)
        t0 = time.time()
        rc, out = 0, ""
        for c in cmds:
            r = subprocess.run(c, cwd=work, env=env, capture_output=True, text=True, timeout=1800)
            out += r.stdout[-2000:] + r.stderr[-4000:]
            rc = r.returncode
            if rc != 0:
                break
        sec = time.time() - t0
        st = stats(addr)
    finally:
        p.terminate()
        p.wait()
        subprocess.run(["chmod", "-R", "u+w", tmp])
        shutil.rmtree(tmp, ignore_errors=True)
    ev = events(log)
    dl = [e for e in ev if e.get("kind") == "download"]
    young = sorted({(e["name"], e["version"]) for e in dl if e.get("rule") == "min-age"})
    refused = [e for e in ev if e.get("decision") == "refuse"]
    lists = [e for e in ev if e.get("kind") == "metadata" and e.get("decision") == "filter"]
    sumdb = [e for e in ev if e.get("kind") == "sumdb" and e["path"].startswith("/lookup/")]
    res = {"name": name, "mode": mode, "rc": rc, "seconds": round(sec, 1),
           "downloads": len({(e["name"], e["version"]) for e in dl}), "young": len(young),
           "refused": len(refused), "hidden": sum(e.get("removed", 0) for e in lists),
           "lists": len(lists), "go_sumdb_lookups": len(sumdb), "proxy": st}
    print(json.dumps(res), flush=True)
    for n, v in young[:8]:
        print("    young:", n, v)
    for e in refused[:5]:
        print("    refused:", e.get("name"), e.get("version"), e.get("rule", "")[:140])
    if rc != 0:
        print("   ", out[-600:].replace("\n", "\n    "))
    return res


def fetch_lock(repo, ref):
    def setup(work):
        for f in ("go.mod", "go.sum"):
            with urllib.request.urlopen(RAW.format(repo=repo, ref=ref, f=f)) as r:
                open(os.path.join(work, f), "wb").write(r.read())
    return setup


def fresh(work):
    open(os.path.join(work, "go.mod"), "w").write("module example.com/x21\n\ngo 1.27\n")


def installs(modes):
    for repo, ref in LOCK.items():
        for m in modes:
            run("lock-" + repo.replace("/", "_"), m, fetch_lock(repo, ref), [["go", "mod", "download"]])
    for m in modes:
        run("fresh", m, fresh, [["go", "get", *GO_FRESH], ["go", "mod", "download"]])


def unreachable():
    bad = ("-index-url", "http://127.0.0.1:9")
    run("unreachable-lock", "filter", fetch_lock("cli/cli", LOCK["cli/cli"]), [["go", "mod", "download"]], bad)
    run("unreachable-fresh", "filter", fresh, [["go", "get", *GO_FRESH]], bad)
    # Calibrated once, then the index goes away and every later request tries
    # to recalibrate: the stale point is kept.
    stale = ("-index-break-after-first", "-cal-refresh", "1ns")
    run("stale-lock", "filter", fetch_lock("cli/cli", LOCK["cli/cli"]), [["go", "mod", "download"]], stale)
    run("stale-fresh", "filter", fresh, [["go", "get", *GO_FRESH], ["go", "mod", "download"]], stale)


def lists(kind):
    log = os.path.join(RESULTS, f"i76.lists.{kind}.jsonl")
    os.makedirs(RESULTS, exist_ok=True)
    if os.path.exists(log):
        os.remove(log)
    extra = ("-list-lazy",) if kind == "lazy" else ()
    p, addr = start("filter", log, extra)
    op = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        op.open(f"http://{addr}/goproxy/golang.org/x/text/@v/list").read()  # calibrate first
        for m in LIST_MODULES:
            esc = "".join("!" + c.lower() if c.isupper() else c for c in m)
            t0 = time.time()
            body = op.open(f"http://{addr}/goproxy/{esc}/@v/list").read().decode()
            print(json.dumps({"module": m, "kept": len(body.split()), "seconds": round(time.time() - t0, 2)}), flush=True)
        print(stats(addr))
    finally:
        p.terminate()
        p.wait()
    for e in events(log):
        if e.get("kind") == "metadata":
            print(f"  {e['name']}: listed {e.get('removed', 0) + e.get('kept', 0)}, hidden {e.get('removed', 0)}, "
                  f"lookups {int(e.get('osv_ms', 0))}, {e.get('ms', 0) / 1000:.2f}s")


def pseudo(n):
    random.seed(76)
    picks = []
    for mod, repo in PSEUDO_REPOS:
        out = subprocess.run(["gh", "api", f"repos/{repo}/commits?until=2022-01-01T00:00:00Z&per_page=100",
                              "-q", ".[].sha"], capture_output=True, text=True).stdout.split()
        picks += [(mod, sha[:12]) for sha in random.sample(out, min(n, len(out)))]
    for m in ("refuse",):
        def setup(work):
            fresh(work)
        # One go get per commit, so that one refusal doesn't hide the others.
        tmp_results = []
        for mod, sha in picks:
            r = run(f"pseudo-{mod.split('/')[-1]}-{sha}", m, setup, [["go", "get", f"{mod}@{sha}"]])
            tmp_results.append(r)
        ok = sum(1 for r in tmp_results if r["rc"] == 0)
        print(f"pseudo-versions of commits before 2022: {len(picks)}, installed {ok}, refused {len(picks) - ok}")


def baseline():
    for repo, ref in LOCK.items():
        tmp = tempfile.mkdtemp(prefix="x21-i76-")
        try:
            work = os.path.join(tmp, "work")
            os.makedirs(work)
            fetch_lock(repo, ref)(work)
            env = go_env("unused", tmp)
            env["GOPROXY"] = "https://proxy.golang.org"
            t0 = time.time()
            r = subprocess.run(["go", "mod", "download"], cwd=work, env=env, capture_output=True, text=True, timeout=1800)
            print(json.dumps({"name": "lock-" + repo.replace("/", "_"), "mode": "no gate", "rc": r.returncode,
                              "seconds": round(time.time() - t0, 1)}), flush=True)
        finally:
            subprocess.run(["chmod", "-R", "u+w", tmp])
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "installs":
        installs(sys.argv[2].split(",") if len(sys.argv) > 2 else ["off", "refuse", "filter"])
    elif cmd == "unreachable":
        unreachable()
    elif cmd == "lists":
        lists(sys.argv[2] if len(sys.argv) > 2 else "full")
    elif cmd == "baseline":
        baseline()
    elif cmd == "pseudo":
        pseudo(int(sys.argv[2]) if len(sys.argv) > 2 else 3)
