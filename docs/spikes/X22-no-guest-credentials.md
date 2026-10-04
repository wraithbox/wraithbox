# X22 - Clients without guest credentials

**Purpose:** The result of the spike for I33. Which common clients
break when `wb-proxyd` removes every credential the guest sends on an
inspected host, and which rule fixes them without letting an attacker's
own token through?

**Requirements:** SEC04-no-guest-secrets, SEC06-repo-writes,
FR07-toolchain-manifest, FR08-no-proxy-config

**Brief:** B33-no-guest-credentials

## For review

- **Decides:** that `wb-proxyd` doesn't forward a credential the guest
  sends in any place it knows, and that a host which needs an anonymous
  credential gets one from a host-side binding. It also decides where
  `wb-proxyd` looks: the headers every host shares, and the headers,
  query parameters and body fields each built-in profile names.
- **You are approving:** the rewritten "Request form" and "Credential
  replacement" bullets and the Homebrew hosts of S07-egress-gateway,
  the fixed-value binding and the new detection finding in
  S09-policy-credentials-audit, and T10-unnamed-credentials. Of the 10
  read clients measured, only Homebrew breaks when every guest
  credential is removed, and a built-in binding that injects Homebrew's
  own anonymous token on `ghcr.io` fixes it. Claude Code needs its
  model binding (X01-model-credential). The other two candidate rules
  forward a value the guest chose, and one of them doesn't fix
  Homebrew.
- **Controls touched:** SEC04-no-guest-secrets is kept, with a
  narrower enforcement: the old text removed generic "API-key headers"
  everywhere, and the new one removes only the headers a profile names
  (T10-unnamed-credentials). SEC06-repo-writes is kept and tightened: a
  git host's credential parameters in the query string or body
  (GitLab's `private_token`, Forgejo's `token`) are refused, and an
  approval grants read methods only. SEC10-audit: each removal and
  refusal is logged with its rule, and a credential the host didn't
  give the guest is a detection finding.
