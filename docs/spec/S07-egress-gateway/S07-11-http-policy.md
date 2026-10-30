# S07-11 - HTTP Policy

**Purpose:** The HTTP rules of inspected hosts, the built-in profiles,
and the parts a user can turn off.

## Design

### HTTP policy

This page serves SEC06-repo-writes. Rules match, in the OpenShell
policy schema (S09-policy-credentials-audit):

- method and path, per host;
- GraphQL operation type and name;
- WebSocket messages.

Each inspected host enforces its rules by default. A host and port can
be set to `audit` while a new rule is tried out. Violations are then
logged but allowed (S07-07-hosts-and-modes, "Modes, per host").

The boundary check sees a host and port in `audit` as the whole host
on that port, every method and path. So it passes only when the
boundary allows every one (S09-01-policy-files, "Audit as the whole host").

The built-in profiles are git hosting, model API and package
registries.

#### git hosting

- Reads are allowed.
- `git-receive-pack` and mutating API calls are allowed only for the
  repositories of the projects with a session in the VM. Those come
  from each project's host repository remotes, plus explicit
  additions. Any process in the VM can then write to them
  (T11-shared-vm-grants).
- Gists, repository creation and forks are denied.

GraphQL mutations are denied unless the operation name is allowlisted.
This operation filter applies to every request to the GraphQL endpoint
of a built-in host, whatever the source of the rule that allows it. A
user or repository rule on that endpoint is a load error
(S09-02-precedence).

The prover can't model GraphQL rules. The boundary check sees each one
as `POST` to its GraphQL path on its host. The boundary then limits
whether that endpoint is reachable, not which operations are allowed
(S09-04-boundary-check, "Built-in profiles in the check").

Git LFS isn't in the profile, and fails in V1 (S08-workspace-and-git,
"Out of scope").

The built-in kinds also name the hosts on which their forges serve
SSH on port 443: `ssh.github.com` for GitHub, and `altssh.gitlab.com`
for GitLab.

- The profile doesn't allow a request to them, but it covers them. So
  they are guarded hosts (S07-07-hosts-and-modes, "pass").
- They can't be pass hosts or have a raw TCP entry. So SSH to them
  always meets the first-byte check (S07-15-non-http-streams, "SSH").

#### model API

The profile holds the paths Claude Code 2.1.289 reached on two hosts
in X01-model-credential. The model credential binding covers four
paths of `api.anthropic.com` (S07-08-requests-and-credentials, "Named places").

On `api.anthropic.com`:

| Path | What it is | Injection |
|---|---|---|
| `/v1/messages` | the model | yes |
| `/api/claude_code/policy_limits` | the organization's limits | yes |
| `/api/claude_code/settings` | remote managed settings | yes |
| `/api/claude_cli/bootstrap` | | yes |
| `/api/eval/` | feature flag evaluation | yes |
| `/mcp-registry/v0/servers` | the public MCP server registry | no |
| `/api/hello` | a reachability check | no |

The last two are allowed without injection, because Claude Code sent
them no credential.

