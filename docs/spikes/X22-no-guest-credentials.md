# X22 - Clients without guest credentials

**Purpose:** The result of the spike for I33. Which common clients
break when `wb-proxyd` removes every credential the guest sends on an
inspected host, and which rule fixes them without letting an attacker's
own token through?

**Requirements:** SEC04-no-guest-secrets, SEC06-repo-writes,
FR07-toolchain-manifest, FR08-no-proxy-config

**Brief:** B33-no-guest-credentials

## For review

- **Decides:** that `wb-proxyd` never forwards a credential the guest
  sends on an inspected host, and that a host which needs an anonymous
  credential gets one from a host-side binding. It also decides where a
  guest credential can be: the headers every host shares, and the
  headers and query parameters each built-in profile names.
- **You are approving:** the rewritten "Credential replacement" bullet
  of S07-egress-gateway, the anonymous binding in S09-policy-credentials-audit,
  and T10-unnamed-credentials. Of 11 clients, only Homebrew breaks when
  every guest credential is removed. A built-in binding that injects
  Homebrew's own anonymous token on `ghcr.io` fixes it. The other two
  candidate rules forward a value the guest chose, and one of them
  doesn't fix Homebrew.
- **Controls touched:** SEC04-no-guest-secrets is kept: nothing the
  guest sends in a credential header reaches upstream, and the only
  anonymous credential comes from the host. SEC06-repo-writes is kept
  and narrowed further: a git host's own credential query parameters
  (GitLab reads `private_token` and `access_token`) are refused, which
  the old text missed. SEC10-audit: each removal and refusal is logged
  with its rule.
