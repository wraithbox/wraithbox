# S02 - Toolchain

**Purpose:** Standardize how this repository manages runtime versions,
repo tasks, and CI.

## Design

### mise

[`mise`](https://mise.jdx.dev/) is the single source of truth for
runtime versions and repo tasks. A contributor needs to know only two
entry points: `mise install` and `mise run <task>`.

`.mise.toml` pins every tool under `[tools]`. It declares every repo
task under `[tasks.<task>]`.

- Tasks are namespaced `<lang>:<verb>`: `go:lint`, `swift:test`,
  `doc:build`, and so on.
- Top-level fan-outs such as `lint`, `test` and `ci` use
  `depends = [...]` on the namespaced tasks.

### Exact pins

Every tool in `[tools]` is pinned to an exact version. This is a
supply-chain rule, not a stability one. An upstream compromise must
never arrive by auto-download.

Nothing in `[tools]` is covered by dependabot. So it must be refreshed
deliberately, with `mise up`.

### Native pin files

Native pin files stay authoritative: the `go` directive in `go.work`
and `go.mod`, and the tools version in `Package.swift`. They must agree
with `.mise.toml`.

### Swift comes from Xcode, not mise

The compiler, `swift-format`, and `xcodebuild` ship inside Xcode. So
the Xcode version is the pin.

- `Package.swift` declares `swift-tools-version: 6.4`, the floor, which
  ships with Xcode 27. Contributors may use a newer Xcode.
- The package's deployment target is macOS 26, one major version below
  the oldest v1 host (NFR04-host-platforms). A contributor whose Mac
  can't run macOS 27 yet can still build the package and run its tests.
  The compiler then makes each call to an API new in macOS 27 check
  `#available`. The programs refuse an older host at run time: `wb-vmd`
  with `vmd-host-unsupported` (S04-04-vm-processes, "Slot checks"), and
  `wb-dev-bundle provision` with `host-unsupported`. The app bundle is
  built for macOS 27, so macOS doesn't open it on an older Mac.
- CI uses the default Xcode of its pinned macOS runner image. Both
  macOS jobs, Swift and Go's macOS leg, run on the `xcode-27` image,
  which is macOS 27 with Xcode 27, the oldest v1 host
  (NFR04-host-platforms). The Go module has no cgo and builds against
  no macOS SDK, but its darwin-only tests (`localipc` peer credentials,
  `vmdspawn`, the `wb-diskfmt` child) depend on how macOS behaves, so
  they run on a macOS that v1 supports.
- SwiftLint, which Xcode does not ship, is pinned in `.mise.toml`.

### CI

CI uses [`jdx/mise-action`](https://github.com/jdx/mise-action),
SHA-pinned, to install the toolchain. Then it calls `mise run <task>`.

CI keeps independent parallel jobs per language. Each job re-installs
the toolchain. The mise cache makes this cheap, and the logs stay
readable.

On a pull request, CI skips a job whose inputs did not change, and a
push to `main` runs every job. `scripts/ci-changes.mjs` maps the changed
files to jobs and runs every job when in doubt. `mise run ci` always
runs every check. The `CI result` job is the one required check
(`docs/github-settings.md`). It needs every job but the vulnerability
scan, and fails unless each passed or was skipped because its inputs
did not change.

A change to the Go module alone runs the build, sign, and bundle steps
of the Swift job, because the app bundle holds the Go programs, and
skips its lint, contract, and test steps. The Go files that the Swift
build reads are the exceptions: `go.mod`, `go.sum`, the version package
and the stamping tool run the whole Swift job, like the other Swift
inputs, such as Swift sources, the contracts, and the version stamp.

The Swift job caches SwiftPM's `.build`, the compiled dependencies,
keyed on the runner image, the runner architecture (`RUNNER_ARCH`), the
Xcode version, `Package.swift`, and `Package.resolved`. Each run
restores it. Without an exact match, a run restores the newest cache of
the same image, architecture, and Xcode, whatever its `Package.swift`
and `Package.resolved` (`restore-keys`). A push to `main` saves it, and
a pull request doesn't, so a pull request can't write a cache that
`main` restores. Before the save, the job deletes what every run builds
again, such as the package's own programs and debug symbols. What the cache
keeps is mostly the compiled dependencies. A miss costs time and
nothing else: no step depends on a hit, and tests always run.

### Go on three operating systems

Go runs on macOS, Linux, and Windows from the start. Most code is
cross-platform Go (S12-platforms).

- CI lints, vets, tests, and builds it natively on all three, in a job
  matrix.
- Those jobs install only the Go tools and `buf` (`install_args`),
  because some pinned tools have no Windows build.
- Locally, `mise run go:cross` vets, lints, and builds for all three
  GOOS values from one machine. It is part of `ci`.
- `go:fuzz` runs every fuzz target briefly
  (S11-verification-and-spikes). `test` depends on it, and the task
  runs a Go program, which works on all three. CI runs it on Linux
  only, in the `fuzz` job and not the Go job. That job runs the targets in
  parallel shards, `go:fuzz -- -shard <i>/<n>`, and `ci.yml` sets the
  shard count. Each shard runs one part of the sorted list of targets.
  The parts don't overlap and together hold the whole list. A target
  fuzzes for the same time as locally. Without `-shard`, as under `ci`, the
  task runs every target.
- `go:vulncheck` analyzes each GOOS.

Swift runs on macOS runners only. Docs, workflow linting, and the
vulnerability scan run on Linux.

### Go tasks under Windows `cmd.exe`

mise runs a task's `run` with `cmd.exe` on Windows. So the tasks CI
calls on Windows are single plain commands in the module directory
(`dir`).

- `run_windows` covers a flag that differs, such as `go test` without
  `-race`, which needs a C compiler.
- There is one Go module. Adding a second means extending the Go tasks
  deliberately.
- Tasks that only run on macOS or Linux may use shell scripts:
  `go:cross`, `go:vulncheck`, the `proto:` tasks, Swift and docs.

### Swift tasks

Swift tasks iterate `packages/*-swift` in-shell. So the tasks pick up a
new package without changes.

### Contracts

`buf` is pinned in `.mise.toml` like every other tool. The plugins it
runs are pinned where their language pins dependencies: `tool` lines in
`go.mod`, and the Swift package's `Package.resolved`
(S10-tech-stack, "Generated contract code").

- `proto:check` runs in the Go job on Linux. Its `buf breaking` step
  compares with `origin/main`, so that job checks out the full history.
- `proto:check-swift` runs in the Swift job.

### .NET

A .NET toolchain is added with the first .NET component
(S12-platforms). It is pinned in `.mise.toml` like every other tool,
and has its own CI job on a Windows runner.

### Prose

Prose is checked like code. Both checks are part of `lint` and run in
the CI docs job.

- `prose:lint` runs Vale over every Markdown and MDX file: specs,
  README, AGENTS.md, and the docs site.
- `prose:spell` runs cspell (American English) over every tracked
  file, source code included.

Vale is pinned in `.mise.toml`. Its style packages are vendored under
`.vale/styles/`, with the pinned release and checksum recorded in
`.vale.ini`. So `ci` stays offline.

cspell is an exact-pinned devDependency of the docs package. mise's
`npm:` backend does not record a checksum, and `bun.lock` pins the
whole tree.

### Secrets

Secrets use `op://` references, resolved by
[`fnox`](https://fnox.jdx.dev/). An `[env]` block loads an optional
gitignored `.env`. Plaintext tokens are never written to disk.

### Tests that run guest VMs

Tests that need to run guest VMs can't run on hosted runners, and are
not part of `ci`. They are the conformance suite and the benchmarks
(S11-verification-and-spikes).

- Until v1 is close, they run on the maintainer's Mac.
- Issues that need a running macOS guest carry the `needs-vm` label. A
  wave builds one of them at a time.
- Whether to add a self-hosted Apple Silicon runner is revisited near
  v1.

## Decisions

### Vale doesn't lint source code comments

- **Why:** a trial run over the Go, Swift, and config comments found
  little besides section rulers, command-line flags, and terse comment
  style.

### One `needs-vm` issue per wave

- **Decided:** I50.

**Status:** Active
