# GitHub organization and repository settings

Settings for the `wraithbox` organization and the `wraithbox/wraithbox`
repository are stored in GitHub, not in this tree. This page records what they
are meant to be, why, and how to apply them with `gh api`, so they can
be checked and re-applied. Last reviewed 2026-10-03.

Every command below is idempotent: it sets a value, it does not toggle.

## Constraints on these settings

- The organization is on the **Team** plan and the repository is
  **private**. Rulesets work. Secret scanning and code scanning on a
  private repository need the paid GitHub Secret Protection and GitHub
  Code Security add-ons, billed per active committer, so they stay off
  until the owner subscribes to them or makes the repository public.
  Private vulnerability reporting, and approval of workflow runs from
  forks, exist only for public repositories.
- Organization-level settings need a token with the `admin:org` scope:
  `gh auth refresh -h github.com -s admin:org`.

## Repository: applied

Needs a token with `repo` scope and admin rights on the repository.

| Setting | Value | Why |
| ------- | ----- | --- |
| Dependabot alerts | on | Supply-chain policy (S10-tech-stack) |
| Dependabot security updates | on | Same; `.github/dependabot.yml` covers version updates |
| Actions allowed | GitHub-owned plus `jdx/mise-action@*`, `zizmorcore/zizmor-action@*` | Only actions the workflows use; a new third-party action needs an explicit entry here |
| Actions SHA pinning required | on | Enforces the "pin to full-length commit SHA" rule from `AGENTS.md` |
| Default `GITHUB_TOKEN` permissions | read | Workflows ask for more per job |
| Actions can approve pull requests | off | |
| Squash merge | off | Owner prefers rebase, then merge commit |
| Rebase merge, merge commit | on | Stacked branches need merge commits |
| Delete branch on merge | on | |
| Suggest updating PR branches | on | |
| Wiki, Projects | off | Unused; the design is in `docs/spec/` |
| Ruleset `main` on the default branch | active | See [Default branch ruleset](#default-branch-ruleset) |

```sh
R=repos/wraithbox/wraithbox
gh api -X PUT "$R/vulnerability-alerts"
gh api -X PUT "$R/automated-security-fixes"
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
```

Adding a third-party action to a workflow means adding its
`owner/repo@*` pattern to both `selected-actions` calls on this page
(repository and organization) in the same pull request, and applying
them; otherwise the workflow fails to start.

### Default branch ruleset

The ruleset `main` (ID 24412616) covers `~DEFAULT_BRANCH` only, so
pushing feature branches and opening pull requests are unaffected. It:

- blocks deletion and force pushes;
- requires a pull request with zero approvals, because a solo
  maintainer cannot approve their own pull request; the CI status
  checks are the gate;
- allows the rebase and merge methods, matching the repository;
- requires the CI checks below, from GitHub Actions (integration ID
  15368);
- lets the repository Admin role (actor ID 5) bypass it, so the owner can
  still repair `main` in an emergency.

The ruleset deliberately leaves out `required_linear_history`: it would
forbid the merge commits that stacked branches need.

`Dependency vulnerability scan` is deliberately **not** required yet.
It fails on `main` because of
[GHSA-ch52-4w7c-c8xp](https://osv.dev/GHSA-ch52-4w7c-c8xp)
(`http-cache-semantics` in `packages/wraithbox-doc/bun.lock`), which has no fixed
version. Add it to `required_status_checks` once that is resolved.

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
      "require_code_owner_review": false,
      "require_last_push_approval": false,
      "required_review_thread_resolution": false,
      "allowed_merge_methods": ["rebase", "merge"]}},
    {"type": "required_status_checks", "parameters": {
      "strict_required_status_checks_policy": false,
      "do_not_enforce_on_create": false,
      "required_status_checks": [
        {"context": "Go (ubuntu-24.04)", "integration_id": 15368},
        {"context": "Go (macos-26)", "integration_id": 15368},
        {"context": "Go (windows-2025)", "integration_id": 15368},
        {"context": "Swift", "integration_id": 15368},
        {"context": "Docs", "integration_id": 15368},
        {"context": "GitHub Actions (lint + audit)", "integration_id": 15368}]}}
  ]
}
```

Save as `ruleset.json`, then update the existing ruleset with
`gh api -X PUT repos/wraithbox/wraithbox/rulesets/24412616 --input ruleset.json`
(or create it with `POST .../rulesets`). The check names are the job
names from `.github/workflows/ci.yml` and change when the runner matrix
does. Check them against a recent CI run on `main` with
`gh api repos/wraithbox/wraithbox/commits/main/check-runs --jq '.check_runs[].name'`.

## Organization: applied

Needs `admin:org`.

| Setting | Value |
| ------- | ----- |
| Members can create repositories (public, private) | no |
| Members can fork private repositories | no |
| Members can create teams | no |
| Dependency graph, Dependabot alerts, Dependabot security updates for new repositories | on |
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

## Not available yet

- Secret scanning and push protection (paid add-on for private
  repositories, or free once public): `PATCH repos/wraithbox/wraithbox`
  with `{"security_and_analysis": {"secret_scanning": {"status": "enabled"},
  "secret_scanning_push_protection": {"status": "enabled"}}}`.
- Code scanning default setup (paid add-on for private repositories, or
  free once public; CodeQL covers Go, Swift, JavaScript/TypeScript and
  Actions, and Swift needs a macOS runner):
  `gh api -X PATCH repos/wraithbox/wraithbox/code-scanning/default-setup -f state=configured`.
- Private vulnerability reporting (public repositories only):
  `gh api -X PUT repos/wraithbox/wraithbox/private-vulnerability-reporting`.
- Fork pull request workflow approval (public repositories only):
  `gh api -X PUT repos/wraithbox/wraithbox/actions/permissions/fork-pr-contributor-approval -f approval_policy=all_external_contributors`.
- `Dependency vulnerability scan` as a required check, once
  GHSA-ch52-4w7c-c8xp has a fix.