- **Assumed:** that `ghcr.io` keeps accepting any bearer token for
  public `homebrew/core` reads. Homebrew depends on it, and the fallback
  (the proxy runs the registry's anonymous token flow) was measured too.
  Browsers and other cookie-keeping clients in the guest weren't
  measured. The body and query refusals are inferred from the probes,
  because the spike proxy never refused a parameter.
- **Open decisions:** each has a recommendation in B33-no-guest-credentials.
  1. Cookies on hosts without a built-in profile. Recommended: remove
     them on every inspected host, as for `Authorization`.
  2. Which anonymous credential the `ghcr.io` binding injects.
     Recommended: the fixed `Bearer QQ==` that Homebrew sends, with the
     token flow as the fallback if `ghcr.io` stops accepting it.
  3. What an approval grants on a host without a built-in profile.
     Recommended: read methods only (GET, HEAD, OPTIONS), with a write
     method or a WebSocket upgrade as a separate approval that the
     risk check flags. The specs were silent on this before, and it
     bounds T10-unnamed-credentials.
- **Brief:** B33-no-guest-credentials

## Question

From X00-index: which common clients (Homebrew against `ghcr.io`, git,
`gh`, npm, pip, uv, SwiftPM, Go, cargo, Claude Code) break when
`wb-proxyd` removes every guest-supplied `Authorization` header and
cookie on inspected hosts? Registries such as `ghcr.io` hand out
anonymous tokens that the client must send back. Find a rule for those
that keeps SEC04-no-guest-secrets and SEC06-repo-writes.

## Answer

**Yes, with conditions.** Of the 10 read clients measured, removing
every guest credential breaks one, Homebrew. A host-side anonymous
binding fixes it, together with
`pkg-containers.githubusercontent.com` in the profile for the bottle
redirect. The fix doesn't forward any value the guest sent.

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
| Claude Code | `claude -p` with a placeholder API key | not measured | the model API always has a binding (X01-model-credential). Without one, the removed key gives "Invalid API key" at once, so it ran only under two rules. |

`gh` without any token, and a push with a fake token, fail under every
rule, so they say nothing about the rules.

The rule: on an inspected host, `wb-proxyd` removes `Authorization`,
`Proxy-Authorization` and `Cookie`, and every further header that the
host's built-in profile names. It refuses a request with a parameter
the profile names as a credential, in the query string or in a form or
top-level JSON body. Then it injects the binding's value, if the host
has a binding. A binding may hold a fixed public value instead of a
secret, and the package registries profile has one:
`Authorization: Bearer QQ==` on reads of `ghcr.io`
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
  the proxy had stripped of credentials. It fixed the clients that run the
  token flow. Homebrew never asks for a token and still failed. It needs
  the proxy to read token responses and keep per-session state.
- **Inject:** both variants passed with all 10 read clients and the
  token-flow script, and the proxy forwarded none of the guest's
  values. With the binding, a client that runs the token flow gets 200
  on its first request.
- No client sent a `Cookie` header under any rule. The npm registry set
  Cloudflare cookies (`__cf_bm`, `_cfuvid`), and npm didn't send them
  back.
- Claude Code also sent a `DD-API-KEY` header to Datadog's log intake.
  That key is built into Claude Code and is the same for every user.
  The intake host stays out of the model API profile, so it's an
  unknown destination like any other.

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
  Bottles of third-party taps can be on `ghcr.io` outside
  `homebrew/core` too. S07-egress-gateway accepts that those fail.
- `brew` doesn't call `ghcr.io/token`, so S07-egress-gateway denies it
  rather than checking its `scope` values.

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

Added in the review of PR85, with `review-probe.sh`:

| Host | Header or parameter | Answer |
|---|---|---|
| `gitlab.com/api/graphql` | `private_token` in a form body, or as a top-level JSON key | 401 |
| `gitlab.com` | `?private%5Ftoken=` | 401 |
| `gitlab.com` | `?PRIVATE_TOKEN=` | 200, names are case-sensitive |
| `gitlab.com` | `?job_token=`, `Deploy-Token` on a project read | 200 |
| `codeberg.org` (Forgejo) | `?token=`, `?access_token=`, `Authorization: token` | 401 |

GitLab reads a token from the query string and the body, so a rule that
only removes headers would let an attacker's GitLab token through.
GitLab's source also reads `job_token` and `Deploy-Token` on the
endpoints that accept them, which a project read doesn't show. Removing
every credential-looking query parameter on every host is no answer
either: the LFS and `ghcr.io` download addresses have `token`, `sig`
and `X-Amz-Signature` parameters, and those must pass. So each built-in
profile names the parameters its hosts read, and the rule refuses only
those. `wb-proxyd` can't tell a server's signature from one the guest
computed for its own bucket, so the approval risk check flags shared
object-storage hosts instead.

## Limits

- One run per client and rule, on one machine and network, with small
  operations (two bottles, two npm packages, two wheels, one module, two
  crates, one Swift package).
- No client authenticated as a real account. Writes were checked by what
  the proxy forwarded, not by a write succeeding upstream.
- No `docker`, `oras` or `crane` was installed, so a shell script ran
  the registry token flow in their place.
- Clients not measured: pnpm, yarn and bun, RubyGems and bundler, Maven
  and Gradle, curl with `.netrc`, and browsers and other clients that
  keep cookies.
- The spike proxy removed headers but never refused a parameter, and
  never canonicalized paths. The refusals in S07-egress-gateway are
  inferred from the probes.
- Go 1.27.1 honored `SSL_CERT_FILE` on this macOS, so `gh` and Go
  reached the inspecting proxy without the plain HTTP `GOPROXY` that
  X21-dep-gate-registries needed. The guest trusts the CA in its system
  store, so this matters only for spikes on the host.

## What it means for the specs

- S07-egress-gateway, "Request form": new. Origin form, no trailers,
  `Host` checked, canonical paths, and the order of the steps.
- S07-egress-gateway, "Credential replacement": rewritten. Always
  removed on inspected hosts: `Authorization`, `Proxy-Authorization`,
  `Cookie`, and the headers each built-in profile names. A request with
  a credential parameter that the profile names, in the query string or
  a form or JSON body, is refused. The named places per kind of git
  host, and how a self-hosted one fails closed. The anonymous binding
  for `ghcr.io` `homebrew/core` reads, with every other method refused,
  `ghcr.io/token` denied and `pkg-containers.githubusercontent.com`
  added to the package registries profile. Signed URLs and what isn't
  covered.
- S07-egress-gateway, "What an approval grants": new, read methods only
  (open decision 3).
- S09-policy-credentials-audit: a binding can hold a fixed public
  value, a foreign credential is a detection finding, and the risk
  check flags shared object-storage hosts and signature parameters on
  writes.
- T00-index: T10-unnamed-credentials.
- S11-verification-and-spikes: fuzz targets for path canonicalization
  and the parameter scan, and conformance cases for the refusals, the
  detection finding and the Homebrew path.

## Spike code

Branch `spike/x22-no-guest-credentials`, commit
[`82e4c9f`](https://github.com/wraithbox/wraithbox/tree/82e4c9f314310fd97aa798ae911e976389750757/spikes/x22-no-guest-credentials):
the proxy, the harness, the probes (including the review probe), and
the raw tables.

**Status:** Answered 2026-10-05: yes, with conditions
