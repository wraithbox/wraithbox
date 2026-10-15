# S10-02 - Packaging and Signing

**Purpose:** How Wraith Box is packaged and signed on each host, and
the one version every build stamps.

## Design

### macOS

An app bundle, `Wraith Box.app`, contains:

- `wb-hostd`, `wb-launcher`, `wb-vmd`, `wb-netd`, `wb-proxyd`, `wb-git`,
  `wb-prover`, `openshell-prover`, `wb-guestadmin`, `wb-credset`,
  `wb-build-ssh`, `wb-askpass`, `wb-diskfmt`, `wb-release-check`,
  `wb-download`;
- the notification helper;
- the guest tools disk image;
- `wb`, which the installer links onto `PATH`.

A bundle is needed for notifications, Keychain access groups, and
launch-agent registration. It uses the hardened runtime. Only `wb-vmd`
has the virtualization entitlement. How it is built, installed and
upgraded is in S17-install-and-upgrade. *Distribution* is via a
Homebrew cask, once signed builds exist.

### Helper bundles

Each program that holds a Keychain access group (S15-02-secret-store,
and S15-03-access-rules, "Declared access") is the main
executable of its own bundle, `Contents/Helpers/<name>.app`. The
executable is at `Contents/MacOS/<name>` inside it. The app's
provisioning profile is copied to its
`Contents/embedded.provisionprofile`.

These are `wb-credset`, which `wb cred` runs, `wb-proxyd`, and
`wb-guestadmin`, which `wb-launcher`'s three `wb-guestadmin-*` entries
start.

`wb` doesn't hold a group, and is a plain executable.

macOS looks for the profile that allows the restricted
`keychain-access-groups` entitlement only in the bundle whose main
executable starts. So AMFI kills a plain executable in `Contents/MacOS`
that claims a group at launch (-413, "No matching profile found").

Each helper bundle has the app's bundle identifier and `LSUIElement`.
Its entitlements hold the groups of its items, from the table in
S15-02-secret-store, "Keychain", and no others. The build fails unless:

- each helper's signed entitlements equal its row's groups exactly;
- every other program, `wb` included, has no `keychain-access-groups`
  entitlement.

The installer's `wb` link on `PATH` points to `Contents/MacOS/wb`. A
program without a group doesn't need a bundle of its own
(X29-dev-signing).

The layout is the same in every build, ad-hoc included. So
`wb-launcher`'s fixed program table and the installer's link hold in
each. An ad-hoc build's helper bundles have no profile and no group.

### Signing

Release builds get Developer ID signing and notarization later.
Development builds (`mise run build`) are signed with the developer's
own Apple Development certificate.

The build takes the developer's team ID as a setting, the environment
variable `WB_TEAM_ID`.

- The bundle identifier of a development build is
  `nl.wraithbox.dev.<teamid>`, with the team ID in lowercase.
  `WB_BUNDLE_ID`, when set, replaces it.
- A release build uses Wraith Box's own identifier.

The build gets the profile from Xcode, as X29-dev-signing did:

1. It builds the app's Xcode project with `xcodebuild`, automatic
   signing and that team.
2. It copies the profile Xcode embeds into each helper bundle.
3. It signs from the inside out.

A build with `WB_TEAM_ID` set fails when Xcode gives it no profile. Its
message names the one-time setup in S12-03-host-prerequisites, "Development builds on macOS".

Without the setting, the build:

- is ad-hoc signed;
- has the bundle identifier `nl.wraithbox.dev.adhoc`, or
  `WB_BUNDLE_ID`;
- doesn't give any program an access group;
- has no Keychain backend (S15-least-privilege).

Where a bundle has no profile, the profile checks of S12-03-host-prerequisites, "Development builds on macOS", don't apply.

These are assumptions:

- An explicit identifier that one team has registered is likely
  refused to another. So each developer's team gets its own.
  It is assumed until a second team builds, because X29-dev-signing
  used one team.
- A Developer ID build also needs a profile in each helper bundle for
  its groups. That is assumed until the first release build.

### Other hosts

- **Windows (later):** a per-user installer, with no administrator
  rights, with the same set of binaries as `.exe`. Code signing when
  available.
- **Linux (later):** a tarball and an Ubuntu package with the same
  binaries, and a systemd user unit.
- **WSL:** the Linux `wb` binary. It requires the Windows installation
  (S12-platforms).

Each host daemon applies its own confinement at start
(S04-architecture).

### One build version

Every build task stamps one version into every program of the bundle,
Go and Swift alike, and into the guest tools disk image. The files are
`internal/version/version.go` and `WraithBoxVM/Version.swift`. The
build tasks are the release build, the dev build (`mise run build`) and
CI.

- The version includes the VCS revision.
- A build of a modified working tree adds a modified flag and a nonce.
  So two different uncommitted trees never match.
- The nonce is made once per `mise run build` invocation, and shared
  by every language task in it. So a partial rebuild of a modified tree
  needs a full build.

Only a bare toolchain build (`go build`, `go run`, `swift build`) is
unstamped. It has no version, and every peer refuses it, an unstamped
peer included. Tests that exercise the version exchange set the
version explicitly. Integration tests build through the stamping task
(I174).

Host processes refuse a peer of another version. `wb-hostd` refuses a
`wb-guestd` of another version (S04-08-records-and-bounds, "Version skew").

### Versioned contracts

Each `.proto` package name ends in its major version
(`wraithbox.<area>.v1`). A change an older peer can't read goes into a
new major package (S04-08-records-and-bounds, "Version skew").

## Decisions

### `wb` is a plain executable

`wb` doesn't hold a Keychain access group. So it is a plain
executable in `Contents/MacOS`, without a bundle of its own.

- **Decided:** I252.

### Development signing through Xcode

- **Decided:** I248 (B248-dev-signing).

**Status:** Draft
