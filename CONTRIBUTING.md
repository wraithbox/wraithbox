Thank you for investing your time in contributing to our project!

Any contributions you make are governed by our [License](LICENSE).

Please follow our [Code of Conduct](CODE_OF_CONDUCT.md) to keep our community approachable and respectable.

You could read the [GitHub Docs Contributing Guide](https://github.com/github/docs/blob/main/CONTRIBUTING.md) for general advice on how to contribute.

Since this is a small hobby project, your contribution may not be noticed for a while if we are busy elsewhere. Sorry!

## Getting set up

The full gate needs an Apple Silicon Mac with Xcode installed (Swift
comes from Xcode). The Go code also builds and tests on Linux and
Windows: there, run the `go:*` tasks. [`mise`](https://mise.jdx.dev/)
installs every other pinned toolchain and runs every repo task.

```bash
mise trust        # allow this repo's .mise.toml
mise install      # install the exact pinned toolchains
mise run install  # install the docs site dependencies
```

## Before you open a pull request

```bash
mise run ci       # lint + typecheck + test + build, every language, Go for every OS
mise run audit    # zizmor supply-chain audit (needs `gh auth login`)
mise run vuln     # scan every lockfile for known vulnerabilities
```

`ci` is deliberately offline; `audit` and `vuln` both need network, which
is why they are separate. CI runs all three.

Design changes start in `docs/spec/`. Read
`docs/spec/003-requirements.md` first: the security requirements there
are not traded away for convenience.

Commit messages follow [Conventional Commits](https://conventionalcommits.org/),
with the language or component as the scope where it helps:
`feat(go): ...`, `fix(swift): ...`, `docs(spec): ...`. Open the pull
request against `main`.

## Reporting a security problem

Do not open a public issue for a vulnerability. See
[SECURITY.md](SECURITY.md).

## Issues

Use the issue forms for bugs, feature requests and spikes (the `X*`
open questions in `docs/spec/011-verification-and-spikes.md`). Labels
and triage are described in
[docs/agents/issue-tracker.md](docs/agents/issue-tracker.md).

## Working with agents

[AGENTS.md](AGENTS.md) is the canonical instruction file for coding
agents (`CLAUDE.md` is a symlink to it). Keep it current when you change
the toolchain or the task list.
