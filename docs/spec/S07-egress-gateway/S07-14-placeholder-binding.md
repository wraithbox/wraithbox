# S07-14 - Placeholder Binding

**Purpose:** Where a credential placeholder may go in a request, how
`wb-proxyd` scans for it, and what it refuses.

## Design

### Placeholder binding

This page serves SEC04-no-guest-secrets.

Each placeholder is bound to the hosts, ports, and paths of its
binding, following OpenShell's provider model.

- The real credential goes only in the header its binding injects
  (S07-06-requests-and-credentials).
- The placeholder itself is never sent upstream.
- For AWS, `wb-proxyd` signs requests (SigV4) rather than injecting a
  key.

### Outside the binding

A placeholder can be found anywhere else in a request: another host,
another header, a query string, a body. The request then gets a `403`
naming the binding, and an audit event with the rule
`placeholder-outside-binding`.

The exception is a header that S07-06-requests-and-credentials
("Credential replacement") removes: `Authorization`,
`Proxy-Authorization` and `Cookie`, plus `x-api-key` on the model API
profile's hosts. `wb-proxyd` removes such a header with an audit event
of its own. It doesn't refuse the request for it.

### A body on the bound host

A request body is outside the binding on every host, `/v1/messages`
on `api.anthropic.com` included.

`/v1/messages` is the one path of the model API profile whose request
body holds the conversation. A placeholder in its request body means
the placeholder reached the conversation, for example when the agent
printed its environment.

Such a request gets a `403` with the rule `model-placeholder-in-body`.
The response says so (NFR06-explained-refusals):

- the placeholder is in the conversation;
- the fix is to clear the conversation (`/clear`) or start a new
  session.

Each later turn of that conversation gets the same `403` until then.
Compacting doesn't help. Claude Code sends the whole conversation in
a `/v1/messages` body to compact it.

A placeholder in any other body, on `api.anthropic.com` too, gets
`placeholder-outside-binding`.

### What the scan reads

On every inspected host, `wb-proxyd` scans, as it decodes them:

- the request target;
- each header line, name and value;
- the body, with its chunked framing removed
  (S07-06-requests-and-credentials, "Request form", "Transfer
  coding").

It reads only the bytes the guest sent, never the request after
credential injection. A refusal then says nothing about an injected
credential.

It looks for every placeholder of the VM in two forms:

- the exact bytes;
- the percent-encoded form, in which any byte may be written as `%XX`
  with either hex case.

### Bounds

This section serves SEC13-bounded-resources. The scan streams. A byte
goes upstream once the bytes after it can no longer complete a match.

`wb-proxyd` holds back at most one window per request. The window is
the length of the longest form of the VM's longest placeholder, three
times its length in bytes. It buffers nothing else for the scan.

A placeholder split across chunks, HTTP/2 frames or reads is found
like one that isn't.

The request target and headers are read whole before anything goes
upstream. A match there doesn't send any of the request.

A match in the body ends a request whose earlier bytes may already be
upstream. `wb-proxyd` then:

1. aborts the upstream request: it resets the HTTP/2 stream, or closes
   the HTTP/1.1 connection. The upstream never gets a complete
   request, or any byte of the placeholder.
2. sends the guest the `403`, or a reset stream if the response had
   already started;
3. over HTTP/1.1, closes the guest connection, since the rest of the
   body is still on it.

### Stated gaps

The scan doesn't decode base64, a compressed body, WebSocket frames,
JSON `\u` escapes, or any other encoding. A placeholder sent in one of
them goes upstream unchanged.

This is acceptable, because the placeholder isn't a secret, and
doesn't open anything. The model credential's placeholder is also
unique to its VM (S09-04-credentials, "Model credential").

### Compressed requests to the model API

The model API profile's hosts are `api.anthropic.com` and
`platform.claude.com`. On them, a request with a `Content-Encoding`
other than `identity` is refused with a `403` and the rule
`model-content-encoding`.

- The scan then sees every body sent there.
- The check runs on the headers, before anything goes upstream.

## Decisions

### A placeholder in the conversation refuses the request

A placeholder in a `/v1/messages` body is refused with
`model-placeholder-in-body`.

- **Why:** the strict rule fails closed, and its cost stays within
  that one conversation.
- **Rejected:** forwarding such a body without replacement, and
  logging a finding.
- **Decided:** I225, which also set the scan's bounds and scope.

**Status:** Draft
