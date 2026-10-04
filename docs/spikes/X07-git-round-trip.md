# X07 - Git round trip

Brief: B21-git-round-trip

**Question:** Does the git round trip work as S08-workspace-and-git
describes: a remote helper over vsock, read-only `upload-pack` against
the user's repository, restricted `receive-pack` into the landing
repository, and flagging of risky paths?

**Answer:** Yes, with conditions. The transport, the read-only fetch,
the restricted push and the flagger all worked, with a socketpair in
place of vsock, on the Go repository (680,000 objects), with Homebrew
git 2.56.0 and Apple git 2.54.0. No write to the user's repository and
no push outside `refs/heads/wb/<session-id>/` succeeded. As written,
S08-workspace-and-git is wrong in two places. `upload-pack` on the
user's repository serves hidden objects by ID over protocol v2. And the
first push into an empty landing repository sends the whole history.
The conditions below fix both.

## For review

- **Decides:** the answer to X07-git-round-trip, and the mechanism that
  restricts pushed refs: a Go filter in `wb-hostd` that reads the
  `receive-pack` command list before git sees it, with
  `receive.hideRefs` as a second layer.
- **You are approving:** the conditions below and the
  S08-workspace-and-git changes in this pull request: fetches from
  `projects/<id>/export.git` as I41 decided, the ref filter, strict
  object checks, cleanup after a refused push, bounds on the transport,
  and a landing repository that borrows objects from `export.git`.
- **Controls touched:** SEC02-no-host-fs-share (no host directory shared
  with the guest) and SEC03-no-host-exec (returned work runs nothing on
  the host) are kept, and the push path is now specified in more detail.
  SEC04-no-guest-secrets (no secrets in the guest) is kept only with
  condition 1, which the I41 decision already made.
- **Assumed:** that vsock behaves like the socketpair used here (a
  reliable byte stream with half-close), which X18-vsock-handoff tests.
  That v2 `upload-pack` also serves objects from an alternate object
  store. It follows from v2 serving an unreferenced object, but no run
  tried it. How `upload-pack` behaves under v0 against a hostile client:
  every v0 refusal came from the guest's own git client, which
  wouldn't send the request.
- **Open decisions:** none. I41 still decides the export repository's
  lifecycle (item 6), with a new constraint from condition 4.
- **Brief:** B21-git-round-trip

## Conditions

1. **Serve `upload-pack` from an export repository, never from the
   user's repository** (as the I41 decision chose). With
   `uploadpack.hideRefs` hiding everything but the session's branch,
   and every `allow*SHA1InWant` setting off, a protocol v2 client still
   fetched by object ID: the commit of a hidden local branch, a commit
   only on a release branch, and an unreferenced blob. The export
   repository refused all three on the server side under v2 ("not our
   ref"). Under v0 the guest's git client refused to send the request
   ("Server does not allow request for unadvertised object"), so the
   server side under v0 against a hostile client wasn't tested. The
   export repository stores a copy of every object it serves: no
   `objects/info/alternates` to the user's repository (inferred, not
   run), and no `git clone --local`, which hard-links everything under
   `objects/` (`git help clone`, `--local`).
2. **The remote helper asks for protocol v2.** Git tells a `connect`
   helper the service but not the protocol version, and `upload-pack`
   speaks v0 unless asked, so `git-remote-wb` sends the version in its
   request and `wb-hostd` passes only `version=0`, `1` or `2` on to git.
3. **A Go filter in front of `receive-pack` enforces the ref
   restriction.** It reads the command list (pkt-lines up to the first
   flush), refuses the whole push unless every ref is strictly under
   `refs/heads/wb/<session-id>/`, refuses deletes, push options and
   signed pushes, and only then forwards the bytes to git. The filter
   parses guest bytes, so it gets a fuzz target. `receive.hideRefs`
   stays as a second layer, and hides other sessions' branches from the
   push advertisement. On its own, `receive.hideRefs` doesn't suffice:
   it let a push create `refs/heads/wb/<session-id>` itself (it matches
   the prefix as a whole path component), which then blocks every
   `refs/heads/wb/<session-id>/*` branch. It also applied the allowed
   half of a push with one allowed and one forbidden ref, and git
   parsed the pack before refusing.
