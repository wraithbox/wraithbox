# X29 - Keychain access group for development builds

**Purpose:** The result of the spike for I248. Can a developer's free
personal team sign the helpers in `Wraith Box.app` with a Keychain
access group, so that development builds keep their store items across
an update with no dialog?

**Requirements:** SEC04-no-guest-secrets, SEC12-least-privilege,
SEC14-no-fake-approvals

**Brief:** B248-dev-signing

## For review

- **Decides:** whether option D of B23-keychain-unsigned, development
  builds signed by the developer's own team, can replace the ad-hoc
  keychain file. It can, with the conditions below.
- **You are approving:** the answer and these conditions:
  - each program that holds an access group is the main executable of
    its own bundle, with the profile embedded. A plain executable in
    `Contents/MacOS` is killed at launch;
  - each developer builds a small Xcode project once in the Xcode app,
    which registers the Mac with the team. After that, command-line
    builds sign without Xcode's window;
  - the access groups are named after the developer's team, so a
    development build never reads a release build's items, or another
    developer's.
- **Controls touched:** SEC12-least-privilege (credentials are held by
  the store and reached only by their declared processes): the code
  signature of a development build enforces G2, as in a release build,
  in place of the ad-hoc keychain file. The free profile grants every
  group under the team's prefix, so any other program the developer
  signs with the same team can claim Wraith Box's groups.
  SEC14-no-fake-approvals (a prompt is never an approval): no dialog
  appeared at any step.
- **Assumed:**
  - that an expired profile makes AMFI refuse the helpers at launch, as
    a missing one did. The profile lasts a year, so the spike couldn't
    wait for it;
  - that macOS keeps matching a profile only for a bundle's main
    executable. Apple doesn't document the rule. The log names it;
  - that an Apple ID that never had a membership gets the same: the
    team the spike used belongs to a lapsed individual membership,
    which Xcode listed as a free personal team (Limits);
  - that a helper signed by another team can't name this team's
    groups. The spike had no second team, and a profile only grants
    groups under its own team's prefix.
- **Open decisions:**
  1. What a build that isn't signed by a team does with the Keychain
     backend: refuse it (recommended), or keep the ad-hoc keychain
     file next to the signed path. B248-dev-signing has the options.
     Decided 2026-10-15 (I248): refuse it. The spec change is I252.

## Question

From I248: can a developer's free personal team (an Apple ID signed in
to Xcode, no paid membership) sign the helper binaries inside
`Wraith Box.app` with a `keychain-access-groups` entitlement and a
provisioning profile, so that each helper reads and writes items in the
data protection keychain behind one access group, and keeps reading
them after a rebuild signed by the same team, with no dialog?

## Answer

**Yes, with conditions.** A team that Xcode lists as a free personal
team gets an Apple Development certificate and a profile that grants
every access group under its prefix, valid for a year. The team the
spike used belongs to a lapsed membership (Limits). Helpers signed with it share a generic
password and Secure Enclave keys behind one group across a rebuild,
with no dialog. Each helper must be the main
executable of its own bundle, and the Mac must be registered once from
the Xcode app.

## Measurements

All runs were on the host, macOS 27.0.1 (26A434), Xcode 27.0, Apple
M2, Go 1.27.1. The Apple ID's individual membership had lapsed. Xcode
listed its team, `G7BF23LV79`, as "Leo Simons (Personal Team)", with
`isFreeProvisioningTeam` set, and it had no certificate or profile
before the spike. The helpers were signed with the hardened runtime, without
`get-task-allow`. The maintainer watched the screen for dialogs, and
none appeared.

### Getting a certificate and a profile

| Step | Result |
|---|---|
| `xcodebuild -allowProvisioningUpdates` on a one-target app project | made the certificate "Apple Development: Leo Simons", then failed: "Your team has no devices from which to generate a provisioning profile" |
| the same with `-allowProvisioningDeviceRegistration` | the same error |
| one build in the Xcode app, destination "My Mac" | registered the Mac and downloaded the profile |
| `xcodebuild` from the command line after that | builds and signs, no flag needed |

The profile, "Mac Team Provisioning Profile: nl.wraithbox.spike.x29":

| Field | Value |
|---|---|
| `com.apple.application-identifier` | `G7BF23LV79.nl.wraithbox.spike.x29` |
| `keychain-access-groups` | `G7BF23LV79.*` |
| `ProvisionedDevices` | this Mac only |
| `TimeToLive` | 365 days |

Xcode accepted the Keychain Sharing group `nl.wraithbox.store` for the
free team without a warning.

### Where a helper can be

