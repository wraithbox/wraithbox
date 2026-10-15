# S08-09 - Flagging Risky Changes

**Purpose:** The changed paths that `wb diff` and the session summary flag,
because the host or CI may run them (SEC03-no-host-exec).

## Design

### What is flagged

`wb diff` and the end-of-session summary flag changed paths that the
host or CI may execute or that change how git behaves:

- hook managers and hook directories;
- shell environment files (for example `.envrc`);
- build entry points: `Makefile`, task-runner files, `package.json`
  `scripts`, toolchain pin files;
- editor and agent configuration (`.vscode/`, `.idea/`, `.claude/`,
  `AGENTS.md`, `CLAUDE.md`);
- Wraith Box's own repository configuration, `.wraithbox/`
  (S09-policy-credentials-audit);
- CI definitions (`.github/workflows/` and equivalents);
- `.gitattributes`, `.gitmodules`, submodule URL changes;
- lockfiles;
- new executables, executable-bit changes, and symlinks pointing outside
  the repository.

### What can't arrive

Git itself never transfers `.git/hooks` or repository configuration, so
those cannot arrive this way. The object checks on push refuse a tree
that tries (`.git`, `.GIT`, `git~1`). They also refuse a symlinked
`.gitmodules` or `.gitattributes`.

### How the flagger reads a change

The flagger takes executables, symlinks, and submodules from the git
file mode. It reads the content where the path alone doesn't say
enough:

- symlink targets;
- `package.json` `scripts` and `bin`;
- the `go` and `toolchain` lines of `go.mod`.

The summary groups flags by rule. A project that often changes its CI
or agent configuration gets many (X07-git-round-trip).

**Status:** Draft
