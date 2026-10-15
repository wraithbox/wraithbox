# S08-01 - Into the Guest

**Purpose:** How the project's code reaches the guest: the clone, its
transport, its remotes, and the uncommitted changes carried in at
session start.

## Design

### Project clone

On first use, the project user's home receives a clone of the host
repository, transferred over vsock. Later sessions fetch only new
objects.

### Transport

The guest uses a git remote helper, `git-remote-wb`, for a remote
named `host`. The helper ships with `wb-guestd`.

- The helper tunnels git's smart protocol over a local Unix socket to
  `wb-guestd`.
- `wb-guestd` sends it to `wb-hostd` on a host-guest connection the
  host opened (S04-architecture, B29-vsock-handoff).
- `wb-hostd` runs `git upload-pack` **read-only** against the
  project's export repository, `projects/<id>/export.git`
  (S08-02-export-repository). It never runs it against the user's
  repository.
- `upload-pack` runs with repository hooks disabled, system and global
  git configuration ignored, and a scrubbed environment.

Nothing in the guest can write to the host repository through this
path.

### Protocol version

Git doesn't tell a `connect` helper which protocol version it wants.
The helper asks for v2 itself. `wb-hostd` passes only `version=0`, `1`
or `2` on to git.

### Remote URLs

The guest's clone gets the host repository's remotes next to `host`.
The agent can then push to the forge through the egress gateway
(SEC06-repo-writes). `wb` reads each remote's URLs as the user's git
resolves them, with `git remote get-url --all`, which applies
`url.<base>.insteadOf`. It cleans each URL before it sends it on:

- Git's own rule tells an SSH short form from a path. `host:path` is
  SSH only when no slash comes before the first colon. On Windows a
  drive letter (`C:/x`) is a local path. An IPv6 host is in brackets
  (`[::1]`).
- User name, password, query string and fragment are removed.
- An SSH remote becomes `https://host/path`: both
  `ssh://[user@]host[:port]/path` and the short form
  `[user@]host:path`. The port is dropped.
- An `https://` URL keeps its host, port, and path.
- Any other URL isn't given to the guest: `http://`, `git://`,
  `file://`, `git+ssh://`, `ssh+git://`, a local path, a `<helper>::`
  URL, a URL whose host is an IP address, and a URL the cleaner can't
  parse. The session summary lists each such remote with the reason.
  Each withheld URL goes to `wb-hostd`'s audit writer with its reason
  (SEC10-audit).

The guest pushes to the cleaned URL. Nothing else from the
repository's configuration reaches the guest: no `pushurl`,
`pushInsteadOf`, `insteadOf`, `proxy`, `http.<url>.extraHeader` or
`credential.*`.

Some remotes don't work after the cleaning. An SSH host alias from the
user's SSH configuration gives an HTTPS URL that doesn't reach the
forge. So does a path that only works on a non-default SSH port. The
push then fails at the egress gateway, which refuses the unknown host
(fail closed).

A `.gitmodules` URL is tracked content and isn't rewritten. Submodules
are out of scope (S08-workspace-and-git, "Out of scope"). Project
identity records the URLs after this cleaning, but they aren't part of
the project key (S05-cli, "Naming").

### Session start

`wb-guestd` creates a worktree for the session at
`<home>/sessions/<session-id>` (S06-08-projects-and-sessions). The worktree is checked out at the session's
`HEAD` in `export.git`. Then `wb-guestd` applies the carry-in
(FR04-carry-in):

- staged and unstaged changes, as a binary diff;
- untracked files that are not ignored, up to a size cap.

Files over the cap are listed in the session summary rather than
silently dropped.

### Carry-in scan

The carry-in can hold a secret. A new `credentials.json` that the user
hasn't ignored yet is an untracked file that isn't ignored, so the
carry-in would send it. `wb` collects the carry-in in the user's
repository. It checks each changed or untracked path before it sends
any of it on (SEC04-no-guest-secrets):

- The guest gets a symbolic link as a link, with its target string
  only. `wb` never reads the file it points to (SEC02-no-host-fs-share).
- Anything other than a regular file or a symbolic link is skipped: a
  FIFO, a socket, a device.
- The size cap applies first. The scanner doesn't read a file over it.
- `wb` scans the new content of each changed file, both its version in
  the index and its version in the working tree. It also scans each
  untracked file. It uses the secret scanner of the image seal
  (S06-02-image-build, "Sealing").
- It scans the content, not the diff text, which for a binary file is
  compressed.
- A file `wb` can't read, or that the scanner can't decode, is skipped.

A skipped file is skipped whole. The guest doesn't get an untracked
file, and a tracked file keeps its `HEAD` version in the guest.

- The session summary lists each skipped path with its reason. For a
  scanner match it names the rule that matched, never the matched
  text.
- Each decision goes to `wb-hostd`'s audit writer with its rule
  (SEC10-audit).

No carry-in reaches the guest when no scanner is configured or chosen,
or when the scanner fails or times out. The session then starts at its
`HEAD` without the carry-in, and says why.

Committed content isn't scanned. Committing a file on a selected
branch is the user's choice to send it.

### No scanner yet

No secret scanner is chosen yet (S06-02-image-build, "Sealing"), so
carry-in is off until one is. The carry-in scan assumes the scanner is
precise enough on working trees that it skips few files by mistake.
That isn't measured.

## Decisions

### Clean the remote URLs

The guest gets the host's remotes, cleaned to HTTPS.

- **Why:** the agent pushes to the forge through the egress gateway.
  The guest has no SSH keys (SEC04-no-guest-secrets) and reaches the
  forge only over HTTPS (I47). An SSH port says nothing about the
  forge's HTTPS port, so the cleaner drops it.
- **Decided:** B41-git-data-scope, item 3.

### Scan the carry-in

`wb` scans every changed and untracked file before the carry-in leaves
the host.

- **Why:** an untracked file the user hasn't ignored yet can hold a
  secret.
- **Decided:** B41-git-data-scope, item 2.

**Status:** Draft
