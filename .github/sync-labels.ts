// Create or update the GitHub labels listed in the given label files
// (default: .github/labels.yml).
//
// Run with `mise run gh:labels`. Uses the authenticated `gh` CLI against
// the repository of the current checkout. Additive: labels on GitHub that
// are not in a file are left alone, never deleted.

type Label = { name: string; color: string; description: string };

const paths = process.argv.length > 2 ? process.argv.slice(2) : [".github/labels.yml"];

let failures = 0;
for (const path of paths) {
  const labels = Bun.YAML.parse(await Bun.file(path).text()) as Label[];
  if (!Array.isArray(labels) || labels.length === 0) {
    console.error(`${path}: expected a non-empty list of labels`);
    process.exit(1);
  }
  failures += syncLabels(path, labels);
}

process.exit(failures === 0 ? 0 : 1);

function syncLabels(path: string, labels: Label[]): number {
  let failures = 0;
  for (const label of labels) {
    if (!label.name || !/^[0-9a-f]{6}$/i.test(label.color ?? "") || !label.description) {
      console.error(`${path}: invalid entry ${JSON.stringify(label)}`);
      failures++;
      continue;
    }
    const result = Bun.spawnSync(
      ["gh", "label", "create", label.name, "--color", label.color, "--description", label.description, "--force"],
      { stdout: "inherit", stderr: "inherit" },
    );
    if (result.exitCode !== 0) {
      console.error(`failed: ${label.name}`);
      failures++;
    } else {
      console.log(`ok: ${label.name}`);
    }
  }

  return failures;
}
