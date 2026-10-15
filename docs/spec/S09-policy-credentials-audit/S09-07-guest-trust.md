# S09-07 - Guest Trust

**Purpose:** How `wb-guestd` makes the guest's clients trust the VM's CAs.

## Design

### Guest trust

The sealed image doesn't contain a Wraith Box CA
(S06-01-image-layers, "Layers"). At runtime `wb-guestd` writes the
VM's current CAs to the trust files below and sets the toolchain
trust variables that point clients at them, so that common clients
accept them (X04-tls-inspection). Installing a CA means writing
these files, and on Linux and Windows guests also adding it to the
system trust store. On a macOS guest it doesn't touch the system
trust store ("System trust store" below). Each install writes the
files whole from `wb-hostd`'s record. On a Linux or Windows guest it
also removes from the system trust store every Wraith Box CA that
the record doesn't hold. A dropped CA leaves the guest at its next
install.

### Files

`wb-guestd` writes three files in a fixed directory that
root owns, `/etc/wraithbox/trust/` on macOS and Linux guests, with
mode 0755. Each file has mode 0644. `wb-guestd` writes each one to a
temporary file in the same directory and renames it into place, so
a client never reads half a file. The path holds no whitespace and
no character a shell treats specially. Project users read the
directory under S13-02-filesystem-profile, "Profile".
- the CA file: every current CA of the VM;
- the bundle: the guest's public roots (`/etc/ssl/cert.pem` on
  macOS) and every current CA. `wb-guestd` rebuilds it from the
  current public roots on every write. A variable that replaces a
  client's roots points here, because pass hosts and raw TCP relays
  present public certificates (S07-05-hosts-and-modes, "Modes, per host");
- the Java trust store: a PKCS12 file without a password, with
  the same certificates as the bundle. `wb-guestd` builds it
  itself, in Go. Neither the standard library nor
  `golang.org/x/crypto/pkcs12` can write PKCS12, so this needs a
  writer with a permissive license, such as
  `software.sslmate.com/src/go-pkcs12` (BSD-3-Clause), which
  S10-tech-stack records when the code lands.

### Variables

`wb-guestd` sets them in the environment of every
project user's processes, and in the toolchain user's environment
for a toolchain reconcile (S06-01-image-layers, "The environment"),
since on a macOS guest Homebrew's downloads from inspected hosts
such as `ghcr.io` have no other way to trust the CA. Each value is
built from constants, and
the value of `JAVA_TOOL_OPTIONS` is exactly
`-Djavax.net.ssl.trustStore=` followed by the Java trust store's
fixed path. `wb-guestd` never appends to a value it inherits or
merges with one. A value that a project or the user sets replaces
Wraith Box's, and that client then fails closed on inspected hosts,
because it doesn't trust the CA.

| Variable | File | Clients X04-tls-inspection saw use it |
|---|---|---|
| `SSL_CERT_FILE` | bundle | Go, `gh`, `curl`, Python's `ssl` and `urllib`, `uv`, Ruby and `gem` |
| `REQUESTS_CA_BUNDLE` | bundle | Python `requests` and `pip` |
| `GIT_SSL_CAINFO` | bundle | `git`, and SwiftPM and `xcodebuild` resolving packages over git |
| `CARGO_HTTP_CAINFO` | bundle | cargo |
| `NODE_EXTRA_CA_CERTS` | CA file | Node, `npm` and Claude Code, which add it to their own roots |
| `JAVA_TOOL_OPTIONS` | Java trust store | Java, through `-Djavax.net.ssl.trustStore` |

Every JVM writes a line about `JAVA_TOOL_OPTIONS` to standard
error when it starts.

### System trust store

On Linux and Windows guests `wb-guestd` also
adds the CAs to the system trust store (S12-platforms). On a macOS
guest it doesn't: macOS 27 refuses to add a trusted root without a
user to confirm it (X04-tls-inspection, answer 1). Decided on I185.
Wraith Box doesn't work around that platform control, and
Virtualization framework VMs can't enroll in MDM.
- *Clients left out.* A client that reads only the system trust
  store doesn't trust the CA: URLSession and what is built on it,
  so Swift programs, SwiftPM's binary target downloads and Xcode's
  own requests, and `pip` when no CA variable points it at the
  bundle, and it uses the system store by default.
  X04-tls-inspection saw each of them reject the inspected leaf.
- *Fails closed.* Over TLS, they reach only pass hosts and raw TCP
  entries, never a guarded host (S07-05-hosts-and-modes, "Modes, per
  host", and S07-10-non-http-streams). Plain HTTP doesn't need the CA.
  FR08-no-proxy-config names this gap. An attended step, in which
  the maintainer confirms trust in the guest's UI, is I213.

**Status:** Draft
