# C06 - Model API

**Purpose:** The test cases for the model credential: its placeholder,
the scan for the placeholder in whole requests, what is injected on
the model API, the event log, and Claude Code end to end.

**Verifies:** S07-egress-gateway ("Credential replacement",
"Placeholder binding", "Bounds", "model API"),
S09-policy-credentials-audit ("Model credential"), S06-vm-lifecycle
("Claude Code in the guest"), S04-08-records-and-bounds ("Accounting and event channels")

## Conformance

### The guest's own key

- Call the model API with an attacker-supplied key, and see that it is
  replaced.

### The placeholder in a request

S07-egress-gateway, "Credential replacement" and "Placeholder binding".
The scan covers whole requests, not only `Authorization` and
`x-api-key`.

- Send the model credential's placeholder in `Authorization`,
  `Proxy-Authorization` or `Cookie` to any
  inspected host, or in `x-api-key` to a host in the model API profile.
  See the header removed before policy runs, logged with its name and
  rule, and the request not refused for it.
- Send it in any header that isn't removed there, or in a query string,
  to `api.anthropic.com`, to `platform.claude.com` and to another
  inspected host. See each request get a `403` that names the binding,
  an audit event, and nothing sent upstream.
- Send it in a form-encoded, `multipart/form-data` and JSON body to
  `platform.claude.com` and to another inspected host, exact and
  percent-encoded. See the same `403` with
  `placeholder-outside-binding`, the upstream request aborted, and no
  byte of the placeholder upstream.
- Send it in the JSON body of a `/v1/messages` request to
  `api.anthropic.com`. See a `403` with `model-placeholder-in-body`,
  whose message says the placeholder is in the conversation and to
  clear the conversation (`/clear`) or start a new session, and no byte
  of the placeholder upstream.
- Send it in a body to another path of the binding, such as
  `/api/eval/`, and see `placeholder-outside-binding`.
- In Claude Code, print the placeholder into the conversation, and see
  the next turn and a `/compact` get the same `403`, and a turn after
  `/clear` pass.

### Bodies split across reads

Spec: S07-egress-gateway, "Bounds".

- Send it in a chunked body split across two chunks, and across two
  HTTP/2 `DATA` frames, at every offset in the placeholder, exact and
  percent-encoded. See each request refused with no byte of the
  placeholder upstream.
- Send the same bodies with the placeholder's last character changed,
  and see them forwarded unchanged, with `wb-proxyd` holding back no
  more than the window.
- Send it late in a body to a stand-in upstream that answers before the
  body ends. See the guest get a reset stream over HTTP/2 and a closed
  connection over HTTP/1.1.
- Send it in a body over HTTP/1.1 to an upstream that waits, and see
  the `403` and then the guest connection closed.
- Send a request with `Content-Encoding: gzip` to `api.anthropic.com`
  and to `platform.claude.com`, and see each get a `403` with
  `model-content-encoding` and nothing sent upstream.

### Placeholders

Spec: S09-policy-credentials-audit, "Model credential".

- Create two VMs with the same stored credential, and see two different
  placeholders, each with the credential's length and prefix.
- Store a credential with a prefix outside the fixed pattern, and see a
  placeholder random throughout. Store one shorter than 32 characters,
  and see a placeholder with at least 32 random characters.
- Store a new value with `wb cred set`, and see a new placeholder in
  the next session and the old one still refused.
- Suspend and resume the VM, and restart `wb-proxyd`, and see both
  placeholders unchanged and both still refused outside their binding.
- Store nine values in turn, and see the oldest placeholder dropped
  from the set.
- Hand `wb-proxyd` a placeholder set holding one placeholder that is
  too short, one with a character outside letters, digits and `-`, and
  one that is a substring of the stored credential. See each discarded,
  a fresh one drawn and sent to `wb-hostd`, and a 5019 event with
  `placeholder-invalid` naming the VM and the binding.
- Send `wb-hostd` a placeholder record that fails its form check, and
  see the waiting session start refused with
  `placeholder-record-invalid`, and `wb-proxyd` ended, counted toward
  the restart limit and restarted (S04-08-records-and-bounds, "Accounting and event channels").

### What is injected

- With a long-lived token stored, send a request to each path of the
  binding on `api.anthropic.com`, and see the token injected only as
  `Authorization: Bearer`.
- With an API key stored, send the same requests, and see the key
  injected only as `x-api-key`.
- Send requests to `/mcp-registry/v0/servers` and `/api/hello`, and see
  them allowed with nothing injected.

### The event log

Spec: S07-egress-gateway, "model API".

- With a global policy that allows `api.anthropic.com`
  `/api/event_logging/`, and with one that allows `/api/`, send a
  request to `/api/event_logging/`, and see it refused with the rule
  `model-event-log`.
- Send `/api/%65vent_logging/`, `/api%2Fevent_logging/` and
  `/api%2fevent_logging/`, and see each refused with the rule
  `model-path-escape`.

### Claude Code end to end

- Run `wb claude -p`, `wb claude -p --resume` and an interactive `wb
  claude` with the model API profile, and see each answer. In the audit
  log, see no request to `/api/event_logging/`, and no DNS lookup and
  no approval request for Datadog's log intake, `downloads.claude.ai`
  and `raw.githubusercontent.com`. That shows the guest's variables
  stopped them (S06-vm-lifecycle, "Claude Code in the guest").
- Run them again with `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` and
  `DISABLE_UPDATES` unset and every approval request denied. See the
  event log refused with the rule `model-event-log`, the hosts outside
  the profile refused as unknown destinations, and Claude Code still
  answer (B60-model-credential).

**Status:** Draft
