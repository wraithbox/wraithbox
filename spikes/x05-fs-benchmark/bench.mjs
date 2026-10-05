// X05-fs-benchmark spike: the NFR02-fs-speed workloads, run the same way on
// the host and in the guest. Throwaway code, not held to the project gates.
//
//   node bench.mjs <tools dir> <work dir> <label> <runs> [--only a,b]
//
// <work dir> holds npm/proj (package.json + package-lock.json), npm/cache
// (every package, so npm ci runs offline), repo (a clone of nodejs/node),
// and gobuild (x05-netd with vendored modules). Prints one JSON line per
// timed run on stdout.
//
// Workloads:
//   npm-ci      rm -rf node_modules, then npm ci --offline
//   git-status  git status --porcelain in repo
//   go-incr     change one line in gobuild/edit.go, then go build
//   go-clean    go build with an empty GOCACHE (once per mode)
//   fsync       500 appends of 4 KiB, each followed by fsync (libuv uses
//               F_FULLFSYNC on macOS)
//   seqwrite    512 MiB in 1 MiB writes, then one fsync
// Modes: warm (caches as the previous run left them) and cold (`sudo -n
// purge` first, which drops this kernel's file cache; skipped where sudo
// needs a password, as on the host).
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";

const [tools, work, label, runsArg, ...rest] = process.argv.slice(2);
const runs = Number(runsArg);
let only = null;
const oi = rest.indexOf("--only");
if (oi >= 0) only = new Set(rest[oi + 1].split(","));

const env = {
  ...process.env,
  PATH: `${tools}/node/bin:${tools}/vcs/bin:${tools}/go/bin:/usr/bin:/bin:/usr/sbin:/sbin`,
  GIT_EXEC_PATH: `${tools}/vcs/git-core`,
  npm_config_cache: `${work}/npm/cache`,
  npm_config_update_notifier: "false",
  GOTOOLCHAIN: "local",
  GOWORK: "off",
  GOFLAGS: "-mod=vendor",
  GOCACHE: `${work}/gocache`,
  GOPATH: `${work}/gopath`,
};

function sh(cmd, args, cwd, extraEnv = {}) {
  const r = spawnSync(cmd, args, { cwd, env: { ...env, ...extraEnv }, encoding: "utf8", maxBuffer: 1 << 28 });
  if (r.status !== 0) {
    throw new Error(`${cmd} ${args.join(" ")} in ${cwd}: status ${r.status} ${r.stderr?.slice(-2000)}`);
  }
  return r.stdout;
}

function timed(fn) {
  const t = process.hrtime.bigint();
  fn();
  return Number(process.hrtime.bigint() - t) / 1e9;
}

const canPurge = spawnSync("sudo", ["-n", "purge"]).status === 0;

function out(kv) {
  process.stdout.write(JSON.stringify({ label, host: os.hostname(), cpus: os.cpus().length, ...kv }) + "\n");
}

const proj = `${work}/npm/proj`;
const repo = `${work}/repo`;
const gob = `${work}/gobuild`;
let edit = 0;

const workloads = {
  "npm-ci": {
    prep: () => fs.rmSync(`${proj}/node_modules`, { recursive: true, force: true }),
    run: () => sh("npm", ["ci", "--offline", "--no-audit", "--no-fund", "--loglevel=error"], proj),
  },
  "git-status": {
    prep: () => {},
    run: () => {
      const s = sh("git", ["status", "--porcelain"], repo);
      if (s.trim() !== "") throw new Error(`repo not clean: ${s.slice(0, 500)}`);
    },
  },
  "go-incr": {
    prep: () => {
      edit++;
      fs.writeFileSync(`${gob}/cmd/x05-netd/edit.go`, `package main\n\nvar x05Edit = "${label}-${edit}"\n`);
    },
    run: () => sh("go", ["build", "-o", `${gob}/x05-netd.bin`, "./cmd/x05-netd"], gob),
  },
  fsync: {
    prep: () => fs.rmSync(`${work}/fsync.bin`, { force: true }),
    run: () => {
      const fd = fs.openSync(`${work}/fsync.bin`, "w");
      const b = Buffer.alloc(4096, 0x61);
      for (let i = 0; i < 500; i++) {
        fs.writeSync(fd, b);
        fs.fsyncSync(fd);
      }
      fs.closeSync(fd);
    },
  },
  seqwrite: {
    prep: () => fs.rmSync(`${work}/seq.bin`, { force: true }),
    run: () => {
      const fd = fs.openSync(`${work}/seq.bin`, "w");
      const b = Buffer.alloc(1 << 20, 0x62);
      for (let i = 0; i < 512; i++) fs.writeSync(fd, b);
      fs.fsyncSync(fd);
      fs.closeSync(fd);
    },
  },
};

// Settle: one untimed run of each, so warm runs start warm, the index of
// the repository matches this filesystem, and GOCACHE is full.
const t0 = Date.now();
for (const [name, w] of Object.entries(workloads)) {
  if (only && !only.has(name)) continue;
  w.prep();
  const s = timed(w.run);
  out({ workload: name, mode: "settle", run: 0, seconds: s });
}

for (const mode of ["warm", "cold"]) {
  if (mode === "cold" && !canPurge) {
    out({ mode: "cold", skipped: "sudo -n purge not allowed" });
    continue;
  }
  for (const [name, w] of Object.entries(workloads)) {
    if (only && !only.has(name)) continue;
    for (let i = 1; i <= runs; i++) {
      w.prep();
      if (mode === "cold") sh("sudo", ["-n", "purge"], work);
      const s = timed(w.run);
      out({ workload: name, mode, run: i, seconds: s });
    }
  }
}

// go-clean: a full build with an empty build cache, once, warm then cold.
if (!only || only.has("go-clean")) {
  for (const mode of canPurge ? ["warm", "cold"] : ["warm"]) {
    fs.rmSync(`${work}/gocache`, { recursive: true, force: true });
    if (mode === "cold") sh("sudo", ["-n", "purge"], work);
    const s = timed(() => sh("go", ["build", "-o", `${gob}/x05-netd.bin`, "./cmd/x05-netd"], gob));
    out({ workload: "go-clean", mode, run: 1, seconds: s });
  }
}
out({ event: "done", seconds: (Date.now() - t0) / 1000 });
