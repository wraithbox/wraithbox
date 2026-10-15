# S07-07 - HTTP Policy

**Purpose:** The HTTP rules of inspected hosts, the built-in profiles, and the
parts a user can turn off.

## Design

### HTTP policy

(SEC06-repo-writes). Rules match method and path per host, GraphQL
operation type and name, and WebSocket messages, in the OpenShell
policy schema (S09-policy-credentials-audit). Each inspected host enforces its rules by
default. A host and port can be set to `audit` while a new rule is
tried out: violations are then logged but allowed (S07-05-hosts-and-modes, "Modes, per host"). The boundary check sees a host and port in `audit` as the
whole host on that port, every method and path, so it passes only
when the boundary allows every one (S09-01-policy-files, "Audit as the whole host"). Built-in profiles:

#### git hosting

reads allowed; `git-receive-pack` and mutating API
calls only for the repositories of the projects with a session in
the VM (derived from each project's host repository remotes, plus
explicit additions), which any process in the VM can then write
to (T11-shared-vm-grants); gists, repository creation and forks
denied. GraphQL mutations are denied unless the operation name is
allowlisted, and this operation filter applies to every request
to the GraphQL endpoint of a built-in host, whatever the source of
the rule that allows it. A user or repository rule on that
endpoint is a load error (S09-11-precedence). The prover can't model GraphQL rules. The boundary
check sees each one as `POST` to its GraphQL path on its host, and
the boundary then limits whether that endpoint is reachable, not
which operations are allowed (S09-03-boundary-check, "Built-in profiles in the check"). Git LFS isn't in the profile and
fails in V1 (S08-workspace-and-git, "Out of scope"). The built-in
kinds also name the hosts on which their forges serve SSH on port
443: `ssh.github.com` for GitHub and `altssh.gitlab.com` for
GitLab. The profile doesn't allow a request to them, but it covers
them, so they are guarded hosts (S07-05-hosts-and-modes, "pass"). They can't be
pass hosts or have a raw TCP entry, so SSH to them always meets the
first-byte check (S07-10-non-http-streams, "SSH").

#### model API

