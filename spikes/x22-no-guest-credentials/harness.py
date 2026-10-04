#!/usr/bin/env python3
"""Throwaway harness for X22-no-guest-credentials.

Runs each client's common read operations through the spike proxy, once
per credential rule, and records whether it worked. Every client runs
with a fresh cache and home in a temp dir, and with the throwaway CA
trusted only for that process. No account is used: "placeholder" variants
configure a fake token, the way a guest holds only placeholders.

usage: harness.py [mode ...] [-k op-substring]
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
PROXY = os.path.join(ROOT, ".scratch", "x22proxy")
RESULTS = os.path.join(HERE, "results")
FAKE = "wbplaceholder0000000000000000000000000000"
MODES = ["observe", "strip", "read-only", "issued", "inject", "inject-flow"]


def start_proxy(mode, logfile, ca, label):
    args = [PROXY, "-ca-out", ca, "-log", logfile, "-label", label]
    if mode == "inject-flow":
        args += ["-mode", "inject", "-inject-kind", "flow"]
    else:
        args += ["-mode", mode]
    p = subprocess.Popen(args, stdout=subprocess.PIPE, text=True)
    addr = p.stdout.readline().strip()
    return p, addr


def base_env(addr, ca, tmp):
    home = os.path.join(tmp, "home")
    os.makedirs(home, exist_ok=True)
    keep = ["PATH", "TERM", "LANG", "USER", "LOGNAME", "TMPDIR", "SHELL"]
    env = {k: os.environ[k] for k in keep if k in os.environ}
    proxy = f"http://{addr}"
    env.update({
        "HOME": home,
        "HTTPS_PROXY": proxy, "https_proxy": proxy,
        "HTTP_PROXY": proxy, "http_proxy": proxy,
        "NO_PROXY": "", "no_proxy": "",
        "SSL_CERT_FILE": ca,
        "NODE_EXTRA_CA_CERTS": ca,
        "GIT_SSL_CAINFO": ca,
        "CURL_CA_BUNDLE": ca,
        "PIP_CERT": ca,
        "REQUESTS_CA_BUNDLE": ca,
        "CARGO_HTTP_CAINFO": ca,
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_CONFIG_NOSYSTEM": "1",
    })
    # mise shims need their own dirs to find the pinned tools.
    for k in ["MISE_DATA_DIR", "MISE_CONFIG_DIR", "MISE_CACHE_DIR"]:
        if k in os.environ:
            env[k] = os.environ[k]
    env.setdefault("MISE_DATA_DIR", os.path.expanduser("~/.local/share/mise"))
    env.setdefault("MISE_CONFIG_DIR", os.path.expanduser("~/.config/mise"))
    env["RUSTUP_HOME"] = os.environ.get("RUSTUP_HOME", os.path.expanduser("~/.rustup"))
    return env


def w(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(text)


# Each op: (name, setup(env, tmp, addr) -> argv list of commands)
def op_brew(env, tmp, addr, placeholder):
    curlrc = os.path.join(tmp, "curlrc")
    w(curlrc, f'cacert = "{env["SSL_CERT_FILE"]}"\n')
    env.update({
        "HOMEBREW_CACHE": os.path.join(tmp, "brewcache"),
        "HOMEBREW_NO_AUTO_UPDATE": "1", "HOMEBREW_NO_ANALYTICS": "1",
        "HOMEBREW_NO_INSTALL_CLEANUP": "1", "HOMEBREW_NO_ENV_HINTS": "1",
        "HOMEBREW_CURLRC": curlrc,
        "HOMEBREW_LOGS": os.path.join(tmp, "brewlogs"),
    })
    if placeholder:
        env["HOMEBREW_GITHUB_API_TOKEN"] = FAKE
    return [["brew", "fetch", "--force", "wget"], ["brew", "fetch", "--force", "jq"]]


def op_git_clone(env, tmp, addr, placeholder):
    url = "https://github.com/octocat/Hello-World.git"
    if placeholder:
        url = f"https://x-access-token:{FAKE}@github.com/octocat/Hello-World.git"
    return [["git", "clone", "--depth", "1", url, os.path.join(tmp, "hw")]]


def op_git_push(env, tmp, addr, placeholder):
    # A write with a token the guest holds: must never reach GitHub with it.
    d = os.path.join(tmp, "hw")
    url = f"https://x-access-token:{FAKE}@github.com/octocat/Hello-World.git"
    return [["git", "clone", "--depth", "1", "https://github.com/octocat/Hello-World.git", d],
            ["git", "-C", d, "push", "--dry-run", url, "HEAD:refs/heads/x22-spike"]]


def op_git_lfs(env, tmp, addr, placeholder):
    d = os.path.join(tmp, "lfs")
    return [["git", "lfs", "install", "--skip-repo"],
            ["git", "clone", "--depth", "1", LFS_REPO, d],
            ["git", "-C", d, "lfs", "ls-files", "--size"]]


LFS_REPO = os.environ.get("X22_LFS_REPO", "")


def op_gh(env, tmp, addr, placeholder):
    env["GH_CONFIG_DIR"] = os.path.join(tmp, "ghcfg")
    env["GH_NO_UPDATE_NOTIFIER"] = "1"
    env["GH_PROMPT_DISABLED"] = "1"
    if placeholder:
        env["GH_TOKEN"] = FAKE
    return [["gh", "api", "repos/cli/cli", "--jq", ".full_name"],
            ["gh", "release", "view", "--repo", "cli/cli", "--json", "tagName", "--jq", ".tagName"]]


def op_npm(env, tmp, addr, placeholder):
    env.update({"npm_config_cache": os.path.join(tmp, "npmcache"),
                "npm_config_update_notifier": "false", "npm_config_fund": "false",
                "npm_config_audit": "false",
                "npm_config_userconfig": os.path.join(tmp, "npmrc")})
    w(os.path.join(tmp, "npmrc"), f"//registry.npmjs.org/:_authToken={FAKE}\n" if placeholder else "")
    p = os.path.join(tmp, "proj")
    os.makedirs(p)
    return [["npm", "view", "is-number", "version"],
            ["npm", "install", "--prefix", p, "is-number@7", "chalk@5"]]


def op_pip(env, tmp, addr, placeholder):
    env["PIP_NO_CACHE_DIR"] = "1"
    env["PIP_DISABLE_PIP_VERSION_CHECK"] = "1"
    if placeholder:
        env["PIP_INDEX_URL"] = f"https://__token__:{FAKE}@pypi.org/simple"
    return [[sys.executable, "-m", "pip", "download", "--no-deps", "--only-binary=:all:",
             "-d", os.path.join(tmp, "dl"), "six", "idna"]]


def op_uv(env, tmp, addr, placeholder):
    env["UV_CACHE_DIR"] = os.path.join(tmp, "uvcache")
    env["UV_NO_BUILD"] = "1"
    env["UV_PYTHON_DOWNLOADS"] = "never"
    if placeholder:
        env["UV_INDEX_URL"] = f"https://__token__:{FAKE}@pypi.org/simple"
    return [["uv", "pip", "install", "--python", sys.executable, "--target",
             os.path.join(tmp, "tgt"), "six", "idna"]]


def op_go(env, tmp, addr, placeholder):
    m = os.path.join(tmp, "mod")
    w(os.path.join(m, "go.mod"), "module x22\n\ngo 1.24\n\nrequire golang.org/x/text v0.14.0\n")
    env.update({"GOPATH": os.path.join(tmp, "gopath"), "GOMODCACHE": os.path.join(tmp, "gomod"),
                "GOFLAGS": "-modcacherw", "GOTOOLCHAIN": "local",
                "GOPROXY": f"http://{addr}/r/proxy.golang.org",
                "GOCACHE": os.path.join(tmp, "gocache")})
    if placeholder:
        host = addr.split(":")[0]
        w(os.path.join(env["HOME"], ".netrc"), f"machine {host} login x password {FAKE}\n")
    return [["sh", "-c", f"cd {m} && go mod download -x golang.org/x/text 2>&1 | tail -5 && go mod verify"]]


def op_go_direct(env, tmp, addr, placeholder):
    # Go through HTTPS_PROXY with the CA from SSL_CERT_FILE (macOS ignores it).
    m = os.path.join(tmp, "mod")
    w(os.path.join(m, "go.mod"), "module x22\n\ngo 1.24\n")
    env.update({"GOPATH": os.path.join(tmp, "gopath"), "GOMODCACHE": os.path.join(tmp, "gomod"),
                "GOFLAGS": "-modcacherw", "GOTOOLCHAIN": "local", "GOCACHE": os.path.join(tmp, "gocache")})
    return [["sh", "-c", f"cd {m} && go mod download golang.org/x/text@v0.14.0"]]


def op_cargo(env, tmp, addr, placeholder):
    p = os.path.join(tmp, "crate")
    w(os.path.join(p, "Cargo.toml"), '[package]\nname = "x22"\nversion = "0.1.0"\nedition = "2021"\n\n[dependencies]\nitoa = "1"\nmemchr = "2"\n')
    w(os.path.join(p, "src", "main.rs"), "fn main() {}\n")
    env["CARGO_HOME"] = os.path.join(tmp, "cargohome")
    env["PATH"] = os.path.expanduser("~/.rustup/toolchains/stable-aarch64-apple-darwin/bin") + ":" + env["PATH"]
    env["CARGO_HTTP_PROXY"] = f"http://{addr}"
    if placeholder:
        env["CARGO_REGISTRY_TOKEN"] = FAKE
    return [[os.path.expanduser("~/.rustup/toolchains/stable-aarch64-apple-darwin/bin/cargo"), "fetch", "--manifest-path", os.path.join(p, "Cargo.toml")]]


def op_swiftpm(env, tmp, addr, placeholder):
    p = os.path.join(tmp, "pkg")
    w(os.path.join(p, "Package.swift"), """// swift-tools-version:5.9
