// Fetch all issues and merged PRs of wraithbox/wraithbox via gh GraphQL.
// Usage: bun fetch.mjs <outdir>
import { writeFileSync } from "node:fs";
const out = process.argv[2];

function gql(query, vars) {
  const args = ["api", "graphql", "-f", `query=${query}`];
  for (const [k, v] of Object.entries(vars)) if (v != null) args.push("-F", `${k}=${v}`);
  const p = Bun.spawnSync(["gh", ...args], { stdout: "pipe", stderr: "pipe" });
  if (p.exitCode !== 0) throw new Error(p.stderr.toString());
  return JSON.parse(p.stdout.toString());
}

const issueQ = `query($cursor:String){ repository(owner:"wraithbox",name:"wraithbox"){
 issues(first:25, after:$cursor, orderBy:{field:CREATED_AT,direction:ASC}){ pageInfo{hasNextPage endCursor}
 nodes{ number title state stateReason createdAt closedAt author{login}
  milestone{title} labels(first:30){nodes{name}} assignees(first:5){nodes{login}}
  blockedBy(first:50){nodes{number state}} blocking(first:50){nodes{number state}}
  parent{number} subIssues(first:50){nodes{number state}}
  closedByPullRequestsReferences(first:10, includeClosedPrs:false){nodes{number merged mergedAt}}
  comments(first:100){nodes{createdAt author{login} body}}
  timelineItems(first:200, itemTypes:[ASSIGNED_EVENT,UNASSIGNED_EVENT,LABELED_EVENT,UNLABELED_EVENT,MILESTONED_EVENT,DEMILESTONED_EVENT,CLOSED_EVENT,REOPENED_EVENT]){ nodes{ __typename
   ... on AssignedEvent{createdAt assignee{... on User{login}}}
   ... on UnassignedEvent{createdAt}
   ... on LabeledEvent{createdAt label{name}}
   ... on UnlabeledEvent{createdAt label{name}}
   ... on MilestonedEvent{createdAt milestoneTitle}
   ... on DemilestonedEvent{createdAt milestoneTitle}
   ... on ClosedEvent{createdAt}
   ... on ReopenedEvent{createdAt} } }
 } } } }`;

const prQ = `query($cursor:String){ repository(owner:"wraithbox",name:"wraithbox"){
 pullRequests(first:25, after:$cursor, states:[MERGED], orderBy:{field:CREATED_AT,direction:ASC}){ pageInfo{hasNextPage endCursor}
 nodes{ number title createdAt mergedAt additions deletions changedFiles headRefName author{login}
  closingIssuesReferences(first:10){nodes{number}}
  files(first:100){nodes{path additions deletions}}
  reviews(first:50){nodes{state submittedAt author{login} body}}
  comments(first:100){nodes{createdAt author{login} body}}
  commits(first:1){totalCount}
 } } } }`;

function all(q, key) {
  let cursor = null, res = [];
  for (;;) {
    const d = gql(q, { cursor });
    const c = d.data.repository[key];
    res.push(...c.nodes);
    console.error(key, res.length);
    if (!c.pageInfo.hasNextPage) break;
    cursor = c.pageInfo.endCursor;
  }
  return res;
}
writeFileSync(`${out}/issues.json`, JSON.stringify(all(issueQ, "issues"), null, 1));
writeFileSync(`${out}/prs.json`, JSON.stringify(all(prQ, "pullRequests"), null, 1));
