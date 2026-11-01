# S07-07 - Hosts and Modes

**Purpose:** How `wb-proxyd` binds a stream to its name, which upstream
addresses it refuses, and the inspect, pass and audit modes.

## Design

### Name binding

The hostname of a stream is the name `wb-proxyd`'s copy holds for the
synthetic address (S07-06-name-mapping).

- For TLS, the ClientHello SNI must equal that hostname, or the stream
  is reset.
- For inspected plain HTTP, the `Host` header must equal that hostname
  (S07-03-dns, "Synthetic pool").

`wb-proxyd` resolves the real upstream address itself.

### Upstream address

This rule serves SEC05-default-deny. It applies to each connection
`wb-proxyd` opens:

- upstream streams in every mode: inspect, pass and raw TCP;
- its own fetches, such as following a Go module proxy redirect, the
  `ghcr.io` token fallback and OSV lookups.

`wb-proxyd` refuses the connection when any address the name resolves
to is in one of these ranges:

- **IPv4:**
  - unspecified and "this network" (0.0.0.0/8);
  - loopback (127.0.0.0/8);
  - RFC 1918 (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16);
  - CGNAT (100.64.0.0/10);
  - link-local (169.254.0.0/16);
  - IETF protocol assignments (192.0.0.0/24);
  - multicast (224.0.0.0/4);
  - reserved (240.0.0.0/4, broadcast included);
  - the synthetic range (198.18.0.0/15).
- **IPv6:** unspecified (`::`), loopback (`::1`), link-local
  (`fe80::/10`), unique local (`fc00::/7`) and multicast (`ff00::/8`).
- **An IPv4 address embedded in IPv6:**
  - IPv4-mapped (`::ffff:0:0/96`) and IPv4-compatible (`::/96`)
    addresses;
  - the NAT64 prefixes (`64:ff9b::/96`, `64:ff9b:1::/48`);
  - 6to4 (`2002::/16`) and Teredo (`2001::/32`). The whole 6to4 and
    Teredo prefixes are refused, whatever address they embed.
- **The host's own networks, at connect time:**
  - any address assigned to a network interface of the host;
  - any address in the on-link prefix of one of the host's
    interfaces.

  This covers the host's own public addresses, and a LAN that uses
  global IPv6 addresses.

`wb-proxyd` connects only to an address it checked. It never resolves
the name again between the check and the connect.

A refused connection is logged with the host, the address, and the
rule `upstream-address-refused` (SEC10-audit). A lookup that fails,
or returns an empty answer, is refused with `upstream-resolve-failed`. When none
of the checked addresses connects, the rule is
`upstream-connect-failed`. Both are logged with the host, and the
second with the last address tried.

- On an inspected stream the guest gets a `502` that names the rule.
- A pass or raw TCP stream is reset (NFR06-explained-refusals).

So a name whose zone someone else controls can't point a relay at the
host itself, at a private or link-local network, or at a network on
the host's own links.

A network on public addresses that the host reaches through another
route, such as a VPN, isn't in these ranges. It is still reachable by
name (T01-allowed-channels). Wraith Box has no LAN opt-in in V1.

### Modes, per host

- Plain HTTP is allowed only when policy names `host:80`. It is always
  inspected.
- A stream that isn't TLS or HTTP is reset, unless policy names its
  host and port for raw TCP (S07-15-non-http-streams).

A TLS host has one of three modes.

#### inspect

`inspect` is the default.

1. `wb-proxyd` terminates TLS with a leaf certificate from the Wraith
   Box CA (S09-policy-credentials-audit).
2. It applies HTTP policy and credential replacement.
3. It opens a new TLS connection upstream, validated against the
   host's system trust store.

It speaks HTTP/1.1 and HTTP/2.

#### pass

`pass` relays TLS bytes unchanged to the named host. It is only for
hosts where inspection breaks the client.

The proxy can't see what is sent. So every pass host is a residual
channel (T01-allowed-channels).

The rules for pass:

- *Still one named host.* Pass isn't an exception to
  SEC05-default-deny. The name must be allowed, and the SNI must equal
  it ("Name binding"). A pass entry names one exact host. A wildcard
  pass entry is refused at load.
- *Only the user sets it.* A host is put in pass mode only in the
  global policy, or in a project's network policy on the host
  (S09-policy-credentials-audit). Repository-supplied configuration
  can never switch a host to pass (SEC09-host-policy). An approval or
  learn mode never does either (S07-17-approvals-and-learning).
