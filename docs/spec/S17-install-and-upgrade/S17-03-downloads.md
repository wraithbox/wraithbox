# S17-03 - Downloads

**Purpose:** How `wb-download`, and only it, fetches what an image build
needs from the internet.

## Design

![wb-hostd opens the files, then starts wb-download through wb-launcher. wb-download fetches Apple's catalog and a restore image, or a Claude Code release, over HTTPS from the hosts compiled into its entry, into those descriptors. wb-hostd checks the restore image's hash, and wb-release-check checks the Claude Code release, before wb-vmd-install or wb-guestd uses them. wb-hostd itself has no network.](S17-03-downloads.svg)

### Downloads: `wb-download`

Only `wb-download` fetches what an image build needs (decided on I49).
`wb-hostd` has no network, and `wb-proxyd` serves guests. `wb-download`
is Go, one run per download, started through `wb-launcher` from one of
two fixed entries. Each entry has an instance cap of one, its hosts
compiled into the program, and a file size limit (`RLIMIT_FSIZE`) that
`wb-launcher` sets before it starts the program:

| Entry | Hosts | Fetches | File size limit |
|---|---|---|---|
| `wb-download-restore` | `mesu.apple.com`, `updates.cdn-apple.com` | Apple's restore image catalog and one restore image | 40 GiB |
| `wb-download-claude` | `downloads.claude.ai` | one Claude Code release (S14-claude-in-guest, "Install") | 1 GiB |

On Windows and Linux, `wb-download` confines itself with that
platform's mechanism (S12-platforms), with the same rules in its code.

### Confinement

It confines itself first thing, from a compiled-in
profile, the network part of the `wb-proxyd` profile that
X23-sandboxed-daemons measured for I61, limited to port 443 and
without the secret store services:

```scheme
(version 1)
(deny default)
(allow network-outbound (remote tcp "*:443"))
(allow network-outbound (remote udp "*:53"))
(allow network-outbound (literal "/private/var/run/mDNSResponder"))
(allow file-read-metadata (literal "/etc") (literal "/var"))
(allow file-read* (literal "/private/etc/resolv.conf")
  (literal "/private/var/run/resolv.conf") (literal "/private/etc/hosts"))
(allow mach-lookup (global-name "com.apple.trustd.agent"))
(allow file-read-metadata (literal (param "BUNDLE")))
(allow sysctl-read (sysctl-name-prefix "net.routetable"))
```

`BUNDLE` is the real path of `Wraith Box.app`, the bundle whose
program it is, worked out before it confines itself. The last line
is for `getifaddrs`, which reads the interface list from the routing
table through `sysctl`. It is assumed: X23-sandboxed-daemons didn't
measure it, and the self-check test of C12-install-and-downloads
measures the exact name. Files, the
secret store and starting programs are denied. It can still use its
inherited descriptors.

### Hosts

Seatbelt can't name hosts. The code refuses a host
outside its entry with `download-host-refused`, a URL that isn't
HTTPS on port 443, every redirect (`download-redirect-refused`), and
an address under the "Upstream address" rule of S07-egress-gateway
(`upstream-address-refused`). That rule lists the host's interfaces
with `getifaddrs`, and a failure to list them refuses the connection
with `upstream-address-refused` as well. TLS is checked
against the system roots. Its HTTP client's `Transport.Proxy` is nil, so `HTTPS_PROXY`
and the system's proxy settings don't apply.

### Deadlines

A connection that isn't up in 30 s fails with
`download-connect-timeout`, and a response with no byte for 60 s with
`download-idle-timeout`. A whole request has 2 minutes for the
catalog, a `HEAD`, the manifest or the signature, 30 minutes for the
Claude Code binary, and 12 hours for the restore image
(`download-deadline`). `wb-hostd` cancels a run by closing its
request pipe, and `wb-download` stops at that end of file with
`download-cancelled`.

### The result record

`wb-download` writes fixed-format records to
its pipe, and `wb-hostd` checks each field's form:

| Field | Form |
|---|---|
| macOS version | `^[0-9]{1,3}(\.[0-9]{1,3}){1,2}$` |
| macOS build | `^[0-9]{2}[A-Z][0-9]{1,5}[a-z]?$` |
| URL | `https://updates.cdn-apple.com/` and a path of 1 to 512 characters of `A-Z a-z 0-9 . _ / -` ending in `.ipsw`, with no empty segment, no `.` or `..` segment, and no port, user, query or fragment |
| SHA-1 | 40 lowercase hexadecimal digits |
| size | a decimal number from 1 to 42,949,672,960 (40 GiB) |
| host, rule, status, bytes | one of the entry's hosts, a rule name of this section, a three-digit status, a decimal count |
| path | the catalog's path, the path of the URL above, or `/claude-code-releases/<version>/` followed by `manifest.json`, `manifest.json.sig` or `darwin-arm64/claude`, with the version's form below |
| address | an IPv4 address in dotted decimal, or an IPv6 address in the form Go's `netip.Addr.String` writes, without a zone |

A field outside its form, or a run that reports more than 16 requests,
ends the run with `download-record-form`. The record parser has a
fuzz target.

### Audit

For each request, and each refusal of one, it reports the
host, the address it connected to, the path, and the status or the
rule. `wb-hostd` writes each as an HTTP Activity (4002) record, a
refusal as a refused one with its rule (SEC10-audit). These records
belong to no VM, so they don't count against any VM's audit budget
(S09-10-audit), and they're never dropped.
When one can't be written, `wb-hostd` cancels the run with
`download-audit-failed`, and the rule of S09-policy-credentials-audit
for a record that can't be written applies.

### Space

Before it starts either entry, `wb-hostd` refuses with
`download-no-space` when the volume of `<data>` has less free space
than the entry's file size limit, less what the partial restore
image already holds, plus `audit_free_space_floor`
(S09-policy-credentials-audit). For a Claude Code release, the limit
counts once for each of its three files. The check uses only numbers
the host knows before the run, and the file size limit holds what a
run writes to them.

### Failures

Any exit other than success fails the build with its rule, and nothing
restarts `wb-download`. The next `wb image build` resumes the restore
image.

**Status:** Draft