4. **The landing repository borrows objects from the export
   repository** (`objects/info/alternates`). Without that, the first
   push of a session sends the whole history: 425 MiB for the Go
   repository, refused by a 64 MiB `receive.maxInputSize`. With it, the
   push sent 12 objects, under 1 KiB. The export repository's garbage
   collection must then keep every object a landing branch needs (I41,
   item 6).
5. **`receive-pack` runs with strict object checks.**
   `receive.fsckObjects=true` turns fsck warnings into errors but leaves
   the INFO-level checks alone: a tree with file mode `100666` was
   accepted until `receive.fsck.badFilemode=error` and the other
   object-level INFO checks were raised to errors. Also `core.hooksPath`
   pointing at an empty location, `receive.denyDeletes`,
   `receive.maxInputSize`, `receive.autogc=false`, push options off.

## Abuse cases

Each ran against throwaway repositories in a temp directory. None
succeeded, with both gits. "Refused" includes what the guest saw.

| Attempt | Result |
|---|---|
| Fetch a hidden branch, a note, `refs/remotes/*` by name | refused: not advertised |
| Fetch a hidden commit, a release-branch commit, an unreferenced blob by ID, from the user's repository, protocol v2 | **succeeded**, so condition 1 |
| The same by ID from the export repository, protocol v2 | refused by the server: "not our ref" |
| The same by ID, protocol v0, either repository | refused by the guest's own git client before it asked; the server side wasn't tested |
| v2 `object-info` for a hidden blob | refused: not advertised, `invalid command` |
| Write to the user's repository: push commands and a pack sent to `upload-pack` | refused: protocol error; the user's `.git` file names, sizes and times unchanged |
| Service other than upload or receive (`/bin/sh`, `git-upload-archive`), extra arguments, bad protocol value, oversize header | refused by `wb-hostd` before git runs |
| Push to `refs/heads/main`, a tag, another session, `wb/S10/*` for session `S1`, `refs/heads/wb/S1` itself | refused by the filter, nothing written |
| Delete the session branch; push options | refused |
| One allowed and one forbidden ref in one push | whole push refused by the filter |
| Pack over the size limit (20 MiB against 16 MiB) | refused: "pack exceeds maximum allowed size" |
| Trees with `.git`, `.GIT`, `git~1`, `..`, a `.gitmodules` or `.gitattributes` symlink, mode `100666`; a commit with a bad date; a `.gitmodules` URL starting with `-` | refused by fsck, nothing written |
| Bad pkt-line length, NUL in a later command, command list cut off | refused by the filter |
| Garbage, a truncated or bit-flipped pack, a header claiming 4 billion objects, a ref to a missing object | refused by `receive-pack`, nothing written |

`results/run1.txt` shows a v0 fetch by ID as succeeded. In that run the
v0 case reused the clone into which the v2 case had already fetched the
object, so git found it locally and asked nothing. Later runs use a
fresh clone for each case. The case of `refs/heads/wb/S1` itself under
`receive.hideRefs` alone ran once, with Homebrew git
(`results/run5.txt`).

## Measurements

Apple M2, 16 GB, macOS 27.0.1, Homebrew git 2.56.0 on both sides, the Go
repository at `master~200`, then at `master`. Other agents' VMs ran on
the same machine during the runs. The table gives the median and the
range over the five Homebrew runs in `results/`: `run1`, `run2`,
`run4`, `run5` and `run6`. Under
that load a few runs took two to four times as long. One run with Apple
git 2.54.0 on both sides (`run3-apple`), also under load, took 19 to
45 s for a first clone and matched the incremental steps.

