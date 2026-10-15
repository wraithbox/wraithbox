# S07-06 - Requests and Credentials

**Purpose:** The form of each request `wb-proxyd` sends upstream, how it removes
the guest's credentials and injects bound ones, and where a
placeholder may go.

## Design

### Request form

On inspected hosts, `wb-proxyd` parses each
request and sends upstream a request it builds itself: origin form
(the path and query, with any userinfo of an absolute-form target
dropped), no trailers, and a `Host` or `:authority` that must equal
the stream's hostname, or the request is refused. Policy, bindings
and the credential rules match on the canonical path, which is also
the one sent upstream. Literal dot segments are removed. A request is
refused if its path has a control byte, a backslash, malformed
percent-encoding (`%zz`, a lone `%`), or a segment whose
percent-decoded value, split on `/`, has a `.` or `..` component
(`%2e%2e`, `wget%2F..%2Fcurl`, `wget%2F%2e%2e%2Fcurl`). An encoded
`/` is data inside its segment, never a separator: rules match it
that way, and it goes upstream as received. So npm's scoped
`/@scope%2fname` and GitLab's `/api/v4/projects/group%2Fproject`
match their rules as one segment, and
`ghcr.io/v2/homebrew%2Fcore/...` matches no `homebrew/core` rule and
is refused by default-deny. Some hosts decode `%2F` before routing,
and `ghcr.io` is one of them. On such a host, profile authors shape
rules by path prefix and method only. A rule that allows a wildcard
segment and denies a sibling path under it isn't sound there,
because the host can route an encoded `/` to that sibling. GitHub,
GitLab, Codeberg, npm and PyPI treat `%2F` as data, so a deny under
an allowed prefix holds on them, such as the git hosting profile's
archive deny.
- *Header cap* (SEC13-bounded-resources). The request head, the
  request line and header bytes over HTTP/1.1 or the decoded header
  list over HTTP/2, is capped at 64 KiB, fixed in code. Over HTTP/2,
  `wb-proxyd` announces the cap as `SETTINGS_MAX_HEADER_LIST_SIZE`.
  A request over the cap gets a `431` with the rule
  `http-header-size`, and nothing goes upstream.
- *Transfer coding.* On every inspected host, a request is refused
  on its headers with a `400` and the rule `http-transfer-coding`
  when its `Transfer-Encoding` is anything but one `chunked`,
  when it has a `Transfer-Encoding` over HTTP/2, or when it has both
  `Transfer-Encoding` and `Content-Length`. Such a request doesn't
  reach the upstream. A
  chunked body whose framing fails to decode ends the request with
  the same rule, and `wb-proxyd` aborts the upstream request as for
  a placeholder in the body ("Bounds").

Each request then goes through these steps in order: canonicalize,
classify and remove guest credentials (audited), the checks on the
request head (the placeholder scan of the target and the remaining
headers, `http-transfer-coding` and `model-content-encoding`), HTTP
policy, the dependency gate, credential injection, and the streaming
placeholder scan of the body as it goes upstream.

### Credential replacement

(SEC04-no-guest-secrets). `wb-proxyd`
removes the credentials the guest sends, whatever the method:
`Authorization`, `Proxy-Authorization` and `Cookie` on every
inspected host, plus each header the host's built-in profile names.
`Cookie` goes even when the server itself set the cookie, so a host
whose flow needs cookies, such as a challenge page or a download
behind a session redirect, fails until a built-in profile for it
allows them. The maintainer decided the rules of this paragraph on
I33 (B33-no-guest-credentials).

A request with a credential parameter that the profile names is
refused with a `403` that names the rule. The parameter counts in
the query string, in a form-encoded body, in a part of a
`multipart/form-data` body, or as a top-level key of a JSON body.
The media type is matched case-insensitively with its parameters
ignored, so `APPLICATION/JSON; charset=utf-8` is scanned. Bodies are
read for this up to a fixed number of bytes received, whatever
`Content-Length` says. On a host whose profile names parameters, a
larger body of those types, or one that fails to parse, is refused.
Names are compared after percent-decoding and JSON string decoding,
with the profile's case rule. A repeated or empty occurrence counts,
and so does the name followed by `[` (`private_token[]`).

If the host has a credential binding, the binding's credential is
then injected (S09-policy-credentials-audit). A guest token in any of
those places is never forwarded. `wb-proxyd` doesn't look for
credentials in other places (T10-unnamed-credentials). Each removal
and refusal is logged with the header or parameter name and the
rule, never the value (SEC10-audit). A removed value that is neither
a placeholder the guest was given nor the binding's fixed public
value means the guest holds a credential from somewhere else. It is
a detection finding (S09-policy-credentials-audit) with the scheme
and the header or parameter name, rate-limited per VM. Git LFS isn't
in the git hosting profile (S08-workspace-and-git, "Out of scope").
On a host the user allows for it, LFS hands the client a
server-issued `Authorization` header for each object. An LFS clone
there raises these findings too.

