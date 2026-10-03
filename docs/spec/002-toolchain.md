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
- **Go and Swift jobs run on macOS runners.** Every binary in this
  repository targets macOS (host daemons, CLIs, guest agent), so builds,
  tests and `govulncheck` reachability analysis must see darwin build
  constraints. Docs and workflow linting run on Linux.
- **Tasks iterate `packages/*-go` and `packages/*-swift` in-shell**, so a
  new module or package is "drop it in" (+ a `go.work` `use` line for Go).
- **CI keeps independent parallel jobs per language.** Each job
  re-installs the toolchain; the mise cache makes this cheap and logs stay
  readable.
- **Tests that need to run macOS guests** (conformance suite, benchmarks;
  spec 011) cannot run on hosted runners and are not part of `ci` until a
  self-hosted Apple Silicon runner exists.

**Status:** Active
