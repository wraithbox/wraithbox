# GitHub organization and repository settings

Settings for the `wraithbox` organization and the `wraithbox/wraithbox`
repository are stored in GitHub, not in this tree. This page records what they
are meant to be, why, and how to apply them with `gh api`, so they can
be checked and re-applied. Last reviewed 2026-11-10.

Every command below is idempotent: it sets a value, it does not toggle.

## Constraints on these settings

- The organization is on the **Team** plan and the repository is
  **public**. Rulesets work. Secret scanning, push protection, code
  scanning, and private vulnerability reporting are free for a public
  repository, and all are on.
- Actions minutes are free for a public repository, and every
  workflow's logs are public. A workflow never prints a secret or a
  token.
- Organization-level settings need a token with the `admin:org` scope:
  `gh auth refresh -h github.com -s admin:org`.

## Repository: applied

Needs a token with `repo` scope and admin rights on the repository.

| Setting | Value | Why |
| ------- | ----- | --- |
| Dependabot alerts | on | Supply-chain policy (S10-tech-stack). Renovate reads them and opens the fixes |
| Dependabot security updates | off | Renovate opens version and security updates ([Renovate](#renovate)) |
| Secret scanning and push protection | on | A pushed credential raises an alert, and push protection rejects a push that contains a known token format |
| Code scanning | advanced setup | CodeQL for Go, Swift, JavaScript/TypeScript and Actions, from `.github/workflows/codeql.yml` and `codeql-swift.yml`. They scan `main` on each push and once a week, and `workflow_dispatch` starts them by hand. Pull requests aren't scanned, because the Swift scan of each one held a macOS runner for 12 to 17 minutes (I752). A push runs the Swift scan only when it changes a Swift input (the workflow's `paths`). The weekly run always scans Swift |
| Private vulnerability reporting | on | `SECURITY.md` sends reporters here, because a vulnerability is never a public issue |
| Actions allowed | GitHub-owned plus `jdx/mise-action@*`, `zizmorcore/zizmor-action@*` | Only actions the workflows use; a new third-party action needs an explicit entry here |
| Actions SHA pinning required | on | Enforces the "pin to full-length commit SHA" rule from `AGENTS.md` |
| Default `GITHUB_TOKEN` permissions | read | Workflows ask for more per job |
| Actions can approve pull requests | off | |
| Fork pull request workflow approval | every outside contributor | A fork's workflow runs on GitHub's runners with public logs. The maintainer reads each outside pull request before its workflows run, not only a contributor's first |
| Environment `renovate`, deployable from `main` only | set | Holds the Renovate App's secrets, which workflows on other branches can't read ([Renovate](#renovate)) |
| Squash merge | off | Owner prefers rebase, then merge commit |
| Rebase merge, merge commit | on | Stacked branches need merge commits |
| Delete branch on merge | on | |
| Suggest updating PR branches | on | |
| Wiki, Projects | off | Unused; the design is in `docs/spec/` |
| Ruleset `main` on the default branch | active | See [Default branch ruleset](#default-branch-ruleset) |
| Ruleset `train` on `train/**` | active | See [Train ruleset](#train-ruleset) |

```sh
R=repos/wraithbox/wraithbox
gh api -X PUT "$R/vulnerability-alerts"
gh api -X DELETE "$R/automated-security-fixes"
gh api -X PATCH "$R" --input - <<'JSON'
{"security_and_analysis": {"secret_scanning": {"status": "enabled"},
 "secret_scanning_push_protection": {"status": "enabled"}}}
JSON
# Advanced setup: the workflows upload the results, so default setup is off.
gh api -X PATCH "$R/code-scanning/default-setup" -f state=not-configured
gh api -X PUT "$R/private-vulnerability-reporting"
gh api -X PATCH "$R" -F has_wiki=false -F has_projects=false \
  -F allow_squash_merge=false -F allow_merge_commit=true -F allow_rebase_merge=true \
  -F delete_branch_on_merge=true -F allow_update_branch=true
gh api -X PUT "$R/actions/permissions" -F enabled=true \
  -f allowed_actions=selected -F sha_pinning_required=true
gh api -X PUT "$R/actions/permissions/selected-actions" \
  -F github_owned_allowed=true -F verified_allowed=false \
  -f 'patterns_allowed[]=jdx/mise-action@*' \
  -f 'patterns_allowed[]=zizmorcore/zizmor-action@*'
gh api -X PUT "$R/actions/permissions/workflow" \
  -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false
gh api -X PUT "$R/actions/permissions/fork-pr-contributor-approval" \
  -f approval_policy=all_external_contributors
```

Adding a third-party action to a workflow means adding its
`owner/repo@*` pattern to both `selected-actions` calls on this page
(repository and organization) in the same pull request, and applying
them; otherwise the workflow fails to start.

### Renovate

Renovate keeps the dependencies current (S02-toolchain, "Dependency
updates"). `.github/workflows/renovate.yml` runs it daily, as a GitHub
App of the `wraithbox` organization, on this repository only. Nothing is shared
with the maintainer's other Renovate setups: it has its own App,
secrets, and configuration.

Create the App under **Organization settings > Developer settings >
GitHub Apps > New GitHub App**:

- Name `wraithbox-renovate`, any homepage URL, webhook off.
- Repository permissions: Administration read, Checks read and write,
  Commit statuses read and write, Contents read and write, Dependabot
  alerts read, Issues read and write, Pull requests read and write, and
  Workflows read and write. Metadata read is implied. These are the
  permissions of [Renovate's GitHub App
  docs](https://docs.renovatebot.com/modules/platform/github/#running-as-a-github-app),
  and the ones the workflow asks for when it creates a token.
- Installable only on this account. Install it on `wraithbox/wraithbox`
  only, through **Only select repositories**.

Then create the environment `renovate`, which only `main` may deploy
to, and store the App's client ID and a private key in it. A workflow on
another branch can't read them. Anyone with write access can push such
a branch, `lsimons-bot` included.

```sh
gh api -X PUT repos/wraithbox/wraithbox/environments/renovate --input - <<'JSON'
{"deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}}
JSON
gh api -X POST repos/wraithbox/wraithbox/environments/renovate/deployment-branch-policies \
  -f name=main -f type=branch
gh secret set RENOVATE_CLIENT_ID -R wraithbox/wraithbox -e renovate
gh secret set RENOVATE_PRIVATE_KEY -R wraithbox/wraithbox -e renovate < wraithbox-renovate.private-key.pem
```

Delete the downloaded key file afterwards.

The App merges its own pull requests, by rebase, once every check has
passed. The `main` ruleset applies to it unchanged: it needs a pull
request and a passing `CI result`, and the App is not a bypass actor.
Renovate also waits for checks the ruleset doesn't require, such as
`Dependency vulnerability scan`. A red one stops the merge.

Run it by hand under **Actions > Renovate > Run workflow**, or:

```sh
gh workflow run renovate.yml -R wraithbox/wraithbox                   # everything due now
gh workflow run renovate.yml -R wraithbox/wraithbox -f dryRun=lookup  # report only
```

A manual run ignores the Monday schedule unless `force=false`. Each run
uploads the `renovate-report` artifact, and the **Dependency
Dashboard** issue lists pending, held, and failed updates.

### Default branch ruleset

The ruleset `main` (ID 24412616) covers `~DEFAULT_BRANCH` only, so
pushing feature branches and opening pull requests are unaffected. It:

- blocks deletion and force pushes;
- requires a pull request with zero approvals, because a solo
  maintainer cannot approve their own pull request; the CI status
  checks are the gate;
- allows the rebase and merge methods, matching the repository;
- requires the `CI result` and `Dependency vulnerability scan` checks,
  from GitHub Actions (integration ID 15368);
- lets the repository Admin role (actor ID 5) bypass it, so the owner can
  still repair `main` in an emergency.

The ruleset deliberately leaves out `required_linear_history`: it would
forbid the merge commits that stacked branches need.

### Train ruleset

The ruleset `train` (ID 26807128) covers `refs/heads/train/**`, the
integration branches of campaigns (`docs/agents/campaigns.md`). It
blocks deletion and force pushes, and nothing else. It requires no pull
request and no checks, because the campaign manager pushes report
commits, reverts and merges from `main` to a train directly. CI runs
on every push to `train/**` as the train's pull request. Blocking deletion also keeps "Delete
branch on merge" from deleting a train after it merges, so trains are
kept.

```sh
gh api -X POST repos/wraithbox/wraithbox/rulesets --input - <<'JSON'
{
  "name": "train",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/heads/train/**"], "exclude": [] } },
  "rules": [{ "type": "deletion" }, { "type": "non_fast_forward" }]
}
JSON
```

`CI result` and `Dependency vulnerability scan` are the required
checks. On a pull request, the `changes` job of `.github/workflows/ci.yml` runs `scripts/ci-changes.mjs`
to list the jobs the changed files need. The workflow skips `Go`, the
`Fuzz smoke` shards, `Swift`, and the site check and build steps of `Docs` when that list
leaves them out. A change that only the app bundle reads runs the
`Swift` job's bundle steps and skips its lint, contract, and test steps. A push to `main` and `workflow_dispatch` run
everything. A skipped matrix job doesn't report the per-OS `Go (…)` or
per-shard `Fuzz smoke (…)` check names, so a ruleset that required them would block every pull
request that skipped Go. A skipped job counts as passed for a required
check, so requiring `Swift` directly would pass a run that skipped it by
mistake. `CI result` runs on every run, even a cancelled one, and fails
unless each job it lists passed or was skipped because its inputs did
not change. It also fails when change detection failed, after the
jobs ran as if every file had changed. A job added to `ci.yml` goes into
the `needs:` list of `CI result` and into `GATED` in
`scripts/ci-changes.mjs`. `scripts/ci-changes.test.mjs` fails when the
jobs of `ci.yml`, other than `vuln` and `result`, or the `needs:` of
`CI result` differ from `GATED`. The verdict also fails a run when a
job of `GATED` is missing from `needs:`.

The `CI result` requirement was applied on 2026-10-16, right after the
pull request that added the `CI result` job (I58) merged.

`Dependency vulnerability scan` (the `vuln` job) runs on every run, not
gated by `scripts/ci-changes.mjs`, so it reports on every pull request
and can be required directly. The ruleset requires it since 2026-11-10.
Before that it stayed out while
[GHSA-ch52-4w7c-c8xp](https://osv.dev/GHSA-ch52-4w7c-c8xp) failed it on
`main`, until I55 moved that dependency to the fixed release. An
advisory published for a pinned dependency fails the scan on every pull
request until Renovate's fix merges, or until the advisory gets an
`osv-scanner.toml` entry (`AGENTS.md`, "Supply chain").

```json
{
  "name": "main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
  "bypass_actors": [{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}],
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"},
    {"type": "pull_request", "parameters": {
      "required_approving_review_count": 0,
      "dismiss_stale_reviews_on_push": false,
      "required_reviewers": [],
      "require_code_owner_review": false,
      "dismissal_restriction": {"enabled": false, "allowed_actors": []},
      "require_last_push_approval": false,
      "required_review_thread_resolution": false,
      "require_extra_approval_for_unattributed_changes": true,
      "allowed_merge_methods": ["rebase", "merge"]}},
    {"type": "required_status_checks", "parameters": {
      "strict_required_status_checks_policy": false,
      "do_not_enforce_on_create": false,
      "required_status_checks": [
        {"context": "CI result", "integration_id": 15368},
        {"context": "Dependency vulnerability scan", "integration_id": 15368}]}}
  ]
}
```

Save as `ruleset.json`, then update the existing ruleset with
`gh api -X PUT repos/wraithbox/wraithbox/rulesets/24412616 --input ruleset.json`
(or create it with `POST .../rulesets`). A `PUT` replaces the whole
ruleset. A setting left out of the JSON changes on the next `PUT`, and
the JSON above lists every setting the live ruleset has. Compare it
with `gh api repos/wraithbox/wraithbox/rulesets/24412616` before you
apply it. The check names are the `name:` of the `result` and `vuln`
jobs in `.github/workflows/ci.yml`. Check them against a
recent CI run on `main` with
`gh api repos/wraithbox/wraithbox/commits/main/check-runs --jq '.check_runs[].name'`.

## Organization: applied

Needs `admin:org`.

| Setting | Value |
| ------- | ----- |
| Members can create repositories (public, private) | no |
| Members can fork private repositories | no |
| Members can create teams | no |
| Dependency graph, Dependabot alerts, Dependabot security updates for new repositories | on (a new repository has no Renovate until it gets its own `renovate.json`) |
| Actions allowed | selected: GitHub-owned plus `jdx/mise-action@*`, `zizmorcore/zizmor-action@*` |
| Actions SHA pinning required | on |
| Default `GITHUB_TOKEN` permissions | read |
| Actions can approve pull requests | off |

```sh
gh api -X PATCH orgs/wraithbox \
  -F members_can_create_repositories=false \
  -F members_can_create_public_repositories=false \
  -F members_can_create_private_repositories=false \
  -F members_can_fork_private_repositories=false \
  -F members_can_create_teams=false \
  -F dependency_graph_enabled_for_new_repositories=true \
  -F dependabot_alerts_enabled_for_new_repositories=true \
  -F dependabot_security_updates_enabled_for_new_repositories=true
gh api -X PUT orgs/wraithbox/actions/permissions \
  -f enabled_repositories=all -f allowed_actions=selected -F sha_pinning_required=true
gh api -X PUT orgs/wraithbox/actions/permissions/selected-actions \
  -F github_owned_allowed=true -F verified_allowed=false \
  -f 'patterns_allowed[]=jdx/mise-action@*' \
  -f 'patterns_allowed[]=zizmorcore/zizmor-action@*'
gh api -X PUT orgs/wraithbox/actions/permissions/workflow \
  -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false
```

Owners keep every permission switched off above; it only limits other
members (today: `lsimons-bot`).

## Organization: to apply in the web UI

The REST API accepts these fields but silently ignores them, so set
them under **Settings > Member privileges**:

- Repository deletion and transfer: off.
- Repository visibility change: off.
- Repository invitations (outside collaborators): off.

Two-factor authentication: every member has 2FA enabled, so
**Settings > Authentication security > Require two-factor
authentication** removes nobody. Do **not** also tick "Only allow secure
two-factor methods" until every member, including the owner, has moved
off SMS: GitHub removes members whose only method is insecure.

Base permission is `read`, so every member, including `lsimons-bot`,
can read every repository. Consider `none` plus explicit per-repository
grants. The owner has kept `read` for now.
