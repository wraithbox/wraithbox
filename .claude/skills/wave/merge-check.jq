# cspell:ignore startswith Rollup elif
# Facts for the standing approval check of a run (docs/agents/orchestration.md, "Runs").
# gh pr view <n> --json headRefOid,changedFiles,mergeable,statusCheckRollup,closingIssuesReferences,reviews,comments --jq "$(cat .claude/skills/wave/merge-check.jq)"
def trusted: .author.login == "lsimons" or .author.login == "lsimons-bot";
def kind: if (.body | startswith("Code review")) then "code"
  elif (.body | startswith("Security review")) then "security" else null end;
def verdict: [.body | scan("Verdict: [a-z ]+")] | last;
([.reviews[] | select(trusted) | select(kind != null) | {kind: kind, verdict: verdict, commit: .commit.oid}]) as $r
| ($r | last | .commit) as $lastCommit
| {
  head: .headRefOid, changed: .changedFiles, mergeable,
  issues: [.closingIssuesReferences[].number],
  checks: [.statusCheckRollup[] | if .__typename == "CheckRun" then (if .status == "COMPLETED" then .conclusion else "PENDING" end) else .state end] | group_by(.) | map({(.[0]): length}) | add,
  code: ([$r[] | select(.kind == "code")] | last),
  security: ([$r[] | select(.kind == "security")] | last),
  lastCommitAllApprove: ($r | length > 0 and all(.[] | select(.commit == $lastCommit); .verdict == "Verdict: approve")),
  rechecked: [.comments[] | select(trusted) | select(.body | startswith("re-checked:")) | .body | split("\n")[0]]
}
