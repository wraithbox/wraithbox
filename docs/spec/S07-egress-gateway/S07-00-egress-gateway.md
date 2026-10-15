# S07 - Egress Gateway

**Purpose:** The only path from a guest to any network: a userspace
network stack (`wb-netd`) and a policy-enforcing proxy (`wb-proxyd`) on
the host.

**Requirements:** FR08-no-proxy-config to FR10-learn-mode, SEC04-no-guest-secrets to SEC08-proj-isolation, SEC10-audit, SEC11-root-gains-nothing, SEC13-bounded-resources, NFR06-explained-refusals. Residual risk T11-shared-vm-grants.

**Tests:** C01-network-and-dns, C02-streams-and-tls, C03-policy-and-approvals, C05-credentials-and-store, C06-model-api, C07-dependency-gate

Briefs: B17-network-path, B32-dep-gate-registries,
B33-no-guest-credentials, B35-openshell-artifacts,
B36-flow-attribution, B40-learn-pass-modes, B47-non-http-streams,
B51-approval-flow, B60-model-credential, B68-proxyd-stream-identity

## Design

Every packet a guest sends reaches `wb-netd`, the packet path. Every
TCP stream to an allowed name goes on to `wb-proxyd`, the stream path.

### Pages

| Page | What it covers |
|---|---|
| S07-01-per-vm-policy | The VM as the unit of policy: the union of its projects, conflicts, changes while sessions run, and session end |
| S07-02-packet-path | `wb-netd`: the packet transport, the link filter, caps, DHCP, the MTU, connections, and performance |
| S07-03-dns | The DNS server of `wb-netd`: synthetic addresses, the name form, negative answers and the wildcard budget |
| S07-12-dns-refusals | How `wb-netd` refuses a name: approval events, the hold, quiet refusals, and the same answer for every reason |
| S07-04-stream-hand-off | How a stream passes from `wb-netd` to `wb-proxyd`, and why `wb-netd` can only narrow |
| S07-13-name-mapping | `wb-proxyd`'s checked copy of the name mapping: reports, releases, replay and the second wildcard count |
| S07-05-hosts-and-modes | Name binding, the upstream address rule, the inspect, pass and audit modes, and guarded hosts |
| S07-06-requests-and-credentials | The request form, and credential replacement |
| S07-14-placeholder-binding | Where a placeholder may go, and the scan that enforces it |
| S07-15-anonymous-credentials | Fixed public tokens for anonymous reads, and the `ghcr.io` token fallback |
| S07-07-http-policy | HTTP rules, the built-in profiles, and profile parts |
| S07-08-dependency-gate | The dependency gate on package registries |
| S07-09-publish-time | Publish times, and the Go module clock from the checksum database |
| S07-16-go-lookups | Verified lookups in the checksum database, their caches and limits |
| S07-10-non-http-streams | The protocol check on each stream, SSH, and raw TCP entries |
| S07-17-tls-and-tunnels | Client certificates, encrypted ClientHello, tunnels, and refusals audit mode doesn't relax |
| S07-11-approvals-and-learning | Approvals of unknown destinations, and learn mode |

The table is in reading order. Pages 12 to 17 continue the page above
them.

**Status:** Draft
