# S07-06 - Requests and Credentials

**Purpose:** The form of each request `wb-proxyd` sends upstream, and
how it removes the guest's credentials and injects bound ones.

## Design

S07-14-placeholder-binding says where a
placeholder may go. S07-15-anonymous-credentials covers hosts that
want a token for public reads.

### Request form

On inspected hosts, `wb-proxyd` parses each request. It sends upstream
a request it builds itself:

- in origin form: the path and query, with any userinfo of an
  absolute-form target dropped;
- with no trailers;
- with a `Host` or `:authority` that must equal the stream's hostname,
  or the request is refused.

Policy, bindings and the credential rules match on the canonical path.
That path is also the one sent upstream. Literal dot segments are
removed.

A request is refused if its path has:

- a control byte or a backslash;
- malformed percent-encoding (`%zz`, a lone `%`);
- a segment whose percent-decoded value, split on `/`, has a `.` or
  `..` component (`%2e%2e`, `wget%2F..%2Fcurl`,
  `wget%2F%2e%2e%2Fcurl`).

#### Encoded slashes

An encoded `/` is data inside its segment, never a separator. Rules
match it that way, and it goes upstream as received.

- npm's scoped `/@scope%2fname` and GitLab's
  `/api/v4/projects/group%2Fproject` match their rules as one segment.
- `ghcr.io/v2/homebrew%2Fcore/...` matches no `homebrew/core` rule. It
  is refused by default-deny.

Some hosts decode `%2F` before routing, and `ghcr.io` is one of them.
On such a host, profile authors shape rules by path prefix and method
only. A rule that allows a wildcard segment and denies a sibling path
under it isn't sound there. The host can route an encoded `/` to that
sibling.

GitHub, GitLab, Codeberg, npm and PyPI treat `%2F` as data. So a deny
under an allowed prefix holds on them, such as the git hosting
profile's archive deny.

#### Header cap

This cap serves SEC13-bounded-resources. The request head is capped at
64 KiB, fixed in code. The head is the request line and header bytes
over HTTP/1.1, or the decoded header list over HTTP/2.

- Over HTTP/2, `wb-proxyd` announces the cap as
  `SETTINGS_MAX_HEADER_LIST_SIZE`.
- A request over the cap gets a `431` with the rule
  `http-header-size`. Nothing goes upstream.

#### Transfer coding

On every inspected host, a request is refused on its headers with a
`400` and the rule `http-transfer-coding` when:

- its `Transfer-Encoding` is anything but one `chunked`;
- it has a `Transfer-Encoding` over HTTP/2;
- it has both `Transfer-Encoding` and `Content-Length`.

Such a request doesn't reach the upstream.

A chunked body whose framing fails to decode ends the request with the
same rule. `wb-proxyd` aborts the upstream request as for a
placeholder in the body (S07-14-placeholder-binding, "Bounds").

#### Order of the steps

Each request then goes through these steps in order:

1. canonicalize;
2. classify and remove guest credentials (audited);
3. the checks on the request head: the placeholder scan of the target
   and the remaining headers, `http-transfer-coding` and
   `model-content-encoding`;
4. HTTP policy;
5. the dependency gate;
6. credential injection;
7. the streaming placeholder scan of the body, as it goes upstream.

### Credential replacement

This section serves SEC04-no-guest-secrets.

#### Headers

`wb-proxyd` removes the credentials the guest sends, whatever the
method:

- `Authorization`, `Proxy-Authorization` and `Cookie` on every
  inspected host;
- each header the host's built-in profile names.

`Cookie` goes even when the server itself set the cookie. So a host
whose flow needs cookies fails until a built-in profile for it allows
them. Examples are a challenge page, or a download behind a session
redirect.

#### Parameters

A request with a credential parameter that the profile names is
refused with a `403` that names the rule. The parameter counts:

- in the query string;
- in a form-encoded body;
- in a part of a `multipart/form-data` body;
- as a top-level key of a JSON body.

The media type is matched case-insensitively, with its parameters
ignored. So `APPLICATION/JSON; charset=utf-8` is scanned.

