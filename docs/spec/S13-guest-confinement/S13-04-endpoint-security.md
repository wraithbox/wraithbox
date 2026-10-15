# S13-04 - Layer 4: Endpoint Security (optional, after X15-endpoint-security)

**Purpose:** What an Endpoint Security client in the guest would add, and why
Wraith Box may ship without it.

## Design

An Endpoint Security client in the guest adds:

- ancestry, so a rule for a program also covers the programs it starts,
  matching OpenShell's semantics;
- an exec allowlist per project, by cdhash or signing identity;
- file-open authorization for cases Seatbelt profiles cannot express.

Its entitlement is granted by Apple on request, which takes time and
may be refused. Without the entitlement, Wraith Box ships without this
layer. Spike X15-endpoint-security tracks it.

**Status:** Draft
