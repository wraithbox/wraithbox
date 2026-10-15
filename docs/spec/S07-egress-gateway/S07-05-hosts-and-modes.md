# S07-05 - Hosts and Modes

**Purpose:** How `wb-proxyd` binds a stream to its name, which upstream addresses
it refuses, and the inspect, pass and audit modes.

## Design

### Name binding

The hostname is the name `wb-proxyd`'s copy holds
for the synthetic address (S07-04-stream-hand-off). For
TLS, the ClientHello SNI must equal that hostname or the stream is
reset. `wb-proxyd` resolves the real upstream address itself. For
inspected plain HTTP, the `Host` header must equal that hostname
(S07-03-dns, "Synthetic pool").

### Upstream address

(SEC05-default-deny). The rule applies to each
connection `wb-proxyd` opens: upstream streams in every mode
(inspect, pass and raw TCP), and its own fetches, such as following
a Go module proxy redirect, the `ghcr.io` token fallback and OSV
lookups. `wb-proxyd` refuses the connection when any address the
name resolves to is in one of these ranges:
- IPv4: unspecified and "this network" (0.0.0.0/8), loopback
  (127.0.0.0/8), RFC 1918 (10.0.0.0/8, 172.16.0.0/12,
  192.168.0.0/16), CGNAT (100.64.0.0/10), link-local
  (169.254.0.0/16), IETF protocol assignments (192.0.0.0/24),
  multicast (224.0.0.0/4), reserved (240.0.0.0/4, broadcast
  included), and the synthetic range (198.18.0.0/15);
- IPv6: unspecified (`::`), loopback (`::1`), link-local
  (`fe80::/10`), unique local (`fc00::/7`) and multicast
  (`ff00::/8`);
- an IPv4 address embedded in IPv6: IPv4-mapped (`::ffff:0:0/96`)
  and IPv4-compatible (`::/96`) addresses, the NAT64 prefixes
  (`64:ff9b::/96`, `64:ff9b:1::/48`), 6to4 (`2002::/16`) and Teredo
  (`2001::/32`). The whole 6to4 and Teredo prefixes are refused,
  whatever address they embed;
- at connect time, any address assigned to a network interface of
  the host, and any address in the on-link prefix of one of the
  host's interfaces. This covers the host's own public addresses
  and a LAN that uses global IPv6 addresses.

`wb-proxyd` connects only to an address it checked, and never
resolves the name again between the check and the connect. A
refused connection is logged with the host, the address, and the
rule `upstream-address-refused` (SEC10-audit). On an inspected
stream the guest gets a `502` that names the rule, and a pass or
raw TCP stream is reset (NFR06-explained-refusals). So a name whose
zone someone else controls can't point a relay at the host itself,
at a private or link-local network, or at a network on the host's
own links. A network on public addresses that the host reaches
through another route, such as a VPN, isn't in these ranges and
is still reachable by name (T01-allowed-channels). Wraith Box has no
LAN opt-in in V1.

### Modes, per host

- Plain HTTP is allowed only when policy names `host:80`, and is
  always inspected.
- A stream that isn't TLS or HTTP is reset unless policy names
  its host and port for raw TCP (S07-10-non-http-streams).

#### inspect

(default): terminate TLS with a leaf certificate from the
Wraith Box CA (S09-policy-credentials-audit), apply HTTP policy and credential
replacement, then open a new TLS connection upstream validated
against the host's system trust store. HTTP/1.1 and HTTP/2.

#### pass

relay TLS bytes unchanged to the named host. Only for hosts
where inspection breaks the client. The proxy can't see what is
sent, so every pass host is a residual channel
(T01-allowed-channels). The maintainer decided its rules on I40
(B40-learn-pass-modes):
- *Still one named host.* Pass isn't an exception to
  SEC05-default-deny. The name must be allowed, and the SNI must
  equal it ("Name binding"). A pass entry names one exact host,
  and a wildcard pass entry is refused at load.
- *Only the user sets it.* A host is put in pass mode only in the
  global policy or a project's network policy on the host
  (S09-policy-credentials-audit). Repository-supplied configuration
  can never switch a host to pass (SEC09-host-policy), and an
  approval or learn mode never does either (S07-11-approvals-and-learning).
