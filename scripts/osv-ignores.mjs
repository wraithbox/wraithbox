// @ts-check
// Checks the advisory overrides in osv-scanner.toml before `mise run vuln`
// scans with them. The format is on
// https://google.github.io/osv-scanner/configuration/: an `[[IgnoredVulns]]`
// entry has an `id`, an optional `ignoreUntil` date and an optional
// `reason`. This repository makes both optional keys required (AGENTS.md,
// "Supply chain"):
//
//   - `ignoreUntil` is a TOML date at most 30 days after today, so an
//     override can't stay forever;
//   - `reason` starts with the issue that tracks the fix (`#55: ...`);
//   - no other table: `[[PackageOverrides]]` ignores a whole package with
//     no date.
//
// Exits 1 and prints one line per broken rule. Run with bun, which parses
// TOML dates as Temporal.PlainDate.
import fs from 'node:fs';

const MAX_DAYS = 30;
const REASON = /^#\d+: \S/;

/**
 * One line per rule the config breaks, or an empty list.
 * @param {string} text the TOML source
 * @param {Temporal.PlainDate} today
 * @returns {string[]}
 */
export function problems(text, today) {
	const config = Bun.TOML.parse(text);
	const found = Object.keys(config)
		.filter((key) => key !== 'IgnoredVulns')
		.map((key) => `\`${key}\` is not allowed, only \`IgnoredVulns\``);
	const entries = config.IgnoredVulns ?? [];
	if (!Array.isArray(entries)) {
		return [...found, '`IgnoredVulns` must be an array of tables (`[[IgnoredVulns]]`)'];
	}
	const latest = today.add({ days: MAX_DAYS });
	entries.forEach((entry, index) => {
		let name = `entry ${index + 1}`;
		if (typeof entry.id === 'string' && entry.id) name = entry.id;
		else found.push(`${name} has no \`id\``);
		const extra = Object.keys(entry).filter((key) => !['id', 'ignoreUntil', 'reason'].includes(key));
		if (extra.length) found.push(`${name} has keys other than id, ignoreUntil and reason: ${extra.join(', ')}`);
		const until = entry.ignoreUntil;
		if (!(until instanceof Temporal.PlainDate)) {
			found.push(`${name} has no \`ignoreUntil\` date (a TOML date, not a string)`);
		} else if (Temporal.PlainDate.compare(until, latest) > 0) {
			found.push(`${name} has \`ignoreUntil\` ${until}, more than ${MAX_DAYS} days after ${today}`);
		}
		if (!(typeof entry.reason === 'string' && REASON.test(entry.reason))) {
			found.push(`${name} has no \`reason\` that starts with its issue, as in \`#55: ...\``);
		}
	});
	return found;
}

if (import.meta.main) {
	const file = process.argv[2] ?? 'osv-scanner.toml';
	const found = fs.existsSync(file) ? problems(fs.readFileSync(file, 'utf8'), Temporal.Now.plainDateISO()) : [];
	for (const line of found) console.error(`${file}: ${line}`);
	process.exit(found.length ? 1 : 0);
}
