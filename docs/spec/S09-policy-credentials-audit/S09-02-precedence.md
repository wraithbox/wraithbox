# S09-02 - Precedence

**Purpose:** How the policy sources merge, how the mode of each host and
port resolves, and how the merge works for a VM with more than one
project.

## Design

### Precedence

The sources, in order:

1. built-in defaults;
2. global;
3. project;
4. repository configuration (with `config_trust`);
5. session approvals.

`wb policy explain` shows the effective value and its source.

#### Network rules merge as a union

Rules are keyed by source and rule name. So a later source adds rules,
and never replaces one. A rule in user or repository policy with the
name of a built-in profile's rule is a load error.

Built-in denies apply whatever the source of the allow. They are
evaluated before any allow.

User and repository policy can't change a built-in host's kind, mode or
rules. A user or repository rule on a built-in host is a load error
when its method and path pattern can match a request to that host's
GraphQL path after normalization. That covers:

- a path prefix or glob that covers it;
- a wildcard method;
- a trailing slash;
- percent-encoding.

The built-in GraphQL operation filter applies whatever the source of
the allow.

The global and project policy can make one change to a built-in
profile: turn a part of it off (S07-11-http-policy, "Profile parts").
This lock holds whatever parts are on. So does the removal of guest
credentials in the places a profile names.

#### The OAuth token paths

A user or repository rule is a load error when it could allow a `POST`
to `/v1/oauth/token`, or to any path that starts with it, on a model
API host (S07-11-http-policy, "model API"). The load check applies the
same normalization as for the GraphQL path, and it counts a host
pattern that matches a model API host, a wildcard included. It covers
rules in the global and project policy, and in repository configuration
with `config_trust`.

The refusal names the rule `model-token-path`, the policy file and the
rule's name (NFR06-explained-refusals). It tells the user that the model
credential comes from `claude setup-token` on the host and is stored
with `wb cred set` (S09-05-credentials, "Long-lived token").

The model API profile doesn't allow these paths, so a login started in
the guest fails at its token request (S14-04-commands-and-endpoints,
"Browser login"). Without this check a policy rule could allow them. The
guest's login would then complete and leave the OAuth tokens in the
guest. With no unlocked Keychain, Claude Code writes them in plaintext
to `~/.claude/.credentials.json` (X01-model-credential, "Without a
Keychain"). This check enforces
SEC04-no-guest-secrets for the model credential's OAuth tokens. Like the
GraphQL lock, it holds whatever profile parts are on.

#### Mode per host and port

`enforcement` is resolved for each host and port over the whole union,
not per endpoint. The union is:

- the built-in profiles;
- the global, project and repository policy;
- the approved rules;
- every endpoint within one file;
- every project with a session in the VM ("Per VM").

The rules of the resolution:

- Any endpoint in `enforce` on a host and port, from any source, puts
  that host and port in `enforce`.
- An endpoint with no `enforcement` field counts as `enforce`.
- An endpoint counts on every host its host pattern matches. So a
  wildcard endpoint in `enforce` keeps a matching exact host in
  `enforce`, which fails closed.
- A host and port resolves to `audit` only when every endpoint on it
  is in `audit`.

`wb-proxyd` applies the resolved mode to every request on that host
and port. It never applies the `enforcement` field of the endpoint
whose rule matched. The boundary check projects the same mode
(S09-01-policy-files, "Audit as the whole host").

A change that moves a host and port from `enforce` to `audit` widens,
whatever removal caused it (S07-01-per-vm-policy, "Policy changes while
sessions run", "Session end").

#### Per VM

The host enforces the merge for a VM, not for a project. The project
sources are those of every project with a session in the VM.

- Limits take the strictest value among them.
- The mode of a host and port resolves over every source. So `enforce`
  wins over `audit` ("Mode per host and port").
- A conflict between projects refuses the joining session
  (S07-01-per-vm-policy). Examples are a host in pass mode for one
  project and inspected or bound for another, or two bindings for one
  host and path.
- The extension check (S09-01-policy-files, "What the prover sees")
  runs over the union at each recompute.

`wb policy explain` shows, for each rule and each credential binding:

- the project it comes from;
- the projects whose sessions can reach it now. Those are all the
  projects with a session in the same VM (T11-shared-vm-grants).

## Decisions

### The mode resolves per host and port, over the whole union

- **Decided:** I109.

### Policy can't allow the OAuth token paths

- **Decided:** I259. A built-in deny on these paths, evaluated before
  any allow, can be added on top later.

**Status:** Draft