- *A reason for each.* Each pass entry holds a reason the user
  writes, and an entry without one is refused at load.
  `wb policy explain` lists every pass host with its file and its
  reason.
- *Never on a guarded host.* A host with a credential binding, or a
  host a built-in profile covers, can't be a pass host. Pass would
  switch off credential replacement, HTTP policy and the dependency
  gate on it, and with them SEC06-repo-writes and SEC07-dep-gate.
  A profile covers a host when any host of a built-in profile (git
  hosting, model API, package registries) matches it, or when the
  remotes of a project's host repository name it, which gives it
  the git hosting profile (S07-06-requests-and-credentials, "Named places").
  - *At load.* A pass entry in the global policy or a project's
    policy on a guarded host is refused at load with an error that
    names the host and the reason (NFR06-explained-refusals).
  - *At every recompute.* `wb-hostd` checks the same rule over the
    union, global pass entries included, so a global entry that
    loaded while no project named its host refuses the session
    start of a project whose remote names it. The error names
    that project, the host and the file of the pass entry
    (NFR06-explained-refusals).
  - *A binding added later.* A change that adds a credential
    binding on a pass host, in a policy file or with `wb cred set`,
    is refused, and the pass entry stays. The error names the pass
    entry and its file, and says to remove the pass entry first
    (NFR06-explained-refusals).
- *Audited.* Each policy change that adds a pass host is a Device
  Config State Change (5019) event with the host, the file and the
  reason (SEC10-audit).
- *Known to break under inspection.* None of the clients
  X04-tls-inspection could test pins a certificate. The guest's
  trustd pinning rules pin Apple hosts a development guest may use,
  so these are expected to refuse an inspected leaf (inferred from
  the rules, not seen): software
  update and the command line tools (`swscan.apple.com`,
  `swcdn.apple.com`, `swdist.apple.com`, `swdownload.apple.com`),
  `xcode-cdn.apple.com`, `mesu.apple.com`, `updates.cdn-apple.com`,
  `gdmf.apple.com`, `gs.apple.com`, `gsa.apple.com` and
  `idmsa.apple.com`. Wraith Box never puts these hosts in pass
  mode itself, and the rules above apply to them. A user who does
  makes each one a T01-allowed-channels channel. The Apple Account
  hosts (`idmsa.apple.com`, `gsa.apple.com`) then carry whatever
  Apple credentials the guest holds, which Wraith Box neither gave
  nor removes (T10-unnamed-credentials). On a macOS guest, clients
  that read only the system trust store, such as URLSession, also
  reject inspection, a known gap of FR08-no-proxy-config that
  S09-07-guest-trust, lists.

#### audit

inspect, with the HTTP policy of the host and port
logging a violation and letting the request through (S07-07-http-policy). The mode is per host and port, resolved over the whole
union, and `enforce` wins when any source has an `enforce`
endpoint on that host and port (S09-11-precedence, "Mode per host and port"). The boundary check sees an `audit` host
and port as every method and path on it
(S09-01-policy-files, "Audit as the whole host"). The
maintainer decided its rules on I109:
- *Only the user sets it.* Only the global policy and a project's
  network policy can put a host and port in `audit`. Repository
  configuration and approvals can't (S09-02-project-settings, "Repository-supplied configuration", and S09-08-approvals, "Approved rule").
- *Never on a guarded host.* A host with a credential binding, or a
  host a built-in profile covers (as for pass above, by name or
  through a project's remotes), can't be in `audit`. With policy
  not enforced there, a write the git hosting profile refuses
  would pass with a credential injected, against
  SEC06-repo-writes.
  - *At load.* An `audit` endpoint on a guarded host is refused at
    load with an error that names the host and the reason
    (NFR06-explained-refusals).
  - *At every recompute.* `wb-hostd` checks the same rule over the
    union, whatever source set `audit`, so a global entry that
    loaded while no project named its host refuses the session
    start of a project whose remote names it. The error names that
    project, the host and the file of the `audit` endpoint
    (NFR06-explained-refusals).
  - *A binding added later.* A change that adds a credential
    binding on a host with an `audit` endpoint, in a policy file or
    with `wb cred set`, is refused, and the `audit` endpoint stays.
    The error names the endpoint and its file, and says to remove
    it first (NFR06-explained-refusals).

**Status:** Draft
