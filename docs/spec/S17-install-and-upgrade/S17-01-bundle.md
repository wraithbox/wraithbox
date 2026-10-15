# S17-01 - The Bundle

**Purpose:** What a Wraith Box bundle holds, and how `mise run build`
builds and signs it.

## Design

### The bundle

On macOS the bundle is `Wraith Box.app`, with the programs and helper
bundles of S10-tech-stack, "Packaging and signing". The programs
without a Keychain access group, `wb-download` and `wb-diskfmt`
included, are in `Contents/MacOS`. The guest tools disk
(S06-12-guest-agent, "`wb-guestd`") is `Contents/Resources/wb-guest-tools.img`.
The app's main executable is the notification helper, because macOS
shows a notification under the bundle of the process that posts it.
`Info.plist` holds the bundle identifier and, under the key
`WBBuildVersion`, the build version that every program is stamped with
(S10-tech-stack, "One build version"). `wb-hostd` reads that key to
notice an upgrade (S04-07-supervision, "Upgrades").

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
the built bundle as `~/Applications/Wraith Box.app` (S17-05-upgrades-and-migrations, "Upgrades").

**Status:** Draft
