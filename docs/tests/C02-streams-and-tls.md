# C02 - Streams, TLS, and the Inspection CA

**Purpose:** The test cases for the streams `wb-proxyd` takes over:
the hand-off from `wb-netd`, the first-byte and TLS checks, upstream
addresses, the HTTP request form, and the inspection CA's leaves,
rotation, and keys.

**Verifies:** S07-05-stream-hand-off, S07-15-non-http-streams,
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
- one with no descriptor, one with two, and one with three, which cuts
  the control data (`MSG_CTRUNC`);
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

Each is refused and logged with its rule and check, and `wb-proxyd`
doesn't resolve a name upstream, open an upstream connection or fetch a
binding. The malformed ones, a name not in canonical form and an
address outside the range, also close VM A's hand-off socket, and
`wb-hostd` ends VM A's `wb-netd` and counts the exit.

- Report an address held for another name twice, once with no stream
  open and once with a stream to it open, and see the old entry removed
  and the open stream closed.
- Replay the live entries, and see the copy and the wildcard count
  unchanged.
- Block the accounting record of a wildcard name for more than 100 ms,
  and see the name refused.
- From `wb-hostd`, send hand-off messages with an older generation, a
  VM that isn't a VM slot, and a datagram with two descriptors, and see
  each refused. An equal generation replaces the old socket and closes
  its streams.
- Change the effective policy, and see it reach `wb-netd` only after
  `wb-proxyd` has acknowledged it.

### Leaf issuance

Spec: S09-06-ca-keys.

- Ask `wb-proxyd` for a leaf for a host the policy doesn't inspect, a
  host in pass mode, a stream whose SNI differs from its hostname, a
  hostname on another VM's stream, and a VM whose only CA has expired.
  In every case `wb-proxyd` doesn't sign a leaf. The pass-mode stream
  is relayed unchanged, and each other stream is reset and logged with
  its rule.

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

### CA removal list

Spec: S09-07-ca-certificates, "Lifetimes".

- Hand `wb-proxyd` batches with `binding/x`, `wbadmin/build/x`, a name
  with a part outside the fixed form, and an entry whose CA is still in
  the record, each next to a valid entry. See each batch refused with
  `ca-removal-refused` and nothing deleted. Then see `wb-hostd` drop the
  refused entry and log it, and the next batch delete the valid one.
- Report an entry whose item is gone, and see it counted as deleted.

## Fuzzing

- TLS ClientHello parsing, a ClientHello split across TLS records
  included.
- The first-byte protocol check of S07-15-non-http-streams.
- HTTP/1.1 and HTTP/2 request handling, and request path
  canonicalization.
- The name reports, releases and stream messages `wb-proxyd` reads from
  `wb-netd`, and the hand-off messages it reads from `wb-hostd`
  (S07-05-stream-hand-off).

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
