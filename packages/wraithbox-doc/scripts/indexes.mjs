// @ts-check
// Rewrites the table in every index of docs/ (S00-index, FR00-index, …)
// from the documents and definitions it lists (src/plugins/indexes.mjs).
// `mise run doc:index`; the docs build fails when a table is out of date.
import { updateIndexes } from '../src/plugins/indexes.mjs';

const stale = updateIndexes({ write: true });
console.log(stale.length > 0 ? `updated ${stale.join(', ')}` : 'all indexes are up to date');
