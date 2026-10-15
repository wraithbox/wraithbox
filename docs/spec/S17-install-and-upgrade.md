# S17 - Install, Downloads and Upgrades

**Purpose:** Build, install, upgrade and remove a Wraith Box bundle,
move its stored data to a new version, and download what an image
build needs from the internet.

**Requirements:** SEC12-least-privilege (no root, each process gets only
what it needs), SEC10-audit (every egress decision recorded),
SEC13-bounded-resources (what the host spends is capped),
NFR06-explained-refusals (each refusal names its rule and the fix).

Brief: B49-install-upgrade

## Design

![wb-hostd opens the files, then starts wb-download through wb-launcher. wb-download fetches Apple's catalog and a restore image, or a Claude Code release, over HTTPS from the hosts compiled into its entry, into those descriptors. wb-hostd checks the restore image's hash, and wb-release-check checks the Claude Code release, before wb-vmd-install or wb-guestd uses them. wb-hostd itself has no network.](S17-install-and-upgrade.svg)

### The bundle

On macOS the bundle is `Wraith Box.app`, with the programs and helper
bundles of S10-tech-stack, "Packaging and signing". The programs
without a Keychain access group, `wb-download` and `wb-diskfmt`
included, are in `Contents/MacOS`. The guest tools disk
(S06-vm-lifecycle, "`wb-guestd`") is `Contents/Resources/wb-guest-tools.img`.
The app's main executable is the notification helper, because macOS
shows a notification under the bundle of the process that posts it.
`Info.plist` holds the bundle identifier and, under the key
`WBBuildVersion`, the build version that every program is stamped with
(S10-tech-stack, "One build version"). `wb-hostd` reads that key to
notice an upgrade (S04-architecture, "Upgrades").

### Building a bundle

`mise run build` runs `app:bundle` on a macOS host, in this order:

1. One version stamp for the whole run (S10-tech-stack).
2. `go build` of the host programs, and of `wb-guestd` and
   `git-remote-wb` for the guest.
