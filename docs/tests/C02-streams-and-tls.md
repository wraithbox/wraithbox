# C02 - Streams, TLS, and the Inspection CA

**Purpose:** The test cases for the streams `wb-proxyd` takes over:
the hand-off from `wb-netd`, the first-byte and TLS checks, upstream
addresses, the HTTP request form, and the inspection CA's leaves,
rotation, and keys.

**Verifies:** S07-05-stream-hand-off, S07-07-hosts-and-modes
("Upstream address"), S07-15-non-http-streams,
S07-08-requests-and-credentials ("Request form"), S09-06-ca-keys ("Valid CA"),
S09-07-ca-certificates ("Lifetimes"), S09-08-guest-trust

## Unit tests

### Stream hand-off

Spec: S07-05-stream-hand-off.

Two VMs at different generations. On VM A's hand-off socket the test
sends these streams:

- one whose generation field is VM B's;
- one to an address only VM B's copy holds, and one to an address with
  no entry;
- one on a port the name's policy doesn't allow;
- one to a name that a policy change removed after its report;
- one with no descriptor, one with two, and one with three, which the
  channel refuses with `fdchan-fd-count`;
- one with a datagram socket, and one with a socket that isn't a Unix
  domain socket, as the stream;
- one sent right behind a report that waits on `wb-hostd` for the
  wildcard budget, which passes once the report is accepted;
- one in a message over 4 KiB;
- streams past the per-socket cap, while VM B's streams still pass.

On the same socket it sends name reports with:

- a name only VM B's policy allows;
- a name that fails the name form rule;
- a name with a capital letter or a trailing dot;
- an address outside the synthetic range;
- new names under a wildcard past the wildcard budget.

Each is refused and logged with its rule and check, a refused stream
gets a `StreamReset`, and the stream handler sees none of them, so
`wb-proxyd` doesn't resolve a name upstream, open an upstream
connection or fetch a binding. The malformed ones also close VM A's
hand-off socket and its streams, and `wb-proxyd` sends
`HandoffSocketClosed`: a name not in canonical form, an address outside
the range, a stream of the wrong form (no descriptor, two, three or
over 4 KiB), and a record that doesn't decode. `wb-hostd` ends VM A's
`wb-netd` and counts the exit.

- Report an address held for another name twice, once with no stream
  open and once with a stream to it open, and see the old entry removed
  and the open stream closed.
- Replay the live entries, and see the copy and the wildcard count
  unchanged.
- Block the accounting record of a wildcard name for more than 100 ms,
  and see the name refused.
- Release an address the copy doesn't hold, and see it logged with
  `release-unheld` and ignored. Release one with a stream open, and see
  the open stream left as it was and a new one refused.
- Narrow the effective policy with streams open, and see each stream it
  no longer allows reset with `policy-recomputed` before the
  `PolicyAck`.
- From `wb-hostd`, send hand-off messages with an older generation, a
  VM that isn't a VM slot, the build VM of a running image build, and a
  `SOCK_STREAM` socket, and see each refused with
  `handoff-socket-refused`. Send a datagram with two descriptors, and see
  it refused with `control-message-refused`. Each refusal ends the
  control channel. An equal generation replaces the old socket and
  closes its streams.
- Change the effective policy, and see it reach `wb-netd` only after
  `wb-proxyd` has acknowledged it.

On macOS, `internal/proxyd/handoff` runs the `wb-proxyd` cases over
socketpairs (`receiver_darwin_test.go`), and the reset that can't be
sent with a fake channel (`TestResetSendFails`).

### Upstream addresses

Spec: S07-07-hosts-and-modes, "Upstream address". The tests are in
`internal/proxyd/upstream`.

- One address inside and one just outside each range, with
  `169.254.169.254`, `192.0.0.8`, `::ffff:127.0.0.1`, `::127.0.0.1`,
  `64:ff9b::a00:1`, 6to4 and Teredo addresses that embed a public IPv4
  address, and public addresses that pass (`TestRefused`).
- With a fake reader of the host's networks, an address assigned to an
  interface, an address in an on-link IPv4 prefix, a global IPv6
  address in an on-link prefix and the IPv4-mapped form of an
  interface address are refused, and nothing is dialed
  (`TestDialHostNetworks`). The networks are read at each connect
  (`TestDialReadsNetworksEachTime`).
