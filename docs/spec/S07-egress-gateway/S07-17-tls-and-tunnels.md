# S07-17 - TLS and Tunnels

**Purpose:** What `wb-proxyd` does with client certificates, encrypted
ClientHello and tunnels, and which refusals no mode or approval
relaxes.

## Design

The protocol check on each stream is on S07-10-non-http-streams.

### TLS with a client certificate

Wraith Box doesn't support client certificates in V1. A client
certificate needs its private key, and the guest can't hold one
(SEC04-no-guest-secrets).

- *Inspect.* `wb-proxyd` doesn't ask the guest for a certificate, and
  never presents one upstream. The upstream server can ask for one,
  and the handshake then fail. The guest's request then gets a `502`
  with the rule `tls-client-cert-unsupported`. The audit entry names
  the host (NFR06-explained-refusals).
- *Pass and raw TCP.* `wb-proxyd` can't see a client certificate in a
  pass or raw relay, because TLS 1.3 encrypts it. A key the guest
  holds can reach such a host. That key is a credential the guest
  brought, which Wraith Box neither gave nor removes
  (T10-unnamed-credentials).

### Encrypted ClientHello

ECH hides the name a client wants inside an encrypted part of the
ClientHello, under a public outer name. `wb-proxyd` binds every TLS
stream to the outer SNI (S07-05-hosts-and-modes, "Name binding").
These rules keep the inner name from reaching a host the guest isn't
allowed.

#### DNS

`wb-netd` answers `HTTPS` and `SVCB` queries without a record
(S07-03-dns). Those records hold ECH configurations. A client that
looks up its keys in DNS doesn't learn an ECH key, so it doesn't try
ECH.

That is a compatibility measure. It doesn't stop a guest that brings
its own ECH configuration. The next two rules do.

#### Inspect

`wb-proxyd` terminates TLS, and doesn't implement ECH as a server. So
it completes the handshake for the outer ClientHello, with a
certificate for the outer name.

- A client that sent a real ECH extension then aborts with an
  `ech_required` alert, as the ECH RFC (RFC 9849) requires. The stream
  fails closed. `wb-proxyd` logs that alert with the rule
  `tls-ech-refused`.
- A client that sends a GREASE ECH extension, which lacks an inner
  name, continues as usual.

#### Pass

In a pass relay, the inner name would reach the upstream front end
unread. `wb-proxyd` can't tell a real ECH extension from a GREASE one.

So it parses the client's handshake records until the server sends a
ServerHello that isn't a HelloRetryRequest. That includes a second
ClientHello after a HelloRetryRequest, whose SNI must equal the bound
host too.

Before that ServerHello, the client's records are handled like this:

- **Other records.** A handshake record that isn't a ClientHello, or
  an application data record, resets the stream with the rule
  `stream-protocol-mismatch`.
- **Early data.** TLS 1.3 early data (0-RTT) is sent as application
  data before the ServerHello. The rule above resets it. A
  ClientHello with the `early_data` extension is reset with the same
  rule, before any early data. The audit entry says early data isn't
  allowed to a pass host (NFR06-explained-refusals).
- **Change Cipher Spec.** One Change Cipher Spec record, holding one
  byte `0x01`, is relayed. A TLS 1.3 client in its compatibility mode
  for network middleboxes (RFC 8446) sends one before its second
  ClientHello. A second one, or one with other content, resets the
  stream with `stream-protocol-mismatch`.
- **Alerts.** An alert record closes the stream in both directions
  without relaying it. The audit entry records the alert's level and
  description, with the rule `tls-client-alert`.

A ClientHello to a pass host is reset when it holds:

- an `encrypted_client_hello` extension (`0xfe0d`), or the earlier
  encrypted SNI extension (`0xffce`), with the rule `tls-ech-pass`;
- any other extension outside a list fixed in code, with the rule
  `tls-extension-unknown` (fail closed). The list holds the extensions
  `wb-proxyd` recognizes, and the GREASE values of RFC 8701.

A client that sends GREASE ECH by default can't reach a pass host
until it is configured not to.

### `CONNECT` and other tunnels

These are refused with a `403` and the rule `http-tunnel`, whatever
policy allows:

- a request with the method `CONNECT`;
- a request with an `Upgrade` header for any protocol other than
  WebSocket.

That holds on every inspected stream, inspected TLS and plain HTTP
alike. A policy rule that names `CONNECT` is refused at load.

The bytes after a `200` would reach whatever host the upstream proxy
picks, outside every rule of this spec (SEC05-default-deny).

On HTTP/2, `wb-proxyd` doesn't offer extended `CONNECT` (RFC 8441). A
client then opens a WebSocket over HTTP/1.1. That is under the host's
HTTP policy, and S07-11-approvals-and-learning, "What an approval
grants".

A proxy that runs inside the guest doesn't need an exception. The
connections it opens are ordinary streams to synthetic addresses,
checked as above. No tool needs proxy settings (FR08-no-proxy-config).

### Not relaxed by audit mode or approvals

These apply on a host in `audit` mode too (S07-07-http-policy):

- the `http-tunnel` refusal;
- the `tls-*` refusals;
- the `stream-*` resets;
- `http-header-timeout`, `http-header-size` and
  `http-transfer-coding`;
- the placeholder refusals `placeholder-outside-binding`,
  `model-placeholder-in-body` and `model-content-encoding`;
- `upstream-address-refused`.

A `CONNECT` request, or an `Upgrade` other than WebSocket, never
raises an approval request.

**Status:** Draft
