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
  (S04-architecture, "Who starts whom"). The label is fixed, so a user
  has one install. When the loaded agent's plist has the same bytes, it
  leaves the agent running. Otherwise replacing it stops `wb-hostd` and
  its VMs, so it asks `wb-hostd` first and refuses with
  `setup-vms-running`, naming each VM and its sessions, while one runs.
  A `wb-hostd` of another build version refuses `wb`
  (S04-architecture, "Version skew"), so `wb setup` can't read its VMs.
  It then refuses with `setup-vms-running` as well, names both
  versions, and says to stop the VMs with the `wb` of the running
  build. When no `wb-hostd` answers, no VM runs, because VMs stop with
  `wb-hostd` (S04-architecture, "When `wb-hostd` stops"), and it goes
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
then unloads the agent, which stops `wb-hostd` (S04-architecture, "When
`wb-hostd` stops"), and deletes the plist and the link. It keeps
`<config>`, `<data>`, `<logs>` and the secret store items, and prints
where they are.

The bundle, location and `launchctl` rules are macOS rules. On Windows
and Linux the installer and service unit of S10-tech-stack take the
place of the agent, and a file `wraithbox-version` next to the programs
holds the build version.

### Downloads: `wb-download`

Only `wb-download` fetches what an image build needs (decided on I49).
`wb-hostd` has no network, and `wb-proxyd` serves guests. `wb-download`
is Go, one run per download, started through `wb-launcher` from one of
two fixed entries. Each entry has an instance cap of one, its hosts
compiled into the program, and a file size limit (`RLIMIT_FSIZE`) that
`wb-launcher` sets before it starts the program:

| Entry | Hosts | Fetches | File size limit |
|---|---|---|---|
| `wb-download-restore` | `mesu.apple.com`, `updates.cdn-apple.com` | Apple's restore image catalog and one restore image | 40 GiB |
| `wb-download-claude` | `downloads.claude.ai` | one Claude Code release (S14-claude-in-guest, "Install") | 1 GiB |

On Windows and Linux, `wb-download` confines itself with that
platform's mechanism (S12-platforms), with the same rules in its code.

- **Confinement.** It confines itself first thing, from a compiled-in
  profile, the network part of the `wb-proxyd` profile that
  X23-sandboxed-daemons measured for I61, limited to port 443 and
  without the secret store services:

  ```scheme
  (version 1)
  (deny default)
  (allow network-outbound (remote tcp "*:443"))
  (allow network-outbound (remote udp "*:53"))
  (allow network-outbound (literal "/private/var/run/mDNSResponder"))
  (allow file-read-metadata (literal "/etc") (literal "/var"))
  (allow file-read* (literal "/private/etc/resolv.conf")
    (literal "/private/var/run/resolv.conf") (literal "/private/etc/hosts"))
  (allow mach-lookup (global-name "com.apple.trustd.agent"))
  (allow file-read-metadata (literal (param "BUNDLE")))
  ```

  `BUNDLE` is the real path of `Wraith Box.app`, the bundle whose
  program it is, worked out before it confines itself. Files, the
  secret store and starting programs are denied. It can still use its
  inherited descriptors.
- **Hosts.** Seatbelt can't name hosts. The code refuses a host
  outside its entry with `download-host-refused`, a URL that isn't
  HTTPS on port 443, every redirect (`download-redirect-refused`), and
  an address under the "Upstream address" rule of S07-egress-gateway
  (`upstream-address-refused`). That rule lists the host's interfaces
  with `getifaddrs`, and a failure to list them refuses the connection
  with `upstream-address-refused` as well. The profile also allows the
  `sysctl` read that `getifaddrs` makes, and the self-check test of
  S11-verification-and-spikes measures that rule. TLS is checked
  against the system roots. Its HTTP client's `Transport.Proxy` is nil, so `HTTPS_PROXY`
  and the system's proxy settings don't apply.
- **Deadlines.** A connection that isn't up in 30 s fails with
  `download-connect-timeout`, and a response with no byte for 60 s with
  `download-idle-timeout`. A whole request has 2 minutes for the
  catalog, a `HEAD`, the manifest or the signature, 30 minutes for the
  Claude Code binary, and 12 hours for the restore image
  (`download-deadline`). `wb-hostd` cancels a run by closing its
  request pipe, and `wb-download` stops at that end of file with
  `download-cancelled`.
- **The result record.** `wb-download` writes fixed-format records to
  its pipe, and `wb-hostd` checks each field's form:

  | Field | Form |
  |---|---|
  | macOS version | `^[0-9]{1,3}(\.[0-9]{1,3}){1,2}$` |
  | macOS build | `^[0-9]{2}[A-Z][0-9]{1,5}[a-z]?$` |
  | URL | `https://updates.cdn-apple.com/` and a path of 1 to 512 characters of `A-Z a-z 0-9 . _ / -` ending in `.ipsw`, with no empty segment, no `.` or `..` segment, and no port, user, query or fragment |
  | SHA-1 | 40 lowercase hexadecimal digits |
  | size | a decimal number from 1 to 42,949,672,960 (40 GiB) |
  | host, rule, status, bytes | one of the entry's hosts, a rule name of this section, a three-digit status, a decimal count |
  | path | the catalog's path, the path of the URL above, or `/claude-code-releases/<version>/` followed by `manifest.json`, `manifest.json.sig` or `darwin-arm64/claude`, with the version's form below |
  | address | an IPv4 address in dotted decimal, or an IPv6 address in the form Go's `netip.Addr.String` writes, without a zone |

  A field outside its form, or a run that reports more than 16 requests,
  ends the run with `download-record-form`. The record parser has a
  fuzz target.
- **Audit.** For each request, and each refusal of one, it reports the
  host, the address it connected to, the path, and the status or the
  rule. `wb-hostd` writes each as an HTTP Activity (4002) record, a
  refusal as a refused one with its rule (SEC10-audit). These records
  belong to no VM, so they don't count against any VM's audit budget
  (S09-policy-credentials-audit, "Audit"), and they're never dropped.
  When one can't be written, `wb-hostd` cancels the run with
  `download-audit-failed`, and the rule of S09-policy-credentials-audit
  for a record that can't be written applies.
- **Space.** Before it starts either entry, `wb-hostd` refuses with
  `download-no-space` when the volume of `<data>` has less free space
  than the entry's file size limit, less what the partial restore
  image already holds, plus `audit_free_space_floor`
  (S09-policy-credentials-audit). For a Claude Code release, the limit
  counts once for each of its three files. The check uses only numbers
  the host knows before the run, and the file size limit holds what a
  run writes to them.

Any exit other than success fails the build with its rule, and nothing
restarts `wb-download`. The next `wb image build` resumes the restore
image.

#### The restore image

1. **The entry.** `wb-hostd` sends the host's macOS version.
   `wb-download` fetches the catalog from
   `https://mesu.apple.com/assets/macos/com_apple_macOSIPSW/com_apple_macOSIPSW.xml`,
   at most 4 MiB, and parses it. The catalog parser has a fuzz target.
   It picks the newest restore image not newer than the host's macOS,
   sends a `HEAD` for its size, and reports the version, build, URL,
   SHA-1 and size.
2. **The floor.** `wb-hostd` refuses an entry older than the minimum
   compiled into Wraith Box, 27.0.1, which X17-image-build built, with
   `restore-image-below-minimum`. It refuses an entry older than the
   macOS version of the last image a build sealed, which `state.db`
   records, with `restore-image-rollback`, unless the user passed
   `wb image build --allow-older-macos`.
3. **Resume or start over.** `wb-hostd` keeps the entry of the partial
   file `<data>/images/macos-restore.ipsw.partial` in `state.db`. When
   the new entry is the same, it answers with the partial file's size
   as the offset. Otherwise it truncates the file and answers 0. It
   opens the file with `O_NOFOLLOW`, creates it mode 600, requires a
   regular file from `fstat`, and passes the descriptor.
4. **The fetch.** At offset 0, `wb-download` sends a plain `GET` and
   requires a `200` with a `Content-Length` equal to the size. At a
   larger offset it sends `Range: bytes=<offset>-`, and requires a `206`
   with exactly `Content-Range: bytes <offset>-<size − 1>/<size>`.
   Anything else fails with `download-range-refused`. It writes from the
   offset, and stops reading at the size. A shorter body fails with
   `download-short`. The `Content-Range` parser has a fuzz target.
5. **The check.** After `wb-launcher` reports the exit, `wb-hostd`
   checks the size with `fstat` on its own descriptor, hashes the file
   with SHA-1 from that descriptor, and renames it to
   `macos-restore.ipsw`. A mismatch deletes it and fails the build with
   `restore-image-hash-mismatch`. Before each start of
   `wb-vmd-install`, `wb-hostd` checks the SHA-1 again unless it did in
   the same build. A build that seals its image deletes the restore
   image.
6. **The trust base.** The SHA-1 comes from a catalog fetched over TLS
   checked against the system trust store, and binds the file to that
   entry. The catalog isn't signed, so a compromised `wb-download` can
   report any entry. Against that, the design relies on Virtualization's
   installer refusing a restore image that Apple didn't sign. That
   property is unmeasured (I262).

#### A Claude Code release

1. **The version** is a build input that `wb-hostd` holds, from the
   user's `wb image build` (I220 decides which release a build picks),
   never a value from a guest or a repository. `wb-hostd` and
   `wb-download` both refuse one outside
   `^[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,6}$`.
2. **The files.** At the build's start `wb-hostd` deletes what an
   earlier build left in `<data>/downloads/`, without following
   symbolic links. It creates `manifest.json`, `manifest.json.sig` and
   `claude` there with `O_CREAT|O_EXCL|O_NOFOLLOW`, mode 600, and
   passes the three descriptors.
3. **The fetch.** `wb-download` fetches `<version>/manifest.json`, at
   most 1 MiB, `<version>/manifest.json.sig`, at most 64 KiB, and
   `<version>/darwin-arm64/claude`, at most 1 GiB. A larger file fails
   with `download-too-large`, and after the exit `wb-hostd` checks each
   size with `fstat` and refuses the same way. It parses none of them.
4. **Exit before check.** `wb-hostd` gives the files to
   `wb-release-check` only after `wb-launcher` reports that
   `wb-download` exited, so nothing writes them after the check. The
   build deletes them when it ends.

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
  transaction that also sets the new version.
- **The backup.** Before the first pending migration, `wb-hostd` writes
  a copy with `VACUUM INTO` to a new temporary name in `<data>`, mode
  600, and renames it to `state.db.v<N>.bak`, `<N>` the version it had.
  It never writes over an existing `.bak` file: when that name exists,
  the copy from an earlier attempt stays. It keeps the two newest
  copies.
- **Configuration.** No process rewrites a file in `<config>`.
  `wb-hostd` refuses a file with a key that a release renamed or
  removed, with `config-key-renamed` and the file, the line and the new
  key, and a file with any other unknown key, with `config-key-unknown`.
- **Refusing mode.** A failed backup (`state-db-backup-failed`), a
  failed migration (`state-db-migration-failed`), a database newer than
  `wb-hostd` knows (`state-db-newer`), and a refused configuration file
  put `wb-hostd` in refusing mode. It keeps running, so launchd doesn't
  restart it in a loop. It doesn't start a daemon or a VM. It answers
  `wb status` and refuses every other request with the rule, naming
  the versions, the file and line, or the newest copy.
- Secret store item names are fixed (S15-least-privilege), and images
  aren't migrated. A saved state of another build version is deleted
  (S06-vm-lifecycle).

## Out of scope

- Release builds: Developer ID signing, notarization and the Homebrew
  cask (S10-tech-stack).
- A restore image from a file the user names, for hosts without access
  to Apple's servers (I263).
- A macOS version other than the newest the host supports, apart from
  `--allow-older-macos`, and an upstream proxy for downloads.
- Deleting data and store items at uninstall (I264), and downgrades:
  an older `wb-hostd` refuses a newer `state.db`.

**Status:** Draft
