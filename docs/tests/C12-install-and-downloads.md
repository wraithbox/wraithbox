# C12 - Install and Downloads

**Purpose:** The test cases for installing and upgrading Wraith Box:
`wb-download` and the checks `wb-hostd` makes on what it fetches,
`wb setup`, and the migrations of `state.db` and configuration.

**Verifies:** S17-install-and-upgrade

## Unit tests

### wb-download

- **Self-check** (S17-install-and-upgrade, "Downloads"). Confined,
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
  `wb-release-check` starts only after `wb-download` has exited.
- **Audit.** A download record that can't be written cancels the run
  with `download-audit-failed`.

### wb setup

See each refused with its rule:

- a bundle outside both `Applications` folders
  (`setup-bundle-location`);
- a bare `wb` (`setup-not-in-bundle`);
- a regular file, and a link into another directory, at
  `~/.local/bin/wb` (`setup-link-exists`);
- a changed plist with a VM running, and a `wb-hostd` of another build
  version (`setup-vms-running`).

With an unchanged plist, it leaves the agent running. With no
`wb-hostd` running, it goes on. It runs `/bin/launchctl` with only
`PATH` in its environment.

### Migrations

A failed backup, a failed migration, a newer database, and a renamed
and an unknown configuration key each put `wb-hostd` in refusing mode
with their rule, and it starts no daemon. An existing `.bak` name is
kept. A backup file is mode 600.

## Fuzzing

Bytes from the internet: Apple's restore image catalog, the
`Content-Range` header, and the result records `wb-hostd` reads from
`wb-download`.

**Status:** Draft
