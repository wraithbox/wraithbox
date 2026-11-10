# C05 - Credentials and the Secret Store

**Purpose:** The test cases for the secret store and its callers, the
signing setup that gives development builds their Keychain groups, and
the credential checks `wb-proxyd` makes on guest requests and server
responses.

**Verifies:** S15-02-secret-store ("Keychain"), S15-06-binding-commands,
S15-03-access-rules ("Declared access"), S09-05-credentials, S12-03-host-prerequisites ("Development builds on macOS"),
S10-02-packaging-and-signing, S07-08-requests-and-credentials ("Credential replacement")

## Unit tests

### Store errors

Spec: S15-least-privilege.

- Make the call that turns Keychain dialogs off fail, and see the
  process refuse every store access with `store-dialogs-on`.
- With a fake store:
  - return -25300 for a binding, and see the binding's own refusal;
  - return -25308, and see the work wait for the unlock and name the
    store;
  - return -34018 and another status for a binding and for a
    `wbadmin/*` item, and see each refused with `store-read-failed`;
  - fail a `wb cred set` write, a `wb cred rm` delete and a `wbadmin/*`
    write with -34018, and see each refused with `store-write-failed`;
  - for a CA item in the record, see -25300 and every status other than
    -25308 give `ca-key-restore`.
- A locked store, -25308 on the Keychain, makes the work wait for the
  unlock, for a CA item as for any other.
- Each log line names the failed call.

The store's side of these cases is in `internal/platform/secretstore`,
over the fake of `secretstore/storetest`: `TestC05StoreDialogsOn`,
`TestC05StoreErrorsNotFoundLocked`, `TestC05StoreErrorsReadFailed`,
`TestC05StoreErrorsWriteFailed` and `TestC05StoreAccessUndeclared`.
Each of them also searches the log for the value it wrote and finds
none. The waits, the binding's refusal and `ca-key-restore` are the
callers' tests.

### Access groups

Spec: S15-02-secret-store, "Keychain".

- A table test maps each item name form of "Declared access" to its
  group, `<TEAMID>.nl.wraithbox.<role>`, with the team from the
  process's own signature.
- Ask the shim to write a `ca/` item with the `binding` group, and to
  use a group the process's entitlements don't hold, and see each
  refused with `store-group-mismatch` before any Keychain call.
- A shim whose own entitlements can't be read refuses every call with
  `store-group-mismatch`.
- Open the store as one program in a process whose signed Info.plist
  names another program's executable, or none, or can't be read, and
  see every call refused with `store-program-mismatch`
  (`TestC05StoreProgramMismatch`, over the fake).
- Start a copy of the test binary from a bundle signed ad hoc, sign the
  bundle again under another identifier while it runs, and see the
  copy read its identifier and executable before, and after it see the
  signature refused (OSStatus -67034), no group read, and the open
  refused with `keychain-needs-team` (`TestC05AdhocSwap`, on macOS CI).
- `TestC05AccessGroups` in `internal/storeitem` is the table test.
  `TestC05AccessGroupsMismatch` and `TestC05AccessGroupsUnreadable` in
  `internal/platform/secretstore` are the refusals, over the fake.
- The build check of S10-tech-stack fails a bundle whose helper has a
  group more or less than its row in S15-02-secret-store, "Keychain",
  and one where `wb` or any other program outside the table has an
  entitlement that names a group: `keychain-access-groups`, the
  application identifier, or application groups. It also fails each
  entitlement outside the build's allowlist, such as `get-task-allow`
  in a team build, and a program another team signed. A profile
  that doesn't decode, or names another team or two, fails it too. So
  does the app executable with the entitlements Xcode signed it with, as
  if step 7 hadn't signed it again. The tests are
  the `TestC05BuildCheck` ones in `internal/appbundle`, with every case
  of S17-01-bundle, "The entitlement check". `TestC05BuildSigningIdentity`
  sees the build choose the team's one Apple Development identity by
  the OU of its certificate, and fail on none or two. One certificate
  listed twice counts once. `TestParseTeam` in
  `internal/platform/codesign` refuses `codesign` output with two
  `TeamIdentifier` lines, or a value that isn't a team ID.

