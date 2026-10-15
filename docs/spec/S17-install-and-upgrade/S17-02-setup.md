# S17-02 - Install

**Purpose:** How `wb setup` installs Wraith Box from its bundle without
administrator rights, and how `wb setup --remove` takes it out.

## Design

`wb setup` installs from the bundle it runs in. It doesn't need
administrator rights (SEC12-least-privilege). On macOS it checks the
bundle, then installs the agent and the link below.

### The bundle check

`wb setup` resolves its own executable's real path. It refuses with
`setup-not-in-bundle` unless that path is `Contents/MacOS/wb` of a
bundle whose `Info.plist` has the same `WBBuildVersion` as `wb`.

It refuses with `setup-bundle-location` a bundle that isn't directly
in `~/Applications` or `/Applications`. The agent then never points
into a build directory that a cleanup or a removed worktree deletes.

### The agent

`wb setup` writes `~/Library/LaunchAgents/nl.wraithbox.hostd.plist`,
mode 644. It writes a new file and renames it over the old one.

- The agent runs the bundle's `Contents/MacOS/wb-hostd` by absolute
  path.
- `RunAtLoad` and `KeepAlive` are set, and `AbandonProcessGroup` isn't
  (S04-07-supervision, "Who starts whom").
- The label is fixed, so a user has one install.

When the loaded agent's plist has the same bytes, `wb setup` leaves
the agent running. Otherwise, replacing the agent stops `wb-hostd` and
its VMs. So `wb setup` first asks `wb-hostd`:

1. While a VM runs, it refuses with `setup-vms-running`, and names
   each VM and its sessions.
2. A `wb-hostd` of another build version refuses `wb`
   (S04-08-records-and-bounds, "Version skew"), so `wb setup` can't
   read its VMs. It then refuses with `setup-vms-running` as well. The
   message names both versions, and says to stop the VMs with the `wb`
   of the running build.
3. When no `wb-hostd` answers, no VM runs, because VMs stop with
   `wb-hostd` (S04-07-supervision, "When `wb-hostd` stops"). It goes
   on.

Then it runs `launchctl bootout` and `launchctl bootstrap gui/<uid>`.
It runs `/bin/launchctl` by absolute path, with the environment
`PATH=/usr/bin:/bin` and nothing else.

### The link

`wb setup` links `~/.local/bin/wb` to the bundle's
`Contents/MacOS/wb`.

A file there is a link into a Wraith Box bundle when all of these
hold:

- `lstat` shows a symbolic link;
- its target ends in `/Contents/MacOS/wb`;
- the `Contents/Info.plist` of that target's bundle has a
  `WBBuildVersion` key.

It replaces only such a link. It refuses any other file with
`setup-link-exists`.

When `~/.local/bin` isn't on `PATH`, it prints the line to add. It
never edits a shell file.

### Running it again

Running `wb setup` again after the bundle moved rewrites the agent and
the link.

### Removal

`wb setup --remove` refuses with `setup-vms-running` in the same way.
Then it:

1. unloads the agent, which stops `wb-hostd` (S04-07-supervision,
   "When `wb-hostd` stops");
2. deletes the plist and the link.

It keeps `<config>`, `<data>`, `<logs>` and the secret store items. It
prints where they are.

### Windows and Linux

The bundle, location and `launchctl` rules are macOS rules. On Windows
and Linux, the installer and service unit of S10-tech-stack take the
place of the agent. A file `wraithbox-version` next to the programs
holds the build version.

## Decisions

### A LaunchAgent plist and a link in `~/.local/bin`

`wb setup` writes the LaunchAgent plist itself, and links `wb` from
`~/.local/bin`.

- **Rejected:** `SMAppService`, which needs a Swift or Objective-C
  call in the registering process and an approval in System Settings.
- **Rejected:** a link in `/usr/local/bin`, which needs administrator
  rights on many Macs.

**Status:** Draft
