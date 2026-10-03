# 009 - Policy, Credentials, Certificates and Audit

**Purpose:** Where policy comes from, how secrets are held, how TLS
inspection is trusted, and what is recorded.

**Requirements:** F9, F10, F15, S4–S6, S9, S10, S12, S14, N6.

## Policy

- **Files.** TOML, validated against a schema. Global defaults in
  `~/.config/wraithbox/config.toml`; per-project overrides in
  `~/.config/wraithbox/projects/<project-id>.toml`. Both live on the
  host, outside every repository and every guest.
- **Contents.** Allowed hosts and ports (exact names and bounded
  wildcards); per-host mode (inspect/pass); HTTP rule profiles and
  additions; extra writable repositories; dependency-gate thresholds;
  the project Brewfile; VM slot (work/isolated); resource limits;
  clipboard and port-forwarding switches.
- **Repository-supplied configuration.** A `.wraithbox.toml` in a
  repository is ignored unless the user has run `wbctl trust` for that
  repository. Even then it may only add allowed hosts, a Brewfile and
  HTTP rules, and every addition is shown in `wbctl policy explain`. It
  can never add credential bindings or switch hosts to pass-through
  (S9).
- **Precedence.** Built-in defaults → global → project → trusted repo
  config → session approvals. `wbctl policy explain` shows the effective
  value and its source.

## Credentials

- **Storage.** Each credential is a Keychain item readable only by
  `wb-proxyd` (an access group bound to Wraith Box's code-signing
  identity once that identity exists; until then, an access-control
  list naming the `wb-proxyd` binary, which is weaker and documented as
  such).
- **Bindings.** A binding names the host(s), the header and scheme to
  inject, and the Keychain item. `wbctl cred set <binding>` reads the
  value from a prompt or stdin; it never appears in arguments or logs.
- **Model credential.** Claude Code in the guest is configured with a
  placeholder and a binding for the model API host. Whether every Claude
  Code authentication mode works with host-side replacement (including
  token refresh) is the first spike in spec 011.
- **Never in the guest.** Images are scanned for secrets at seal time;
  the environment of every guest process is built from an allowlist.

## TLS inspection certificate authority

- **Key.** A P-256 key generated in the Secure Enclave, non-exportable,
  used by `wb-proxyd` to sign leaf certificates (Go's certificate
  creation accepts any signer). Fallback if the Secure Enclave is
  unavailable: a Keychain-held software key.
- **Scope.** The CA certificate carries name constraints restricting it
  to the hostnames configured for inspection. It is trusted **only inside
  guests**, never on the host. When the inspected set changes, a new CA
  is issued and `wb-guestd` installs it.
- **Lifetimes.** CA: 30 days, rotated automatically. Leaf certificates:
  24 hours, cached in memory.
- **Guest trust.** `wb-guestd` installs the CA in the guest's system
  trust store and sets toolchain-specific trust variables so that every
  common client accepts it.

## Approvals (S14)

Approval requests are delivered as macOS notifications from `wb-hostd`
and through `wbctl approve`/`deny`. They are never written to the
terminal stream of a session, which the guest controls.

## Audit (S10)

- JSONL in `~/Library/Logs/WraithBox/`, written by `wb-hostd` from events
  sent by `wb-netd` and `wb-proxyd`; rotated and size-capped.
- Records: timestamp; project; session; guest user; destination host and
  port; decision and the rule that made it; HTTP method and path for
  inspected requests; bytes in and out; approval actions; returned work
  and its flags. Process attribution reported by the guest is stored as
  an untrusted label.
- Never recorded: credential values, request or response bodies.

**Status:** Draft