#### Credential headers in responses

On an inspected host with a
credential binding, `wb-proxyd` removes these headers from the
response before it reaches the guest:
- every `Set-Cookie`. A session cookie that a server issues to a
  request with the injected credential is a secret derived from
  it, which SEC04-no-guest-secrets keeps out of the guest;
- `X-OAuth-Scopes`, `X-Accepted-OAuth-Scopes` and
  `X-OAuth-Client-Id`, which describe the injected credential.

Each removal is logged with the header's name, and for a cookie
the cookie's name, with the rule `set-cookie-bound-host` for
cookies and `credential-header-bound-host` for the others, never
the value (SEC10-audit).

#### Named places

The model API profile names `Authorization` and
`x-api-key`. Its binding injects the model credential on
`api.anthropic.com` alone, and there only on the paths where
X01-model-credential saw Claude Code send it: `/v1/messages`,
`/api/claude_code/`, `/api/claude_cli/bootstrap` and `/api/eval/`.
A long-lived token from `claude setup-token` is injected only as
`Authorization: Bearer <token>`, and an API key only as
`x-api-key` (S09-04-credentials, "Model credential").
The git hosting profile names, per kind of host: GitHub, `Authorization`
only (GitHub itself refuses `?access_token=`). GitLab, the headers
`PRIVATE-TOKEN`, `JOB-TOKEN` and `Deploy-Token`, and the parameters
`private_token`, `access_token`, `job_token` and `bearer_token`, compared
case-sensitively, as GitLab does. Gitea and Forgejo, the
parameters `token` and `access_token`, compared case-sensitively
(X22-no-guest-credentials). `github.com`, `gitlab.com` and
`codeberg.org` get their kind built in. A git remote on any other
host gets the git hosting profile when the host repository's
remotes name it. Until the user sets its kind in policy, it gets the
union of every kind's names, and a self-hosted host fails closed.

#### Anonymous credentials

Some hosts answer public reads only to a
request with a token. `wb-proxyd` doesn't forward the guest's token
for those, not even one the host issued in the same session. A
host-side binding with a fixed public value injects one instead.
The package registries profile has one:
`Authorization: Bearer QQ==` on GET and HEAD of `ghcr.io`
`/v2/homebrew/core/`, the value Homebrew itself sends. On those
paths any method other than GET and HEAD is refused and logged with
the rule `anonymous-binding-read-only`. Without the binding, every
bottle download fails with a 401, because `brew` never asks the
token endpoint for a token. `ghcr.io/token` is denied to the
guest, since no client needs it once the binding answers.
Forwarding guest tokens on reads only was rejected: it forwards an
attacker's token on every GET, and a placeholder without a binding
then fails the request. Forwarding only tokens that the host issued
in this session was rejected because it doesn't fix Homebrew.

#### Token fallback

If `ghcr.io` stops accepting the fixed value,
`wb-proxyd` fetches an anonymous token itself, the documented flow
(X22-no-guest-credentials), and injects it in place of the fixed
value:
- It builds the URL itself: host `ghcr.io`, path `/token`, the
  parameter `service=ghcr.io` and the scope
  `repository:homebrew/core/<name>:pull`, percent-encoded.
  `<name>` comes from the request path. It must match the OCI
  distribution specification's repository-name grammar and the
  binding's path rule, or the request is refused. The URL never
  comes from the realm of a `WWW-Authenticate` header.
- `wb-proxyd` sends no credential with the fetch.
- The token response is read up to a fixed size, and a larger one
  fails the fetch. Its `expires_in` is clamped to a range fixed in
  code.
- A token is cached per scope until it expires. Fetches are
  rate-limited per VM, and a request over the rate is refused with
  the rule `anonymous-token-cap` (SEC13-bounded-resources).
- A request that gets a 401 with a freshly fetched token isn't
  retried. The 401 goes to the guest and is logged.
- Each fetch is a Network Activity (4001) event with the rule
  `anonymous-token-fallback` (SEC10-audit).

#### Signed URLs

