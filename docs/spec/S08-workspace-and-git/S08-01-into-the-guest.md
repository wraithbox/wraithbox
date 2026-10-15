# S08-01 - Into the Guest

**Purpose:** How the project's code reaches the guest: the clone, its transport,
its remotes, and the uncommitted changes carried in at session start.

## Design

### Project clone

On first use, the project user's home receives a
clone of the host repository, transferred over vsock. Later sessions
fetch only new objects.

### Transport

The guest uses a git remote helper (`git-remote-wb`,
shipped with `wb-guestd`) for a remote named `host`. It tunnels git's
smart protocol over a local Unix socket to `wb-guestd`, which sends
it to `wb-hostd` on a host-guest connection the host opened
(S04-architecture, B29-vsock-handoff). `wb-hostd` runs `git upload-pack`
**read-only** against the project's export repository
(`projects/<id>/export.git`, B41-git-data-scope), never against the
user's repository: repository hooks disabled, system and global git
configuration ignored, environment scrubbed. Nothing in the guest can
write to the host repository through this path.

### Protocol version

Git doesn't tell a `connect` helper which
protocol version it wants. The helper asks for v2 itself, and
`wb-hostd` passes only `version=0`, `1` or `2` on to git.

### Remote URLs

(B41-git-data-scope, item 3). The guest's clone gets
the host repository's remotes next to `host`, so the agent can push
to the forge through the egress gateway (SEC06-repo-writes). `wb`
reads each remote's URLs as the user's git resolves them
(`git remote get-url --all`, which applies `url.<base>.insteadOf`)
and cleans each one before it sends it on:
- git's own rule tells an SSH short form from a path: `host:path` is
  SSH only when no slash comes before the first colon, and on Windows
  a drive letter (`C:/x`) is a local path. An IPv6 host is in
  brackets (`[::1]`);
- user name, password, query string and fragment are removed;
- an SSH remote, `ssh://[user@]host[:port]/path` or the short form
  `[user@]host:path`, becomes `https://host/path`. The port is
  dropped, because an SSH port says nothing about the forge's HTTPS
  port. The guest has no SSH keys (SEC04-no-guest-secrets) and
  reaches the forge only over HTTPS (I47);
- an `https://` URL keeps its host, port and path;
- any other URL isn't given to the guest: `http://`, `git://`,
  `file://`, `git+ssh://`, `ssh+git://`, a local path, a
  `<helper>::` URL, a URL whose host is an IP address, and a URL the
  cleaner can't parse. The session summary lists each such remote
  with the reason.

An SSH host alias from the user's SSH configuration, or a path that
only works on a non-default SSH port, gives an HTTPS URL that doesn't
reach the forge. The push then fails at the egress gateway, which
refuses the unknown host (fail closed). Nothing else from the
repository's configuration reaches the guest: no `pushurl`,
`pushInsteadOf`, `insteadOf`, `proxy`, `http.<url>.extraHeader` or
`credential.*`. The guest pushes to the cleaned URL. A `.gitmodules`
URL is tracked content and isn't rewritten (submodules are in "Open
points"). Project identity records the URLs after this cleaning, but
they aren't part of the project key (S05-cli, "Naming"). Each withheld URL goes to `wb-hostd`'s audit
writer with its reason (SEC10-audit).

### Session start

`wb-guestd` creates a worktree for the session at
`<home>/sessions/<session-id>` (S06-vm-lifecycle, "Projects and
sessions inside a VM"), checked out at
the session's `HEAD` in `export.git`, then applies the carry-in:
staged and unstaged changes as a binary diff, plus untracked files
that are not ignored, up to a size cap (FR04-carry-in). Files over
the cap are listed in the session summary rather than silently
dropped.

### Carry-in scan

(B41-git-data-scope, item 2). A new
`credentials.json` that the user hasn't ignored yet is an untracked
file that isn't ignored, so the carry-in would send it. `wb` collects
the carry-in in the user's repository and checks each changed or
untracked path before it sends any of it on (SEC04-no-guest-secrets):
- the guest gets a symbolic link as a link, with its target
  string only, and `wb` never reads the file it points to
  (SEC02-no-host-fs-share);
- anything other than a regular file or a symbolic link (a FIFO, a
  socket, a device) is skipped;
- the size cap applies first. The scanner doesn't read a file over it;
- `wb` scans the new content of each changed file, both its version
  in the index and its version in the working tree, and each
  untracked file, with the secret scanner of the image seal
  (S06-vm-lifecycle, "Sealing"). It scans the content, not the diff
  text, which for a binary file is compressed;
- a file `wb` can't read, or that the scanner can't decode, is
  skipped.

A skipped file is skipped whole: the guest doesn't get an untracked
file, and a tracked file keeps its `HEAD` version in the guest. The
session summary lists each skipped path with its reason, and for a
scanner match the rule that matched, never the matched text. Each
decision goes to `wb-hostd`'s audit writer with its rule
(SEC10-audit). When no scanner is configured or chosen, or the
scanner fails or times out, no carry-in reaches the guest: the
session starts at its `HEAD` without it and says why. Committed
content isn't scanned, because committing a file on a selected
branch is the user's choice to send it.

### No scanner yet

No secret scanner is chosen yet (S06-vm-lifecycle, "Sealing"), so
carry-in is off until one is. The carry-in scan assumes the scanner is
precise enough on working trees that it skips few files by mistake,
which isn't measured.

**Status:** Draft