3. `swift build -c release` of `wb-vmd`.
4. `xcodebuild` of the app project, `packages/wraithbox-swift/WraithBox.xcodeproj`,
   one app target whose executable is the notification helper. With
   `WB_TEAM_ID` set, it builds with automatic signing for that team and
   the bundle identifier `nl.wraithbox.dev.<teamid>` or `WB_BUNDLE_ID`,
   and fails when Xcode gives it no profile (S12-platforms, "Development
   builds on macOS"). Without it, the identifier is
   `nl.wraithbox.dev.adhoc` or `WB_BUNDLE_ID`, and Xcode signs nothing
   (`CODE_SIGNING_ALLOWED=NO`).
5. The guest tools disk: the guest programs, signed as in step 7, and a
   `VERSION` file in one APFS volume of a raw disk image, the format
   Virtualization attaches.
6. Assembly into the app from step 4: the programs, `wb-vmd`, a helper
   bundle for each program with a group, with the app's profile copied
   in when there is one, the guest tools disk, and `WBBuildVersion`.
7. Signing from the inside out, with the hardened runtime: each
   program, then each helper bundle, then the app. The identity is the
   team's Apple Development certificate when `WB_TEAM_ID` is set, and
   ad-hoc (`codesign -s -`) otherwise. Only `wb-vmd` gets the
   virtualization entitlement.
8. The entitlement check of S10-tech-stack and `codesign --verify --strict`
   on the app. Either failing fails the build.

The bundle is written to `packages/wraithbox-swift/.build/Wraith Box.app`.
CI runs `app:bundle` without `WB_TEAM_ID`. Every pull request builds
the ad-hoc bundle and runs its checks. `mise run app:install` installs
the built bundle as `~/Applications/Wraith Box.app` ("Upgrades").

### Install: `wb setup`

`wb setup` installs from the bundle it runs in. It resolves its own
executable's real path, and refuses with `setup-not-in-bundle` unless
that is `Contents/MacOS/wb` of a bundle whose `WBBuildVersion` is its
own. It doesn't need administrator rights (SEC12-least-privilege):

- It writes `~/Library/LaunchAgents/nl.wraithbox.hostd.plist`, by a
  new file and a rename, mode 644. The agent runs the bundle's
  `Contents/MacOS/wb-hostd` by absolute path, with `RunAtLoad` and
  `KeepAlive` set and no `AbandonProcessGroup` (S04-architecture, "Who
  starts whom"). The label is fixed, so a user has one install.
- It replaces a loaded agent with `launchctl bootout`, then loads the
  new one with `launchctl bootstrap gui/<uid>`.
- It links `~/.local/bin/wb` to the bundle's `Contents/MacOS/wb`. It
  replaces only a link into a Wraith Box bundle, and refuses any other
  file there with `setup-link-exists`. When `~/.local/bin` isn't on
  `PATH`, it prints the line to add, and it never edits a shell file.

Running it again after the bundle moved rewrites both. Rejected:
`SMAppService`, which needs a Swift or Objective-C call in the
registering process and
an approval in System Settings, and a link in `/usr/local/bin`, which
needs administrator rights on many Macs.

`wb setup --remove` unloads the agent, which stops `wb-hostd` and its
VMs (S04-architecture, "When `wb-hostd` stops"), and deletes the plist
and the link. It keeps `<config>`, `<data>`, `<logs>` and the secret
store items, and prints where they are.

### Downloads: `wb-download`

Only `wb-download` fetches what an image build needs (decided on I49).
`wb-hostd` has no network, and `wb-proxyd` serves guests. It is Go, one
run per download, started through `wb-launcher` from one of two fixed
entries, each with an instance cap of one and its hosts compiled in:

| Entry | Hosts | Fetches |
|---|---|---|
| `wb-download-restore` | `mesu.apple.com`, `updates.cdn-apple.com` | Apple's restore image catalog and one restore image |
| `wb-download-claude` | `downloads.claude.ai` | one Claude Code release (S14-claude-in-guest, "Install") |

- **Confinement.** It confines itself first thing, from a compiled-in
  profile: outbound TCP to port 443, name lookups, and the certificate
  check services that X23-sandboxed-daemons measured for `wb-proxyd`,
  and its inherited descriptors. It denies files, the secret store and starting
  programs. Seatbelt can't name hosts. The code refuses any host
  outside its entry, plain HTTP, and every redirect
  (`download-redirect-refused`), and applies the "Upstream address"
  rule of S07-egress-gateway. TLS is checked against the system roots.
  It doesn't read a proxy setting.
- **Channels.** A pipe to `wb-hostd` for fixed-format requests and
  results, its log pipe, and the descriptors of the files `wb-hostd`
  opened for it. It reports each request's host, path, status and
  bytes, and `wb-hostd` writes each as an HTTP Activity (4002) record.
- **Restore image.** It fetches the catalog from
  `https://mesu.apple.com/assets/macos/com_apple_macOSIPSW/com_apple_macOSIPSW.xml`
  (at most 4 MiB), picks the newest restore image not newer than the
  host's macOS, and reports its version, build, URL, SHA-1 and size.
  The catalog parser has a fuzz target. `wb-hostd` refuses a URL that
  isn't HTTPS on `updates.cdn-apple.com`, and a size over 64 GiB. It
  refuses the download with `download-no-space` when the volume of
  `<data>` has less free space than the bytes still to fetch plus
  `audit_free_space_floor` (S09-policy-credentials-audit). It resumes a
  partial file only when the entry recorded for it in `state.db` is the
  same, and starts over otherwise. `wb-download` writes to the
  descriptor of `<data>/images/macos-restore.ipsw.partial`. After
  `wb-launcher` reports its exit, `wb-hostd` checks the size and the
  SHA-1 and renames the file to `macos-restore.ipsw`. A mismatch
  deletes it and fails the build with `restore-image-hash-mismatch`.
  Before each start of `wb-vmd-install`, `wb-hostd` checks the SHA-1
  again unless it did in the same build. Apple's catalog lists SHA-1
  only, and isn't signed. The check catches a damaged or swapped file.
  Against a compromised `wb-download`, the design relies on
  Virtualization's installer refusing a restore image that Apple didn't
  sign (I262). A build that seals its image deletes the restore image.
- **Claude Code.** It reads the version from the pipe, refuses one
  outside `^[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,6}$`, and fetches
  `manifest.json` (at most 1 MiB), `manifest.json.sig` (64 KiB) and
  `darwin-arm64/claude` (1 GiB) of that version into three new files
  in `<data>/downloads/` that `wb-hostd` opened. A larger file fails
  with `download-too-large`. It parses none of them. The build deletes
  them when it ends.

Any other exit fails the build with its rule, and nothing restarts it.
The next `wb image build` resumes the restore image.

### Upgrades

An upgrade replaces the bundle at its path. `app:install`, and later the
Homebrew cask, copy the new bundle next to the old one, rename the old
one aside, rename the new one in, and delete the old one. They never
copy over the files of an installed bundle, so launchd or `wb-launcher`
never starts a program from a bundle that is half old and half new.
Running processes keep their open files. Then
S04-architecture, "Upgrades", applies: `wb-hostd` sees a new
`WBBuildVersion`, exits, and launchd starts the new one. The guest
side updates from the guest tools disk at each VM's next cold start
(S06-vm-lifecycle).

A build signed by another team has another bundle identifier and other
access groups (S15-least-privilege), so it can't read the old team's
store items. The user sets the bindings again with `wb cred set`, and
each VM start is refused with `ca-key-restore` until `wb vm reset-ca`.

### Migrations

Only `wb-hostd` migrates, at start, after it confines itself and before
it serves a request. `wb` and the other processes never do.

- **`state.db`.** Its schema version is `PRAGMA user_version`. Numbered,
  forward-only migrations are compiled into `wb-hostd`, each in a
  transaction that also sets the new version. Before the first one,
  `wb-hostd` writes a copy with `VACUUM INTO` to `state.db.v<N>.bak`,
  `<N>` the version it had, and keeps the two newest copies.
- **Refusing mode.** When a migration fails, or the database's version
  is newer than `wb-hostd` knows, `wb-hostd` keeps running, so launchd
  doesn't restart it in a loop. It answers `wb status` and refuses
  every other request with `state-db-migration-failed` or
  `state-db-newer`, naming both versions and the newest copy.
- **Configuration.** No process rewrites a file in `<config>`.
  `wb-hostd` refuses a file with a key that a release renamed or
  removed, with `config-key-renamed` and the file, the line and the new
  key, and a file with any other unknown key, with `config-key-unknown`.
- Secret store item names are fixed (S15-least-privilege), and images
  aren't migrated. A saved state of another build version is deleted
  (S06-vm-lifecycle).

On Windows and Linux the installer and service unit of S10-tech-stack
take the place of `wb setup`'s files, and a file `wraithbox-version`
next to the programs holds the build version.

## Out of scope

- Release builds: Developer ID signing, notarization and the Homebrew
  cask (S10-tech-stack).
- A restore image from a file the user names, for hosts without access
  to Apple's servers (I263).
- A macOS version other than the newest the host supports, and an
  upstream proxy for downloads.
- Deleting data and store items at uninstall (I264), and downgrades:
  an older `wb-hostd` refuses a newer `state.db`.

**Status:** Draft