`wb-proxyd` passes a credential in any other
parameter unchanged, as the presigned storage URLs of Homebrew
bottles need, and those of git LFS on a host the user allows.
`wb-proxyd` can't tell who signed such a URL, and the guest can
sign one for its own bucket. A storage host still has to be
allowed by policy, and the approval risk check flags a shared
object-storage host (such as `*.s3.amazonaws.com`,
`storage.googleapis.com`, `*.blob.core.windows.net`), a wildcard
over one, and a rule that allows signature parameters on a write
method.

#### Accepted gaps

`brew` also reads
`ghcr.io/v2/homebrew/command-not-found/`, and bottles of
third-party taps can be on `ghcr.io` outside `homebrew/core`. Both
are outside the profile and the anonymous binding, and fail in V1.
Claude Code sends a built-in `DD-API-KEY` header to Datadog's log
intake. That host stays out of the model API profile and is an
unknown destination like any other.

### Placeholder binding

(SEC04-no-guest-secrets). Each placeholder
is bound to the hosts, ports, and paths of its binding, following
OpenShell's provider model. The real credential goes only in the
header its binding injects. The placeholder itself is never sent
upstream. For AWS, `wb-proxyd`
signs requests (SigV4) rather than injecting a key. The maintainer
decided the scan's bounds and scope, and the body rule on the bound
host, on I225.
- *Outside the binding.* A placeholder found anywhere else in a
  request (another host, another header, a query string, a body)
  gets a `403` naming the binding, and an audit event with the rule
  `placeholder-outside-binding`. The exception is a header that
  "Credential replacement" removes (`Authorization`,
  `Proxy-Authorization` and `Cookie`, plus `x-api-key` on the model
  API profile's hosts). `wb-proxyd` removes such a header with an
  audit event of its own and doesn't refuse the request for it.
- *A body on the bound host.* A request body is outside the binding
  on every host, `/v1/messages` on `api.anthropic.com` included.
  `/v1/messages` is the one path of the model API profile whose
  request body holds the conversation. A placeholder in its request body means
  the placeholder reached the conversation, for example when the
  agent printed its environment. Such a request gets a `403` with the
  rule `model-placeholder-in-body`, and the response says so: the
  placeholder is in the conversation, and the fix is to clear the
  conversation (`/clear`) or start a new session
  (NFR06-explained-refusals). Each later turn of that conversation
  gets the same `403` until then. Compacting doesn't help, because
  Claude Code sends the whole conversation in a `/v1/messages` body
  to compact it. A placeholder in any other body, on
  `api.anthropic.com` too, gets `placeholder-outside-binding`. Forwarding such a body without replacement and logging a
  finding was rejected: the strict rule fails closed, and its cost
  stays within that one conversation.
- *What the scan reads.* On every inspected host, `wb-proxyd` scans
  the request target, each header line, name and value, as it decodes
  them, and the body with its chunked framing removed ("Request
  form", "Transfer coding"). It reads only the bytes the guest sent,
  never the request after credential injection. A refusal then says
  nothing about an injected credential. It looks for
  every placeholder of the VM in two forms: the exact bytes, and the
  percent-encoded form, in which any byte may be written as `%XX`
  with either hex case.
- *Bounds* (SEC13-bounded-resources). The scan streams. A byte goes
  upstream once the bytes after it can no longer complete a match.
  `wb-proxyd` holds back at most one window per request: the length
  of the longest form of the VM's longest placeholder, three times
  its length in bytes. It buffers nothing else for the scan. A
  placeholder split across chunks, HTTP/2 frames or reads is found
  like one that isn't. The request target and headers are read whole
  before anything goes upstream, and a match there doesn't send any
  of the request. A match in the body ends a request whose earlier
  bytes may already be upstream. `wb-proxyd` then aborts the upstream
  request: it resets the HTTP/2 stream or closes the HTTP/1.1
  connection. The upstream never gets a complete request or any byte
  of the placeholder. The guest gets the `403`, or a reset stream if
  the response had already started. Over HTTP/1.1, `wb-proxyd` then
  closes the guest connection, since the rest of the body is still
  on it.
- *Stated gaps.* The scan doesn't decode base64, a compressed body,
  WebSocket frames, JSON `\u` escapes, or any other encoding. A
  placeholder sent in one of them goes upstream unchanged. This is
  acceptable because the placeholder isn't a secret and doesn't open
  anything. The model credential's placeholder is also unique to its VM
  (S09-04-credentials, "Model credential").
- *Compressed requests to the model API.* On the model API profile's
  hosts, `api.anthropic.com` and `platform.claude.com`, a request
  with a `Content-Encoding` other than `identity` is refused with a
  `403` and the rule `model-content-encoding`. The scan then sees
  every body sent there. The check runs on the headers, before
  anything goes upstream.

**Status:** Draft
