# S07-10 - Non-HTTP Streams

**Purpose:** How `wb-proxyd` checks the protocol of each stream, and
what it does with SSH, other protocols and raw TCP.

## Design

A guest can send SSH, or any other protocol, to port 443 of an allowed
host. So `wb-proxyd` checks the first bytes of each stream against the
protocol policy expects on that host and port. It resets a stream that
is neither TLS nor HTTP, unless policy names that host and port for
raw TCP. SEC05-default-deny and SEC06-repo-writes hold as written.

Client certificates, ECH and tunnels are on S07-17-tls-and-tunnels.

### One protocol per host and port

Each host and port that the effective policy allows
(S07-01-per-vm-policy) expects one protocol:

| Port | Protocol |
|---|---|
| 80, when policy names `host:80` | plain HTTP, inspected (S07-05-hosts-and-modes, "Modes, per host") |
| a port that a raw TCP entry names ("Raw TCP entries") | raw TCP |
| every other allowed port | TLS, inspected or passed by the host's mode |

A policy file whose rules give one host and port two of these is
refused at load. In the union it is a conflict that refuses the
joining session (S07-01-per-vm-policy, "Conflicts refuse the joining
session"). Both errors name the host, the port, and both sources
(NFR06-explained-refusals).

TCP to a port that policy doesn't name for the host is reset by
`wb-netd`, with the rule `port-not-allowed` (S07-02-packet-path,
"Connections").

### First bytes

On a TLS or plain HTTP port, `wb-proxyd` reads the first bytes of the
stream before it resolves or connects upstream. So no byte of a
refused stream leaves the host.

- **On a TLS port** the stream must start with a TLS handshake record
  that holds a ClientHello. It is read by the ClientHello parser of
  S07-05-hosts-and-modes, "Name binding". The parser reassembles a
  ClientHello split across more than one record.
- **On port 80** the stream must start with an HTTP/1.1 or HTTP/1.0
  request line. An HTTP/2 connection preface is reset, because
  S07-06-requests-and-credentials ("Request form") parses HTTP/2 only
  inside TLS.
- **The byte cap.** The check reads at most a byte count fixed in code
  (SEC13-bounded-resources). A stream that doesn't match by then, or
  whose bytes are another protocol, is reset with the rule
  `stream-protocol-mismatch`.
- **The deadline.** A deadline fixed in code runs from the moment
  `wb-netd` hands the stream over to the decision. A stream that isn't
  decided by then is reset with the rule `stream-first-bytes-timeout`.
  That holds whether its client didn't send or sent too slowly.

In a server-first protocol, such as SMTP or MySQL, the client waits
for the server's greeting, which never comes. Without the deadline,
the stream would hang instead of failing with a reason.

#### Logging a reset

Each reset is a refusal in the audit log (SEC10-audit). It holds the
VM, the host, the port, the rule, and the protocol the first bytes
looked like: `ssh`, `tls`, `http`, `http2` or `unknown`. It never
holds the bytes themselves.

- Resets and refusals are rate-limited per rule, like the other events
  the guest can trigger (S07-02-packet-path, "Events and rate
  limits").
- `wb policy explain` shows the refusal with its rule, and the policy
  change that would allow it, if any (NFR06-explained-refusals).
- The guest sees a TCP reset.

### Inside TLS on an inspected host

`wb-proxyd` offers only the ALPN protocols `h2` and `http/1.1`. A
ClientHello whose ALPN list is non-empty and names neither is reset
with `stream-protocol-mismatch`, before the handshake completes.

- **After the handshake** the bytes must start with an HTTP/1.1
  request line, or the HTTP/2 connection preface. The byte cap
  applies, and a deadline runs from the hand-off, as on a plain port.
  Only then does `wb-proxyd` resolve and connect upstream.
- **Each later request** on the stream has a deadline fixed in code,
  from its first byte to the end of its headers. A request past it
  ends the stream with the rule `http-header-timeout`
  (SEC13-bounded-resources).
- **Protocol switches.** An upstream `101 Switching Protocols` answer
  resets the stream with the rule `http-tunnel`. The exception is a
  WebSocket upgrade that the host's policy allowed.

### SSH

SSH to a git forge is refused.

SSH reaches a host only through a raw TCP entry. Every other allowed
port expects TLS or HTTP, and SSH fails the first-byte check there.

These are guarded hosts, which can't have a raw TCP entry ("Raw TCP
entries"):

- a forge that a built-in kind or a project's remote names;
- the SSH hosts of the built-in kinds, such as `ssh.github.com`
  (S07-07-http-policy, "git hosting").

The guest doesn't need SSH:

- It has no SSH keys (SEC04-no-guest-secrets).
- The guest's clone gets each SSH remote rewritten to
  `https://host/path` (S08-01-into-the-guest, "Remote URLs").

So every push to a forge that a built-in kind or a project's remote
names goes over HTTPS through the git hosting profile.
SEC06-repo-writes holds for it.

A forge that nothing names, reached through a raw TCP entry the user
added, is a T01-allowed-channels channel like any raw TCP entry.

### Other protocols on allowed ports

A database protocol, SMTP, or any other protocol sent to a TLS or HTTP
port is reset as above.

A protocol that starts in plain text and switches to TLS is reset too.
Examples are PostgreSQL's `SSLRequest` and SMTP's `STARTTLS`.

A user who needs such a host adds a raw TCP entry.

### Raw TCP entries

A raw TCP entry names one host and one port. The entry allows that
host and port by itself.

- The host doesn't need another rule. Its name resolves for the guest
  once an entry names it.
- `wb-proxyd` resolves the host itself. It connects to that port under
  S07-05-hosts-and-modes, "Upstream address".
- It relays bytes both ways unchanged, with no first-byte check. A
  server-first protocol works there.

An entry is in the same class as a pass host, with the same
restrictions (S07-05-hosts-and-modes, "pass"):

- *One named host.* The entry names one exact host and one port. A
  wildcard host, an IP literal (S07-03-dns, "IP literals") or a port
  range is refused at load. The hostname comes from the synthetic
  address, which is the only name binding a raw stream has.
- *Only the user sets it.* Only the global policy or a project's
  network policy on the host adds one (S09-policy-credentials-audit).
  Repository configuration, an approval, `wb allow`, and learn mode
  never do (SEC09-host-policy, S07-11-approvals-and-learning).
- *A reason for each.* An entry without a reason is refused at load.
  `wb policy explain` lists every raw TCP entry with its host, port,
  file and reason.
- *Never on a guarded host.* A guarded host can't have a raw TCP entry
  on any port (S07-05-hosts-and-modes, "Guarded hosts"). The same
  checks as for a pass host apply: at load, at every recompute over
  the union, and when a binding is added later. So git hosting, the
  model API and the package registries are reached only over
  inspected TLS (SEC06-repo-writes, SEC07-dep-gate).
- *Audited.* Each policy change that adds a raw TCP entry is a Device
  Config State Change (5019) event, with the host, the port, the file
  and the reason (SEC10-audit).
- *Residual channel.* `wb-proxyd` can't read the bytes of a raw
  stream. So each entry is a T01-allowed-channels channel, as a pass
  host is.

S07-01-per-vm-policy lists the raw TCP cases of a conflict, of a
change while sessions run, and of a session end. S07-01-per-vm-policy,
"Open streams" closes a raw relay whose entry is gone.

## Decisions

### Check the first bytes of every stream

`wb-proxyd` resets a stream that is neither TLS nor HTTP, unless
policy names its host and port for raw TCP.

- **Why:** a guest can send SSH, or any other protocol, to port 443 of
  an allowed host.
- **Decided:** I47 (B47-non-http-streams).

**Status:** Draft