import PackageDescription
let package = Package(
  name: "X22",
  dependencies: [.package(url: "https://github.com/apple/swift-argument-parser", from: "1.3.0")],
  targets: [.executableTarget(name: "X22", dependencies: [.product(name: "ArgumentParser", package: "swift-argument-parser")])]
)
""")
    w(os.path.join(p, "Sources", "X22", "main.swift"), "print(1)\n")
    return [["swift", "package", "--package-path", p, "--scratch-path", os.path.join(tmp, "build"),
             "--cache-path", os.path.join(tmp, "spmcache"), "--disable-dependency-cache", "resolve"]]


def op_claude(env, tmp, addr, placeholder):
    # Not a login test (X01-model-credential). Placeholder API key only.
    env["CLAUDE_CONFIG_DIR"] = os.path.join(tmp, "claudecfg")
    env["ANTHROPIC_API_KEY"] = "sk-ant-api03-" + FAKE
    env["DISABLE_AUTOUPDATER"] = "1"
    return [["claude", "-p", "--max-turns", "1", "say hi"]]


def op_oci_flow(env, tmp, addr, placeholder):
    return [["sh", os.path.join(HERE, "oci-flow.sh")]]


def op_attacker(env, tmp, addr, placeholder):
    return [["sh", os.path.join(HERE, "attacker.sh")]]


OPS = {
    "oci-flow": op_oci_flow,
    "attacker": op_attacker,
    "brew-fetch": op_brew,
    "git-clone": op_git_clone,
    "git-push-dry": op_git_push,
    "git-lfs": op_git_lfs,
    "gh": op_gh,
    "npm": op_npm,
    "pip": op_pip,
    "uv": op_uv,
    "go": op_go,
    "go-direct": op_go_direct,
    "cargo": op_cargo,
    "swiftpm": op_swiftpm,
    "claude": op_claude,
}


def run(opname, mode, placeholder):
    label = f"{opname}{'+ph' if placeholder else ''}"
    tmp = tempfile.mkdtemp(prefix="x22-")
    ca = os.path.join(tmp, "ca.pem")
    os.makedirs(RESULTS, exist_ok=True)
    logfile = os.path.join(RESULTS, f"{mode}.jsonl")
    proc, addr = start_proxy(mode, logfile, ca, label)
    try:
        env = base_env(addr, ca, tmp)
        cmds = OPS[opname](env, tmp, addr, placeholder)
        t0 = time.time()
        rc, out = 0, ""
        for c in cmds:
            try:
                r = subprocess.run(c, env=env, capture_output=True, text=True, timeout=600, cwd=tmp)
                rc, out = r.returncode, (r.stdout + r.stderr)
            except subprocess.TimeoutExpired:
                rc, out = 124, "timeout"
            if rc != 0:
                break
        dt = time.time() - t0
    finally:
        proc.terminate()
        proc.wait()
        shutil.rmtree(tmp, ignore_errors=True)
    tail = " | ".join(l.strip() for l in out.strip().splitlines()[-4:])[:400]
    res = {"op": label, "mode": mode, "rc": rc, "secs": round(dt, 1), "tail": tail}
    with open(os.path.join(RESULTS, "runs.jsonl"), "a") as f:
        f.write(json.dumps(res) + "\n")
    print(f"{label:18} {mode:12} rc={rc:<4} {dt:6.1f}s  {tail[:200]}", flush=True)
    return res


if __name__ == "__main__":
    args = sys.argv[1:]
    sel = None
    if "-k" in args:
        i = args.index("-k")
        sel = args[i + 1].split(",")
        args = args[:i] + args[i + 2:]
    modes = args or MODES
    for opname in OPS:
        if sel and opname not in sel:
            continue
        for ph in (False, True):
            for mode in modes:
                # Claude Code retries a 401 for about three minutes, and its
                # model API always has a binding: two modes are enough.
                if opname == "claude" and mode not in ("observe", "strip"):
                    continue
                run(opname, mode, ph)