| Step | Median (range) | Runs | Transferred |
|---|---|---|---|
| Local clone without the tunnel (`--no-local`), for comparison | 17.1 s (16.8 to 54.9) | 5 | 679,335 objects, 426 MiB |
| First clone through the tunnel, protocol v2 | 16.3 s (16.1 to 56.5) | 5 | same |
| First clone through the tunnel, protocol v0 | 16.6 s (16.3 to 22.6) | 5 | same |
| Build the export repository (first session of a project) | 16.2 s (15.7 to 61.5) | 5 | |
| First clone from the export repository | 16.6 s (16.4 to 32.4) | 5 | same |
| Update the export repository, 200 new commits | 0.83 s (0.69 to 1.19) | 5 | |
| Incremental fetch from the export repository, 200 commits | 0.57 s (0.57 to 0.72) | 5 | 2,590 objects, 912 KiB |
| Fetch with nothing new | 0.05 s | 5 | |
| Push 3 commits, landing borrows from export | 0.10 s (0.09 to 0.23) | 5 | 12 objects, under 1 KiB |
| Push 20 MiB of random data | 1.0 s | 5 | 20 MiB |
| Push into an empty landing repository (no borrowing) | 19.3 s (19.0 to 19.4) | 5 | 681,925 objects, 425 MiB |
| `wb land`: the user's git fetches the session branch | 1.1 s | 5 | |

The tunnel doesn't measurably slow a clone down. A session start in
an existing project costs an export update and an incremental fetch,
about 1.4 s for 200 Go commits. That fits NFR01-startup, whose budget
leaves out first-time project setup.

## Risky-path flagger

The prototype matched the S08-workspace-and-git list on paths, used the
git file mode for executables (`100755`), symlinks (`120000`) and
submodules (`160000`), and read the content for symlink targets,
`package.json` `scripts` and `bin`, and the `go` and `toolchain` lines of
`go.mod`. Over the last 400 first-parent commits (all 43 for Wraith Box):

| Repository | Commits flagged | Commits per rule |
|---|---|---|
| golang/go | 9 of 400 (2%) | lockfile 7, agent configuration 1, toolchain pin 1 |
| wraithbox/wraithbox | 22 of 43 (51%) | agent configuration 14, toolchain pin 12, CI 7, build entry 3, lockfile 3, git behavior 2, package scripts 1, new executable 1 |
| Express | 69 of 400 (17%) | CI 65, package scripts 3, build entry 1 |

The mean time per commit was 11 to 18 ms, including reading blobs. The
examples checked by hand, six per repository, were all deserved, for
example a new `src/simd/AGENTS.md` in Go and `.gitattributes` and
`.claude/settings.json` changes here. Projects that edit their agent
configuration or CI often get many flags. Reviewers need to see those
changes, and grouping the flags per rule in the summary keeps a long
list short to read.

## What changes in the specs

- S08-workspace-and-git: fetches come from `export.git` (the I41
  decision), and everything in its object store counts as readable by
  the guest. The spec names the ref filter and its rules, the second
  layer, the `receive-pack` settings, cleanup after a refused push, the
  transport bounds, the landing repository borrowing from `export.git`,
  and how the flagger detects executables, symlinks and `package.json`
  scripts.
- S11-verification-and-spikes: a fuzz target for the request header
  and the push command list, and conformance cases for fetch by ID,
  pushes outside the session's refs, and a refused push leaving
  nothing behind.
- I41 (git data scope) gets conditions 1, 4 and 5 as input: item 1 is
  confirmed with both gits, item 4 learns that Apple git 2.54.0 behaved
  the same as 2.56.0 here, and item 6 must keep the objects that landing
  branches borrow. Item 5 is settled by condition 3.

## Code

Throwaway code on branch `spike/x07-git-round-trip`, commit
[`a0a10cc`](https://github.com/wraithbox/wraithbox/tree/a0a10ccfb5a61305d23b8df34cc30b1eed34d98d/spikes/x07-git-round-trip):
the helper, the host side, the filter with its fuzz target, the flagger,
the harness, and the output of each run under `results/`.

**Status:** Answered: yes, with conditions
