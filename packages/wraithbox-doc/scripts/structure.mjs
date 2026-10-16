// @ts-check
// Lists where each spec breaks a size limit of src/plugins/structure.mjs.
// The build fails only for the specs in its STRICT list, and so does this
// script.
import { checkStructure } from '../src/plugins/structure.mjs';

const { strict, other } = checkStructure();
for (const problem of other) console.log(`warning: ${problem}`);
for (const problem of strict) console.log(`error: ${problem}`);
console.log(`${strict.length} errors, ${other.length} warnings`);
process.exit(strict.length > 0 ? 1 : 0);
