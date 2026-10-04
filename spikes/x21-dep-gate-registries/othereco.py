#!/usr/bin/env python3
"""Throwaway: Go modules and crates through the spike proxy, all three modes.

go lock:     cli/cli go.mod + go.sum at a pinned commit, `go mod download`.
go fresh:    a module that `go get`s fast-moving modules @latest.
cargo lock:  sharkdp/bat Cargo.toml + Cargo.lock at a pinned commit, `cargo fetch --locked`.
cargo fresh: the same Cargo.toml without the lockfile, `cargo fetch`.
"""

import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import harness  # noqa: E402

ROOT = harness.ROOT
GHCLI = "https://raw.githubusercontent.com/cli/cli/6fc1c29d5477bfe71da7af290eb481c0df7811f1/{f}"
BAT = "https://raw.githubusercontent.com/sharkdp/bat/{ref}/{f}"
CARGO = os.path.expanduser("~/.rustup/toolchains/1.98.1-aarch64-apple-darwin/bin/cargo")
CARGO_FRESH = """[package]
name = "x21"
version = "0.1.0"
edition = "2024"

[dependencies]
tokio = { version = "1", features = ["full"] }
serde = { version = "1", features = ["derive"] }
serde_json = "1"
clap = { version = "4", features = ["derive"] }
reqwest = "0.12"
anyhow = "1"
regex = "1"
tracing = "0.1"
aws-sdk-s3 = "1"
"""
GO_FRESH = ["github.com/aws/aws-sdk-go-v2/service/s3@latest", "golang.org/x/net@latest",
            "google.golang.org/grpc@latest", "github.com/spf13/cobra@latest", "k8s.io/client-go@latest"]


def run(eco, kind, mode, bat_ref):
    tmp = tempfile.mkdtemp(prefix="x21-")
    ca = os.path.join(tmp, "ca.pem")
    logfile = os.path.join(harness.RESULTS, f"{eco}.{kind}.{mode}.jsonl")
    if os.path.exists(logfile):
        os.remove(logfile)
    proc, addr = harness.start_proxy(mode, logfile, ca)
    proc_args = None
    try:
        env = harness.env_for(addr, ca, tmp)
        work = os.path.join(tmp, "work")
        os.makedirs(work)
        if eco == "go":
            env["GOPROXY"] = f"http://{addr}/goproxy"
            env["GOFLAGS"] = "-mod=mod"
            env["GOTOOLCHAIN"] = "local"
            # proxy.golang.org does not proxy the checksum database, and Go on
            # macOS ignores SSL_CERT_FILE, so sum.golang.org can't be inspected
            # here. go.sum hashes are still checked for the lock case.
            env["GOSUMDB"] = "off"
            for k in ("HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"):
                env.pop(k)
            if kind == "lock":
                for f in ("go.mod", "go.sum"):
                    with urllib.request.urlopen(GHCLI.format(f=f)) as r:
                        open(os.path.join(work, f), "wb").write(r.read())
                cmds = [["go", "mod", "download"]]
            else:
                open(os.path.join(work, "go.mod"), "w").write("module example.com/x21\n\ngo 1.27\n")
                cmds = [["go", "get"] + GO_FRESH, ["go", "mod", "download"]]
        else:
            env["PATH"] = os.path.dirname(CARGO) + os.pathsep + env["PATH"]
            if kind == "lock":
                for f in ("Cargo.toml", "Cargo.lock"):
                    with urllib.request.urlopen(BAT.format(ref=bat_ref, f=f)) as r:
                        open(os.path.join(work, f), "wb").write(r.read())
            else:
                # bat's own manifest doesn't resolve fresh (a yanked crate), so a
                # manifest of popular crates with caret ranges instead.
                open(os.path.join(work, "Cargo.toml"), "w").write(CARGO_FRESH)
            os.makedirs(os.path.join(work, "src"))
            open(os.path.join(work, "src", "main.rs"), "w").write("fn main() {}\n")
            open(os.path.join(work, "src", "lib.rs"), "w").write("\n")
            cmds = [[CARGO, "fetch"] + (["--locked"] if kind == "lock" else [])]
        t0 = time.time()
        rc = 0
        out = ""
        for c in cmds:
            r = subprocess.run(c, cwd=work, env=env, capture_output=True, text=True, timeout=1800)
            out += r.stdout[-2000:] + r.stderr[-4000:]
            rc = r.returncode
            if rc != 0:
                break
        print({"eco": eco, "kind": kind, "mode": mode, "rc": rc, "seconds": round(time.time() - t0, 1)}, flush=True)
        if rc != 0:
            print("   ", out[-800:].replace("\n", "\n    "))
    finally:
        proc.terminate()
        proc.wait()
        subprocess.run(["chmod", "-R", "u+w", tmp])  # Go module cache is read-only
        shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    harness.PROXY = os.path.join(harness.SCRATCH, "x21proxy-go")
    bat_ref = sys.argv[1]
    for eco in sys.argv[2].split(",") if len(sys.argv) > 2 else ("go", "cargo"):
        for kind in ("lock", "fresh"):
            for mode in ("off", "refuse", "filter"):
                run(eco, kind, mode, bat_ref)
