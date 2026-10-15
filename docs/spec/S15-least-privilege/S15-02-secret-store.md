# S15-02 - The Secret Store

**Purpose:** Where credentials are held: the secret store, its backends, and
the guarantees a backend must give.

## Design

### The store holds credentials

Every credential is nominally held by the configured secret store, in a
container that holds only Wraith Box's items. No Wraith Box process is
the holder for another, and none gets a secret from the store through
another. One secret passes between two of them before it is written
to the store: `wb` hands the value the user enters for `wb cred set` to
`wb-credset` on its standard input (S15-03-access-rules, rule 5). Keys
that must never leave hardware (the CA signing key) are in the
platform's hardware key store whatever the backend, on every host
that has one (S12-platforms). On
macOS the store holds only the key's wrapped form, which no other Mac
can use (S09-05-ca-keys, "Key").

### Keychain

The data protection keychain, in every build that a
team signs, with one access group per role, so each item is reachable
only by the binaries of its role and there is nothing to unlock
(decided on I248, B248-dev-signing). Release builds are signed with
Wraith Box's Developer ID. Development builds are signed with the
developer's own Apple Development certificate and the provisioning
profile that Xcode makes for the developer's team (S10-tech-stack,
"Packaging and signing", S12-platforms, "Development builds on
macOS"). The groups are named `<TEAMID>.nl.wraithbox.<role>`, where
`<TEAMID>` is the team that signed the build. So a development
build's groups differ from a release build's and from another
developer's. That a program signed by another team can't name them
is assumed: X29-dev-signing had one team, and a profile grants only
groups under its own team's prefix. Each program that holds a group
is a bundle of its own, with the profile embedded (S10-tech-stack).

| Role | Items | Programs that hold the group |
|---|---|---|
| `binding` | `binding/<name>` | `wb-credset`, `wb-proxyd` |
| `ca` | `ca/<vm>/<ca>` | `wb-proxyd` |
| `wbadmin` | `wbadmin/build/<build>`, `wbadmin/image/<image>`, `wbadmin/vm/<vm>/<disk>` | `wb-guestadmin` |

A group lets the programs that hold it read, write and delete its
items. So an access narrower than that, such as `wb-credset`'s write
of bindings, is enforced by our code (S15-03-access-rules, rule 1). `wb` doesn't hold a group
(decided on I252). It parses guest terminal output
(S16-terminal-stream) without a sandbox, so a bug there doesn't reach
the store (SEC04-no-guest-secrets), and AMFI never refuses to start
`wb` for a profile.

`wb-credset` is the helper that `wb cred set`, `wb cred rm` and `wb
cred list` run as the user. `wb` starts it from the installed bundle
(S10-tech-stack), with the operation and the binding name as its
arguments, never the value. It confines itself first thing, from a
compiled-in profile that allows the secret store service and its
standard streams, and denies files, the network and starting
programs (S04-03-self-sandboxing, "Each host daemon is self-sandboxed").
It does one operation and exits: *write* reads the value from its
standard input, *delete* removes the item, and *list* prints the
names of the `binding/` items, never a value. It takes exactly two
arguments, the operation and the binding name, and an environment
of only the variable names on a fixed list compiled into it, which
`wb` also uses when it starts it. It refuses any other argument
count, any variable not on that list, and an operation or name
outside the fixed forms of S15-03-access-rules, "Declared access", with the rule
`credset-refused`,
before any store call.

Each Keychain call names its group. The cgo shim (S12-platforms)
sets `kSecAttrAccessGroup` to `<TEAMID>.nl.wraithbox.<role>`, where
`<role>` is the first part of the item name and `<TEAMID>` is the
team identifier of the process's own code signature, read once at
start and never taken from configuration or a request. Without the
attribute, a write by a process with two groups, such as
`wb-proxyd`, would go to the first group of its entitlements, so a
`ca/` item could end up in `binding`. Each call states the group its
caller expects. Before any Keychain call, the shim refuses with the
rule `store-group-mismatch`, and logs the item and both groups, when
that group isn't the one the item name gives, or when the process's
entitlements don't hold it. An entitlement set the shim can't read
counts as not holding it.

### Ad-hoc signed builds

An ad-hoc signed build, such as a CI
build or the build of a developer without an Apple ID in Xcode, has
no Keychain backend.
Each process with store access reads the team identifier of its own
code signature at start. With none, it refuses every Keychain access
with the rule `keychain-needs-team` and logs it. So in such a build
these are refused with that rule: a session that needs a binding,
each VM start, because it needs a CA item (S09-05-ca-keys, "Valid CA"), `wb cred set` through `wb-credset`, and `wb image build` and the `wbadmin`
rotation at first contact, because they need `wbadmin/*` items. `wb
setup` refuses to set up the Keychain backend in such a build with
the same rule, says that the 1Password backend still works, and says
what to do: sign in to Xcode with an Apple ID, build once in the
Xcode app, and build Wraith Box with the team set (S12-platforms,
"Development builds on macOS"). It still does the rest of its work,
the per-user services included, and then exits with a nonzero
status.

### 1Password

The desktop app integration, with one vault per role,
and no service account token to store. It runs only after the user
turns it on by name.

### Backend guarantees

A backend must give these guarantees:

| Guarantee | Keychain | 1Password |
|---|---|---|
| G1. A container of Wraith Box items only | yes | yes, a vault |
| G2. Each item reachable only by its declared processes | yes, by code signature, per access group. A group's holders reach every item in it ("Keychain", above). Release builds: only Wraith Box signs with its team. Development builds: team-wide. The free profile grants every group under the developer's team (`<TEAMID>.*`), so another program that the developer's team signs can claim Wraith Box's groups (accepted on I248, B248-dev-signing) | enforced by our code only. A compromised declared reader reaches every item in the vaults the session has open. A vault per role narrows it |
| G3. Locked or unreachable fails, with no fallback | yes | yes |
| G4. Values never written outside the store | yes | yes |

A backend without G1, G3 or G4 is refused. For a backend where our code
alone enforces G2, `wb setup`, `wb cred list` and the audit record of
each session start say so. In a development build, one signed by a
team other than Wraith Box's release team, whose ID the build compiles
in, they name the signing team and say that every program that team
signs can reach Wraith Box's groups. A fallback is never silent.

### Failed store calls

Each failed store call is logged with the call, the item, the status
and the rule, in every build and on every backend:

- A binding that isn't in the store, -25300 (`errSecItemNotFound`) on
  the Keychain, refuses a session start that needs it, and `wb` names
  the binding and says to set it with `wb cred set`.
- A locked store, -25308 on the Keychain, is rule 7 of S15-03-access-rules.
- A CA item follows S09-05-ca-keys, "Valid CA".
- Any other failure of a binding or `wbadmin/*` read, such as -34018
  (`errSecMissingEntitlement`), is `store-read-failed`, and the work
  that needed the item is refused with it.
- A failed write or delete of any item, outside the cases above and
  the CA issue of S09-05-ca-keys, "Valid CA", is
  `store-write-failed`, and the command or work that made it is
  refused with it.

**Status:** Draft
