# S07-10 - Non-HTTP Streams

**Purpose:** How `wb-proxyd` checks the protocol of each stream, and what it does
with SSH, raw TCP, client certificates, ECH and tunnels.

## Design

A guest can send SSH, or any other protocol, to port 443 of an
allowed host. So `wb-proxyd` checks the first bytes of each stream
against the protocol policy expects on that host and port. It resets
a stream that is neither TLS nor HTTP, unless policy names that host
and port for raw TCP. The maintainer decided this on I47
(B47-non-http-streams). SEC05-default-deny and SEC06-repo-writes hold
as written.

### One protocol per host and port

Each host and port that the
effective policy allows (S07-01-per-vm-policy) expects one protocol:
- port 80, when policy names `host:80`: plain HTTP, inspected
  (S07-05-hosts-and-modes, "Modes, per host");
- a port that a raw TCP entry names (below): raw TCP;
- every other allowed port: TLS, inspected or passed by the host's
  mode.

A policy file whose rules give one host and port two of these is
refused at load. In the union it is a conflict that refuses the
joining session (S07-01-per-vm-policy, "Conflicts refuse the joining session"). Both
errors name the host, the port, and both sources
(NFR06-explained-refusals). TCP to a port that policy doesn't name
for the host is reset by `wb-netd` with the rule `port-not-allowed`
(S07-02-packet-path, "Connections").

### First bytes

On a TLS or plain HTTP port, `wb-proxyd` reads the
first bytes of the stream before it resolves or connects upstream,
so no byte of a refused stream leaves the host:
- On a TLS port the stream must start with a TLS handshake record
  that holds a ClientHello, read by the ClientHello parser of
  S07-05-hosts-and-modes, "Name binding". The parser reassembles a ClientHello split across
  more than one record.
- On port 80 it must start with an HTTP/1.1 or HTTP/1.0 request
  line. An HTTP/2 connection preface is reset, because S07-06-requests-and-credentials, "Request form" parses HTTP/2 only inside TLS.
- The check reads at most a byte count fixed in code
  (SEC13-bounded-resources). A stream that doesn't match by then, or
  whose bytes are another protocol, is reset with the rule
  `stream-protocol-mismatch`.
- A deadline fixed in code runs from the moment `wb-netd` hands the
  stream over to the decision. A stream that isn't decided by then
  is reset with the rule `stream-first-bytes-timeout`, whether its
  client didn't send or sent too slowly. In a server-first
  protocol, such as SMTP or MySQL, the client waits for the server's
  greeting, which never comes. Without the deadline the stream would
  hang instead of failing with a reason.

Each reset is a refusal in the audit log (SEC10-audit) with the VM,
the host, the port, the rule and the protocol the first bytes looked
like (`ssh`, `tls`, `http`, `http2` or `unknown`), never the bytes
themselves. Resets and refusals are rate-limited per rule like the
other events the guest can trigger (S07-02-packet-path, "Events and rate limits"). `wb policy explain` shows the refusal with its rule and
the policy change that would allow it, if any
(NFR06-explained-refusals). The guest sees a TCP reset.

### Inside TLS on an inspected host

`wb-proxyd` offers only the
ALPN protocols `h2` and `http/1.1`. A ClientHello whose ALPN list is
non-empty and names neither is reset with `stream-protocol-mismatch`
before the handshake completes.
- After the handshake, the bytes must start with an HTTP/1.1 request
  line or the HTTP/2 connection preface. The byte cap applies, and a
  deadline runs from the hand-off, as on a plain port. Only then
  does `wb-proxyd` resolve and connect upstream.
- Each later request on the stream has a deadline fixed in code from
  its first byte to the end of its headers. A request past it ends
  the stream with the rule `http-header-timeout`
  (SEC13-bounded-resources).
- An upstream `101 Switching Protocols` answer to anything but a
  WebSocket upgrade the host's policy allowed resets the stream with
  the rule `http-tunnel`.

### SSH

