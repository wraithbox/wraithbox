# S09 - Policy, Credentials, Certificates and Audit

**Purpose:** Where policy comes from, how secrets are held, how TLS
inspection is trusted, and what is recorded.

**Requirements:** FR09-approve-unknown, FR10-learn-mode, FR15-inspect, SEC04-no-guest-secrets to SEC06-repo-writes, SEC08-proj-isolation to SEC10-audit, SEC12-least-privilege, SEC14-no-fake-approvals, NFR06-explained-refusals. Residual risk T11-shared-vm-grants.

**Tests:** C02-streams-and-tls, C03-policy-and-approvals, C05-credentials-and-store, C06-model-api, C11-host-daemons

Briefs: B15-browser-login, B23-keychain-unsigned, B35-openshell-artifacts,
B39-ca-rotation, B40-learn-pass-modes, B42-trust-placement,
B47-non-http-streams, B51-approval-flow, B60-model-credential,
B248-dev-signing

## Design

### Pages

| Page | What it covers |
|---|---|
| S09-01-policy-files | The policy files, the network policy schema, how the prover sees it, and narrowing by program and project |
| S09-02-project-settings | The project settings and repository-supplied configuration |
| S09-03-boundary-check | The check of policy against the boundary with OpenShell's prover |
| S09-04-credentials | The secret store, bindings, and the model credential and its placeholder |
| S09-05-ca-keys | The TLS inspection CA's key, and when a CA is valid |
| S09-06-ca-certificates | The CA's profile and scope, leaf certificates, and rotation |
| S09-07-guest-trust | How `wb-guestd` makes the guest's clients trust the CA |
| S09-08-approvals | The approved rule, the risk check, and join approvals |
| S09-09-approval-flow | How an approval request behaves, from the lookup to its answer or expiry |
| S09-10-audit | The audit log, its budgets, and free space and failed writes |
| S09-11-precedence | How the policy sources merge, and the mode of each host and port |
| S09-12-terminal-filter | The terminal filter's reports to `wb-hostd`, and the records written from them |

**Status:** Draft