### Builds without a team, and development builds

- In an ad-hoc signed build:
  - see `wb setup` refuse the Keychain backend with
    `keychain-needs-team` and name the setup steps of S12-03-host-prerequisites, "Development builds on macOS";
  - see `wb-proxyd` refuse a binding fetch and a CA issue, `wb-credset`
    refuse the write of `wb cred set`, and `wb-guestadmin` refuse the
    items of `wb image build` and of a rotation, each with the same
    rule and logged, before any Keychain call;
  - see `wb setup` still install the services and exit nonzero.

  The `wb setup` cases are `TestC05SetupAdhocKeychain` in
  `internal/setup`. A bundle with profile files and ad-hoc signatures
  is still ad-hoc: `wb setup` reads the signature.
  The store's own refusal is `TestC05StoreNeedsTeam` in
  `internal/platform/secretstore`, over the fake, and
  `TestC05AdhocShim`, which opens the darwin backend from the ad-hoc
  test binary on macOS CI. `TestNoShim`, which `go:test` runs
  with `CGO_ENABLED=0`, sees a darwin build without cgo refuse every
  call with `store-shim-missing`.
- In a development build, see `wb setup`, `wb cred list` and the
  session-start audit record name the signing team and say the grant is
  team-wide. The `wb setup` case is `TestC05SetupTeamReport`, which
  also sees the accept logged under `keychain-team`.
- Give `wb setup` helpers that don't agree on one team: another team's
  signature, an ad-hoc one among team ones, an unreadable signature,
  and a profile that is missing, a link, of another team, without a
  team name, or of another team name. See each refused with `keychain-team-mixed` after
  the install, and a nonzero exit (`TestC05SetupTeamMixed`).

### wb-credset

Spec: S15-06-binding-commands.

- Pass a third argument, one argument, an environment variable not on
  its fixed list, an unknown operation, and a binding name outside the
  fixed form, and see each refused with `credset-refused` before any
  store call.
- Run `wb cred set`, and see `wb-credset` get the value on its standard
  input only, never in its arguments or environment.
- Run `wb cred list`, and see it print names and no value.

`TestC05CredsetRefused` in `internal/credset` is the first case, and
`TestC05CredsetWrite`, `TestC05CredsetWriteRefusedValue`,
`TestC05CredsetList`, `TestC05CredsetDelete`,
`TestC05CredsetDeleteMissing`, `TestC05CredsetStoreErrors` and
`TestC05CredsetNeedsTeam` run each operation over the fake store. Each
searches the output for the value and finds none.
`TestC05CredsetAdhocWrite` and `TestC05CredsetAdhocRefused` in
`cmd/wb-credset` run the test binary as `wb-credset` on macOS. They see
it confine itself, and refuse with `keychain-needs-team` and
`credset-refused`. `TestC05CredsetStartRefused` sees it exit 1 when it
can't set its core file limit to 0 or can't confine itself, before it
reads its standard input. It logs a refusal with `credset-no-core`,
the sandbox's rule, or `credset-not-confined`.

The `wb cred` side is in `internal/credcmd`. Its tests put a copy of the
test binary at `wb-credset`'s place in a bundle, and run it as the
helper. `TestC05CredSetFromPipe` and `TestC05CredSetFromPrompt` see `wb`
pass the value to the helper on its standard input only, with two arguments
and an empty environment. The value is in nothing `wb` or the helper
prints or logs. `TestC05CredSetNewline` sees one trailing newline
removed, and `TestC05CredSetValueRefused` an empty or too long value
refused before the helper starts. `TestC05CredNameInvalid` sees a bad
name refused with exit 2 before anything is read.
`TestC05CredHelperRule`, `TestC05CredHelperNoDecision` and
`TestC05CredHelperKilled` see `wb` print the helper's rule and its fix to the
user, and a helper that ended on `SIGKILL` named.
`TestC05CredList`, `TestC05CredListBadName` and `TestC05CredRm` cover
`list` and `rm`. `TestC05CredHelperOutputCut` sees output longer than
`wb` keeps fail the run, and `TestC05CredHelperNoRead` a helper that
exits without reading the value. `TestC05CredBundleCheck` sees another
build version, an unstamped `wb`, a missing `Info.plist` and a linked
helper path refused. `TestC05CredNoCore` and `TestC05CredPromptLimit`
cover the core file limit and the prompt's line limit, and
`TestC05CredHostLater` and `TestC05CredWindowsLater` a host later in
the platform matrix.