the paths Claude Code 2.1.289 reached on two hosts in
X01-model-credential, with the model credential binding on four
paths of `api.anthropic.com` (S07-06-requests-and-credentials, "Named places"). The maintainer
decided the list on I60 (B60-model-credential).
- `api.anthropic.com`: `/v1/messages` (the model),
  `/api/claude_code/policy_limits` (the organization's limits),
  `/api/claude_code/settings` (remote managed settings),
  `/api/claude_cli/bootstrap`, `/api/eval/` (feature flag
  evaluation), `/mcp-registry/v0/servers` (the public MCP server
  registry) and `/api/hello` (a reachability check). The last two
  are allowed without injection, because Claude Code sent them no
  credential. Claude Code reaches `/api/eval/` only when
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` is unset in the guest
  (S06-08-projects-and-sessions, "Claude Code in the guest"), as in the second
  pass of the C06-model-api run. Its allow stays,
  because that variable isn't the control: feature flags may change
  what Claude Code does, and what it does without them wasn't
  measured.
- `platform.claude.com`: `/v1/oauth/hello`, a reachability check
  of the login host. The binding doesn't cover it.
- *Event log denied.* `/api/event_logging/` on `api.anthropic.com`,
  Anthropic's event log, which receives the model credential and
  usage data, is a built-in deny of the profile, logged with the
  rule `model-event-log`. Like the archive deny, it holds whatever
  the guest sets and whatever a policy file allows. It matches the
  canonical path. Built-in denies, this one included, are evaluated
  before any allow.
- *No percent-escapes.* On the profile's hosts,
  `api.anthropic.com` and `platform.claude.com`, `wb-proxyd`
  refuses a request whose path holds any percent-escape, logged
  with the rule `model-path-escape`. None of the paths Claude Code
  sent in X01-model-credential has one, and this one rule closes
  encoded letters, either hex case, `%2F` and double encoding
  around the event log deny.
- *Outside the profile.* Datadog's log intake
  (`http-intake.logs.us5.datadoghq.com`, S07-06-requests-and-credentials, "Accepted gaps"),
  `downloads.claude.ai` (the updater and the plugin marketplace)
  and `raw.githubusercontent.com` (the changelog), with no
  exact-path entry for any of them. Each is an unknown destination
  like any other host. The guest's Claude Code is set not to ask
  for them (S06-08-projects-and-sessions, "Claude Code in the guest"), but the
  profile is the control. The updater would fetch and run a binary
  past SEC07-dep-gate, and `raw.githubusercontent.com` serves any
  file of any public repository.
- *Parts.* The profile has one part, `model`, with every allow rule
  above.

#### package registries

metadata and downloads only, publish and
upload endpoints denied. npm, PyPI, crates.io and the Go module
proxy, plus `sum.golang.org` read-only (`/lookup/`, `/latest`,
`/tile/`), because the module proxy doesn't serve the checksum
database. Homebrew bottles: `ghcr.io` only for GET and HEAD of
`/v2/homebrew/core/...`, with the anonymous binding above and
`ghcr.io/token` denied, `pkg-containers.githubusercontent.com` GET
and HEAD only, because each bottle download redirects there with a
signed query string (X22-no-guest-credentials), and
`formulae.brew.sh` read-only.

### Profile parts

Each built-in profile is split into named parts, and
every allow rule of a profile belongs to one part. A built-in deny,
such as the git hosting profile's archive deny, belongs to no part
and applies whatever parts are on. Turning a part off therefore
can't remove a deny. All parts are on by default.
- *Switches.* The global policy and a project's network policy turn
  a part off with a Wraith Box extension key. Repository
  configuration has no such key. A repository configuration rule on a
  built-in host whose part is off is a load error that names the
  part (NFR06-explained-refusals).
- *Effect.* A part that is off is not in the effective policy, and
  the requests its rules would allow are refused whatever other rule
  allows them,
  from any project or approval. A request refused this way is logged
  with the rule `profile-part-off`. A host whose built-in rules are
  all in parts that are off is refused at DNS with the same rule and
  no approval event (S07-03-dns).
- *Bindings.* A credential binding covers the hosts and paths of the
  parts that are on, and nothing of a part that is off. With only
  `git` on, the GitHub binding is injected into git's transport and
  nowhere else.
- *What parts don't change.* The removal of guest credentials in
  the places a profile names (S07-06-requests-and-credentials, "Credential replacement") and the lock
  on a built-in host's kind, mode and rules
  (S09-11-precedence) hold whatever parts
  are on.
- *Per VM.* A part is off in a VM when the global policy or any
  project with a session in the VM turns it off. Turning a part off
  is a limit, and the VM takes the strictest value of a limit
  (S07-01-per-vm-policy). The result doesn't depend on the order in
  which sessions started.
- *Narrowing.* Turning a part off only narrows. It needs no boundary
  check to pass (S07-01-per-vm-policy, "Policy changes while sessions run"). Turning a
  part back on widens, and so does a session end that removes the
  last project that turned a part off (S07-01-per-vm-policy, "Session end").

`wb policy explain` shows each part, whether it is on, and the policy
that turned it off, and a request refused because its part is off
names the part and that policy (NFR06-explained-refusals). The
maintainer decided this on I35 (B35-openshell-artifacts). The GitHub
kind of the git hosting profile has these parts:
- `git`: git's smart HTTP transport on `github.com`, which is the
  `info/refs` advertisement and the `git-upload-pack` and
  `git-receive-pack` requests of a repository, so fetch, pull and
  push. Push stays limited to the repositories above.
- `rest`: the REST API on `api.github.com`.
- `graphql`: the GraphQL API, `POST api.github.com/graphql`.
- `web`: every other rule of the GitHub kind.

With only `git` on, git can fetch, pull and push to `github.com` and
every API request, REST and GraphQL, is refused. The other kinds of
the git hosting profile, and the other profiles, name their parts in
their profile, and the docs list them.

**Status:** Draft
