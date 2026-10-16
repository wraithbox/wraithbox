# S15-02 - The Secret Store

**Purpose:** Where credentials are held: the secret store, its
backends, and the guarantees a backend must give.

## Design

### The store holds credentials

Every credential is nominally held by the configured secret store. The
store keeps them in a container that holds only Wraith Box's items.

- No Wraith Box process is the holder for another.
- No process gets a secret from the store through another.
- One secret passes between two of them before it is written to the
  store. `wb` hands the value the user enters for `wb cred set` to
  `wb-credset` on its standard input (S15-03-access-rules, rule 5).

Keys that must never leave hardware, which means the CA signing key,
are in the platform's hardware key store, whatever the backend. That holds
on every host that has one (S12-platforms). On macOS the store holds
only the key's wrapped form, which no other Mac can use
(S09-06-ca-keys, "Key").

### Keychain

The Keychain backend is the data protection keychain, in every build
that a team signs. It has one access group per role. So each item is
reachable only by the binaries of its role, and there is nothing to
unlock.

Release builds are signed with Wraith Box's Developer ID. Development
builds are signed with the developer's own Apple Development
certificate. They use the provisioning profile that Xcode makes for
the developer's team (S10-02-packaging-and-signing,
S12-03-host-prerequisites, "Development builds on macOS").

The groups are named `<TEAMID>.nl.wraithbox.<role>`, where `<TEAMID>`
is the team that signed the build. So a development build's groups
differ from a release build's and from another developer's.

That a program signed by another team can't name them is assumed.
X29-dev-signing had one team, and a profile grants only groups under
its own team's prefix.

Each program that holds a group is a bundle of its own, with the
profile embedded (S10-tech-stack).

| Role | Items | Programs that hold the group |
|---|---|---|
| `binding` | `binding/<name>` | `wb-credset`, `wb-proxyd` |
| `ca` | `ca/<vm>/<ca>` | `wb-proxyd` |
| `wbadmin` | `wbadmin/build/<build>`, `wbadmin/image/<image>`, `wbadmin/vm/<vm>/<disk>` | `wb-guestadmin` |

A group lets the programs that hold it read, write and delete its
items. So an access narrower than that, such as `wb-credset`'s write
of bindings, is enforced by our code (S15-03-access-rules, rule 1).

`wb` doesn't hold a group ("`wb` doesn't hold a group" below).

#### `wb-credset`

`wb-credset` is the helper that `wb cred set`, `wb cred rm` and `wb
cred list` run as the user.

- `wb` starts it from the installed bundle (S10-tech-stack).
- Its arguments are the operation and the binding name, never the
  value.
- It confines itself first thing, from a compiled-in profile. The
  profile allows the secret store service and its standard streams. It
  denies files, the network and starting programs
  (S04-03-self-sandboxing, "Each host daemon is self-sandboxed").

It does one operation and exits:

- *write* reads the value from its standard input;
- *delete* removes the item;
- *list* prints the names of the `binding/` items, never a value.

It takes exactly two arguments, the operation and the binding name.
Its environment has only the variable names on a fixed list compiled
into it, which `wb` also uses when it starts it. Before any store call,
it refuses each of these with the rule `credset-refused`:

- any other argument count;
- any variable not on that list;
- an operation or name outside the fixed forms of
  S15-03-access-rules, "Declared access".

#### The group of each call

Each Keychain call names its group. The cgo shim (S12-platforms) sets
`kSecAttrAccessGroup` to `<TEAMID>.nl.wraithbox.<role>`:

- `<role>` is the first part of the item name;
- `<TEAMID>` is the team identifier of the process's own code
  signature. The shim reads it once at start, and never takes it from
  configuration or a request.

Without the attribute, a write by a process with two groups, such as
`wb-proxyd`, would go to the first group of its entitlements. A `ca/`
item could then end up in `binding`.

Each call states the group its caller expects. Before any Keychain
call, the shim refuses in each of these cases with the rule
`store-group-mismatch`, and logs the item and both groups:

- the group isn't the one the item name gives;
- the process's entitlements don't hold the group.

