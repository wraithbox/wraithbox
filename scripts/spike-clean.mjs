// @ts-check
// Removes the VM bundles a spike left in the spike cache
// (`mise run spike:clean x<NN>`), or lists what the cache holds
// (`mise run spike:clean`). The layout and when to run it are in
// docs/agents/spikes.md, "VM bundles".
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
// that are real directories named `x<NN>` or `x<NN>-...`. It refuses:
//
//   - `restore-images/`, a symbolic link, a file, and a path that
//     resolves outside the cache, and a cache directory that is itself a
//     symbolic link;
//   - a spike directory that holds a `KEEP` file, which names the issue
//     that still needs the bundles;
//   - a spike directory with anything in it on another device, such as a
//     disk image's volume mounted inside a bundle, so a removal doesn't
//     reach into a volume that is mounted when the task starts.
//
// Every refusal prints the rule that made it and exits 1 before anything
// is removed. The cache path comes from the account database, not from
// `$HOME`, so no environment variable can point the task elsewhere.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

export const SHARED = 'restore-images';
export const KEEP_FILE = 'KEEP';
const SPIKE_ID = /^x(\d+)$/i;
const SPIKE_ENTRY = /^x(\d+)(?:-.+)?$/;

/**
 * @typedef {object} Fs the file system calls the tests replace
 * @property {(target: string) => fs.Stats} lstat
 * @property {(target: string) => void} remove removes a tree, unlinking
 *   symbolic links in it without following them
 */

/** @type {Fs} */
const realFs = {
	lstat: (target) => fs.lstatSync(target),
	remove: (target) => fs.rmSync(target, { recursive: true }),
};

/**
 * The current user's home directory from the account database. Bun's
 * `os.userInfo().homedir` and `os.homedir()` return `$HOME`, so neither is
 * used. The commands run by absolute path with an empty environment.
 * @returns {string}
 */
export function accountHome() {
	if (typeof process.getuid !== 'function') throw new Error('no user ID on this platform');
	const uid = String(process.getuid());
	const options = { encoding: /** @type {const} */ ('utf8'), env: {} };
	let home = '';
	if (process.platform === 'darwin') {
		const name = execFileSync('/usr/bin/id', ['-un', uid], options).trim();
		const record = execFileSync('/usr/bin/dscl', ['.', '-read', `/Users/${name}`, 'NFSHomeDirectory'], options);
		home = record.replace(/^NFSHomeDirectory:\s*/, '').trim();
	} else {
		home = execFileSync('/usr/bin/getent', ['passwd', uid], options).trim().split(':')[5] ?? '';
	}
	if (!path.isAbsolute(home)) throw new Error(`the account database gave no absolute home directory: ${JSON.stringify(home)}`);
	return home;
}

/** The cache directory for the current user. */
export function defaultRoot() {
	return path.join(accountHome(), 'Library', 'Caches', 'wraithbox-spikes');
}

/**
 * Bytes on disk under a path, counted like `du`: allocated blocks, links
 * not followed. APFS clones share blocks, so the space a removal frees can
 * be less than this. The walk stops at the first entry on a device other
 * than `dev` and returns its path as `foreign`.
 * @param {string} target
 * @param {number} dev the cache's device
 * @param {Fs} files
 * @returns {{ bytes: number, foreign: string | null }}
 */
