# S12-03 - Host Prerequisites

**Purpose:** What `wb setup` checks on each host, and how a development
build on macOS gets signed.

## Design

### Prerequisites

`wb setup` checks these and explains them to the user. It never changes
them silently.

| Host | Prerequisite | Who can do it |
|---|---|---|
| macOS | Apple Silicon, macOS 15+ | n/a (hardware and OS version) |
| Windows | Virtual Machine Platform optional feature enabled (it is if WSL 2 is installed) | administrator, once |
| Windows | Permission to create VMs through HCS as a standard user (spike X11-windows-host) | open |
| Linux | Read-write access to `/dev/kvm` (desktop sessions usually have it; otherwise the `kvm` group) | administrator, once |

Spike X11-windows-host may show that HCS needs administrator rights for
every VM operation. The Windows design then needs a narrowly scoped
system service. That would change SEC12-least-privilege for Windows. It
requires a spec change, not a workaround.

### Development builds on macOS

A development build has the Keychain backend only when the developer's
own team signs it (S15-02-secret-store;
S10-02-packaging-and-signing). The developer sets that up
once per Mac, without a paid membership (X29-dev-signing):

1. Sign in to Xcode with an Apple ID (Xcode, Settings, Accounts). Xcode
   lists its team as a personal team.
2. Build a small app project once in the Xcode app, with that team and
   "My Mac" as the destination.
   - That registers the Mac with the team. Xcode makes the Apple
     Development certificate and downloads the profile.
   - `xcodebuild` alone can't register the Mac.
   - The organizational unit (OU) in the certificate's subject is the
     team ID, the value for `WB_TEAM_ID`.

After that, `mise run build` signs from the command line. The profile
lasts 365 days (X29-dev-signing, measured on a team with a lapsed
membership). A second Mac needs both steps too.

`wb` doesn't hold an access group. So it starts whatever state the
profiles are in, and reports each case itself.

#### Ad-hoc signed build

`wb setup` refuses the Keychain backend with the rule
`keychain-needs-team`. It names both steps and `WB_TEAM_ID`. It says
that the 1Password backend works without them.

#### Missing profile

A build with `WB_TEAM_ID` set fails before it makes the bundle, and
names both steps. macOS then never gets a bundle it would refuse to
start (S10-tech-stack).

#### Profile within 30 days of its expiry date

- `wb setup` and `wb status` read the expiry date from the helper
  bundles' profiles and print it, with the fix. The fix: build the small
  project in the Xcode app again for a new profile, then build Wraith
  Box again.
- `wb-hostd` reads the same date from the helpers' profiles at start,
  and logs a warning with it.
- `wb claude` prints one line with the date before the session starts.

A bundle without a profile, such as an ad-hoc one, gets none of these
checks. Whether a release build has a profile is the assumption in
S10-02-packaging-and-signing, "Signing".

#### Expired profile

AMFI refuses to start a program whose profile has expired, as it
refuses one with no profile (assumed in X29-dev-signing). The programs
are `wb-credset`, `wb-proxyd` and `wb-guestadmin`.

- `wb setup`, `wb status` and `wb claude` read the helpers' profiles.
  For an expired one they print the date, the rule
  `signing-profile-expired` and the fix.
- When `wb cred` sees `wb-credset` exit on `SIGKILL`, and its profile
  has expired, it reports the same.

`wb-hostd` doesn't hold a group, so it keeps running. It gives a second
signal:

1. `wb-launcher` reports that a program with a group exited on
   `SIGKILL` (S04-07-supervision, "Who starts whom").
2. `wb-hostd` reads that program's `embedded.provisionprofile`.
3. For an expired one, it logs the rule `signing-profile-expired` with
   the date.

A `SIGKILL` of a program whose profile is valid, or that has no
profile, is logged as a plain exit with the signal.

The fix is the one for a profile near its expiry date.

**Status:** Draft
