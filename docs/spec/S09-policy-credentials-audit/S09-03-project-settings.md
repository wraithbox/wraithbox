# S09-03 - Project Settings

**Purpose:** What a project's settings hold, and what a repository's own
`.wraithbox/` configuration may add.

## Design

### Settings contents

A project's settings hold:

- the project toolchain manifest (Brewfile on macOS guests);
- extra writable repositories;
- the guest OS;
- `placement`;
- `shared`;
- `config_trust`;
- resource limits.

Clipboard and port-forwarding switches come after V1
(V1-08-no-forward-clipboard).

#### `placement`

`placement` is `work` or `isolated`: the VM the project's sessions run
in. A new project gets `work`. `--isolated` overrides it for one
session (S06-04-vms-and-placement).

A project is set to `isolated` when its credentials or write grants
must stay apart from other projects, or when the user doesn't trust its
repository. It then gets the isolated VM to itself, unless `shared` is
on (T11-shared-vm-grants).

#### `shared`

`shared` is off by default. On for a project with
`placement = isolated`, it lets the project share the isolated VM with
`--isolated` sessions and other shared projects, and their grants
(S06-04-vms-and-placement). It has no effect with `placement = work`.

#### Learn mode

A learn-mode session (FR10-learn-mode) needs `placement = work`, and is
refused with `--isolated`.

That isn't a vouch for the repository, because every new project has
`placement = work`. The condition keeps learn mode in the work VM,
where the project's sessions run and its learned allowlist applies. It
keeps learn mode out of sessions the user isolated.

A learn period stays attributable to one project for another reason:
`wb-hostd` runs it only while no other project has a session in the VM
(S07-17-approvals-and-learning).

#### `config_trust`

`config_trust` says whether the repository's `.wraithbox/` is read
("Repository-supplied configuration").

- It is off for a new project.
- Only `wb trust` sets it, after the boundary check.
- `wb untrust` and `wb project confirm` clear it (S05-04-naming).
- Learn mode doesn't look at it.

The word "trusted" alone names neither `placement` nor `config_trust`.

### Repository-supplied configuration

A repository can hold a `.wraithbox/` directory (`config.toml`,
`policy.yaml`). It is ignored unless the project's `config_trust` is on
(`wb trust`).

Even then it may only add:

- allowed hosts;
- a toolchain manifest;
- HTTP rules.

Every addition is shown in `wb policy explain`.

It can never add credential bindings, switch hosts to pass-through, or
add a raw TCP entry (SEC09-host-policy).

Repository configuration that sets `enforcement: audit` on an endpoint
is refused at load, with an error that names the host, like a pass host
or a raw TCP entry. An approved rule can't be in `audit` either
(S09-09-approvals, "Approved rule").

#### Closed keys

It is parsed against a closed list of those keys. Any other key is a
load error, so it can't set `placement` or `config_trust`.

A rule on a built-in host whose profile part is off is a load error too
(S07-11-http-policy, "Profile parts").

#### Which commit

It is read only from the host repository's checked-out `HEAD`. It is
never read from the landing or export repository, or from a push's
quarantine (S08-04-push-checks). So the guest can't change it by pushing.

Returned work that changes `.wraithbox/` is flagged
(S08-workspace-and-git). The guest can still propose such a change. So
the parser has a fuzz target (C03-policy-and-approvals, network policy
YAML).

## Decisions

### `placement` and `config_trust` are two settings

- **Decided:** I42 (B42-trust-placement).

### No `audit` from repository configuration

- **Decided:** I109.

**Status:** Draft