Claude Code reaches `/api/eval/` only when
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` is unset in the guest
(S06-08-projects-and-sessions, "Claude Code in the guest"). That was
so in the second pass of the C06-model-api run. Its allow stays,
because that variable isn't the control. Feature flags may change what
Claude Code does, and what it does without them wasn't measured.

On `platform.claude.com`: `/v1/oauth/hello`, a reachability check of
the login host. The binding doesn't cover it.

No policy file can allow a `POST` to a path that starts with the string
`/v1/oauth/token` on the profile's hosts. Such a rule is a load error with the rule
`model-token-path` (S09-02-precedence, "The OAuth token paths").

- *Event log denied.* `/api/event_logging/` on `api.anthropic.com` is
  Anthropic's event log. It receives the model credential and usage
  data. It is a built-in deny of the profile, logged with the rule
  `model-event-log`.
  - Like the archive deny, it holds whatever the guest sets, and
    whatever a policy file allows.
  - It matches the canonical path.
  - Built-in denies, this one included, are evaluated before any
    allow.
- *No percent-escapes.* On the profile's hosts, `api.anthropic.com`
  and `platform.claude.com`, `wb-proxyd` refuses a request whose path
  holds any percent-escape. It is logged with the rule
  `model-path-escape`. None of the paths Claude Code sent in
  X01-model-credential has one. This one rule closes encoded letters,
  either hex case, `%2F` and double encoding around the event log
  deny.
- *Outside the profile.* These have no exact-path entry, and each is
  an unknown destination like any other host:
  - Datadog's log intake, `http-intake.logs.us5.datadoghq.com`
    (S07-10-anonymous-credentials, "Accepted gaps");
  - `downloads.claude.ai`, the updater and the plugin marketplace;
  - `raw.githubusercontent.com`, the changelog.

  The guest's Claude Code is set not to ask for them
  (S06-08-projects-and-sessions, "Claude Code in the guest"), but the
  profile is the control. The updater would fetch and run a binary
  past SEC07-dep-gate. `raw.githubusercontent.com` serves any file of
  any public repository.
- *Parts.* The profile has one part, `model`, with every allow rule
  above.

#### package registries

Metadata and downloads only. Publish and upload endpoints are denied.

- npm, PyPI, crates.io and the Go module proxy.
- `sum.golang.org` read-only: `/lookup/`, `/latest` and `/tile/`. The
  module proxy doesn't serve the checksum database.
- Homebrew bottles:
  - `ghcr.io` only for GET and HEAD of `/v2/homebrew/core/...`, with
    the anonymous binding (S07-10-anonymous-credentials), and
    `ghcr.io/token` denied;
  - `pkg-containers.githubusercontent.com` GET and HEAD only, because
    each bottle download redirects there with a signed query string
    (X22-no-guest-credentials);
  - `formulae.brew.sh` read-only.

### Profile parts

Each built-in profile is split into named parts. Every allow rule of a
profile belongs to one part. All parts are on by default.

A built-in deny, such as the git hosting profile's archive deny,
belongs to no part. It applies whatever parts are on. Turning a part
off therefore can't remove a deny.

- *Switches.* The global policy and a project's network policy turn a
  part off with a Wraith Box extension key. Repository configuration
  has no such key. A repository configuration rule on a built-in host
  whose part is off is a load error that names the part
  (NFR06-explained-refusals).
- *Effect.* A part that is off is not in the effective policy. The
  requests its rules would allow are refused, whatever other rule
  allows them, from any project or approval.
  - A request refused this way is logged with the rule
    `profile-part-off`.
  - A host whose built-in rules are all in parts that are off is
    refused at DNS with the same rule, and no approval event
    (S07-04-dns-refusals).
- *Bindings.* A credential binding covers the hosts and paths of the
  parts that are on, and nothing of a part that is off. With only
  `git` on, the GitHub binding is injected into git's transport and
  nowhere else.
- *What parts don't change.* These hold whatever parts are on:
  - the removal of guest credentials in the places a profile names
    (S07-08-requests-and-credentials, "Credential replacement");
  - the lock on a built-in host's kind, mode and rules
    (S09-02-precedence).
- *Per VM.* A part is off in a VM when the global policy, or any
  project with a session in the VM, turns it off. Turning a part off
  is a limit, and the VM takes the strictest value of a limit
  (S07-01-per-vm-policy). The result doesn't depend on the order in
  which sessions started.
- *Narrowing.* Turning a part off only narrows. It needs no boundary
  check to pass (S07-01-per-vm-policy, "Policy changes while sessions run"). Turning a part back on widens. So does a session end that
  removes the last project that turned a part off
  (S07-01-per-vm-policy, "Session end").

`wb policy explain` shows each part, whether it is on, and the policy
that turned it off. A request refused because its part is off names
the part and that policy (NFR06-explained-refusals).

#### GitHub parts

The GitHub kind of the git hosting profile has these parts:

| Part | Rules |
|---|---|
| `git` | git's smart HTTP transport on `github.com`: the `info/refs` advertisement, and the `git-upload-pack` and `git-receive-pack` requests of a repository. So fetch, pull and push. Push stays limited to the repositories above. |
| `rest` | the REST API on `api.github.com` |
| `graphql` | the GraphQL API, `POST api.github.com/graphql` |
| `web` | every other rule of the GitHub kind |

With only `git` on, git can fetch, pull and push to `github.com`, and
every API request, REST and GraphQL, is refused.

The other kinds of the git hosting profile, and the other profiles,
name their parts in their profile. The docs list them.

## Decisions

### The model API paths

The model API profile lists the paths Claude Code reached in
X01-model-credential.

- **Decided:** I60 (B60-model-credential).

### Profile parts

Built-in profiles are split into parts a user can turn off, and
turning a part off only narrows.

- **Decided:** I35 (B35-openshell-artifacts).

**Status:** Draft
