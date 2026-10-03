# GitHub organization and repository settings

Settings for the `wraithbox` organization and the `wraithbox/wraithbox`
repository are stored in GitHub, not in this tree. This page records what they
are meant to be, why, and how to apply them with `gh api`, so they can
be checked and re-applied. Last reviewed 2026-10-03.

Every command below is idempotent: it sets a value, it does not toggle.

## Constraints on these settings

- The repository is **private** on the organization's **Free** plan.
  On that combination GitHub offers no rulesets, no branch protection,
  no secret scanning, no code scanning, and no private vulnerability
  reporting (the API answers 403 "Upgrade to GitHub Pro or make this
  repository public"). Making the repository public, or moving the
  organization to Team, unlocks them; the commands are listed under
  [Blocked by plan](#blocked-by-plan) so they can be applied then.
- Organization-level settings need a token with the `admin:org` scope:
  `gh auth refresh -h github.com -s admin:org`.

## Repository: applied

Needs a token with `repo` scope and admin rights on the repository.

| Setting | Value | Why |
| ------- | ----- | --- |
| Dependabot alerts | on | Supply-chain policy (spec 010) |
| Dependabot security updates | on | Same; `.github/dependabot.yml` covers version updates |
| Actions allowed | GitHub-owned plus `jdx/mise-action@*`, `zizmorcore/zizmor-action@*` | Only actions the workflows use; a new third-party action needs an explicit entry here |
| Actions SHA pinning required | on | Enforces the "pin to full-length commit SHA" rule from `AGENTS.md` |
| Default `GITHUB_TOKEN` permissions | read | Workflows ask for more per job |
| Actions can approve pull requests | off | |
| Squash merge | off | Owner prefers rebase, then merge commit |
| Rebase merge, merge commit | on | Merge commits stay available for stacked branches |
| Delete branch on merge | on | |
| Suggest updating PR branches | on | |
| Wiki, Projects | off | Unused; the design is in `docs/spec/` |

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
`owner/repo@*` pattern to the `selected-actions` call above in the same
pull request, and applying it; otherwise the workflow fails to start.

## Organization: to apply

Needs `admin:org`. Not yet applied.

```sh
gh api -X PATCH orgs/wraithbox \
  -f default_repository_permission=read \
  -F members_can_create_repositories=false \
  -F members_can_create_public_repositories=false \
  -F members_can_create_private_repositories=false \
  -F members_can_fork_private_repositories=false \
  -F members_can_delete_repositories=false \
  -F members_can_change_repo_visibility=false \
  -F members_can_invite_outside_collaborators=false \
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

Two-factor authentication: every member has 2FA enabled, so
**Settings > Authentication security > Require two-factor
authentication** removes nobody. Do **not** also tick "Only allow secure
two-factor methods" until every member, including the owner, has moved
off SMS: GitHub removes members whose only method is insecure.

## Blocked by plan

Apply once the repository is public or the organization is on Team.

- Secret scanning and push protection:
  `gh api -X PATCH repos/wraithbox/wraithbox --input -` with
  `{"security_and_analysis": {"secret_scanning": {"status": "enabled"},
  "secret_scanning_push_protection": {"status": "enabled"}}}`.
- Private vulnerability reporting (public repositories only):
  `gh api -X PUT repos/wraithbox/wraithbox/private-vulnerability-reporting`.
- Code scanning default setup (CodeQL covers Go, Swift, JavaScript/TypeScript
  and Actions; Swift needs a macOS runner):
  `gh api -X PATCH repos/wraithbox/wraithbox/code-scanning/default-setup -f state=configured`.
- Fork pull request workflow approval (public repositories only):
  `gh api -X PUT repos/wraithbox/wraithbox/actions/permissions/fork-pr-contributor-approval -f approval_policy=all_external_contributors`.
- A ruleset on the default branch. It requires a pull request with zero
  approvals, because a solo maintainer cannot approve their own pull
  request. The CI status checks are the gate. Repository admins can
  bypass it, so the owner can still repair `main` in an emergency.
  Feature branches are not covered, so pushing branches and opening
  pull requests are unaffected.

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
        "required_status_checks": [
          {"context": "Go (ubuntu-24.04)"}, {"context": "Go (macos-26)"},
          {"context": "Go (windows-2025)"}, {"context": "Swift"},
          {"context": "Docs"}, {"context": "Dependency vulnerability scan"},
          {"context": "GitHub Actions (lint + audit)"}]}}
    ]
  }
  ```

  Save as `ruleset.json`, then
  `gh api -X POST repos/wraithbox/wraithbox/rulesets --input ruleset.json`
  (use `PUT .../rulesets/<id>` to update). Check the status check names
  against a recent CI run first: they are the job names from
  `.github/workflows/ci.yml`, and change when the runner matrix does.
  There is deliberately no `required_linear_history` rule: it would
  forbid the merge commits that stacked branches need.
