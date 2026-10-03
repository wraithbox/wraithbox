# 008 - Workspace and Git Round Trip

**Purpose:** How code gets into the guest and how the agent's work gets
back out, without sharing the host filesystem.

**Requirements:** F2–F5, S2, S3, S10, N1, N2.

## Into the guest

- **Project clone.** On first use, the project user's home receives a
  clone of the host repository, transferred over vsock. Later sessions
  fetch only new objects.
- **Transport.** The guest uses a git remote helper (`git-remote-wb`,
  shipped with `wb-guestd`) for a remote named `host`. It tunnels git's
  smart protocol over vsock to `wb-hostd`, which runs `git upload-pack`
  against the user's repository **read-only**: hooks disabled, system
  and global git configuration ignored, environment scrubbed. Nothing in
  the guest can write to the host repository through this path.
- **Session start.** `wb-guestd` creates a worktree for the session at the
  host's current `HEAD`, then applies the carry-in: staged and unstaged
  changes as a binary diff, plus untracked files that are not ignored, up
  to a size cap (F4). Files over the cap are listed in the session
  summary rather than silently dropped.

## Out of the guest

- **Landing repository.** Each project has a bare repository in Wraith
  Box's state directory (`projects/<id>/landing.git`), not the user's
  repository. The guest pushes to it through the same transport;
  `wb-hostd` runs `git receive-pack` with hooks disabled, object
  checking (`fsck`) enabled, a size limit, and refs restricted to
  `refs/heads/wb/<session-id>/*`.
- **Session end.** If the worktree has uncommitted changes, `wb-guestd`
  commits them to the session branch as a clearly marked WIP commit, then
  pushes. `wbctl` can also push mid-session.
- **Landing on the host.** `wbctl land <session>` runs the user's own
  `git fetch` from the landing repository into `wb/<session-id>` in the
  user's repository. It never checks out, merges or runs anything. A
  fetch only writes objects and refs.

## Flagging risky changes (S3)

`wbctl diff` and the end-of-session summary flag changed paths that the
host or CI may execute or that change how git behaves:

- hook managers and hook directories;
- shell environment files (for example `.envrc`);
- build entry points: `Makefile`, task-runner files, `package.json`
  `scripts`, toolchain pin files;
- editor and agent configuration (`.vscode/`, `.idea/`, `.claude/`,
  `AGENTS.md`, `CLAUDE.md`);
- CI definitions (`.github/workflows/` and equivalents);
- `.gitattributes`, `.gitmodules`, submodule URL changes;
- lockfiles;
- new executables, executable-bit changes, and symlinks pointing outside
  the repository.

Git itself never transfers `.git/hooks` or repository configuration, so
those cannot arrive this way.

## Other transfers

Files copied out of the guest by any means other than git (for example a
future `wbctl cp`) carry the macOS quarantine attribute.

## Open points

Git LFS objects, submodules and very large repositories are not covered
in v1. They are listed in spec 011.

**Status:** Draft
