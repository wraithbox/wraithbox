// @ts-check
// Removes the VM bundles a spike left in the spike cache
// (`mise run spike:clean x<NN>`), or lists what the cache holds
// (`mise run spike:clean`). The layout and when to run it are in
// docs/agents/planning.md, "Spikes".
//
// The cache is `~/Library/Caches/wraithbox-spikes/`:
//
//   - `restore-images/` is a shared cache of macOS restore images. It is
//     kept, and this script never removes it.
//   - `x<NN>/` and `x<NN>-<anything>/` hold the bundles of spike
//     `X<NN>-...`. They are throwaway once the spike's result is written
//     up and its numbers are committed to the spike branch.
//
// Agents may run this task without a broad `rm -rf` permission
// (`.claude/settings.json`), so it refuses anything else. Each argument
// is a spike ID, `x<NN>`, and selects only direct children of the cache
// that are real directories named `x<NN>` or `x<NN>-...`. A symbolic link,
// a path that resolves outside the cache, and `restore-images/` are
// refused, and so is a cache directory that is itself a symbolic link.
// Every refusal prints the rule that made it and exits 1 before anything
// is removed. The cache path comes from the account database, not from
// `$HOME`, so no environment variable can point the task elsewhere.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

export const KEEP = 'restore-images';
const SPIKE_ID = /^x(\d+)$/i;
const SPIKE_ENTRY = /^x(\d+)(?:-.+)?$/;

/**
 * Whether a cache entry belongs to a spike. The number is compared, not
 * the text, so `x8` and `x08` select `x08/` and `x08-outside/`.
 * @param {string} name the entry's name
 * @param {number} id the spike's number
 */
export function selects(name, id) {
	const match = SPIKE_ENTRY.exec(name);
	return match !== null && Number(match[1]) === id;
}

/** The cache directory for the current user. */
export function defaultRoot() {
	return path.join(os.userInfo().homedir, 'Library', 'Caches', 'wraithbox-spikes');
}

/**
 * Bytes on disk under a path, counted like `du`: allocated blocks, links
 * not followed. APFS clones share blocks, so the space a removal frees can
 * be less than this.
 * @param {string} target
 * @returns {number}
 */
export function diskBytes(target) {
	const stat = fs.lstatSync(target);
	let total = stat.blocks * 512;
	if (!stat.isDirectory()) return total;
	for (const name of fs.readdirSync(target)) total += diskBytes(path.join(target, name));
	return total;
}

