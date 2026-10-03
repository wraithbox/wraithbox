# 009 - Policy, Credentials, Certificates and Audit

**Purpose:** Where policy comes from, how secrets are held, how TLS
inspection is trusted, and what is recorded.

**Requirements:** F9, F10, F15, S4 to S6, S9, S10, S12, S14, N6.

## Policy

- **Files.** TOML, validated against a schema. Global defaults in
  `<config>/config.toml`; per-project overrides in
  `<config>/projects/<project-id>.toml` (`<config>` per OS in spec 012;
  `~/.config/wraithbox` on macOS). Both are on the host, outside every
  repository and every guest. The format is the same on every host OS
  (F17).
- **Contents.** Allowed hosts and ports (exact names and bounded
  wildcards); per-host mode (inspect/pass); HTTP rule profiles and
  additions; extra writable repositories; dependency-gate thresholds;
  the project toolchain manifest (Brewfile on macOS guests); guest OS;
  VM slot (work/isolated); resource limits;
  clipboard and port-forwarding switches.
- **Repository-supplied configuration.** A `.wraithbox.toml` in a
  repository is ignored unless the user has run `wb trust` for that
  repository. Even then it may only add allowed hosts, a toolchain
  manifest and HTTP rules, and every addition is shown in
  `wb policy explain`. It
  can never add credential bindings or switch hosts to pass-through
  (S9).
- **Precedence.** Built-in defaults → global → project → trusted repo
  config → session approvals. `wb policy explain` shows the effective
  value and its source.

## Credentials

- **Storage.** Each credential is an item in the platform's secret
  store (spec 012), readable only by `wb-proxyd`. On macOS: a Keychain
  item in an access group bound to Wraith Box's code-signing identity
  once that identity exists; until then, an access-control list naming
  the `wb-proxyd` binary, which is weaker and documented as such. Other
  platforms scope items as narrowly as their store allows; the limits
  are documented per platform in spec 012.
- **Bindings.** A binding names the hosts, the header and scheme to
  inject, and the secret store item. `wb cred set <binding>` reads the
  value from a prompt or stdin; it never appears in arguments or logs.
- **Model credential.** Claude Code in the guest is configured with a
  placeholder and a binding for the model API host. Whether every Claude
  Code authentication mode works with host-side replacement (including
  token refresh) is the first spike in spec 011.
- **Never in the guest.** Images are scanned for secrets at seal time;
  the environment of every guest process is built from an allowlist.

## TLS inspection certificate authority

- **Key.** A P-256 key generated in the platform's hardware key store
  (Secure Enclave on macOS, TPM elsewhere; spec 012), non-exportable,
  used by `wb-proxyd` to sign leaf certificates (Go's certificate
  creation accepts any signer). Fallback if no hardware key store is
  available: a software key held in the secret store.
- **Scope.** The CA certificate has name constraints restricting it
  to the hostnames configured for inspection. It is trusted **only inside
  guests**, never on the host. When the inspected set changes, a new CA
  is issued and `wb-guestd` installs it.
- **Lifetimes.** CA: 30 days, rotated automatically. Leaf certificates:
  24 hours, cached in memory.
- **Guest trust.** `wb-guestd` installs the CA in the guest OS's system
  trust store and sets toolchain-specific trust variables so that every
  common client accepts it.

## Approvals (S14)

Approval requests are delivered as native notifications (through the
platform's notification helper, spec 012) and through `wb approve` /
`wb deny`. They are never written to the terminal stream of an agent
session, which the guest controls. Guest-influenced text in a request is
stripped of control and escape sequences wherever it is shown.

## Audit (S10)

- JSONL in `<logs>` (spec 012; `~/Library/Logs/WraithBox/` on macOS),
  written by `wb-hostd` from events
  sent by `wb-netd` and `wb-proxyd`; rotated and size-capped.
- Records: timestamp; project; session; guest user; destination host and
  port; decision and the rule that made it; HTTP method and path for
  inspected requests; bytes in and out; approval actions; returned work
  and its flags. Process attribution reported by the guest is stored as
  an untrusted label.
- Never recorded: credential values and request or response bodies.

**Status:** Draft
