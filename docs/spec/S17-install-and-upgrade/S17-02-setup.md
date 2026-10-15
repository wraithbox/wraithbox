# S17-02 - Install

**Purpose:** How `wb setup` installs Wraith Box from its bundle without
administrator rights, and how `wb setup --remove` takes it out.

## Design

### Install: `wb setup`

`wb setup` installs from the bundle it runs in, and doesn't need
administrator rights (SEC12-least-privilege). On macOS:

- **The bundle.** It resolves its own executable's real path, and
  refuses with `setup-not-in-bundle` unless that is `Contents/MacOS/wb`
  of a bundle whose `Info.plist` has the same `WBBuildVersion` as `wb`. It
  refuses with `setup-bundle-location` a bundle that isn't directly in
  `~/Applications` or `/Applications`. The agent then never points into
  a build directory that a cleanup or a removed worktree deletes.
- **The agent.** It writes `~/Library/LaunchAgents/nl.wraithbox.hostd.plist`,
  mode 644, to a new file that it renames over the old one. The agent
  runs the bundle's `Contents/MacOS/wb-hostd` by absolute path, with
  `RunAtLoad` and `KeepAlive` set and no `AbandonProcessGroup`
  (S04-07-supervision, "Who starts whom"). The label is fixed, so a user
  has one install. When the loaded agent's plist has the same bytes, it
  leaves the agent running. Otherwise replacing it stops `wb-hostd` and
  its VMs, so it asks `wb-hostd` first and refuses with
  `setup-vms-running`, naming each VM and its sessions, while one runs.
  A `wb-hostd` of another build version refuses `wb`
  (S04-08-records-and-bounds, "Version skew"), so `wb setup` can't read its VMs.
  It then refuses with `setup-vms-running` as well, names both
  versions, and says to stop the VMs with the `wb` of the running
  build. When no `wb-hostd` answers, no VM runs, because VMs stop with
  `wb-hostd` (S04-07-supervision, "When `wb-hostd` stops"), and it goes
  on.
  Then it runs `launchctl bootout` and `launchctl bootstrap gui/<uid>`.
  It runs `/bin/launchctl` by absolute path, with the environment
  `PATH=/usr/bin:/bin` and nothing else.
- **The link.** It links `~/.local/bin/wb` to the bundle's
  `Contents/MacOS/wb`. A file there is a link into a Wraith Box bundle
  when `lstat` shows a symbolic link, its target ends in
  `/Contents/MacOS/wb`, and the `Contents/Info.plist` of that target's
  bundle has a `WBBuildVersion` key. It replaces only such a link, and
  refuses any other file with `setup-link-exists`. When `~/.local/bin`
  isn't on `PATH`, it prints the line to add, and it never edits a
  shell file.

Running it again after the bundle moved rewrites the agent and the
link. Rejected: `SMAppService`, which needs a Swift or Objective-C call
in the registering process and an approval in System Settings, and a
link in `/usr/local/bin`, which needs administrator rights on many
Macs.

`wb setup --remove` refuses with `setup-vms-running` in the same way,
then unloads the agent, which stops `wb-hostd` (S04-07-supervision, "When `wb-hostd` stops"), and deletes the plist and the link. It keeps
`<config>`, `<data>`, `<logs>` and the secret store items, and prints
where they are.

The bundle, location and `launchctl` rules are macOS rules. On Windows
and Linux the installer and service unit of S10-tech-stack take the
place of the agent, and a file `wraithbox-version` next to the programs
holds the build version.

**Status:** Draft