/** @param {number} bytes */
export function human(bytes) {
	const units = ['B', 'KB', 'MB', 'GB', 'TB'];
	let value = bytes;
	let unit = 0;
	while (value >= 1024 && unit < units.length - 1) {
		value /= 1024;
		unit += 1;
	}
	return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

/**
 * The cache directory with its symbolic links resolved, or a refusal. A
 * cache that does not exist yet is not an error: there is nothing in it.
 * @param {string} root
 * @returns {{ real: string | null, refusal: string | null }}
 */
function resolveRoot(root) {
	if (!fs.existsSync(root)) return { real: null, refusal: null };
	const stat = fs.lstatSync(root);
	if (stat.isSymbolicLink()) {
		return { real: null, refusal: `${root}: refused, the cache directory is a symbolic link (rule: the cache is a real directory)` };
	}
	if (!stat.isDirectory()) {
		return { real: null, refusal: `${root}: refused, the cache is not a directory (rule: the cache is a real directory)` };
	}
	return { real: fs.realpathSync(root), refusal: null };
}

/**
 * Checks one cache entry that a spike ID selected. Returns a refusal, or
 * null when the entry may be removed.
 * @param {string} real the resolved cache directory
 * @param {string} name the entry's name in it
 * @returns {string | null}
 */
export function entryRefusal(real, name) {
	const target = path.join(real, name);
	if (name === KEEP) return `${target}: refused, ${KEEP}/ is the shared image cache (rule: ${KEEP}/ is kept)`;
	const stat = fs.lstatSync(target);
	if (stat.isSymbolicLink()) return `${target}: refused, it is a symbolic link (rule: only real directories in the cache are removed)`;
	if (!stat.isDirectory()) return `${target}: refused, it is not a directory (rule: only real directories in the cache are removed)`;
	const resolved = fs.realpathSync(target);
	if (path.dirname(resolved) !== real || path.basename(resolved) === KEEP) {
		return `${target}: refused, it resolves to ${resolved} (rule: only direct children of ${real} are removed)`;
	}
	return null;
}

/**
 * What a run would remove: the entries the arguments select, or the
 * refusals. Nothing is removed while there is a refusal.
 * @param {string} root the cache directory
 * @param {string[]} args spike IDs, `x<NN>`
 * @returns {{ targets: string[], refusals: string[], unmatched: string[] }}
 */
export function plan(root, args) {
	const refusals = [];
	const ids = [];
	for (const arg of args) {
		const match = SPIKE_ID.exec(arg);
		if (match) ids.push(arg);
		else refusals.push(`${JSON.stringify(arg)}: refused, not a spike ID like x25 (rule: arguments are spike IDs)`);
	}
	const { real, refusal } = resolveRoot(root);
	if (refusal) refusals.push(refusal);
	if (refusals.length || real === null) return { targets: [], refusals, unmatched: ids };

	const names = fs.readdirSync(real).sort();
	const targets = [];
	const unmatched = [];
	for (const id of ids) {
		const selected = names.filter((name) => selects(name, Number(id.slice(1))));
		if (!selected.length) unmatched.push(id);
		for (const name of selected) {
			const found = entryRefusal(real, name);
			if (found) refusals.push(found);
			else targets.push(path.join(real, name));
		}
	}
	return { targets: refusals.length ? [] : targets, refusals, unmatched };
}

/**
 * Removes the spike bundles the arguments select, or lists the cache when
 * there are none. Returns the exit code.
 * @param {string} root the cache directory
 * @param {string[]} args spike IDs, `x<NN>`
 * @param {{ log: (line: string) => void, error: (line: string) => void }} out
 * @returns {number}
 */
export function run(root, args, out) {
	if (!args.length) return list(root, out);
	const { targets, refusals, unmatched } = plan(root, args);
	for (const line of refusals) out.error(line);
	if (refusals.length) {
		out.error('Removed nothing.');
		return 1;
	}
	for (const id of unmatched) out.log(`${id}: nothing in ${root}`);
	let freed = 0;
	for (const target of targets) {
		const bytes = diskBytes(target);
		// rmSync unlinks a symbolic link inside the tree and does not follow it.
		fs.rmSync(target, { recursive: true });
		freed += bytes;
		out.log(`removed ${target} (${human(bytes)})`);
	}
	out.log(`freed ${human(freed)} (allocated blocks; APFS clones can share some of them)`);
	return 0;
}

/**
 * Lists each entry of the cache with its size, and removes nothing.
 * @param {string} root
 * @param {{ log: (line: string) => void, error: (line: string) => void }} out
 * @returns {number}
 */
function list(root, out) {
	const { real, refusal } = resolveRoot(root);
	if (refusal) {
		out.error(refusal);
		return 1;
	}
	if (real === null) {
		out.log(`${root}: does not exist, nothing cached`);
		return 0;
	}
	const names = fs.readdirSync(real).sort();
	if (!names.length) out.log(`${real}: empty`);
	for (const name of names) {
		const note = name === KEEP ? ', kept' : '';
		out.log(`${human(diskBytes(path.join(real, name))).padStart(9)}  ${name}${note}`);
	}
	out.log('Remove a spike\'s bundles with `mise run spike:clean x<NN>`.');
	return 0;
}

if (import.meta.main) {
	const out = { log: console.log, error: console.error };
	process.exit(run(defaultRoot(), process.argv.slice(2), out));
}
