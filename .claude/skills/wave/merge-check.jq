# cspell:ignore startswith Rollup
# Facts for the standing approval check of a run (docs/agents/orchestration.md, "Runs").
# gh pr view <n> --json headRefOid,changedFiles,mergeable,statusCheckRollup,closingIssuesReferences,reviews,comments --jq "$(cat .claude/skills/wave/merge-check.jq)"
def trusted: .author.login == "lsimons" or .author.login == "lsimons-bot";
def latest($kind): [.reviews[] | select(trusted) | select(.body | startswith($kind))] | last
  | if . == null then null else {verdict: ([.body | scan("Verdict: [a-z ]+")] | last), commit: .commit.oid} end;
{
  head: .headRefOid, changed: .changedFiles, mergeable,
  issues: [.closingIssuesReferences[].number],
  checks: [.statusCheckRollup[] | if .__typename == "CheckRun" then (if .status == "COMPLETED" then .conclusion else "PENDING" end) else .state end] | group_by(.) | map({(.[0]): length}) | add,
  code: latest("Code review"),
  security: latest("Security review"),
  rechecked: [.comments[] | select(trusted) | select(.body | startswith("re-checked:")) | .body | split("\n")[0]]
}