On 2026-11-10 the maintainer ran `wb cred` on the head of PR744
(9b3f461a) with a team build installed in `~/Applications`. Each step
passed, with no Keychain dialog:

- `wb setup` exited 0 and printed the note on team-wide groups;
- `wb cred set` from a pipe and from the prompt, which showed no
  characters, each ended with `credset-done`;
- `wb cred list` printed both names, and the team note on standard
  error;
- `wb cred rm` removed both. A second `rm` printed only the note on
  standard error, with `store-item-not-found`, and exited 0. A list
  after it printed no names;
- with an ad-hoc build, `wb setup` refused with `keychain-needs-team`
  and exited 1, and `wb cred set` exited 255 with the same rule;
- no output file held a value.

A first try of `wb cred set`, run through a remote session while the
console screen was locked, got `store-locked` (-25308). The system log
showed the Keychain locked, and Xcode got the same error. The fix `wb`
prints names the Mac's own screen for that reason
(`TestC05CredLockedFix`).

### Signing profile

Spec: S12-03-host-prerequisites, "Development builds on macOS".

- Give `wb setup` and `wb status` a profile that expires in 29 days,
  and see them print the date and the fix. Give them one that expires
  in 31 days, and see them print nothing about it. See `wb-hostd` log
  the warning at start for the first and not the second.
- Build with `WB_TEAM_ID` set and no profile from Xcode, and see the
  build fail before it makes the bundle. `TestC05BuildMissingProfile`
  in `cmd/wb-dev-app` feeds the build's profile check a missing
  profile, a directory, and a profile of another team or of two.
- Send a fake `wb-launcher` report of a group helper's exit on
  `SIGKILL`, and see `wb-hostd` log `signing-profile-expired` with an
  expired profile, and a plain exit with a valid profile and with none.
- With an expired profile, see `wb setup`, `wb status` and `wb claude`
  print the date and `signing-profile-expired`, and `wb cred set` do
  the same when `wb-credset` exits on `SIGKILL`.
- See `wb-launcher` accept a table whose entries name
  `Contents/Helpers/<name>.app/Contents/MacOS/<name>` in the installed
  bundle, and still refuse one under `<data>`.

### Host tests

Spec: S15-02-secret-store, "Keychain" and "The group of each call";
S15-03-access-rules, "No Keychain dialogs" and rule 7.

These need the data protection keychain, so they run only on a Mac
with a team and its profile, in an attended session
(`docs/agents/host-work.md`). `mise run store:host-test`, with
`WB_TEAM_ID` set and after a team build of `app:bundle`, signs the
test binary of `internal/platform/secretstore` with the team's
identity into `.scratch/store-test/wb-store-test.app`, with the
`binding` and `ca` groups, and into `wb-store-test-binding.app`, with
`binding` alone. Each bundle gets a copy of the app's profile. It runs
them with an empty environment but for `WB_STORE_HOST_TEST=1`,
`WB_STORE_HOST_STEP` and `WB_STORE_HOST_G2_OUT`. The tests, and the
read-back of an item's attributes that only they use, build only with
the `wbstorehost` tag, which the task sets and no program's build does.
CI lints them with the tag and doesn't run them, and without the
variables each one skips. Each test
item is `wraithbox/binding/wbtest-<random>` or
`wraithbox/ca/wbtest-<random>/wbtest-<random>`, removed when its test
ends, and the run fails when a `wbtest-` item is left. The maintainer
sees no dialog at any step. The same task runs the CA key's host tests
of C02-streams-and-tls, "CA key", in the bundles `wb-hwkey-test.app`,
with the `ca` group, and `wb-proxyd.app`, with both groups. Their CA
items are `wraithbox/ca/wbtest-<random>/<CA id>`, under the same check.