- **Assumed:** that `ghcr.io` keeps accepting any bearer token for
  public `homebrew/core` reads. Homebrew depends on it, and the fallback
  (the proxy runs the registry's anonymous token flow) was measured too.
  Browsers and other cookie-keeping clients in the guest weren't
  measured.
- **Open decisions:** each has a recommendation in B33-no-guest-credentials.
  1. Cookies on hosts without a built-in profile. Recommended: remove
     them on every inspected host, as for `Authorization`.
  2. Which anonymous credential the `ghcr.io` binding injects.
     Recommended: the fixed `Bearer QQ==` that Homebrew sends, with the
     token flow as the fallback if `ghcr.io` stops accepting it.
- **Brief:** B33-no-guest-credentials

## Question

From X00-index: which common clients (Homebrew against `ghcr.io`, git,
`gh`, npm, pip, uv, SwiftPM, Go, cargo, Claude Code) break when
`wb-proxyd` removes every guest-supplied `Authorization` header and
cookie on inspected hosts? Registries such as `ghcr.io` hand out
anonymous tokens that the client must send back. Find a rule for those
that keeps SEC04-no-guest-secrets and SEC06-repo-writes.

## Answer

**Yes, with conditions.** Removing every guest credential breaks one
client, Homebrew, and a host-side anonymous binding fixes it. The fix
doesn't forward any value the guest sent.

| Client | Read operations run | Breaks when guest credentials are removed? | Why |
|---|---|---|---|
| Homebrew | `brew fetch` of two bottles | yes | `brew` sends `Authorization: Bearer QQ==` to `ghcr.io` and never runs the token flow. Without it, `ghcr.io` answers 401. |
| git | `git clone` over HTTPS | no | doesn't send a credential to a public repository |
| git LFS | clone of a repository with four LFS objects | no | the download addresses are presigned: the credential is in the query string, which the server issued |
| `gh` | `gh api`, `gh release view` | no | `gh` refuses to start without a token. With a placeholder token, removing it gives anonymous reads. Forwarding it gives 401. |
| npm | `npm view`, `npm install` | no | sends a configured token, and the registry answers reads without it |
| pip | `pip download` | no | same, with index URL credentials |
| uv | `uv pip install` | no | same |
| Go | `go mod download`, through `GOPROXY` and `HTTPS_PROXY` | no | doesn't send a credential to the module proxy or the checksum database |
| cargo | `cargo fetch` | no | doesn't send a credential for reads |
| SwiftPM | `swift package resolve` | no | resolves through git, which sends none |
| Claude Code | `claude -p` with a placeholder API key | not a break | the model API always has a binding (X01-model-credential). Without one, the removed key gives "Invalid API key" at once. |

The rule: on an inspected host, `wb-proxyd` removes `Authorization`,
`Proxy-Authorization` and `Cookie`, and every further header that the
host's built-in profile names. It refuses a request with a query
parameter the profile names as a credential. Then it injects the
binding's value, if the host has a binding. A binding may hold a fixed
public value instead of a secret, and the package registries profile
has one: `Authorization: Bearer QQ==` on reads of `ghcr.io`
`/v2/homebrew/core/`.

## Measurements

The spike ran on 2026-10-05 with a throwaway inspecting proxy on
localhost and a CA trusted only by each client process, under five
rules, against the public services and with no account. Every client
ran twice: once as installed, and once with a fake token configured the
way a guest holds a placeholder (npm `_authToken`, pip and uv index URL
credentials, `GH_TOKEN`, `.netrc`, git URL credentials,
`CARGO_REGISTRY_TOKEN`, `HOMEBREW_GITHUB_API_TOKEN`).

### Clients under each rule

| Client | Remove all | Remove on writes only | Pass issued tokens | Inject, fixed token | Inject, token flow |
|---|---|---|---|---|---|
| Homebrew | fail | pass | fail | pass | pass |
| Registry token flow, as `docker` or `oras` run it | fail | pass | pass | pass | pass |
| `gh` with a placeholder token | pass | fail | pass | pass | pass |
| git, git LFS, npm, pip, uv, Go, cargo, SwiftPM, with or without a placeholder | pass | pass | pass | pass | pass |

- **Remove on writes only:** forward guest credentials on GET and HEAD.
  It forwarded the placeholder `gh` holds, and GitHub answered 401 "Bad
  credentials". It also forwarded the attacker's token on every GET in
  the attack script, including a GET to `ghcr.io/token` with Basic
  credentials. On `ghcr.io` that request is how a client asks for a
  push token, so a GET is not always harmless.
- **Pass issued tokens:** forward a bearer token only if the proxy saw
  the host's token endpoint issue it, in this session, to a request
  the proxy had stripped of credentials. It fixes clients that run the
  token flow, but not Homebrew, which never asks for a token. It needs
  the proxy to read token responses and keep per-session state.
- **Inject:** both variants fix every client, and the guest's own value
  is never forwarded. With the binding, a client that runs the token
  flow never sees a 401.
- No client sent a `Cookie` header under any rule. The npm registry set
  Cloudflare cookies (`__cf_bm`, `_cfuvid`), and npm didn't send them
  back.
- Claude Code also sent a `DD-API-KEY` header to Datadog's log intake.
  That key is built into Claude Code, is the same for every user, and
  isn't a credential S07-egress-gateway removes.

### `ghcr.io`

- A read of `/v2/homebrew/core/<name>/...` without `Authorization` gets
  401 with a `Bearer` challenge. With `Bearer QQ==`, or with a random
  bearer value, it gets 200.
- An anonymous token from `ghcr.io/token` for one formula also read
  another formula.
- Writes failed with every anonymous credential: starting a blob upload
  with `QQ==` or with an issued anonymous token gave 401. Asking the
  token endpoint for `push` without credentials gave "denied".
- The token endpoint takes only GET. POST with an OAuth 2 password or
  refresh-token grant gave 405, so a credential can't hide in a token
  request body.
- `brew` (Homebrew 7.0.7) sets `HOMEBREW_GITHUB_PACKAGES_AUTH` to
  `Bearer QQ==` by default (`Library/Homebrew/brew.sh`) and sends it on
  every bottle and manifest request. The bottle download then redirects
  to `pkg-containers.githubusercontent.com` with a signed query string.
- `brew` also reads `homebrew/core/portable-ruby`, inside the
  `homebrew/core` scope, and `homebrew/command-not-found`, outside it.

### Attacker's own token

The attack script sent a token of its own on writes. The token was
fake, so upstream refused it under every rule. The table shows whether
the proxy forwarded it.

| Request | Remove all | Writes only | Issued | Inject |
|---|---|---|---|---|
| POST `api.github.com/user/repos`, `/gists`, `/graphql` | removed | removed | removed | removed |
| GET `api.github.com/user` | removed | forwarded | removed | removed |
| GET `ghcr.io/token` with Basic credentials | removed | forwarded | removed | removed |
| POST a blob upload to `ghcr.io` | removed | removed | removed | removed, or refused on `homebrew/core` |
| PUT to `registry.npmjs.org`, POST to `upload.pypi.org` | removed | removed | removed | removed |
| `x-api-key` to `api.anthropic.com` | removed | removed | removed | removed |
| `Cookie` on `github.com` | removed | forwarded | removed | removed |
| An issued anonymous token, then a POST blob upload | removed | removed | removed | refused on `homebrew/core` |

### Credentials outside the headers

Probed with a fake token and no account. A host that reads the value
answers 401, and one that ignores it gives the anonymous answer.

| Host | Header or parameter | Answer |
|---|---|---|
| `api.github.com` | `Authorization` | 401 |
| `api.github.com` | `?access_token=` | 400, refused by GitHub |
| `gitlab.com` | `Authorization`, `PRIVATE-TOKEN` | 401 |
| `gitlab.com` | `?private_token=`, `?access_token=` | 401 |
| `gitlab.com` | `JOB-TOKEN` | 200 |
| `registry.npmjs.org` | `?token=` | 200 |

GitLab reads a token from the query string, so a rule that only removes
headers would let an attacker's GitLab token through. Removing every
credential-looking query parameter on every host is no answer either:
the LFS and `ghcr.io` download addresses have server-issued `token`,
`sig` and `X-Amz-Signature` parameters, and those must pass. So each
built-in profile names the parameters its hosts read, and the rule
refuses only those.

## Limits

- One run per client and rule, on one machine and network, with small
  operations (two bottles, two npm packages, two wheels, one module, two
  crates, one Swift package).
- No client authenticated as a real account. Writes were checked by what
  the proxy forwarded, not by a write succeeding upstream.
- No `docker`, `oras` or `crane` was installed, so a shell script ran the token flow in place of
  a client that runs the registry token flow.
- Browsers and other clients that keep cookies were not measured.
- Go 1.27.1 honored `SSL_CERT_FILE` on this macOS, so `gh` and Go
  reached the inspecting proxy without the plain HTTP `GOPROXY` that
  X21-dep-gate-registries needed. The guest trusts the CA in its system
  store, so this matters only for spikes on the host.

## What it means for the specs

- S07-egress-gateway, "Credential replacement": rewritten. Always
  removed on inspected hosts: `Authorization`, `Proxy-Authorization`,
  `Cookie`, and the headers each built-in profile names. A request with
  a credential query parameter that the profile names is refused. Guest
  values are never forwarded, whatever the method. The package
  registries profile has an anonymous binding for `ghcr.io`
  `homebrew/core` reads, and refuses writes there.
- S09-policy-credentials-audit, "Bindings": a binding can hold a fixed
  public value, with no secret store item.
- T00-index: T10-unnamed-credentials, a guest's own credential in a
  header or parameter that no profile names, on a host without a
  built-in profile.
- S11-verification-and-spikes: conformance cases for the query
  parameter refusal and the anonymous binding.

## Spike code

Branch `spike/x22-no-guest-credentials`, commit
[`8951c77`](https://github.com/wraithbox/wraithbox/tree/8951c7705efac42dd3b365a6f1e04c37087a1b30/spikes/x22-no-guest-credentials):
the proxy, the harness, the probes, and the raw tables.

**Status:** Answered 2026-10-05: yes, with conditions
