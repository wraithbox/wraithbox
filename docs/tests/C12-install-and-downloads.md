# C12 - Install and Downloads

**Purpose:** The test cases for installing and upgrading Wraith Box:
`wb-download` and the checks `wb-hostd` makes on what it fetches,
`wb setup`, and the migrations of `state.db` and configuration.

**Verifies:** S17-install-and-upgrade

## Unit tests

### wb-download

- **Self-check** (S17-03-downloads). Confined,
  `wb-download` fails to open a file under the home directory, to
  connect to TCP port 80 and to UDP port 123, to reach the Keychain
  services, and to run `/bin/echo`. It lists the host's interfaces with
  `getifaddrs`, and it completes a TLS fetch on port 443 against the
  system roots.
- **Deny tests.** Through a local test server and a fake resolver, feed
  it each of these, and see each refused with its rule and a refused
  4002 record that names it:
  - a host outside its entry (`download-host-refused`);
  - plain HTTP, and a redirect (`download-redirect-refused`);
  - a name that resolves to a private address, and a failure to list
    the host's interfaces (`upstream-address-refused`);
  - a server that doesn't accept the connection
    (`download-connect-timeout`), one that stops sending
    (`download-idle-timeout`), and one that sends past the request's
    deadline (`download-deadline`);
  - a closed request pipe (`download-cancelled`);
  - a `200` to a range request, and a `206` with another
    `Content-Range` (`download-range-refused`);
  - a short body (`download-short`);
  - a manifest of 1 MiB and one byte (`download-too-large`).
- **Bounds.** A body longer than the size is written only up to the
  size. A write past the entry's file size limit fails.
- **Proxy.** A run with `HTTPS_PROXY` set to a local proxy sees the
  proxy get no connection.

### wb-hostd

- **Result records.** Feed it records with each field outside its form,
  a URL with a port, a user, a query, a fragment, an empty segment and
  a `..` segment, and a 17th request, and see each end the run with
  `download-record-form`.
- **Restore image.** See each refused with its rule:
  - a catalog entry below the minimum, with and without
    `--allow-older-macos` (`restore-image-below-minimum`);
  - an entry older than the last sealed image, without
    `--allow-older-macos` (`restore-image-rollback`). With the flag, it
    is accepted;
  - less free space than the entry's file size limit, less the
    partial file, plus the floor (`download-no-space`);
  - a file of the right size with another SHA-1
    (`restore-image-hash-mismatch`).

  A partial file that is a symbolic link isn't opened. A changed entry
  truncates the partial file.
- **Claude Code.** A file left in `<data>/downloads/` is deleted at the
  build's start. A version from outside the build's input is refused.
  A channel file over 64 bytes, empty, with two line feeds, or with
  anything but a version, is refused (`claude-channel-invalid`). A
  channel version older than the last sealed image's is refused
  (`claude-release-rollback`). The same version from
  `--claude-version` is accepted.
  `wb-release-check` starts only after `wb-download` has exited.
- **Audit.** A download record that can't be written cancels the run
  with `download-audit-failed`.

### Bundle

Spec: S17-01-bundle.

- **Entitlement check.** Each case of S17-01-bundle, "The entitlement
  check", fails the check with the program and the rule in its message.
  The tests are the `TestC05BuildCheck` ones in `internal/appbundle`
  (C05-credentials-and-store, "Access groups").
- **Identity.** A `WB_TEAM_ID` that isn't ten uppercase letters and
  digits fails the build before anything is built, with a message that
  names it. A valid one gives `nl.wraithbox.dev.<teamid>` in lowercase.
  A `WB_BUNDLE_ID` that isn't reverse-DNS is refused
  (`TestC12BundleIdentity`).
- **Install.** Installing two bundles in turn at one temporary
  destination leaves the second, with no file of the first and nothing
  next to it (`TestC12BundleInstallSwap`). A destination with another
  name, or one that is there and isn't a bundle, is refused. A copy
  that doesn't verify is never renamed in.
- **Build.** CI's macOS job builds the ad-hoc bundle and runs the check
  on it (`mise run app:bundle`).

### wb setup

See each refused with its rule, and nothing written:

- a bundle outside both `Applications` folders
  (`setup-bundle-location`);
- a bare `wb`, an unstamped one, and one whose `WBBuildVersion`
  differs (`setup-not-in-bundle`);
- a regular file, and a link into another directory, at
  `~/.local/bin/wb` (`setup-link-exists`);
- a changed plist with a VM running, with a same-version `wb-hostd`
  that can't list its VMs, and with a `wb-hostd` of another build
  version, whose message names both versions (`setup-vms-running`);
- a Mac without Apple Silicon or with an older macOS
  (`setup-host-unsupported`), and a Linux or Windows host
  (`setup-host-later`).

With an unchanged plist and a loaded agent, it leaves the agent running
and asks `wb-hostd` nothing. With no `wb-hostd` listening, it goes on.
It runs `/bin/launchctl` with only `PATH` in its environment. The plist
is mode 644, written by rename. An old Wraith Box link is replaced, and
a `~/.local/bin` that isn't on `PATH` gets the line to add. The
projects root and the probe file are made when missing and left alone
when present. The tests are the `TestC12Setup` ones in
`internal/setup` and `internal/platform/service`, and the reader of
`WBBuildVersion` is `TestReadBuildVersion` in `internal/appbundle`.

### Migrations

A failed backup, a failed migration, a newer database, and a renamed
and an unknown configuration key each put `wb-hostd` in refusing mode
with their rule, and it starts no daemon. An existing `.bak` name is
kept. A backup file is mode 600. The tests are the `TestC12` ones in
`internal/hostd/state`, `internal/config` and `internal/hostd/startup`.

## Fuzzing

Bytes from the internet: Apple's restore image catalog, the
`Content-Range` header, and the result records `wb-hostd` reads from
`wb-download`.

**Status:** Draft
