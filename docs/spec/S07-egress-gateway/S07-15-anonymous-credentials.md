# S07-15 - Anonymous Credentials

**Purpose:** How `wb-proxyd` answers hosts that want a token even for
public reads, without forwarding the guest's token.

## Design

### Anonymous credentials

Some hosts answer public reads only to a request with a token.
`wb-proxyd` doesn't forward the guest's token for those, not even one
the host issued in the same session. A host-side binding with a fixed
public value injects one instead (S07-06-requests-and-credentials,
"Credential replacement").

The package registries profile has one: `Authorization: Bearer QQ==`
on GET and HEAD of `ghcr.io` `/v2/homebrew/core/`. That is the value
Homebrew itself sends.

- On those paths, any method other than GET and HEAD is refused. It is
  logged with the rule `anonymous-binding-read-only`.
- Without the binding, every bottle download fails with a 401, because
  `brew` never asks the token endpoint for a token.
- `ghcr.io/token` is denied to the guest. No client needs it once the
  binding answers.

### Token fallback

If `ghcr.io` stops accepting the fixed value, `wb-proxyd` fetches an
anonymous token itself, by the documented flow
(X22-no-guest-credentials). It injects that token in place of the
fixed value.

- **The URL.** `wb-proxyd` builds it itself: host `ghcr.io`, path
  `/token`, the parameter `service=ghcr.io`, and the scope
  `repository:homebrew/core/<name>:pull`, percent-encoded.
  - `<name>` comes from the request path. It must match the OCI
    distribution specification's repository-name grammar and the
    binding's path rule, or the request is refused.
  - The URL never comes from the realm of a `WWW-Authenticate` header.
- **Credentials.** `wb-proxyd` doesn't send a credential with the
  fetch.
- **The response.** It is read up to a fixed size, and a larger one
  fails the fetch. Its `expires_in` is clamped to a range fixed in
  code.
- **Cache and rate.** A token is cached per scope until it expires.
  Fetches are rate-limited per VM. A request over the rate is refused
  with the rule `anonymous-token-cap` (SEC13-bounded-resources).
- **Retries.** A request that gets a 401 with a freshly fetched token
  isn't retried. The 401 goes to the guest, and is logged.
- **Audit.** Each fetch is a Network Activity (4001) event, with the
  rule `anonymous-token-fallback` (SEC10-audit).

### Accepted gaps

These fail or stay unknown in V1:

- `brew` also reads `ghcr.io/v2/homebrew/command-not-found/`.
- Bottles of third-party taps can be on `ghcr.io`, outside
  `homebrew/core`.
- Claude Code sends a built-in `DD-API-KEY` header to Datadog's log
  intake.

The first two are outside the profile and the anonymous binding, and
fail in V1. Datadog's host stays out of the model API profile. It is
an unknown destination like any other.

## Decisions

### A fixed public value for anonymous reads

A host-side binding injects Homebrew's own public value on reads of
`ghcr.io` `/v2/homebrew/core/`.

- **Rejected:**
  - Forwarding guest tokens on reads only. It forwards an attacker's
    token on every GET, and a placeholder without a binding then fails
    the request.
  - Forwarding only tokens that the host issued in this session. It
    doesn't fix Homebrew.

**Status:** Draft
