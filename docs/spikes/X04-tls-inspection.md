# X04 - Inspection compatibility

**Purpose:** Whether Go-based, Swift-based and Xcode clients in a macOS guest accept the per-VM CA when only the guest trusts it, which clients pin certificates, and which reread trust without a restart.

Brief: B18-tls-inspection

## For review

- **Decides:** the answer to X04-tls-inspection (I18), the trust
  variables `wb-guestd` sets (S09-policy-credentials-audit, "Guest
  trust"), and the hosts S07-egress-gateway names as known to break
  under inspection.
- **You are approving:** the answer "no". Each client that reads a CA
  file or a variable accepted the CA, and none of the clients that
  could be tested pins. But
  `wb-guestd` can't put the CA in a macOS guest's system trust store,
  because macOS 27 refuses every way the spike tried without a user to
  confirm it. So URLSession clients, which read only that store,
  reject every inspected host: Swift programs, SwiftPM's binary
  target downloads, and Xcode's own requests. Only Python's `requests`
  rereads its CA file, so the overlap of 28 days stands as S09 states
  it.
- **Controls touched:** none weakened. A client that doesn't trust the
  CA fails closed. FR08-no-proxy-config isn't met for URLSession
  clients on inspected hosts.
- **Assumed or inferred:**
  - That Homebrew's builds stand for the clients a project installs.
    Homebrew's Node honored `SSL_CERT_FILE`, which Node's own builds
    don't by default, so `NODE_EXTRA_CA_CERTS` stays.
  - That `/etc/ssl/cert.pem` is a fair source for the public roots in
    the bundle.
  - That Claude Code reads its trust once at start, like Node. It
    couldn't run in a loop without an API key.
  - That the Java trust store S09-policy-credentials-audit specifies
    works: built by `wb-guestd` from the bundle, without a password,
    named by `JAVA_TOOL_OPTIONS` alone. The spike tested a different
    one: the JDK's own roots plus the CA, built with `keytool`, with
    the password `changeit` set in `JAVA_TOOL_OPTIONS`.
  - That Apple's pinned hosts refuse an inspected leaf. Inferred from
    trustd's pinning rules, not seen, because the CA never got into
    the system store.
  - That the six variables of S09-policy-credentials-audit work as a
    set. Inferred: the spike ran all eight it set together, and each
    client with one variable at a time, but never these six alone.
  - That Homebrew's downloads accept the CA through the trust
    variables, which S09-policy-credentials-audit and S06-vm-lifecycle
    now give the toolchain user. Untested: Homebrew filters its
    environment, and the spike installed every formula before the relay
    ran (I180).
  - That option B of I185 works at all (below).
- **Open decisions:**
  1. How a macOS guest gets the CA into its system trust store, if at
     all. I185 has the options: accept that URLSession clients reach
     only pass hosts, change one authorization right of the guest at
     image build so root may set trust settings, or a configuration
     profile through MDM. Recommended: accept it for now, which means
     amending FR08-no-proxy-config for URLSession clients, and try the
     authorization right in a VM before deciding on it. The spike didn't
     try that change, because it edits authd's database directly. It
     gives guest root admin trust over trustd and URLSession, which
     T05-cross-proj-clones already concedes to guest root, and trustd's
     own entitlement check may defeat it.
- **Brief:** B18-tls-inspection

## Question

From X00-index: do Go-based, Swift-based and Xcode clients accept a CA
that only the guest trusts? Which clients pin certificates, and which
reread trust without a restart, which sets the CA overlap
(S09-policy-credentials-audit, "Lifetimes")? The CA has no name
constraints since I39, so the issue's checks of name constraints fall
away. I18 lists the clients: `curl`, Apple `git`, Homebrew `git`, `gh`,
a Go program, URLSession, SwiftPM resolve through `xcodebuild`, Node,
Python `requests` and `pip`, `uv`, Ruby, cargo, Java and Claude Code.

## Answer

**No.** The CA works for every client that can be pointed at a CA file
or a trust store variable. It doesn't work for clients that read only
the macOS system trust store, because `wb-guestd` can't add it there.
Each part, with the evidence in "Measurements":

1. **The system trust store can't be written without a user: verified
   refused.** As root, from the guest daemon, with no user logged in:
   - `security add-trusted-cert -d -r trustRoot -k
     /Library/Keychains/System.keychain` fails with "The authorization
     was denied since no user interaction was possible". The rule
     behind it, `com.apple.trust-settings.admin`, is `entitled` or
     `authenticate-admin`.
   - `security authorizationdb write com.apple.trust-settings.admin
     allow`, the workaround CI machines use, is refused with
     `NO (-60005)`, and so is `is-root`.
   - `security trust-settings-import -d` is refused the same way.
   - Trust for the project user's own domain, set as that user, is
     refused the same way (rule
     `entitled-session-owner-or-authenticate-session-owner`).
   - `profiles install` answers "profiles tool no longer supports
     installs".
   - A hand-written `/Library/Security/Trust Settings/Admin.plist` is
     ignored, also after `trustd` restarts. The host has admin trust settings
     too, and that folder is empty there.
     `/private/var/protected/trustd/private`, which root can't list, is
     the likely place.
   - The certificate alone in the System keychain, without trust
     settings, isn't trusted.
2. **URLSession clients need that store: verified.** With every
   variable set, the Swift URLSession client fails with
   `NSURLErrorDomain -1202`, a SwiftPM binary target download fails with
   "The certificate for this server is invalid", and `xcodebuild`'s
   requests to `developer.apple.com` fail. `pip` also reads that store by
   default (Apple's message "certificate is not trusted"), but takes a
   variable.
3. **Every other client accepts the CA through a variable: verified.**
   None of these clients pins. Which variable each one needs is in the table below.
   `xcodebuild -resolvePackageDependencies` and `swift package resolve`
   clone over git, so `GIT_SSL_CAINFO` covers them, with Xcode's
   built-in source control and with `-scmProvider system`.
4. **Variables that replace the default roots need the public roots
   too: verified.** `SSL_CERT_FILE`, `GIT_SSL_CAINFO` and the rest replace
   a client's roots. With a file that held only the Wraith Box CA, curl,
   the Go program and Python failed on a pass-mode host, which shows
   its public certificate. With the guest's public roots
   (`/etc/ssl/cert.pem`, 128 certificates) plus the CA, all three
   passed. Go on macOS reads `SSL_CERT_FILE` and then uses only that
   file.
5. **Only Python's `requests` rereads trust: verified.** Long-running
   processes started with a file that held one CA, made a request
   every 10 s, and the file then got the successor and the relay
   switched to it. The Go program, Node, Python's `urllib`, Ruby and
   Java failed every request after the switch. `requests` passed,
   because it loads its CA file for every new session. Processes
   started after the file got the successor passed. Claude Code runs
   on Bun and couldn't be run in a loop without an API key, so it
   wasn't tested. Read at start, as Node and the others do, is the
   safe assumption. A 28-day overlap is therefore what bounds a
   process's life, as S09-policy-credentials-audit states.
6. **Apple pins its own hosts: verified from the guest's rules.** No
   tested client pins. trustd's pinning rules in the guest
   (`/private/var/protected/trustd/pinningrules.sqlite3`, 131 rules)
   pin Apple hosts that a development guest may contact: software update
   and the command line tools (`swscan`, `swcdn`, `swdist`,
   `swdownload.apple.com`), `xcode-cdn.apple.com`, `mesu.apple.com`,
   `updates.cdn-apple.com`, `gdmf.apple.com`, `gs.apple.com` and
   `gsa.apple.com`, and the Apple Account hosts (`idmsa.apple.com`).
   Four of them carry the rules' `transparentConnection` flag
   (`mesu.apple.com`, `updates.cdn-apple.com`, `gdmf.apple.com`,
   `gs.apple.com`), which by its name applies the pin to every client of
   the system's TLS. The others apply to Apple's own clients that ask for
   that rule. So even with the CA in the system store, these hosts would
   refuse an inspected leaf. That is inferred: the spike couldn't show
   it, because the CA never got in.

| Client | Version | Variable that works | Pins | Rereads |
|---|---|---|---|---|
| Apple `curl` | 8.7.1 (LibreSSL) | `SSL_CERT_FILE`, `CURL_CA_BUNDLE` | no | new process |
| Homebrew `curl` | 8.22.0 (OpenSSL 3.6.5) | `SSL_CERT_FILE`, `CURL_CA_BUNDLE` | no | new process |
| Apple `git` | 2.54.0 (Apple Git-157) | `GIT_SSL_CAINFO` | no | new process |
| Homebrew `git` | 2.56.0 | `GIT_SSL_CAINFO` | no | new process |
| `gh` | 2.102.0 | `SSL_CERT_FILE` | no | new process |
| Go program, `go mod download` | Go 1.27.1 | `SSL_CERT_FILE` | no | no |
| Swift URLSession | Swift 6.4 | none | can't tell | not tested |
| SwiftPM git (`swift package resolve`) | Xcode 27.0 | `GIT_SSL_CAINFO` | no | new process |
| SwiftPM binary target download | Xcode 27.0 | none | can't tell | not tested |
| `xcodebuild -resolvePackageDependencies` | Xcode 27.0 | `GIT_SSL_CAINFO` for packages | can't tell | new process |
| Node, `npm` | 26.10.0 (Homebrew) | `NODE_EXTRA_CA_CERTS`, and `SSL_CERT_FILE` in Homebrew's build | no | no |
| Python `urllib` | 3.14.8 (Homebrew) | `SSL_CERT_FILE` | no | no |
| Python `requests` | 2.34.2 | `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE` | no | yes |
| `pip` | 26.2.1 | `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `PIP_CERT` | no | new process |
| `uv` | 0.12.23 | `SSL_CERT_FILE` (also with `UV_NATIVE_TLS`) | no | new process |
| Ruby, `gem` | 4.0.7 | `SSL_CERT_FILE` | no | no |
| cargo | 1.99.0 | `CARGO_HTTP_CAINFO` | no | new process |
| Java | OpenJDK 27 | `JAVA_TOOL_OPTIONS` with a trust store file | no | no |
| Claude Code | 2.1.293 | `NODE_EXTRA_CA_CERTS` | no | not tested |

"New process" means a command that runs and exits, so it reads trust
each time it starts. "No" means a long-running process kept the trust
it read at start.

The conditions under which inspection works:

1. **`wb-guestd` sets these variables** for every project user's
   processes: `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `GIT_SSL_CAINFO`,
   `CARGO_HTTP_CAINFO` and `JAVA_TOOL_OPTIONS` point at files with the
   public roots and every current CA of the VM, and
   `NODE_EXTRA_CA_CERTS` at a file with only the CAs, since Node adds it
   to its own roots. `CURL_CA_BUNDLE` and `PIP_CERT` add nothing that
   these don't cover. That is inferred from the runs with one variable
   at a time: these six never ran as a set.
2. **Java prints a line for `JAVA_TOOL_OPTIONS`.** Every JVM writes
   "Picked up JAVA_TOOL_OPTIONS: …" to standard error at start. A tool
   that parses a JVM's standard error sees it.
3. **URLSession clients reach only pass hosts** until I185 decides
   otherwise.
4. **Apple's pinned hosts work only as pass hosts.** Whether a user
   puts one in pass mode is the user's choice, and each one is then a
   T01-allowed-channels channel.

## Measurements

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0
(27A266a), no `sudo`. Guest: macOS 27.0.1 from the X17-image-build
spike's sealed image, 4 vCPU, 4 GiB, Apple's NAT network. The guest got
the Xcode command line tools from Apple, Xcode 27.0 from the host,
Homebrew in a prefix the toolchain user owns (X20-shared-homebrew), and
Claude Code from its native installer, all before any inspection. The
clients ran as a standard project user, from the guest daemon as root
with `sudo -u`.

- **The relay.** A Go program in the guest, as `nobody`, took the place of
  `wb-proxyd`. pf in the guest routed every other process's TCP 443 to
  it, dropped UDP 443 so no client used QUIC, and refused IPv6 port 443.
  No client had proxy settings. The relay read the SNI, signed a
  24-hour leaf for that one name with the CA profile of
  S09-policy-credentials-audit (P-256, `pathLen:0`, `keyCertSign` only,
  no name constraints; the leaf one `dNSName`, `serverAuth`, no common
  name, no key usage), passed the client's ALPN list upstream, and
  logged each client's alert. Session tickets were off, so each
  connection was a full handshake. Its log has 448 connections.
- **Control.** With no trust anywhere, every one of the 27 client runs
  failed its request, and the relay logged each client's refusal
  ("unknown certificate authority", "bad certificate", or a closed
  connection). The verdict comes from the relay's log and the client's
  output, not from exit codes. Four runs in `matrix-none.jsonl` exit
  0 (`go-mod-download`, `swiftpm-cli` and both `xcodebuild-spm` runs),
  though the relay logged the refusal and the output shows the failed
  fetch. Their commands end in a pipe. In the later runs the same
  commands exit non-zero when they fail, for example `swiftpm-cli`
  with 1 and both `xcodebuild-spm` runs with 74 in
  `matrix-only-SSL_CERT_FILE.jsonl`.
- **Variables.** With all eight variables set, every client but the
  URLSession ones passed. Then one variable at a time, for the table
  above.
- **Reread.** Six long-running clients from a file with one CA, the
  file replaced by one with two at 25 s, a second group started then,
  and the relay switched to the second CA at 45 s.
- **Install time.** Writing the variable files, with the Java trust
  store, took under 1 s. The system store refused in 0.3 s.
- **The relay's own upstream checks.** 6 of the 448 upstream handshakes
  failed the relay's own check ("certificate is not trusted"), all on
  connections that sent no ALPN, to `api.anthropic.com`,
  `registry.npmjs.org` and `example.com`. Each is followed 6 to 25 ms
  later by a client's "bad certificate" alert for the same name (lines
  45 and 46, 100 and 101, 194 and 195, 263 and 264, 352 and 353, 417
  and 418 of `proxy.jsonl`). The last pair is the error of group B's
  Node client at 01:37:21 in the reread test. The likely cause is
  trustd's own fetches being redirected by pf into the relay, which
  runs as `nobody`. It is unexplained, it is a property of the stand-in
  and not of `wb-proxyd` on the host, and no conclusion rests on it.

## What was not measured

- **Any client with the CA in the system store**, so neither whether
  URLSession and Xcode accept it nor whether they reread it. Nothing
  put it there (answer 1).
- **Editing authd's database** to let root set trust settings. The
  supported tool refuses the change, and editing the database directly
  changes a guest security setting, which is the maintainer's call
  (I185, option B).
- **Homebrew under inspection.** Every formula went in before the relay
  ran. Whether `brew install` from `ghcr.io` accepts the CA, and
  whether Homebrew passes `SSL_CERT_FILE` and the other trust variables
  through the environment it filters before it runs curl, is untested
  (I180).
- **Claude Code's reread**, and `NODE_USE_SYSTEM_CA` for Node and Claude
  Code, which needs the CA in the system store.
- **Official Node builds** from nodejs.org, and Python from python.org.
- **The guest's background daemons.** They contacted
  `api.apple-cloudkit.com` and `ocsp2.apple.com` during the runs and
  closed the connection on the relay's leaf. With default deny, they
  reach nothing that isn't allowed.

## What it means for the specs

- **S09-policy-credentials-audit**, "Guest trust": names the variables
  and their files, where the files live and how they're written, that
  the replacing files hold the public roots, how the Java trust store
  is built, that an inherited value is never merged, and that a macOS
  guest's system store waits on I185. "Lifetimes" states which clients
  reread, and that day 60 removes the CA from the trust files (changed
  in this pull request).
- **S07-egress-gateway**, "Modes": names Apple's pinned hosts and the
  URLSession clients as known to break under inspection (changed in
  this pull request). Wraith Box doesn't put them in pass mode itself.
- **S06-vm-lifecycle**, "The environment": the toolchain user's
  allowlist gets the trust variables (changed in this pull request).
- **S13-guest-confinement**, "Profile": the read allowlist names the
  trust files' directory (changed in this pull request).
- **S12-platforms**: the macOS guest's "CA trust" cell says the System
  keychain waits on I185 (changed in this pull request).
- **S11-verification-and-spikes**, "Spikes": names X04-tls-inspection
  (changed in this pull request).
- **FR08-no-proxy-config** stays as written. It isn't met for
  URLSession clients until I185 is decided.

## Spike code

Branch `spike/x04-tls-inspection`, at
[7e10eab](https://github.com/wraithbox/wraithbox/tree/7e10eab7bc56f3ac8d28bf8501b3928f36865d16/spikes/x04-tls-inspection):
the `wb-vmd` stand-in with a read-only share, the relay, the clients,
the guest scripts, and the raw results in `results/`. The spike's bundle
`g1` is a spike artifact, and no product image may descend from it.

**Status:** Answered 2026-10-08: no
