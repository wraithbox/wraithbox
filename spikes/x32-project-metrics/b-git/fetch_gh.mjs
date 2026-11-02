import fs from 'node:fs';
import { execFileSync } from 'node:child_process';
import { RAW } from './lib.mjs';
function gql(query, vars) {
  const args = ['api', 'graphql', '-f', 'query=' + query];
  for (const [k, v] of Object.entries(vars)) args.push(v === null ? '-F' : '-f', v === null ? k + '=null' : `${k}=${v}`);
  return JSON.parse(execFileSync('gh', args, { encoding: 'utf8', maxBuffer: 1 << 29 }));
}
function paged(name, conn, nodeSel, size) {
  let cursor = null, i = 0;
  for (;;) {
    const f = `${RAW}/${name}_${String(i).padStart(3, '0')}.json`;
    let r;
    if (fs.existsSync(f)) r = JSON.parse(fs.readFileSync(f));
    else {
      const q = `query($c:String){repository(owner:"wraithbox",name:"wraithbox"){${conn}(first:${size},after:$c,orderBy:{field:CREATED_AT,direction:ASC}){pageInfo{hasNextPage endCursor} nodes{${nodeSel}}}}}`;
      r = gql(q, { c: cursor }); if (r.errors) throw new Error(JSON.stringify(r.errors));
      fs.writeFileSync(f, JSON.stringify(r));
    }
    const pg = r.data.repository[conn].pageInfo; i++;
    if (!pg.hasNextPage) break; cursor = pg.endCursor;
  }
}
const cm = 'comments(first:100){totalCount nodes{author{login} createdAt body}}';
paged('issues', 'issues', `number title createdAt closedAt state stateReason author{login} labels(first:20){nodes{name}} milestone{title number} assignees(first:5){nodes{login}} ${cm} timelineItems(itemTypes:[CLOSED_EVENT],last:1){nodes{... on ClosedEvent{actor{login} createdAt closer{__typename ... on PullRequest{number} ... on Commit{oid}}}}}`, 40);
paged('prs', 'pullRequests', `number title createdAt mergedAt closedAt state isDraft headRefName author{login} mergedBy{login} additions deletions changedFiles commits{totalCount} labels(first:20){nodes{name}} body closingIssuesReferences(first:20){nodes{number}} reviews(first:100){totalCount nodes{author{login} state createdAt body comments{totalCount}}} ${cm}`, 20);
console.log('done');