An entitlement set the shim can't read counts as not holding it.

### Ad-hoc signed builds

An ad-hoc signed build has no Keychain backend. A CI build is one, and
so is the build of a developer without an Apple ID in Xcode.

Each process with store access reads the team identifier of its own
code signature at start. With none, it refuses every Keychain access
with the rule `keychain-needs-team`, and logs it. So in such a build
these are refused with that rule:

- a session that needs a binding;
- each VM start, because it needs a CA item (S09-06-ca-keys, "Valid CA");
- `wb cred set`, through `wb-credset`;
- `wb image build` and the `wbadmin` rotation at first contact,
  because they need `wbadmin/*` items.

`wb setup` refuses to set up the Keychain backend in such a build,
with the same rule, and says what to do:

1. Sign in to Xcode with an Apple ID.
2. Build once in the Xcode app.
3. Build Wraith Box with the team set (S12-03-host-prerequisites, "Development builds on macOS").

`wb setup` still does the rest of its work, the per-user services
included. Then it exits with a nonzero status.

### 1Password

The 1Password backend comes after V1 (I151), so the Keychain is
V1's only backend. It is the desktop app integration, with one vault
per role. It doesn't need a service account token in the store. It
runs only after the user turns it on by name.

### Backend guarantees

A backend must give these guarantees:

| Guarantee | Keychain | 1Password |
|---|---|---|
| G1. A container of Wraith Box items only | yes | yes, a vault |
| G2. Each item reachable only by its declared processes | yes, by code signature, per access group | enforced by our code only |
| G3. Locked or unreachable fails, with no fallback | yes | yes |
| G4. Values never written outside the store | yes | yes |

G2 on the Keychain has these limits:

- A group's holders reach every item in it ("Keychain", above).
- In release builds, only Wraith Box signs with its team.
- In development builds, the groups are team-wide. The free profile
  grants every group under the developer's team (`<TEAMID>.*`). So
  another program that the developer's team signs can claim Wraith
  Box's groups ("Team-wide groups in development builds" below).

On 1Password, a compromised declared reader reaches every item in the
vaults the session has open. A vault per role narrows that.

A backend without G1, G3 or G4 is refused.

For a backend where our code alone enforces G2, these say so:
`wb setup`, `wb cred list`, and the audit record of each session start.

The build compiles in the ID of Wraith Box's release team. A
development build is one signed by any other team. In a development
build, `wb setup`, `wb cred list` and that audit record also name the
signing team. They say that every program that team signs can reach
Wraith Box's groups.

A fallback is never silent.

### Failed store calls

Each failed store call is logged with the call, the item, the status
and the rule. That holds in every build and on every backend.

- A binding that isn't in the store refuses a session start that
  needs it. On the Keychain that is -25300 (`errSecItemNotFound`).
  `wb` names the binding and says to set it with `wb cred set`.
- A locked store is rule 7 of S15-03-access-rules. On the Keychain
  that is -25308.
- A CA item follows S09-06-ca-keys, "Valid CA".
- Any other failure of a binding or `wbadmin/*` read is
  `store-read-failed`, -34018 (`errSecMissingEntitlement`) among them.
  The work that needed the item is refused with it.
- A failed write or delete of any item is `store-write-failed`,
  outside the cases above and the CA issue of S09-06-ca-keys, "Valid CA". The command or work that made it is refused with it.

## Decisions

### One access group per role, in every team-signed build

The Keychain backend is the data protection keychain, with one access
group per role.

- **Why:** each item is then reachable only by the binaries of its
  role, and there is nothing to unlock.
- **Decided:** I248, B248-dev-signing.

### `wb` doesn't hold a group

- **Why:** `wb` parses guest terminal output (S16-terminal-stream)
  without a sandbox. So a bug there doesn't reach the store
  (SEC04-no-guest-secrets). And AMFI never refuses to start `wb` for a
  profile.
- **Decided:** I252.

### Team-wide groups in development builds

Another program that the developer's team signs can claim Wraith Box's
groups in a development build.

- **Decided:** accepted on I248, B248-dev-signing.

**Status:** Draft
