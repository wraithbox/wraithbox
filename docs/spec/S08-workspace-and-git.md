# S08 - Workspace and Git Round Trip

**Purpose:** How code gets into the guest and how the agent's work gets
back out, without sharing the host filesystem.

**Requirements:** FR02-any-repo to FR05-parallel-sessions, SEC02-no-host-fs-share, SEC03-no-host-exec, SEC04-no-guest-secrets, SEC06-repo-writes, SEC10-audit, SEC13-bounded-resources, NFR01-startup, NFR02-fs-speed.

**Tests:** C08-git

Briefs: B21-git-round-trip, B41-git-data-scope, B74-pre-receive-check,
B102-git-alloc-limit

![The git round trip. The guest reaches only wb-hostd, which serves export.git read-only and passes pushes through the ref filter and the pack scanner into landing.git. wb, not wb-hostd, moves refs between your repository and the two repositories. A fetch by ID from your repository has nothing to reach, and a push to main stops at the ref filter.](S08-workspace-and-git.svg)

## Design

Code goes into the guest, and the agent's work comes back out, only
as git objects over the host-guest socket. Each project has two
repositories of its own on the host, next to the user's:

| Repository | Written by | Read by |
|---|---|---|
| The user's repository | the user, and `wb land` | `wb`, for the export update and the carry-in |
| `<data>/projects/<id>/export.git` | `wb` only | the guest, through `upload-pack` |
| `<data>/projects/<id>/landing.git` | the guest's pushes, through `receive-pack` | `wb land` and `wb diff` |

### Pages

| Page | What it covers |
|---|---|
| S08-01-into-the-guest | The clone, the transport, remote URLs, session start and the carry-in scan |
| S08-02-export-repository | Which refs the guest can fetch, and how `wb` keeps `export.git` safe to run git in |
| S08-03-pushes | The landing repository, the settings of `receive-pack`, the ref filter, and session end |
| S08-04-push-checks | The pack scanner, git's allocation limit and the pre-receive check |
| S08-05-push-limits | Cleanup after a failed push, the landing size cap, and time and memory bounds |
| S08-06-landing-on-the-host | `wb land` and `wb diff`, and the checks before each fetch |
| S08-07-repository-lifecycle | Discarding sessions, the export lock, and shrinking `export.git` |
| S08-08-host-git | Which git binary runs on the host, and its minimum version |
| S08-09-risky-changes | The paths `wb diff` and the session summary flag |
| S08-10-wsl | The fetches between a WSL distribution and the Windows side |

### Other transfers

Files copied out of the guest by any means other than git, such as a
future `wb cp`, carry the platform's downloaded-file marker
(S12-platforms). That is the quarantine attribute on macOS, and Mark of
the Web on Windows.

## Out of scope

- Git LFS objects, submodules, and very large repositories are not covered
  in v1. They are listed in V1-initial.

**Status:** Draft