SSH to a git forge is refused. SSH reaches a host only
through a raw TCP entry, because every other allowed port expects
TLS or HTTP and SSH fails the first-byte check there. A forge that a
built-in kind or a project's remote names is a guarded host, and so
are the SSH hosts of the built-in kinds, such as `ssh.github.com`
(S07-07-http-policy, "git hosting"). A guarded host can't have a raw TCP
entry (below). The guest doesn't need SSH. It has no SSH keys
(SEC04-no-guest-secrets), and the guest's clone gets each SSH remote
rewritten to `https://host/path` (S08-01-into-the-guest, "Remote URLs"). So every push to a forge that a built-in kind or a project's
remote names goes over HTTPS through the git hosting profile, and
SEC06-repo-writes holds for it. A forge that nothing names, reached
through a raw TCP entry the user added, is a T01-allowed-channels
channel like any raw TCP entry.

### Other protocols on allowed ports

A database protocol, SMTP, or
any other protocol sent to a TLS or HTTP port is reset as above. A
protocol that starts in plain text and switches to TLS, such as
PostgreSQL's `SSLRequest` or SMTP's `STARTTLS`, is reset too. A user
who needs such a host adds a raw TCP entry.

### Raw TCP entries

A raw TCP entry names one host and one port.
The entry allows that host and port by itself. The host doesn't
need another rule, and its name resolves for the guest once an entry names
it. `wb-proxyd` resolves the host itself, connects to that port
under S07-05-hosts-and-modes, "Upstream address", and relays bytes both ways unchanged, with
no first-byte check. A server-first protocol works there. An entry
is in the same class as a pass host, with the same restrictions
(S07-05-hosts-and-modes, "pass"):
- *One named host.* The entry names one exact host and one port. A
  wildcard host, an IP literal (S07-03-dns, "IP literals")
  or a port range is refused at load. The hostname comes from the
  synthetic address, which is the only name binding a raw stream
  has.
- *Only the user sets it.* Only the global policy or a project's
  network policy on the host adds one (S09-policy-credentials-audit).
  Repository configuration, an approval, `wb allow`, and learn mode
  never do (SEC09-host-policy, "Approvals and learning").
- *A reason for each.* An entry without a reason is refused at load.
  `wb policy explain` lists every raw TCP entry with its host, port,
  file and reason.
- *Never on a guarded host.* A host with a credential binding, or one
  a built-in profile covers, can't have a raw TCP entry on any port.
  The same checks as for a pass host apply: at load, at every
  recompute over the union, and when a binding is added later. So
  git hosting, the model API and the package registries are reached
  only over inspected TLS (SEC06-repo-writes, SEC07-dep-gate).
- *Audited.* Each policy change that adds a raw TCP entry is a
  Device Config State Change (5019) event with the host, the port,
  the file and the reason (SEC10-audit).
- *Residual channel.* `wb-proxyd` can't read the bytes of a raw
  stream, so each entry is a T01-allowed-channels channel, as a pass
  host is.

S07-01-per-vm-policy lists the raw TCP cases of a conflict, of a change
while sessions run, and of a session end. S07-01-per-vm-policy, "Open streams" closes a
raw relay whose entry is gone.

### TLS with a client certificate

Wraith Box doesn't support client
certificates in V1. A client certificate needs its private key, and
the guest can't hold one (SEC04-no-guest-secrets).
- *Inspect.* `wb-proxyd` doesn't ask the guest for a certificate, and
  never presents one upstream. When the upstream server asks for one
  and the handshake then fails, the guest's request gets a `502` with
  the rule `tls-client-cert-unsupported`, and the audit entry names
  the host (NFR06-explained-refusals).
- *Pass and raw TCP.* `wb-proxyd` can't see a client certificate in
  a pass or raw relay, because TLS 1.3 encrypts it. A key the guest
  holds can reach such a host. That key is a credential the guest
  brought, which Wraith Box neither gave nor removes
  (T10-unnamed-credentials).

### Encrypted ClientHello (ECH)