| Layout | Result |
|---|---|
| plain executable in `Contents/MacOS`, signing identifier `nl.wraithbox.spike.x29.a` | killed at launch (exit 137). `taskgated-helper`: "Disallowing nl.wraithbox.spike.x29.a because no eligible provisioning profiles found". `amfid`: -413 "No matching profile found" |
| the same with the app's identifier `nl.wraithbox.spike.x29` | killed the same way |
| its own bundle in `Contents/Helpers/x29-a.app`, the app's profile copied to `Contents/embedded.provisionprofile`, the same bundle identifier as the app | runs |

The helper bundles shared one profile and one bundle identifier. A
helper whose entitlements held only `keychain-access-groups`, without
the application identifier, also ran and read the items.

### Keychain

Items in the data protection keychain, group
`G7BF23LV79.nl.wraithbox.store`, accessible after first unlock on this
device only.

| Step | Result |
|---|---|
| helper `a` adds a generic password | 7 ms |
| helper `b` reads it | 5 ms |
| `a` makes an ephemeral Secure Enclave key and keeps its token object ID in an item (B23-keychain-unsigned decision 2) | works, 13 ms |
| `b` reads the item, restores the key, and signs | the same public key, the signature verifies, 5 ms per signature |
| `a` makes a permanent Secure Enclave key in the group | works. X09-keychain-unsigned got -34018 for this without the entitlement |
| `b` finds the permanent key and signs | works |

### After a rebuild

Both helpers rebuilt and signed again by the same team, so every cdhash
changed.

| Step | Result |
|---|---|
| new `a` and new `b` read the password | works |
| both restore the wrapped key and sign | works, the same public key |
| both find the permanent key and sign | works |
| the whole app copied to another directory and run from there | works |

### Other callers

| Caller | Result |
|---|---|
| ad-hoc signed, no entitlements, asks for the group | -34018 (`errSecMissingEntitlement`) |
| ad-hoc signed, no entitlements, no group in the query | -25300 (`errSecItemNotFound`), and a list of all items is empty |
| ad-hoc signed, claims the group | killed at launch |
| `security find-generic-password` | not found |
| signed by the same team, holds the group `G7BF23LV79.nl.wraithbox.other` | -34018 for the store's group, and its own list is empty |

### How a helper is started

| Started by | Result |
|---|---|
| a shell | works |
| launchd, as a LaunchAgent in the user's GUI domain | works, the key signs, exit 0 |
| a symbolic link on `PATH` to the helper, as the installer links `wb` | works, writes and reads an item |

`wb-launcher` starts programs with `posix_spawn`, as the shell does.
The spike didn't confine the helpers, which is I61.

## Limits

- The team wasn't one of an Apple ID that never had a membership. It
  had a lapsed one, and Xcode treated it as a free personal team. The
  profile's `TeamName` was "Leo Simons", not the "(Personal Team)"
  name Xcode showed. Its terms, the year above all, may come from the
  old membership. Free profiles for iOS last 7 days. A contributor
  without a membership confirms or corrects this on their first build.
- No helper signed by another team was tried, because the spike had no
  second team.
- The profile lasts a year. The spike didn't see what an expired one
  does.
- One Mac and one macOS version. A developer's second Mac must be
  registered with the team too.
- The helpers weren't sandboxed. I61 adds the sandbox rules for
  `securityd` and the Secure Enclave.
- The free profile grants `<TEAMID>.*`. Any program the developer signs
  with that team can claim Wraith Box's groups. Release builds, signed
  with Wraith Box's Developer ID, don't share this, because only Wraith
  Box signs with that team.

## What it means for the specs

- S10-tech-stack, "Packaging and signing": each program that holds an
  access group is a bundle under `Contents/Helpers`, with the profile
  embedded. Development builds sign with the developer's team, which
  the build takes as a setting.
- S15-least-privilege and S09-policy-credentials-audit: development
  builds use the data protection keychain, with the groups under the
  developer's team. The ad-hoc keychain file, the `unlock` item and
  "After an update" for ad-hoc builds go away (B23-keychain-unsigned,
  option D).
- S12-platforms: a developer's one-time setup, Xcode signed in and one
  build in the Xcode app.

## Spike code

Throwaway code on branch `spike/x29-dev-signing`, commit
[`d236057`](https://github.com/wraithbox/wraithbox/tree/d236057c768d198765e5fe98db2b13e7ffc75acc/spikes/x29-dev-signing):
a one-target Xcode
project (`app/`), the Go helper with a cgo shim over Security.framework
(`helper/`), and `build.sh`, which puts the helpers into nested bundles
and signs them.

**Status:** Answered 2026-10-15: yes, with conditions. Decided 2026-10-15 (I248): no Keychain backend for a build without a team signature