Bodies are read for this up to a fixed number of bytes received,
whatever `Content-Length` says. On a host whose profile names
parameters, a larger body of those types is refused, and so is one
that fails to parse.

Names are compared after percent-decoding and JSON string decoding,
with the profile's case rule. A repeated or empty occurrence counts,
and so does the name followed by `[` (`private_token[]`).

#### Injection and findings

If the host has a credential binding, the binding's credential is then
injected (S09-policy-credentials-audit).

- A guest token in any of those places is never forwarded.
- `wb-proxyd` doesn't look for credentials in other places
  (T10-unnamed-credentials).
- Each removal and refusal is logged with the header or parameter name
  and the rule, never the value (SEC10-audit).

A removed value can be neither a placeholder the guest was given nor
the binding's fixed public value. That means the guest holds a
credential from somewhere else. It is a detection finding
(S09-policy-credentials-audit), with the scheme and the header or
parameter name. Findings are rate-limited per VM.

Git LFS isn't in the git hosting profile (S08-workspace-and-git, "Out
of scope"). On a host the user allows for it, LFS hands the client a
server-issued `Authorization` header for each object. An LFS clone
there raises these findings too.

#### Credential headers in responses

On an inspected host with a credential binding, `wb-proxyd` removes
these headers from the response before it reaches the guest:

- Every `Set-Cookie`. A session cookie that a server issues to a
  request with the injected credential is a secret derived from it.
  SEC04-no-guest-secrets keeps that out of the guest.
- `X-OAuth-Scopes`, `X-Accepted-OAuth-Scopes` and `X-OAuth-Client-Id`,
  which describe the injected credential.

Each removal is logged with the header's name, never the value
(SEC10-audit). For a cookie the log also names the cookie. The rule is
`set-cookie-bound-host` for cookies, and
`credential-header-bound-host` for the others.

#### Named places

The model API profile names `Authorization` and `x-api-key`. Its
binding injects the model credential on `api.anthropic.com` alone.
There it injects only on the paths where X01-model-credential saw
Claude Code send it:

- `/v1/messages`;
- `/api/claude_code/`;
- `/api/claude_cli/bootstrap`;
- `/api/eval/`.

A long-lived token from `claude setup-token` is injected only as
`Authorization: Bearer <token>`. An API key is injected only as
`x-api-key` (S09-04-credentials, "Model credential").

The git hosting profile names, per kind of host
(X22-no-guest-credentials):

| Kind | Headers | Parameters |
|---|---|---|
| GitHub | `Authorization` only. GitHub itself refuses `?access_token=`. | none |
| GitLab | `PRIVATE-TOKEN`, `JOB-TOKEN`, `Deploy-Token` | `private_token`, `access_token`, `job_token`, `bearer_token`, compared case-sensitively, as GitLab does |
| Gitea and Forgejo | none | `token`, `access_token`, compared case-sensitively |

`github.com`, `gitlab.com` and `codeberg.org` get their kind built in.

A git remote on any other host gets the git hosting profile when the
host repository's remotes name it. Until the user sets its kind in
policy, it gets the union of every kind's names. A self-hosted host
then fails closed.

#### Signed URLs

`wb-proxyd` passes a credential in any other parameter unchanged.
The presigned storage URLs of Homebrew bottles need that, and so do
those of git LFS on a host the user allows.

`wb-proxyd` can't tell who signed such a URL. The guest can sign one
for its own bucket. A storage host still has to be allowed by policy.
The approval risk check flags:

- a shared object-storage host, such as `*.s3.amazonaws.com`,
  `storage.googleapis.com` or `*.blob.core.windows.net`;
- a wildcard over one;
- a rule that allows signature parameters on a write method.

## Decisions

### Remove guest credentials on every inspected host

`wb-proxyd` removes `Authorization`, `Proxy-Authorization`, `Cookie`
and the profile's named headers, refuses named parameters, and injects
only the binding's credential.

- **Decided:** I33 (B33-no-guest-credentials).

**Status:** Draft
