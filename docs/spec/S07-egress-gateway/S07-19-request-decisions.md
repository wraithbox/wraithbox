# S07-19 - Request Decisions

**Purpose:** How `wb-proxyd` decides a request on an inspected host:
the order of the steps, their rule names, what a git hosting write
is, and what `audit` relaxes.

## Design

### Order of evaluation

`wb-proxyd` decides each request on an inspected host in this order.
The first step that decides the request ends it.

1. *Form.* A method that isn't 1 to 32 token characters in upper case,
   or `*`, is refused with `http-method-form`. A path that doesn't
   canonicalize is refused with `http-path-form`
   (S07-08-requests-and-credentials, "Request form"). A WebSocket
   upgrade matches as a `GET` on its path, and one with another method
   is refused with `http-method-form`.
2. *Built-in denies.* On a covered host: `model-path-escape`,
   `git-archive`, `git-create-denied`, `model-event-log`,
   `registry-token`, `registry-publish` and
   `git-write-outside-project-set`. They match the canonical path in
   lowercase, and its stem: each segment cut at its first `;`, `.` or
   `%`, without a trailing `/`. A segment the cut would leave empty
   stays as it is. So `/forks;x`, `/forks.json`,
   `/forks%2F` and `/forks/` meet the deny of `/forks`. They hold in every mode and on every port, against any
   rule that allows the request.
3. *Parts.* The first built-in allow rule that matches puts the
   request in its part. A rule of git's transport matches only a
   transport path ("Writes to git hosting"). A part that is off refuses it with
   `profile-part-off`.
4. *GraphQL.* Until the operation filter is in place, every request
   to the GraphQL path of a built-in host is refused with
   `graphql-operation`.
5. *`deny_rules`.* A matching `deny_rules` entry of any source refuses
   the request with `http-deny-rule`.
6. *Allows.* A built-in rule of a part that is on, or a rule of any
   source, allows the request with `http-allowed`. Otherwise it is
   refused with `http-no-rule`.

A host and port in `audit` relaxes only `http-no-rule` and
`http-deny-rule`. The request goes on, and is logged with the rule
`audit-allowed` and the rule it broke (S07-16-tls-and-tunnels, "Not
relaxed by audit mode or approvals"). A guarded host can't be in
`audit` (S07-07-hosts-and-modes, "Guarded hosts"), and `wb-proxyd`
holds each host a profile covers in `enforce`, even if a policy put it
in `audit`.

Each decision is one log line (SEC10-audit). It holds the VM, policy
generation, host, port, method, canonical path, rule, the source of
the rule, the mode, and in `audit` the rule broken. A refusal is a
`403` whose body names the rule, host, method, path and source. For
`profile-part-off` it names the part and the policies that turned it
off (NFR06-explained-refusals).

The built-in allow rules apply on port 443, and on a remote's host on
the port of its `https` or `http` URL. A host a project's remote names
that S07-11-http-policy, "Covered hosts", doesn't list gets git's transport alone, in the part
`git`. It is off when the `git` part of any kind is off, and it gets
the archive denies of every kind.

### Writes to git hosting

S07-11-http-policy, "git hosting", allows writes only to the project
set. A write is any method other than `GET`, `HEAD` and `OPTIONS`,
and any request whose query names a `service` other than `git-upload-pack`.
A `POST` of `git-upload-pack` on a transport path is a fetch, and a request to the GraphQL
path is left to the operation filter. A write is allowed only when
`wb-proxyd` reads its repository from the path, and that repository is
in the project set:

- git's transport: the path before `/info/refs`, `/git-upload-pack` or
  `/git-receive-pack`, without a trailing `.git`. It is
  `/<owner>/<repo>` on GitHub and Gitea. On GitLab and a host a remote
  names, it has two segments or more, the first isn't `api`, and none
  is `-`, which GitLab reserves for its web routes. So
  an API path that ends in `/git-upload-pack` isn't a fetch;
- GitHub's REST API: `/repos/<owner>/<repo>` on `api.github.com` and
  `uploads.github.com`;
- Gitea and Forgejo: `/api/v1/repos/<owner>/<repo>`;
- GitLab: `/api/v4/projects/<id>`, where the id is the path with each
  `/` as `%2F`. The proxy can't read a repository from a numeric id.

Any other write, such as a web form `POST` or a request under
`/user/`, is refused with `git-write-outside-project-set`, whatever
rule allows it (SEC06-repo-writes). GitHub compares owner and
repository names without regard to case, and the other forges compare
them as they are. The port isn't compared. The gists, creation and fork denies are logged
with the rule `git-create-denied`, and hold for a repository in the
project set too.

## Decisions

### Built-in denies, decided in order

The order of evaluation, the rule names, the project set as a built-in
deny, and the two refusals `audit` relaxes.

- **Why:** a broad user rule such as `POST github.com/**` must not
  allow a push outside the project set, and a deny of a policy file
  must not be lifted by the built-in rules.
- **Decided:** I501.

**Status:** Draft