export function walk(target, dev, files) {
	const stat = files.lstat(target);
	if (stat.dev !== dev) return { bytes: 0, foreign: target };
	let bytes = stat.blocks * 512;
	if (!stat.isDirectory()) return { bytes, foreign: null };
	for (const name of fs.readdirSync(target)) {
		const inner = walk(path.join(target, name), dev, files);
		if (inner.foreign) return { bytes, foreign: inner.foreign };
		bytes += inner.bytes;
	}
	return { bytes, foreign: null };
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
 * Whether a cache entry belongs to a spike. The number is compared, not
 * the text, so `x8` and `x08` select `x08/` and `x08-outside/`.
 * @param {string} name the entry's name
 * @param {number} id the spike's number
 */
export function selects(name, id) {
	const match = SPIKE_ENTRY.exec(name);
	return match !== null && Number(match[1]) === id;
}

/**
 * The cache directory with its symbolic links resolved, and its device, or
 * a refusal. A cache that does not exist yet is not an error: there is
 * nothing in it.
 * @param {string} root
 * @param {Fs} files
 * @returns {{ real: string | null, dev: number, refusal: string | null }}
 */
function resolveRoot(root, files) {
	if (!fs.existsSync(root)) return { real: null, dev: 0, refusal: null };
	const stat = files.lstat(root);
	if (stat.isSymbolicLink()) {
		return { real: null, dev: 0, refusal: `${root}: refused, the cache directory is a symbolic link (rule: the cache is a real directory)` };
	}
	if (!stat.isDirectory()) {
		return { real: null, dev: 0, refusal: `${root}: refused, the cache is not a directory (rule: the cache is a real directory)` };
	}
	return { real: fs.realpathSync(root), dev: stat.dev, refusal: null };
}

/**
 * The first line of a `KEEP` file in a spike directory, or null when there
 * is none.
 * @param {string} target
 * @returns {string | null}
 */
function keepReason(target) {
	const file = path.join(target, KEEP_FILE);
	/** @type {fs.Stats} */
	let stat;
	try {
		stat = fs.lstatSync(file);
	} catch {
		return null;
	}
	if (!stat.isFile()) return `(${KEEP_FILE} is not a regular file)`;
	const text = fs.readFileSync(file, 'utf8').split('\n')[0].trim().slice(0, 200);
	return text || '(no reason given)';
}

/**
 * Checks one cache entry that a spike ID selected, and sizes it.
 * @param {string} real the resolved cache directory
 * @param {number} dev the cache's device
 * @param {string} name the entry's name in it
 * @param {Fs} files
 * @returns {{ refusal: string | null, bytes: number }}
 */
export function checkEntry(real, dev, name, files) {
	const target = path.join(real, name);
	const refuse = (/** @type {string} */ refusal) => ({ refusal: `${target}: refused, ${refusal}`, bytes: 0 });
	if (name === SHARED) return refuse(`${SHARED}/ is the shared image cache (rule: ${SHARED}/ is kept)`);
	const stat = files.lstat(target);
	if (stat.isSymbolicLink()) return refuse('it is a symbolic link (rule: only real directories in the cache are removed)');
	if (!stat.isDirectory()) return refuse('it is not a directory (rule: only real directories in the cache are removed)');
	const resolved = fs.realpathSync(target);
	if (path.dirname(resolved) !== real || path.basename(resolved) === SHARED) {
		return refuse(`it resolves to ${resolved} (rule: only direct children of ${real} are removed)`);
	}
	const keep = keepReason(target);
	if (keep !== null) return refuse(`its ${KEEP_FILE} file says: ${keep} (rule: a directory with a ${KEEP_FILE} file is kept)`);
	const { bytes, foreign } = walk(target, dev, files);
	if (foreign) return refuse(`${foreign} is on another device (rule: detach mounted volumes first, a removal stays on the cache's device)`);
	return { refusal: null, bytes };
}

/**
 * The spike numbers the arguments name, each once, or the refusals.
 * @param {string[]} args
 * @returns {{ ids: Map<number, string>, refusals: string[] }}
 */
function parseIds(args) {
	const ids = new Map();
	const refusals = [];
	for (const arg of args) {
		const match = SPIKE_ID.exec(arg);
		if (!match) refusals.push(`${JSON.stringify(arg)}: refused, not a spike ID like x25 (rule: arguments are spike IDs)`);
		else if (!ids.has(Number(match[1]))) ids.set(Number(match[1]), arg);
	}
	return { ids, refusals };
}

/**
 * What a run would remove: the entries the arguments select with their
 * sizes, or the refusals. Every selected entry is checked and walked
 * before anything is removed, and nothing is removed while there is a
 * refusal.
 * @param {string} root the cache directory
 * @param {string[]} args spike IDs, `x<NN>`
 * @param {Fs} [files]
 * @returns {{ targets: { path: string, bytes: number }[], refusals: string[], unmatched: string[] }}
 */
export function plan(root, args, files = realFs) {
	const { ids, refusals } = parseIds(args);
	const { real, dev, refusal } = resolveRoot(root, files);
	if (refusal) refusals.push(refusal);
	if (refusals.length || real === null) return { targets: [], refusals, unmatched: [...ids.values()] };

	const names = fs.readdirSync(real).sort();
	const targets = [];
	const unmatched = [];
	for (const [id, arg] of ids) {
		const selected = names.filter((name) => selects(name, id));
		if (!selected.length) unmatched.push(arg);
		for (const name of selected) {
			const { refusal: found, bytes } = checkEntry(real, dev, name, files);
			if (found) refusals.push(found);
			else targets.push({ path: path.join(real, name), bytes });
		}
	}
	return { targets: refusals.length ? [] : targets, refusals, unmatched };
}

/**
 * @typedef {object} Out
 * @property {(line: string) => void} log
 * @property {(line: string) => void} error
 */

/**
 * Removes the spike bundles the arguments select, or lists the cache when
 * there are none. Returns the exit code.
 * @param {string} root the cache directory
 * @param {string[]} args spike IDs, `x<NN>`
 * @param {Out} out
 * @param {Fs} [files]
 * @returns {number}
 */
export function run(root, args, out, files = realFs) {
	if (!args.length) return list(root, out, files);
	const { targets, refusals, unmatched } = plan(root, args, files);
	for (const line of refusals) out.error(line);
	if (refusals.length) {
		out.error('Removed nothing.');
		return 1;
	}
	for (const arg of unmatched) out.log(`${arg}: nothing in ${root}`);
	let freed = 0;
	for (const target of targets) {
		try {
			files.remove(target.path);
		} catch (error) {
			out.error(`${target.path}: removal failed, part of it can be left: ${error instanceof Error ? error.message : error}`);
			out.error(`Stopped. Freed ${human(freed)} before the failure.`);
			return 1;
		}
		freed += target.bytes;
		out.log(`removed ${target.path} (${human(target.bytes)})`);
	}
	out.log(`freed ${human(freed)} (allocated blocks; APFS clones can share some of them)`);
	return 0;
}

/**
 * Lists each entry of the cache with its size, and removes nothing.
 * @param {string} root
 * @param {Out} out
 * @param {Fs} files
 * @returns {number}
 */
function list(root, out, files) {
	const { real, dev, refusal } = resolveRoot(root, files);
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
		const target = path.join(real, name);
		const { bytes, foreign } = walk(target, dev, files);
		const notes = [];
		if (name === SHARED) notes.push('kept');
		if (keepReason(target) !== null) notes.push(`${KEEP_FILE}: ${keepReason(target)}`);
		if (foreign) notes.push(`holds a mounted volume at ${foreign}`);
		out.log(`${human(bytes).padStart(9)}  ${name}${notes.length ? `, ${notes.join(', ')}` : ''}`);
	}
	out.log('Remove a spike\'s bundles with `mise run spike:clean x<NN>`.');
	return 0;
}

if (import.meta.main) {
	const out = { log: console.log, error: console.error };
	let root = '';
	try {
		root = defaultRoot();
	} catch (error) {
		out.error(`refused, no home directory from the account database (rule: the cache path does not come from the environment): ${error instanceof Error ? error.message : error}`);
		process.exit(1);
	}
	process.exit(run(root, process.argv.slice(2), out));
}