- With a fake resolver, a name with one bad address among good ones is
  refused, nothing is dialed, and the refusal is logged with the host,
  the address and `upstream-address-refused`
  (`TestDialRefusesOneBadAddress`). A good name is dialed at the
  checked address after one lookup (`TestDialGoodName`), and its
  addresses are tried in order (`TestDialInOrder`). A failed lookup and
  one with no address give `upstream-resolve-failed`
  (`TestDialResolveFailed`).
- A name that resolves to `127.0.0.1` reaches no listener there
  (`TestDialLoopback`).

### Stream checks

Spec: S07-15-non-http-streams, S07-07-hosts-and-modes ("Name
binding"), S07-16-tls-and-tunnels ("Pass"). The tests are in
`internal/proxyd/stream`, with a fake hand-off side for each stream
and loopback upstream servers, and in `internal/proxyd/tlsparse`. Each
reset is logged with its rule, its check and what the first bytes
looked like, and reaches the audit hook.

- Send a ClientHello whose SNI differs from the stream's name, one
  without an SNI, one with an IP address and one with a trailing dot,
  and see each reset with `tls-sni-mismatch` and nothing dialed. An
  SNI in upper case goes to the Inspector, with its bytes to read again
  (`TestC02SNIBinding`).
- Send SSH, a PostgreSQL startup message, an `SSLRequest`, a cleartext
  HTTP/2 preface and an HTTP request line to a TLS port, and an HTTP/2
  preface, a ClientHello, SSH and an HTTP/0.9 request line to port 80.
  See each reset with `stream-protocol-mismatch` and the protocol it
  looked like, and nothing dialed (`TestC02FirstBytesMismatch`).
  HTTP/1.1 and HTTP/1.0 request lines sent a byte at a time go to the
  Inspector (`TestC02PlainHTTP`). A port with no protocol is reset
  (`TestC02NoProtocol`).
- Open a stream and don't send, and send a ClientHello a byte at a
  time, and see each reset with `stream-first-bytes-timeout`
  (`TestC02FirstBytesTimeout`).
- Send a ClientHello split over three records, and a flight of exactly
  the byte cap, and see each taken. A flight one byte past the cap is
  reset (`TestC02ClientHelloRecords`).
- Send an ALPN list of only `ftp`, and see it reset. `h2`, `http/1.1`
  and no list go to the Inspector (`TestC02ALPN`). A host in audit
  goes to the Inspector in audit mode, after the same checks
  (`TestC02AuditMode`).
- To a pass host, send ClientHellos that hold an
  `encrypted_client_hello` or encrypted SNI extension
  (`tls-ech-pass`), `early_data`, an unknown extension
  (`tls-extension-unknown`), or another SNI. See each reset before any
  dial (`TestC02PassHello`).
- To a pass host, before the ServerHello, send application data, a
  handshake record that isn't a ClientHello, a second ClientHello
  without a HelloRetryRequest, a second change cipher spec, one that
  isn't the byte `0x01`, and after a HelloRetryRequest a second
  ClientHello with another SNI or with ECH. See each reset with its
  rule, and the server get only the records that passed
  (`TestC02PassRecords`). A ServerHello after a HelloRetryRequest and
  before the second ClientHello (`TestC02PassServerHelloEarly`), and a
  server's application data, second HelloRetryRequest or bytes that
  aren't TLS (`TestC02PassServerRecords`), are reset too.
- See one change cipher spec relayed, and the bytes after the
  ServerHello relayed unchanged both ways (`TestC02PassOneCCS`). See an
  alert close both ways with `tls-client-alert`, its level and
  description logged, and the alert not relayed (`TestC02PassAlert`).
- Run a full TLS 1.3 handshake of `crypto/tls`, with a
  HelloRetryRequest, through a pass relay (`TestC02PassHandshake`).
- Relay a server-first protocol through a raw TCP entry
  (`TestC02RawRelay`). A pass and a raw TCP stream to a name that
  resolves to `127.0.0.1` are reset with `upstream-address-refused`
  (`TestC02UpstreamRefused`).
- Remove the raw TCP entry of an open relay, and take an open pass
  relay's host out of the pass list, and see each relay end both ways
  with `policy-recomputed` (`TestC02PolicyRecomputed`). Each logs its
  one allowed record and no refusal of its own.
- Reset a stream from the hand-off's side while its first bytes are
  read, during its dial, and as its dial opens. See no refusal and no
  allowed record logged by the handler, and nothing reach the
  upstream server (`TestC02EndedByHandoff`).
- `tlsparse` refuses each malformed record and ClientHello with its
  check (`TestReadClientHelloRefuses`), and reads a server's records
  only to its ServerHello (`TestServerFlight`).

### Leaf issuance

Spec: S09-06-ca-keys.

- Ask `wb-proxyd` for a leaf for a host the policy doesn't inspect, a
  host in pass mode, a stream whose SNI differs from its hostname, a
  hostname on another VM's stream, and a VM whose only CA has expired.
  In every case `wb-proxyd` doesn't sign a leaf. The pass-mode stream
  is relayed unchanged, and each other stream is reset and logged with
  its rule.
- In `internal/proxyd/ca`, over the fake key store and the fake secret
  store (S09-07-ca-certificates, "Leaf issuance"):
  - Ask for a leaf for a wildcard, an IP address, a name not in
    canonical form, a VM with no CA set, an older generation, a VM
    with no signing CA, and an expired CA with a leaf cached. See each
    refused with `ca-leaf-refused` and its reason, or
    `ca-not-installed`, and logged (`TestC02LeafRefusals`).
  - See a leaf with exactly the leaf profile, a chain of the leaf only,
    and a validity cut to the CA's end, that verifies against its CA
    (`TestC02LeafProfile`).
  - See the cache return the same leaf for the same VM, CA and
    hostname, a new one an hour before its end and after a signing CA
    change, and drop the least recently used leaf at its cap
    (`TestC02LeafCache`).
  - See a leaf signed for one VM fail to verify against another VM's CA
    (`TestC02VMScope`), and the package's API give or take no
    `x509.CertPool` (`TestNoCertPool`).
  - See a VM's streams refused until `wb-hostd` accepts the CA set's
    generation, and a CA set of an older generation end the control
    channel (`TestC02AcceptGating`).
  - See `wb-proxyd`'s hand-off receiver, which has no CA handler yet,
    refuse a CA message and end the control channel
    (`TestCAMessageRefused`).

### CA rotation

On a fake clock, with each step checking its 5019 event:

- Day 30 issues and installs the successor.
- Day 58 switches only after the install is recorded, and otherwise
  refuses the VM's streams with `ca-not-installed`.
- Day 60 removes the old CA and destroys its key.
- A restore and a host wake each follow this order: clock set, expired
  CAs removed, the current CAs installed from the host's record, and
  only then streams accepted, signed with the CA `wb-proxyd` signed
  with before the save or the sleep.
- A save on day 40 and a restore on day 45 keep signing with the old
  CA.
- A host that sleeps from day 50 to day 61 removes the old CA before it
  accepts a stream.
- In `internal/hostd/carecord`, over a fake `wb-proxyd`, a fake guest
  and a fake clock, each with its 5019 events:
  - Day 30 issues the successor, sends both in the CA set and records
    the install (`TestC02RotationDay30`).
  - Day 58 switches once the install is recorded
    (`TestC02RotationDay58`), and otherwise leaves the VM without a signer and logs
    `ca-not-installed` (`TestC02RotationDay58NotInstalled`).
  - Day 60 drops the old CA and puts its item on the removal list
    (`TestC02RotationDay60`).
  - A boot runs the seven steps in order (`TestC02RotationBootOrder`).
    A restore and a wake keep the old signer
    (`TestC02RotationRestoreAndWakeKeepSigner`), also from a save on
    day 40 to a restore on day 45
    (`TestC02RotationSaveDay40RestoreDay45`).
  - A sleep from day 50 to day 61 drops the old CA before the accept
    (`TestC02RotationSleepDay50To61`).
  - An install the guest doesn't report in time refuses the VM's
    streams with `ca-install-unreported`
    (`TestC02RotationInstallUnreported`).

### CA key

Spec: S09-06-ca-keys, "Valid CA".

- Hand `wb-proxyd` a token object ID of another key, random bytes, a
  missing item, and a restore that returns an error. See each VM start
  refused with `ca-key-restore`, and no new CA issued until `wb vm
  reset-ca`.
- Fail an issue's read-back or restore, and see the record unchanged
  and its item deleted.
- Through the cgo shim, read back a new CA key and its item, and check
  `kSecAttrIsPermanent` false, `kSecAccessControlPrivateKeyUsage` as
  the only access control flag,
  `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`, and
  `kSecAttrSynchronizable` false.
- On Linux, record "software" on a host with a TPM, and "TPM" on one
  without, and see each CA issue and restore refused with
  `ca-key-store-mismatch`.
- Run `wb vm reset-ca` and `wb setup --ca-key-store`, and see each
  write a 5019 event with the command and each dropped CA's
  fingerprint.
- In `internal/proxyd/ca`, over the fakes:
  - Restore from the wrapped form of another key, a token of another
    key, random bytes, a missing item, and a restore error. See each
    CA set fail with `ca-key-restore`, the VM get no leaf, and no key
    made or left open (`TestC02RestoreFails`). A CA set whose
    certificate isn't the item's fails the same way
    (`TestC02RestoreBadCertificate`).
  - Fail an issue's read-back and restore, and see its item deleted
    (`TestC02IssueRoundTripFails`). An issue for a CA id in use, for
    either VM and also after a CA set that failed to restore, fails with
    `ca-issue-refused` and no store call (`TestC02IssueRefused`). After
    a restart, an issue for an id whose item the store holds fails with
    `ca-issue-refused` and leaves the item, and a failed write deletes
    nothing (`TestC02IssueStored`).
  - See a locked store wait, log the wait once and continue at the
    unlock (`TestC02StoreLockedWaits`), and fail closed when the wait
    ends (`TestC02StoreLockedCancel`). See an ad-hoc signed build fail
    with `keychain-needs-team` (`TestC02AdHocStore`).
  - See a new CA with exactly the CA profile (`TestC02IssueProfile`).
- In `internal/hostd/carecord`, see a restore failure refuse the start
  with `ca-key-restore` and issue nothing, and after `wb vm reset-ca`
  the next boot issue a new CA and remove the old one's item
  (`TestC02CAKeyRestoreFailed`).
- In `internal/vmcmd`, against the real service and CA record, see `wb
  vm reset-ca` refused without a terminal, on a wrong answer and while
  the VM runs, each leaving the record as it was
  (`TestResetCARefusals`). On success see the record empty, both items
  on the removal list and one 5019 event with the command
  (`TestResetCAAccepted`).

### CA removal list

Spec: S09-07-ca-certificates, "Lifetimes".

- Hand `wb-proxyd` batches with `binding/x`, `wbadmin/build/x`, a name
  with a part outside the fixed form, and an entry whose CA is still in
  the record, each next to a valid entry. See each batch refused with
  `ca-removal-refused` and nothing deleted. Then see `wb-hostd` drop an
  entry refused for its form and log it, keep one refused because its
  CA is in the record until the VM's next CA set, and the next batch
  delete the valid one.
- Report an entry whose item is gone, and see it counted as deleted.
- `internal/proxyd/ca` covers the `wb-proxyd` side: the refused
  batches, each entry before and after the valid one, an entry of
  another VM's CA set and one of a CA set that failed to restore among
  them (`TestC02RemovalRefused`), and the
  deletes, an item already gone and a failed delete
  (`TestC02RemovalDeletes`).
- `internal/hostd/carecord` covers the `wb-hostd` side: a list of 3
  entries, and one of 70 sent as batches of 64 and 6, with deleted
  entries dropped, entries refused for their form dropped and logged,
  and the rest sent next (`TestC02CARemovalList`). An entry refused
  because its CA is in the record stays, logged, and goes out after the
  VM's next CA set (`TestC02CARemovalHeldBack`).

## Fuzzing

- Upstream addresses: arbitrary 16-byte addresses, checked against a
  second reading of the ranges (`FuzzUpstreamAddress`).
- TLS ClientHello parsing, a ClientHello split across TLS records
  included: `FuzzClientHello` feeds any bytes, whole and split into
  records at any points. The parser doesn't panic, decides the same on
  the bytes cut at the cap, so it doesn't read past it, and a server
  name it accepts is in the name form. `FuzzServerFlight` checks that
  the server's records are read within the cap with at most one
  HelloRetryRequest.
- The first-byte protocol check of S07-15-non-http-streams:
  `FuzzFirstBytes` checks that a TLS port takes nothing the
  ClientHello parser refuses, that port 80 never takes the HTTP/2
  preface, and that a decision on a prefix holds for the whole.
  `FuzzPassClientRecords` checks that before the ServerHello a pass relay
  sends upstream at most one change cipher spec and, after a
  HelloRetryRequest, one ClientHello that passes the pass checks.
  `FuzzPassInterleaved` steps the relay's client and server directions
  in any order, and checks that the checks end only when no second
  ClientHello is owed.
- HTTP/1.1 and HTTP/2 request handling, and request path
  canonicalization.
- The name reports, releases and stream messages `wb-proxyd` reads from
  `wb-netd`, and the hand-off messages it reads from `wb-hostd`
  (S07-05-stream-hand-off). `FuzzNetdMessage` checks that an accepted
  message has a canonical name, an address inside 198.18.0.0/15, a
  stream's generation, id and port of at least 1, and a descriptor on a
  stream and no other message. The message encodes again within 4 KiB.
  `FuzzProxydMessage` does the same for the `StreamReset` that
  `wb-netd` reads, and `FuzzControlMessage` for `wb-hostd`'s messages.
  `FuzzHandoffDecision` drives `wb-proxyd`'s decisions on them and
  checks its copy of the name mapping after each.
- The CA messages of the control channel: `FuzzCAControlMessage` feeds
  any bytes through the decoder to `wb-proxyd`'s CA authority over the
  fakes. A message that parses has a VM slot and CA ids of the fixed
  form, and each reply has the form `control.proto` gives it.
- Leaf hostnames: `FuzzLeafName` feeds any hostname to `wb-proxyd`'s
  leaf signing over the fakes. A leaf it signs has exactly one
  `dNSName`, equal to the input and neither a wildcard nor an IP
  address. It is `CA:FALSE`, signed by the VM's CA, and valid within
  the CA's validity.
- `wb-proxyd`'s CA replies as `wb-hostd` reads them:
  `FuzzProxydCAReply` feeds any bytes to the check of a *CA issued*
  reply. It never panics, and a reply that passes has a certificate of
  the CA profile: a P-256 key, self-signed, and 60 days of validity.

## Conformance

### Stream checks

- Present an SNI that differs from the resolved name.
- Send SSH, a PostgreSQL startup message and a cleartext HTTP/2 preface
  to an allowed TLS port, and SSH to port 22 of a git forge. Open a
  stream to an allowed port and don't send. Send an HTTP `CONNECT` and
  an `Upgrade: h2c` to an inspected host. Send a ClientHello with an
  `encrypted_client_hello` extension to a pass host. See each reset or
  refused with its rule from S07-15-non-http-streams,
  and no connection opened upstream for a stream reset on its first
  bytes.
- Send a ClientHello with a real ECH configuration to an inspected
  host, and see the client abort and `tls-ech-refused` logged.
- After a HelloRetryRequest from a pass host, send a second ClientHello
  with another SNI, and see it reset.
- To a pass host, send client application data, or a handshake record
  that isn't a ClientHello, before the server's ServerHello, and see it
  reset.
- Try to add a raw TCP entry on a guarded host (`github.com`,
  `ssh.github.com`, a host with a credential binding), at load and at a
  recompute, and see it refused with the host named.

### Upstream addresses

- With an allowed name that resolves to `127.0.0.1`, an RFC 1918
  address, `169.254.169.254` or an IPv4-mapped form of one, open an
  inspected, a pass and a raw TCP stream to it, and see each refused
  with `upstream-address-refused` and no upstream connection.
- Do the same for a name that resolves to an address of one of the
  host's interfaces, to an address in an on-link prefix of the host (a
  global IPv6 LAN address included), to `192.0.0.8`, and to
  IPv4-compatible, 6to4, and Teredo addresses.
- Load a policy whose host is `127.0.0.1` or `0x7f000001`, and see it
  refused.

### Request form

Spec: S07-08-requests-and-credentials, "Request form".

- On an inspected host, send `Transfer-Encoding: gzip, chunked`,
  `chunked, chunked`, `identity`, two `Transfer-Encoding` lines,
  `Transfer-Encoding` with `Content-Length`, and any
  `Transfer-Encoding` over HTTP/2. See each get a `400` with
  `http-transfer-coding` and nothing sent upstream.
- Send a chunked body with a bad chunk size, and see the request end
  with the same rule and the upstream request aborted.
- Send a request head over 64 KiB over HTTP/1.1, and a header list over
  the announced `SETTINGS_MAX_HEADER_LIST_SIZE` over HTTP/2, and see a
  `431` with `http-header-size` and nothing sent upstream.

**Status:** Draft