- *A reason for each.* Each pass entry holds a reason the user writes.
  An entry without one is refused at load. `wb policy explain` lists
  every pass host with its file and its reason.
- *Never on a guarded host.* A guarded host can't be a pass host
  ("Guarded hosts").
- *Audited.* Each policy change that adds a pass host is a Device
  Config State Change (5019) event, with the host, the file and the
  reason (SEC10-audit).

#### Known to break under inspection

None of the clients X04-tls-inspection could test pins a certificate.

The guest's trustd pinning rules pin Apple hosts a development guest
may use. So these are expected to refuse an inspected leaf (inferred
from the rules, not seen):

- software update and the command line tools: `swscan.apple.com`,
  `swcdn.apple.com`, `swdist.apple.com`, `swdownload.apple.com`;
- `xcode-cdn.apple.com`, `mesu.apple.com`, `updates.cdn-apple.com`;
- `gdmf.apple.com`, `gs.apple.com`, `gsa.apple.com` and
  `idmsa.apple.com`.

Wraith Box never puts these hosts in pass mode itself, and the rules
above apply to them. A user who does makes each one a
T01-allowed-channels channel. The Apple Account hosts
(`idmsa.apple.com`, `gsa.apple.com`) then carry whatever Apple
credentials the guest holds. Wraith Box neither gave nor removes them
(T10-unnamed-credentials).

On a macOS guest, clients that read only the system trust store, such
as URLSession, also reject inspection. That is a known gap of
FR08-no-proxy-config, which S09-08-guest-trust lists.

#### audit

`audit` inspects, with the HTTP policy of the host and port logging a
violation and letting the request through (S07-11-http-policy).

- The mode is per host and port, resolved over the whole union.
- `enforce` wins when any source has an `enforce` endpoint on that
  host and port (S09-02-precedence, "Mode per host and port").
- The boundary check sees an `audit` host and port as every method and
  path on it (S09-01-policy-files, "Audit as the whole host").

The rules for audit:

- *Only the user sets it.* Only the global policy and a project's
  network policy can put a host and port in `audit`. Repository
  configuration and approvals can't (S09-03-project-settings, "Repository-supplied configuration", and S09-09-approvals, "Approved rule").
- *Never on a guarded host.* A guarded host can't be in `audit`
  ("Guarded hosts").

### Guarded hosts

A host is guarded when it has a credential binding, or a built-in
profile covers it. A profile covers a host when:

- any host of a built-in profile (git hosting, model API, package
  registries) matches it;
- or the remotes of a project's host repository name it, which gives
  it the git hosting profile (S07-08-requests-and-credentials, "Named places").

Neither pass nor `audit` is allowed on a guarded host:

- Pass would switch off credential replacement, HTTP policy and the
  dependency gate on it, and with them SEC06-repo-writes and
  SEC07-dep-gate.
- With policy not enforced in `audit`, a write the git hosting profile
  refuses would pass with a credential injected, against
  SEC06-repo-writes.

The rule is checked at three points. Each error names what it says
below (NFR06-explained-refusals).

- *At load.* A pass entry or an `audit` endpoint on a guarded host, in
  the global policy or a project's policy, is refused at load. The
  error names the host and the reason.
- *At every recompute.* `wb-hostd` checks the same rule over the
  union, whatever source set the mode, global entries included. So a
  global entry that loaded while no project named its host refuses
  the session start of a project whose remote names it. The error
  names that project, the host, and the file of the pass entry or
  `audit` endpoint.
- *A binding added later.* A change that adds a credential binding on
  a pass host, or on a host with an `audit` endpoint, is refused. That
  holds in a policy file and with `wb cred set`. The pass entry or
  `audit` endpoint stays. The error names it and its file, and says to
  remove it first.

## Decisions

### Rules for pass

Pass names one exact host, only the user sets it, each entry has a
reason, it is never on a guarded host, and each new pass host is
audited.

- **Why:** the proxy can't see what a pass host is sent, so each one
  is a residual channel (T01-allowed-channels).
- **Decided:** I40 (B40-learn-pass-modes).

### Rules for audit

Only the user sets `audit`, and never on a guarded host.

- **Why:** with policy not enforced, a write the git hosting profile
  refuses would pass with a credential injected.
- **Decided:** I109.

**Status:** Draft
