# C05 - Credentials and the Secret Store

**Purpose:** The test cases for the secret store and its callers, the
signing setup that gives development builds their Keychain groups, and
the credential checks `wb-proxyd` makes on guest requests and server
responses.

**Verifies:** S15-02-secret-store ("Keychain"),
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

- Pass a third argument, one argument, an environment variable not on
  its fixed list, an unknown operation, and a binding name outside the
  fixed form, and see each refused with `credset-refused` before any
  store call.
- Run `wb cred set`, and see `wb-credset` get the value on its standard
  input only, never in its arguments or environment.
- Run `wb cred list`, and see it print names and no value.

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

## Fuzzing

- The credential parameter scan of query strings and of form and JSON
  bodies (X22-no-guest-credentials).

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
