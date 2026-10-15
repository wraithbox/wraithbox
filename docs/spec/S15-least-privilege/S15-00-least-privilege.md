# S15 - Least Privilege on the Host

**Purpose:** The security design for SEC12-least-privilege: which
rights Wraith Box takes on the host, where credentials are held, and
how each host process gets only the credentials it needs, for only as
long as it needs them.

**Requirements:** SEC12-least-privilege, with SEC04-no-guest-secrets,
SEC10-audit and SEC11-root-gains-nothing where a credential crosses a
boundary.

**Tests:** C05-credentials-and-store, C09-guest-and-sessions

Briefs: B145-least-privilege, B23-keychain-unsigned, B248-dev-signing,
B28-image-build

![The secret store is outside every Wraith Box process. wb-credset, which wb runs for wb cred, writes, lists and deletes binding secrets in it. wb-proxyd fetches the binding secrets of the VM's sessions and the wrapped CA key, which only the host's hardware key store can sign with. wb-guestadmin writes and fetches the wbadmin passwords, pipes the build password to the build's wb-vmd and to wb-build-ssh, and writes the old and new password to wb-guestd at a VM's first contact. wb-hostd has no store access and passes descriptors on. A VM that runs project code gets placeholders only.](S15-00-least-privilege.svg)

## Design

### Pages

| Page | What it covers |
|---|---|
| S15-01-rights | The rights Wraith Box takes on the host: no root, no extra accounts, signing and entitlements |
| S15-02-secret-store | The store that holds every credential, its backends, and what they guarantee |
| S15-03-access-rules | The rules for store access, and which process may access which item |
| S15-04-guest-admin | The guest admin user `wbadmin` and its password, from the build to each VM |

## Out of scope

- The browser login for a Claude subscription (I257). The long-lived
  token is in S09-04-credentials, "Model credential".
- Project users' accounts in the guest, which have no password the host
  keeps.
- The other `SEC*` controls.

**Status:** Draft
