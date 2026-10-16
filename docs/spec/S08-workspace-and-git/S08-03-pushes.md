# S08-03 - Pushes

**Purpose:** Where the guest's pushes land, the settings `receive-pack`
runs with, which refs a push may write, and the push at session end.

## Design

### Landing repository

Each project has a bare repository in Wraith Box's state directory,
`projects/<id>/landing.git`. The guest pushes there, never to the
user's repository.

`landing.git` borrows objects from the same project's `export.git`. At
each session start `wb-hostd` rewrites its alternates file to exactly
that one entry. A push then sends only new objects.

The guest pushes to `landing.git` through the same transport as its
fetches (S08-01-into-the-guest).

### Settings of `receive-pack`

`wb-hostd` runs `git receive-pack` with these settings:

- Repository hooks are disabled. `core.hooksPath` is the absolute path
  of a hooks directory in the read-only installed bundle. That
  directory holds only the pre-receive check (S08-04-push-checks).
  `receive.procReceiveRefs` is unset.
- Every object check (`fsck`) is an error, including the checks git
  only reports by default.
- Deletes, push options and signed pushes (`push-cert`) are refused.
- `receive.unpackLimit=1`, so every push goes through `index-pack`.
- `pack.threads=1`.
- A size limit, `receive.maxInputSize`, of 256 MiB. That is the same
  value as the pack scanner's wire-byte cap (S08-04-push-checks).
- `GIT_ALLOC_LIMIT` in its environment, at the per-object cap plus one
  byte (104857601). Its children inherit it. Git then refuses each
  allocation above it before making it (X28-git-alloc-limit,
  S08-04-push-checks).

Git reads a `GIT_ALLOC_LIMIT` of `0` as no limit. So the value is never
computed from a setting that can be zero. The environment builder in
`wb-hostd` has a unit test that pins the value (C08-git).

### Ref restriction

A filter in `wb-hostd` reads the push's command list before
`receive-pack` sees any of it. It refuses the whole push unless every
ref is under `refs/heads/wb/<session-id>/`, where `<session-id>` is:

- a live session: starting, running, paused, ending, or lost
  (S06-vm-lifecycle);
- of the project the request names;
- in the VM the push came from.

A push to an ended or discarded session's branch is refused and logged
with the rule (SEC06-repo-writes). So is a push to another project's
or another VM's branch.

The filter also checks the form of each command:

- Below the prefix, ref components use only `A-Z`, `a-z`, `0-9`, `-`,
  `_` and `.`.
- A component doesn't start with `.`, doesn't end with `.` or `.lock`,
  and holds no `..`.
- A component is at most 255 bytes, and a ref at most 1024.
- Object IDs must have the length of the landing repository's hash.
- `shallow` lines are refused.
- The first command must ask for `side-band-64k`. Git then sends its
  messages, and those of `index-pack` and the pre-receive check, to
  the guest inside the protocol, not on its own standard error.

The filter reads no byte past the flush that ends the command list. The
pack scanner then gets the pack from its first byte
(S08-04-push-checks).

The filter is a parser of guest bytes, so it has a fuzz target
(C08-git). It logs each ref it allows or refuses with the rule.

`receive.hideRefs` restricts the same refs inside git as a second
layer, and hides other sessions' branches.

### Git's standard error

With `side-band-64k`, `receive-pack` sends the messages of `index-pack`
and the hook to the guest inside the protocol. `wb-hostd` passes them
on without reading them.

What `receive-pack` still writes to its own standard error reaches
`wb-hostd`, such as a `fatal:` line. Its text depends on the pack, for
example an fsck message that quotes a tag name or a `.gitmodules`
value. So `wb-hostd` treats it as guest input:

- it keeps at most 4 KiB per push;
- it replaces control characters, escape sequences and invalid UTF-8
  with U+FFFD;
- it logs the result as one quoted field next to the rule that ended
  the push (SEC10-audit).

### Session end

If the worktree has uncommitted changes, `wb-guestd` commits them to
the session branch as a WIP commit marked as such, then pushes. `wb`
can also push mid-session.

- The same happens whether `claude` exited by itself or after a
  hangup, because closing the terminal ends the session
  (S06-09-session-lifecycle).
- `wb-guestd` commits only after `claude` has exited or was killed,
  and after the session's processes were killed
  (S13-01-process-hardening).
- A process that left the session while another session of the
  project runs can still write during the commit
  (S13-02-filesystem-profile, "Known gaps").
- The commit and the push run as the project user (S06-09-session-lifecycle, "As the project user").
- A session that was lost gets the same commit and push at the next
  connection to its VM, unless it was discarded first.

When the commit or the push fails:

- the worktree keeps the changes;
- the summary says what failed;
- `wb` exits with 255 (S05-02-exit-status-and-signals).

A later `wb claude --continue` starts from the host's `HEAD`
(S08-01-into-the-guest). The old session's uncommitted files reach a
new session only after the user lands its WIP commit and merges it, or
carries the files in by hand (FR04-carry-in).

## Decisions

### Borrow from `export.git`

- **Why:** without borrowing, a session's first push sends the whole
  history (X07-git-round-trip).

### `index-pack` for every push

`receive.unpackLimit=1` sends every push through `index-pack`, never
`unpack-objects`.

- **Why:** `unpack-objects --strict` keeps the trees and commits of a
  push in memory until it ends, which only the per-push cap would
  bound. And `unpack-objects` resolves a delta as soon as its base has
  arrived. `index-pack` waits for the pack's checksum, which the pack
  scanner holds back (X28-git-alloc-limit).

### A filter in front of `receive.hideRefs`

- **Why:** `receive.hideRefs` can't be the only layer. It accepts
  `refs/heads/wb/<session-id>` itself, and applies the allowed half of
  a mixed push (X07-git-round-trip).

### Closing the terminal ends the session

A hangup ends the session like an exit of `claude`. The same WIP
commit and push follow.

- **Decided:** I46, recorded in S06-09-session-lifecycle.

**Status:** Draft