- `TestC05HostEnvironment`: the environment holds only the two
  variables, the bundle holds its two groups, and dialogs are off after
  an open.
- `TestC05HostProgramMismatch`: the real shim reads the bundle's own
  executable and signing identifier, and an open as `wb-credset` is
  refused with `store-program-mismatch`.
- `TestC05HostBindingRoundTrip`: write, fetch, update by a second
  write, list and delete of a `binding/` item, as `wb-credset` and
  `wb-proxyd`, with the value read back equal and the list holding
  names only. `TestC05HostCARoundTrip` does the same for a `ca/` item.
  Both read the item's attributes back: one item, in the group of its
  role, with the account `wraithbox`, with
  `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`, and not
  synchronizable.
- `TestC05HostNotFound`: a fetch of a missing item gives `ErrNotFound`.
- `TestC05HostGroupMismatch`, in the `binding`-only bundle: a fetch of
  a `ca/` item is refused with `store-group-mismatch`, and the same
  fetch and a list made on the backend past the checks get -34018 from
  the Keychain.
- `TestC05HostLocked`, with `-- --lock` alone: the test waits while
  the maintainer locks the screen, sees a fetch give `ErrLocked`
  (-25308), then waits for the unlock and sees the same fetch succeed.
- `TestC05HostConfineAfterOpen` opens the store, confines the process
  with the store-only profile of S04-03-self-sandboxing, and makes a
  round trip. `TestC05HostConfineFirst` confines first, then opens,
  and sees the open refused with `keychain-needs-team`: the profile
  blocks the read of the process's own signature (OSStatus 100001).
- `TestC05HostG2`, with `-- --g2` alone: the test holds a `binding/`
  item and prints a `security find-generic-password -s <item>` command,
  which names no group. The maintainer runs it in another terminal, and
  the test sees it not find the item, and the item still there after
  (G2 of S15-02-secret-store).

On 2026-11-10 the maintainer ran these on the head of PR743, with a
team build. Each passed:

- the confine-first step still got OSStatus 100001, from the read of
  the signature before the validity check;
- the lock step got -25308 while the screen was locked, and the fetch
  succeeded 24 seconds later, after the unlock;
- for G2, `security` printed "The specified item could not be found in
  the keychain.";
- the maintainer saw no dialog.

## Fuzzing

- The credential parameter scan of query strings and of form and JSON
  bodies (X22-no-guest-credentials).
- Item names, which the CA removal list gets from `wb-hostd`
  (T16-hostd-landing): `FuzzItemName` in `internal/storeitem`.

## Conformance

- Send an attacker-supplied token in a credential parameter that a git
  host's profile names, and get a `403`: GitLab's `private_token` in
  the query string, as `private%5Ftoken`, as `private_token[]`,
  repeated, empty, as a form-encoded body field, as a
  `multipart/form-data` part, as a top-level JSON key, and as a
  JSON-escaped key (`"private\u005ftoken"`) (X22-no-guest-credentials).
- Send a JSON body that doesn't parse, or one past the size cap with no
  `Content-Length`, and get a `403`.
- Send a token on a GET, and see it removed.
- Send a credential that is neither a placeholder nor a fixed value, and
  find a detection finding with its header name and no value.
- Send a credential placeholder outside its binding (another host, a
  query string, a body), and get a `403`.
- From an inspected host with a credential binding, fetch a response
  that sets a cookie and has `X-OAuth-Scopes`,
  `X-Accepted-OAuth-Scopes` and `X-OAuth-Client-Id` headers, and see
  none of them reach the guest, and a log record with each name and no
  value.

**Status:** Draft
