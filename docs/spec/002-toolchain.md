# 002 - Toolchain

**Purpose:** Standardize how this repository manages runtime versions,
repo tasks, and CI.

**Requirements:**
- [`mise`](https://mise.jdx.dev/) is the single source of truth for
  runtime versions and repo tasks. `mise install` + `mise run <task>`
  are the only entry points a contributor needs to know.
- `.mise.toml` pins every tool under `[tools]` and declares every repo
  task under `[tasks.<task>]`. Tasks are namespaced `<lang>:<verb>`
  (`go:lint`, `swift:test`, `doc:build`, …) with top-level fan-outs
  (`lint`, `test`, `ci`, …) that `depends = [...]` on the namespaced ones.
- Native pin files stay authoritative (`go.work` / `go.mod` `go`
  directive, `Package.swift` tools version). They must agree with
  `.mise.toml`.
- **Swift comes from Xcode, not mise.** The compiler, `swift-format` and
  `xcodebuild` ship inside Xcode, so the Xcode version is the pin.
  `Package.swift` declares `swift-tools-version: 6.2`, the floor;
  contributors may use a newer Xcode. CI uses the default Xcode of its
  pinned macOS runner image. SwiftLint, which Xcode does not ship, is
  pinned in `.mise.toml`.
- CI uses [`jdx/mise-action`](https://github.com/jdx/mise-action),
  SHA-pinned, to install the toolchain, then calls `mise run <task>`.
- Secrets use `op://` references resolved by
  [`fnox`](https://fnox.jdx.dev/) via an `[env]` block that loads an
  optional gitignored `.env`. Plaintext tokens never land on disk.

**Design Approach:**
- **Every tool in `[tools]` is pinned to an exact version.** This is a
  supply-chain rule, not a stability one: an upstream compromise must
  never arrive by auto-download. Nothing in `[tools]` is covered by
  dependabot, so it must be refreshed deliberately with `mise up`.
- **Go runs on macOS, Linux and Windows from the start.** Most code is
  cross-platform Go (spec 012), so CI lints, vets, tests and builds it
  natively on all three (a job matrix), with only the Go tools installed
  (`install_args`), because some pinned tools have no Windows build.
  Locally, `mise run go:cross` vets, lints and builds for all three
  GOOS values from one machine and is part of `ci`; `go:vulncheck`
  analyses each GOOS. Swift runs on macOS runners only. Docs, workflow
  linting and the vulnerability scan run on Linux.
- **Go tasks work under Windows `cmd.exe`.** mise runs a task's `run`
  with `cmd.exe` on Windows, so the tasks CI calls on Windows are single
  plain commands in the module directory (`dir`), with `run_windows`
  where a flag differs (`go test` without `-race`, which needs a C
  compiler). There is one Go module; adding a second means extending
  the Go tasks deliberately. Tasks that only run on macOS or Linux
  (`go:cross`, `go:vulncheck`, Swift, docs) may use shell scripts.
- **Swift tasks iterate `packages/*-swift` in-shell**, so a new package is
  "drop it in".
- **A .NET toolchain is added with the first .NET component** (spec 012),
  pinned in `.mise.toml` like every other tool, with its own CI job on a
  Windows runner.
- **CI keeps independent parallel jobs per language.** Each job
  re-installs the toolchain; the mise cache makes this cheap and logs stay
  readable.
- **Tests that need to run guest VMs** (conformance suite, benchmarks;
  spec 011) cannot run on hosted runners and are not part of `ci` until
  self-hosted runners exist (Apple Silicon for macOS guests).

**Status:** Active
