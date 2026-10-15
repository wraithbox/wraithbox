# S13-04 - Layer 4: Endpoint Security (optional, after X15-endpoint-security)

**Purpose:** What an Endpoint Security client in the guest would add, and why
Wraith Box may ship without it.

## Design

An Endpoint Security client in the guest adds:

- ancestry, so a rule for a program also covers the programs it starts,
  matching OpenShell's semantics;
- an exec allowlist per project, by cdhash or signing identity;
- file-open authorization for cases Seatbelt profiles can't express.

## Decisions

### Ship without it when Apple refuses the entitlement

Without the entitlement, Wraith Box ships without this layer.

- **Why:** Apple grants the entitlement on request, which takes time
  and may be refused.

Spike X15-endpoint-security tracks the entitlement.

**Status:** Draft
