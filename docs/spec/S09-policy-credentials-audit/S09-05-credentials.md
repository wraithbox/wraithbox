# S09-05 - Credentials

**Purpose:** How credentials are stored and bound to requests, and how the model
credential reaches the model API without entering the guest.

## Design

### Storage

Each credential is an item in the platform's secret store
(S12-platforms). Only the processes that S15-least-privilege declares
for it read it:

- `wb-proxyd` fetches bindings;
- `wb-credset`, which `wb cred` runs, writes, lists and deletes them.

`wb` itself doesn't have store access.

Other platforms scope items as narrowly as their store allows. The
limits are documented per platform in S12-platforms.

#### On macOS

A credential is an item in the data protection keychain, in the access
group `<TEAMID>.nl.wraithbox.binding` of the team that signed the
build.

`wb-credset` and `wb-proxyd` both hold that group. So the Keychain
would let `wb-credset` read a binding too. Our code is what keeps it
from fetching one (S15-02-secret-store, "Keychain").

Signing:

- Release builds are signed with Wraith Box's Developer ID.
- Development builds are signed with the developer's own Apple
  Development certificate and team. A rebuild keeps reading the items
  with no dialog (X29-dev-signing). In a development build, any other
  program the developer's team signs can claim the group, which
  S15-least-privilege documents.
- An ad-hoc signed build has no Keychain backend. `wb setup` refuses
  it with the rule `keychain-needs-team`, and says how to sign the
  build. The 1Password backend still works (S15-02-secret-store).

### Bindings

A binding names:

- the hosts, ports and paths it applies to;
- the header and scheme to inject;
- the secret store item.

S07-egress-gateway covers placeholders found elsewhere.

Besides static values, a binding can hold:

- an OAuth 2 refresh token or client credentials, which `wb-proxyd`
  exchanges for short-lived access tokens;
- AWS keys, for SigV4 signing.

`wb cred set <binding>` reads the value from a prompt or stdin. The
value never appears in arguments or logs.

#### Public values

A binding can also hold a fixed public value in place of a secret store
item. That is for a host that answers public reads only to a request
with a token. Built-in profiles ship these, such as the anonymous
`ghcr.io` token for Homebrew bottles (S07-egress-gateway,
X22-no-guest-credentials).

Such a value isn't a secret, and the guest doesn't get a placeholder
for it.

#### Built-in hosts

A binding on a host that a built-in profile covers applies only to the
hosts and paths of the profile's parts that are on
(S07-11-http-policy, "Profile parts").

### Reach

A project's binding is injected for requests from the whole VM while
the project has a session in it. So any process in that VM can have it
used on its behalf, though none can read it (T11-shared-vm-grants).

`wb policy explain` shows who can reach it (S09-02-precedence).

### Model credential

Claude Code in the guest is configured with a placeholder. The model
API profile's binding injects the real credential on
`api.anthropic.com` alone (S07-08-requests-and-credentials, "Named places").

The kind of credential stored for the binding decides both:

- the guest variable that holds the placeholder;
- the header the binding injects.

#### Placeholder form

Each VM gets its own placeholder for the model credential.

##### Made by `wb-proxyd`

`wb-proxyd` generates the placeholder when it first fetches the binding
for a VM. It is the only process that reads the binding
(S15-03-access-rules, "Declared access"). It sends `wb-hostd` only the
placeholder, in a placeholder record.

`wb-hostd` keeps the VM's placeholder set, every placeholder of each
binding, with the VM's state. It:

- puts the newest in the guest environment;
- hands the whole set back to `wb-proxyd` after a suspend or a restart
  (S04-08-records-and-bounds, "Accounting and event channels", and
  S04-07-supervision, "Restarting a daemon").

`wb-proxyd` drops the binding at suspend as before. It doesn't keep
placeholder state of its own past it.

##### Checked when handed back

A handed-back placeholder is only a string to scan for, because
injection never depends on it. But `wb-proxyd` checks it before it
scans for it:

- its form: letters, digits and `-` only, 32 to 256 characters (a cap
  fixed in code). It is either random throughout, or the fixed prefix
  ("Shape") followed by at least 32 random characters;
- neither it nor any of the VM's binding credentials is a substring of
  the other. That is checked when `wb-proxyd` has the binding;
- at most 8 placeholders per binding per VM. The oldest is dropped past
  that.

A placeholder that fails a check is discarded. `wb-proxyd` then:

1. draws a fresh one;
2. sends `wb-hostd` its record;
3. logs a Device Config State Change (5019) event that names the VM
   and the binding, with the rule `placeholder-invalid` (SEC10-audit).

##### Random

The random part comes from the OS's cryptographic random source
(S15-03-access-rules, rule 8). It is independent of the credential. It
has at least 32 letters and digits, even when the stored credential is
shorter.

##### Shape

The placeholder has the stored credential's length, or more when the
random part needs it. It has at most 256 characters.

It keeps the credential's prefix only when the prefix matches a pattern
fixed in code: `sk-ant-`, then 1 to 8 lowercase letters or digits, then
`-`. The placeholder then looks like the credential to Claude Code.

Any other credential gets a placeholder that is random throughout.

##### Never reused, by chance

Every record of a placeholder is deleted with its VM.

A random part of at least 32 letters and digits holds about 190 bits.
So two VMs drawing the same placeholder, or one drawing an earlier one,
won't happen in practice.

`wb cred set` with a new value gives the VM a new placeholder for its
next session. The old one stays in the VM's scan set for the rest of
that VM's lifetime, up to the cap of 8. It goes with the VM's state
when the VM is deleted.

##### What this gives

Ordinary text then can't match a placeholder by chance. A placeholder
seen in a refusal names its VM (S07-09-placeholder-binding).

#### Credential kinds

##### Long-lived token

A Claude subscription user runs `claude setup-token` on the host, and
stores the token it prints with `wb cred set`.

- The guest's placeholder goes in `CLAUDE_CODE_OAUTH_TOKEN`.
- The binding injects `Authorization: Bearer <token>`.

X01-model-credential measured this mode.

`claude setup-token` says the token is valid for one year. Nothing in
Wraith Box renews it. At expiry, or after a leak, the user:

1. revokes the token at claude.ai/settings/claude-code, under
   "Authorization tokens";
2. runs `claude setup-token` on the host again;
3. stores the new token with `wb cred set`.

##### API key

- The guest's placeholder goes in `ANTHROPIC_API_KEY`.
- The binding injects `x-api-key`.

OpenShell has shown that an API key works this way.
X01-model-credential saw the placeholder go out as `x-api-key` and be
replaced. A run of this mode end to end is still open in I253.

##### Browser login

Browser login is not a V1 mode. The subscription login through a
browser (`/login`, SSO) and its token refresh need two things
(X01-model-credential, "Browser login (I15)"):

- the login happens outside the guest;
- only the host refreshes.

The host-side login is in I257. A guest's own login fails at the code
exchange (S14-04-commands-and-endpoints, "Commands that don't fit a guest").

### Never in the guest

Images are scanned for secrets at seal time. The environment of every
guest process is built from an allowlist.

## Decisions

### `wb` has no store access

- **Decided:** I252.

### Ad-hoc signed builds get no Keychain backend

- **Decided:** I248 (B248-dev-signing).

### A placeholder per VM, made by `wb-proxyd`

- **Decided:** I225.

### The long-lived token is a V1 mode

- **Decided:** I60 (B60-model-credential).

### No browser login in V1

- **Decided:** I15 (B15-browser-login).

**Status:** Draft