ECH hides the name a client wants
inside an encrypted part of the ClientHello, under a public outer
name. `wb-proxyd` binds every TLS stream to the outer SNI (S07-05-hosts-and-modes, "Name binding"), and these rules keep the inner name from reaching a host
the guest isn't allowed:
- *DNS.* `wb-netd` answers `HTTPS` and `SVCB` queries without a
  record (S07-03-dns). Those records hold ECH
  configurations. A client that looks up its keys in DNS doesn't
  learn an ECH key, so it doesn't try ECH. That is a compatibility
  measure, and it doesn't stop a guest that brings its own ECH
  configuration. The next two rules do.
- *Inspect.* `wb-proxyd` terminates TLS and doesn't implement ECH as
  a server, so it completes the handshake for the outer ClientHello
  with a certificate for the outer name. A client that sent a real
  ECH extension then aborts with an `ech_required` alert, as the ECH
  RFC (RFC 9849) requires, and the stream fails closed. `wb-proxyd`
  logs that alert with the rule `tls-ech-refused`. A client that
  sends a GREASE ECH extension, which lacks an inner name, continues
  as usual.
- *Pass.* In a pass relay the inner name would reach the upstream
  front end unread, and `wb-proxyd` can't tell a real ECH extension
  from a GREASE one. So it parses the client's handshake records
  until the server sends a ServerHello that isn't a
  HelloRetryRequest. That includes a second ClientHello after a
  HelloRetryRequest, whose SNI must equal the bound host too. Before
  that ServerHello, the client's records are handled like this:
  - A handshake record that isn't a ClientHello, or an application
    data record, resets the stream with the rule
    `stream-protocol-mismatch`. TLS 1.3 early data (0-RTT) is sent
    as application data before the ServerHello, so this rule resets
    it. A ClientHello with the `early_data` extension is reset with
    the same rule before any early data, and the audit entry says
    early data isn't allowed to a pass host
    (NFR06-explained-refusals).
  - One Change Cipher Spec record, holding one byte `0x01`,
    is relayed, because a TLS 1.3 client in its compatibility mode
    for network middleboxes (RFC 8446) sends one before its second ClientHello. A second one, or
    one with other content, resets the stream with
    `stream-protocol-mismatch`.
  - An alert record closes the stream in both directions without
    relaying it, and the audit entry records the alert's level and
    description with the rule `tls-client-alert`.

  A ClientHello to a pass host is reset when it holds:
  - an `encrypted_client_hello` extension (`0xfe0d`) or the
    earlier encrypted SNI extension (`0xffce`), with the rule
    `tls-ech-pass`;
  - any other extension outside a list fixed in code of the
    extensions `wb-proxyd` recognizes and the GREASE values of
    RFC 8701, with the rule `tls-extension-unknown` (fail closed).

  A client that sends GREASE ECH by default can't reach a pass host
  until it is configured not to.

### `CONNECT` and other tunnels

On every inspected stream, inspected
TLS and plain HTTP alike, a request with the method `CONNECT`, or
with an `Upgrade` header for any protocol other than WebSocket, is
refused with a `403` and the rule `http-tunnel`, whatever policy
allows. A policy rule that names `CONNECT` is refused at load. The
bytes after a `200` would reach whatever host the upstream proxy
picks, outside every rule of this spec (SEC05-default-deny). On
HTTP/2, `wb-proxyd` doesn't offer extended `CONNECT` (RFC 8441). A
client then opens a WebSocket over HTTP/1.1, under the host's HTTP
policy and S07-11-approvals-and-learning, "What an approval grants". A proxy that runs inside the
guest doesn't need an exception. The connections it opens are
ordinary streams to synthetic addresses, checked as above, and no
tool needs proxy settings (FR08-no-proxy-config).

### Not relaxed by audit mode or approvals

The `http-tunnel`
refusal, the `tls-*` refusals, the `stream-*` resets,
`http-header-timeout`, `http-header-size`, `http-transfer-coding`,
the placeholder refusals `placeholder-outside-binding`,
`model-placeholder-in-body` and `model-content-encoding`, and
`upstream-address-refused` apply on a host in `audit` mode too
(S07-07-http-policy). A `CONNECT` request or an `Upgrade` other than
WebSocket never raises an approval request.

**Status:** Draft
