import fs from 'node:fs';
import { execFileSync } from 'node:child_process';
import { RAW } from './lib.mjs';
const f = RAW + '/contributions.json';
if (!fs.existsSync(f)) {
  const q = `{user(login:"lsimons"){contributionsCollection(from:"2026-08-31T22:00:00Z",to:"2026-11-02T22:59:59Z"){
 contributionCalendar{totalContributions weeks{contributionDays{date contributionCount}}}
 commitContributionsByRepository(maxRepositories:100){repository{nameWithOwner isPrivate} contributions(first:100){totalCount nodes{occurredAt commitCount}}}
 issueContributionsByRepository(maxRepositories:100){repository{nameWithOwner} contributions(first:100){totalCount nodes{occurredAt}}}
 pullRequestContributionsByRepository(maxRepositories:100){repository{nameWithOwner} contributions(first:100){totalCount nodes{occurredAt}}}
 pullRequestReviewContributionsByRepository(maxRepositories:100){repository{nameWithOwner} contributions(first:100){totalCount nodes{occurredAt}}}
 restrictedContributionsCount }}}`;
  fs.writeFileSync(f, execFileSync('gh', ['api', 'graphql', '-f', 'query=' + q], { encoding: 'utf8', maxBuffer: 1 << 28 }));
}
const cc = JSON.parse(fs.readFileSync(f)).data.user.contributionsCollection;
console.log(cc.contributionCalendar.totalContributions, cc.restrictedContributionsCount);
for (const k of ['commit','issue','pullRequest','pullRequestReview']) { const a = cc[k + 'ContributionsByRepository']; console.log(k, a.length, a.map((x) => x.contributions.totalCount > x.contributions.nodes.length ? 'TRUNC' : '').join('')); }
