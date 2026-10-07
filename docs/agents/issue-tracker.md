# Issue tracker: GitHub

Issues for this project are managed as GitHub issues in
[wraithbox/wraithbox](https://github.com/wraithbox/wraithbox/issues), the
same repository as the source code.

Use the `gh` CLI for all operations. `gh issue --help` lists them.

```bash
gh issue list --label needs-triage
gh issue view <number>
gh issue create --title "..." --body "..." --label bug --label needs-triage
gh issue edit <number> --add-label ready-for-agent --remove-label needs-triage
```

## Issue forms

`.github/ISSUE_TEMPLATE/` holds three forms for the web UI: bug report,
feature request, and spike. Each applies its type label and
`needs-triage`. `gh issue create` does not use the forms, so apply the
same labels by hand. Documentation and chore issues have no form; open
them blank.

A spike issue is titled `X<NN>-<slug>: <question>` and tracks one open question
from [X00-index](../spikes/X00-index.md). Give a new
question its `X*` number in X00-index rather than only in the issue.

## Labels

The generic labels are recorded in
[`.github/labels.yml`](../../.github/labels.yml), which is the source of
truth. Apply it to GitHub with `mise run gh:labels` (creates or updates,
never deletes).

| Kind     | Labels                                                                            |
| -------- | --------------------------------------------------------------------------------- |
| Type     | `bug`, `enhancement`, `spike`, `documentation`, `chore` (one per issue)           |
| Priority | `priority: critical`, `priority: high`, `priority: medium`, `priority: low`       |
| Status   | `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human` (at most one)  |
| Other    | `blocked`, `wontfix`, `needs-vm`                                                  |

- `needs-triage`: a maintainer needs to evaluate the issue. New issues
  start here.
- `needs-info`: waiting on the reporter for more information.
- `ready-for-agent`: fully specified; an autonomous agent can do it
  without asking questions.
- `ready-for-human`: needs a human, for example a decision, access, or
  hardware an agent does not have.
- `blocked`: waits on another issue, a pull request, or an outside
  decision. Name the blocker in a comment, and record it with GitHub's
  issue relationships (`gh issue edit <n> --add-blocked-by <m>`).
  A `blocked` label whose blockers are all closed is stale: remove it
  with a `Triage (YYYY-MM-DD):` comment.
- `needs-vm`: the work needs a running macOS guest on the maintainer's
  Mac. A wave runs at most one such builder at a time
  ([`orchestration.md`](orchestration.md), "A wave"). Triage adds it
  when the issue says it needs a macOS guest.

Area labels (`area:go`, `area:spec`, ...) and component labels
(`comp:wb-netd`, ...) are recorded in
[`.github/labels-areas.yml`](../../.github/labels-areas.yml), applied by
the same `mise run gh:labels`, and explained in
[`planning.md`](planning.md) together with milestones and spikes. GitHub's
default labels (`duplicate`, `invalid`, `question`, `good first issue`,
`help wanted`, `accessibility`) and the ones dependabot adds to its pull
requests (`dependencies`, ...) also exist.
