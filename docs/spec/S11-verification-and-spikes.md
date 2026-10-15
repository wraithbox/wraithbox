# S11 - Verification

**Purpose:** How the requirements are proven: the test layers, what
gates a release, and where each area's test cases are.

## Design

### Unit tests

Go `go test -race`, table-driven, and Swift Testing. They run in `ci`
on every host OS (S02-toolchain).

### Fuzzing

Every parser that sees guest-controlled bytes has a Go native fuzz
target. Where fuzzing is impractical in Swift, property-based tests
take its place.

Where an area page names properties for a target, the target checks
them, not only that the parser doesn't crash. `ci` runs each target
briefly as a smoke run.

### Conformance suite

Adversarial checks run *inside a guest* against a real `wb-netd` and
`wb-proxyd`. Each must fail safely.

The suite is a release gate for every host and guest combination in
the platform matrix (S12-platforms). A combination isn't supported
until it passes.

OpenShell's adversarial end-to-end tests are the first source of
further cases. They are in `e2e/rust/tests/`, and cover bypass
detection, L7 proxy bypass, and credential gating.

### Benchmarks

The benchmarks measure NFR01-startup and NFR02-fs-speed:

- Time to the Claude prompt: warm, suspended, and cold boot. The
  suspended case restores a state saved after a Claude Code session and
  a build, not an idle guest. It records the state file's size
  (X02-warm-start).
- `npm ci` with the dependency gate on, `git status` on a large
  repository, and an incremental build on the data disk. Each is
  compared with the host.
- Throughput of a large download through the gateway, with the CPU time
  of `wb-netd` and `wb-proxyd`. It is compared with the figures of
  X03-network-path at the same MTU.

Those CPU figures of X03-network-path are for the two together. The
spike ran a `wb-netd` stand-in and a plain HTTP stand-in for
`wb-proxyd` in one process.

The startup and filesystem figures are reported against the
NFR01-startup and NFR02-fs-speed goals, with:

- the host's conditions: load, FileVault, Endpoint Security agents,
  chip;
- the guest's memory size;
- whether the state file was in the host's page cache.

They don't pass or fail the release gate ("Benchmarks don't gate the
release").

### Platform contract tests

Each `internal/platform` interface has one test suite that every OS
implementation runs (S12-platforms).

### Test cases

C00-index lists a page of test cases per area, with each case under
its test layer:

- C01-network-and-dns
- C02-streams-and-tls
- C03-policy-and-approvals
- C04-projects
- C05-credentials-and-store
- C06-model-api
- C07-dependency-gate
- C08-git
- C09-guest-and-sessions
- C10-terminal
- C11-host-daemons
- C12-install-and-downloads

A spec names the pages that verify it in a `**Tests:**` line, under its
`**Requirements:**` line (S01-spec-based-development).

A change to a spec adds or changes its cases on the page of their area.

- One case per bullet, under the heading of its test layer.
- A case says what the test does and what it sees.
- It names the rule it checks, and the spec section that states it.

### CI

These run on hosted runners:

- Go unit tests, lint, and vet, natively on macOS, Linux, and Windows;
- Swift, on macOS;
- the fuzz smoke runs.

The conformance suite and the benchmarks need machines that can run
the guest VMs. Those are Apple Silicon for macOS guests, and nested
virtualization or bare metal elsewhere. Hosted runners mostly can't.

Until v1 is close, tests that need a running macOS guest run on the
maintainer's Mac, outside `ci`. They are the spikes, the conformance
suite, and the benchmarks.

- Agents may build issues that need a guest. The `needs-vm` label marks
  them.
- A wave runs at most one such builder at a time. That keeps the Mac
  within NFR05-two-macos-vms (`docs/agents/orchestration.md`).
- The runner question is revisited near v1.

### Open questions

Open questions are spikes, in X00-index, each with its result page once
answered. What V1 leaves out is in V1-initial.

## Decisions

### Benchmarks don't gate the release

- **Why:** filesystem results vary more between hosts than the gap
  they would judge.
- **Decided:** I19 and I111.

### Guest tests on the maintainer's Mac until v1 is close

- **Decided:** I50.

**Status:** Draft
