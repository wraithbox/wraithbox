# R08 - Markdown line wrapping

**Purpose:** Whether the line wrapping of the Markdown in this
repository can be automated without breaking code spans, tables, MDX
and the docs site's own parsers, with which tool, at which width, and
how to roll it out. Issue I517. Measured on 2026-11-01 over a copy of
the 238 Markdown and MDX files at commit `10b7ab59`.

**Requirements:** none. This note is about the docs tooling and the
agent harness, not the product.

## Method

- **Corpus.** `git archive HEAD` into `.scratch/corpus/base/`, then
  one copy of that tree per candidate. `git ls-files '*.md' '*.mdx'`
  lists 239 paths: 191 `.md` files, 47 `.mdx` files and `CLAUDE.md`,
  a symbolic link to `AGENTS.md` that the counts leave out.
- **Measures.** Short Python scripts in `.scratch/bin/`, run with
  `python3 -I`, compared each copy with the base:
  - changed files and diff lines, from `difflib.unified_diff`;
  - whether a file's change is whitespace only, by comparing the
    sequence of whitespace-separated tokens;
  - whether front matter, fenced code blocks, table rows and HTML
    comment lines stayed byte-identical;
  - prose lines with an odd number of backticks, which is a code span
    split over two lines, and prose lines over 72 and 80 characters.
- **Checks.** In each copy: the docs build (`bun run build`, which also
  runs the index check of `doc:index` and the limits of
  `structure.mjs`), Vale with the copy's `.vale.ini`, and cspell with
  the copy's `cspell.json`. Vale findings were compared with the line
  and column numbers removed.
- **Rendering.** Every HTML page of the built site, compared with the
  base build's page after collapsing each run of whitespace to one
  space.
- **Tools.** Prettier 3.9.10 and dprint 0.64.3 from npm, installed into
  `.scratch/tools/` with `--ignore-scripts`. The dprint Markdown plugin
  0.28.2 was downloaded from `plugins.dprint.dev`, and its SHA-256
  (`77d00951…`) matched the checksum that the plugin's `latest.json`
  publishes. Both tools ran only on the copies.
- **Tools not run.** Running mdformat and rumdl from PyPI through
  `uvx` was refused by the session's permission check, so those two
  are compared from their documentation only. `mdd prose reflow` is
  in a draft pull request (`schubergphilis/mdd#102`) whose code the
  maintainer doesn't trust yet, and building another project's code
  is out of bounds for this note. Its numbers are the maintainer's,
  from the comment on I517. The agent route could not be measured
  either, because this session can't start a subagent.

## The texts today

From `.scratch/bin/widths.py` over the base copy, counting prose lines
outside front matter, fenced code and tables:

| | Files | Prose lines over 80 |
|---|---|---|
| `docs/briefs/`, 46 MDX briefs and B00-index | 47 | 2,614 |
| `docs/spec/` | 111 | 119 |
| `docs/spikes/` | 23 | 123 |
| Everything else | 57 | 158 |

- **72 is the width in use.** In the `.md` files, 15,195 lines outside
  tables are 60 to 72 characters long, 385 are 73 to 80, and 463 are
  longer, front matter and code blocks included (`awk` over the base
  copy).
- **Briefs aren't wrapped.** The `/brief` skill writes one paragraph
  per line in MDX, with inline SVG figures and Starlight components.
- **Code spans are already split by hand.** 104 prose lines in the
  `.md` files hold an odd number of backticks, as in
  `S06-01-image-layers.md` line 151, where a code span that starts
  with `brew deps` ends the line and the next line closes it.
- **Specs sit at the page limit.** S04-02-vm-provider,
  S04-04-vm-processes, S04-08-records-and-bounds,
  S05-03-management-commands, S05-04-naming and S06-02-image-build
  have exactly 300 lines, the page limit of `structure.mjs`
  (S01-spec-based-development, "Size and structure"), and two more
  have 299 (`wc -l` plus one, as `structure.mjs` counts them).

## Prior art

- **A formatter run by a Claude Code hook.**
  `mittwald/developer-portal` merged `#1314` on 2026-10-08: a
  `PostToolUse` hook (`.claude/hooks/prettier-format.sh`) runs the
  repository's Prettier on each `.md` or `.mdx` file an agent writes,
  with `proseWrap: "preserve"`, so it formats but never wraps. Read
  with `gh pr view 1314 -R mittwald/developer-portal`. A web search
  found more hooks of the same kind and repositories that leave
  Markdown out of Prettier through `.prettierignore`. Each of them
  formats or skips Markdown, and leaves the line breaks alone.
- **Semantic line breaks.** `sembr.org` and the sentence modes of
  rumdl (`reflow-mode = "sentence-per-line"`, `rumdl.dev/md013/`),
  dprint (`textWrap: "sentence"`) and `mdd prose reflow` put one
  sentence per line.

No repository found wraps agent-written prose at a fixed width with a
formatter. The ones that format leave the line breaks alone.

## Candidates

| Tool | License | Wraps | MDX | Tables | Pinning |
|---|---|---|---|---|---|
| Prettier 3.9.10 | MIT | `--prose-wrap always` | formats it | always aligns them | `npm:prettier` in mise, or `bun.lock` |
| dprint 0.64.3, Markdown plugin 0.28.2 | MIT | `textWrap`: `always`, `maintainAndWrap`, `sentence` | `.md` only | `table.skipFormat` | `aqua:dprint/dprint` in mise, plugin URL with checksum |
| mdformat 1.0.0 with `mdformat-gfm` | MIT | `--wrap <n>` | no plugin on PyPI | not measured | Python, which the repository doesn't use |
| rumdl 0.3.2 | MIT | MD013 `reflow` | `mdx` flavor | not wrapped | `aqua:rvben/rumdl` in mise |
| `mdd prose reflow` | Apache-2.0 | one sentence per line | not yet (`schubergphilis/mdd#198`) | not measured | unreleased |
| remark-stringify | MIT | no option to wrap | with `remark-mdx` | | |

Licenses are from `gh api repos/<owner>/<repo>` and the npm and PyPI
metadata. All are permissive (S10-tech-stack).

## Results

### Churn and content

Over the 191 `.md` files, from `.scratch/bin/measure.py`:

| Run | Changed files | Diff lines | Files with other than whitespace changed |
|---|---|---|---|
| Prettier, 72, defaults | 191 | 23,273 | 111 |
| dprint `always`, 72, defaults | 191 | 23,661 | 111 |
| dprint `always`, 72, tuned | 190 | 18,968 | 2 |
| dprint `always`, 80, tuned | 191 | 40,143 | 3 |
| dprint `maintainAndWrap`, 72, tuned | 168 | 2,234 | 2 |
| dprint `maintainAndWrap`, 80, tuned | 125 | 1,168 | 2 |
| dprint `sentence`, tuned | 191 | 35,097 | 3 |
| `mdd prose reflow`, 29 files of `schubergphilis/ai-training` | 29 | about 8,200 | not counted, 0 render differently |

"Tuned" is `wrapCodeSpans: false`, `table.skipFormat: true` and
asterisks for emphasis and strong text. With its defaults, dprint
pads every table and turns `*emphasis*` into `_emphasis_`, as Prettier
does. Under every tuned run, the tokens of `AGENTS.md` change where a
lazy block quote line gets its `>`, and those of `CODE_OF_CONDUCT.md`
where `*` list markers become `-`. At 80 and in the sentence mode,
`README.md` changes too, where block quote markers move to the new
line starts. Each renders the same page
as before.

A full rewrap is large at either width, because a formatter refills
paragraphs that people broke short of the width. At 80 it rewrites
twice the lines it does at 72, since the texts are wrapped at 72.

### The hard cases

From `.scratch/bin/hardcases.py`, the builds and the HTML comparison:

| Case | Prettier, 72 | dprint `always`, 72, tuned |
|---|---|---|
| Code spans in `.md` | splits none, joins the 104 split lines | splits none, joins the 104 split lines |
| Code spans in MDX | splits them on 40 lines | not touched |
| Tables, generated ones included | realigned in 95 `.md` files | byte-identical |
| Front matter of `.md` files | long values of the 6 agents, the 4 skills and `writing-pages.md` turned into YAML blocks | byte-identical |
| Fenced code | one block changed, in `github-settings.md` | byte-identical |
| HTML comments | unchanged | unchanged |
| MDX | quotes, SVG text and front matter rewritten | not touched |
| Lists, nested and with code blocks | not checked, the build failed | render the same |
| Rendered site | build fails | 227 of 227 pages identical after whitespace |

Prettier failed the docs build twice over. It pads the
`| ID | Description | Status |` header, so `indexes.mjs` doesn't
find the table in B00-index. On MDX it rewrites `title:` in front
matter as a YAML block, which the index's `^title: (.+)$` doesn't read.

Each tuned dprint run that was built (`always` at 72, and
`maintainAndWrap` at 72 and 80) rendered all 227 pages identically to
the base build once the index status and page limit problems below
were patched in the copies. Long URLs, image alt text, and code spans
over the width are left on lines of their own. In the `.md` files, 54
prose lines over 80 remain: 28 hold a URL, 12 a code span, and 13 of
the other 14 image alt text.

### What the formatter breaks in our own tooling

The index, page limit and section parsers of the docs site read
Markdown line by line, so any change of line breaks reaches them:

- **The index status.** `statusOf` in `indexes.mjs` (line 63) takes
  `^\*\*Status:\*\*\s*(.+)$`, the first line of the status paragraph.
  R02-builder-effort, R03-fix-rounds-compaction, X01-model-credential
  and seven other notes and spikes have a status line over 72
  characters. Wrapped, their R00-index and X00-index rows lose the rest
  of the status, and every dprint build failed with
  `index tables out of date`. Reading the paragraph up to the next
  blank line and joining it with spaces made the base build produce
  the same indexes and the wrapped builds pass.
- **The page limit.** `structure.mjs` counts a page in lines. Wrapping
  at 72 put S05-03-management-commands at 302 lines and S05-04-naming
  at 304, over the limit of 300, and `maintainAndWrap` at 72 put seven
  pages over it. A sentence per line instead shortens the pages:
  the 25,902 prose lines of the `.md` files become 17,179. A page then
  holds more text under the same limit. The limit measures the
  wrapping as much as the page.
- **Section references.** `sections.mjs` blanks a code span that holds
  a `"` before it looks for `("Section")` references, with
  `` `[^`\n]*"[^`\n]*` ``. When two quoted code spans share a line, as
  in `S04-04-vm-processes ("`wb-diskfmt`"), S05-03-management-commands
  ("`wb vm reset-data`")` from C11-host-daemons once the sentence mode
  joined it, the pattern pairs the closing backtick of the first with
  the opening one of the second, and the build fails with
  `S04-04 has no section`. Hand wrapping hides this today, and a
  writer who keeps such a sentence on one line meets the same failure.

### Vale and cspell

cspell found nothing in any copy. Vale found zero errors in each
copy, and its warnings, compared without line numbers, moved:

| Run | Warnings | Gone | New |
|---|---|---|---|
| Base | 1,946 | | |
| dprint `always`, 72, tuned | 1,968 | 57 | 79 |
| dprint `maintainAndWrap`, 80, tuned | 1,949 | 0 | 3 |
| dprint `sentence`, tuned | 2,059 | 118 | 231 |

Most of the moved warnings are `Google.OxfordComma`, whose match ends
at a line break and so quotes a different number of words. The
sentence mode adds the most, because rules such as
`ai-tells.StackedAnaphora` and `House.VerbTricolon` match sentence
openings at the start of a line.

### Wrapping only what a change touches

`dprint fmt --lines <file>:<first>-<last>` formats a line range, and
the Markdown plugin honors it. On a copy of
`docs/agents/working.md`, `--lines 263-263,6-6` rewrapped lines 5 to 8
and 262 to 263 and left the rest of the file as it was. A script that
passes the lines of `git diff -U0` to dprint would wrap the
paragraphs a change touched, with no rewrap of the rest.

`maintainAndWrap` is the other low-churn mode, and its output is poor:
it breaks a long line without drawing the next one up, which leaves
one-word lines such as `worker` and `or` behind (`working.md` lines 5
and 86 at 72).

A second run of the tuned dprint over its own output left every file
as it was, and `dprint check` over `docs/` took 0.13 seconds.

### MDX

No candidate measured here wraps MDX safely. Prettier rewrites the
inline SVG of the briefs (`<text>wb-hostd</text>` becomes
`<text> wb-hostd </text>`), their front matter and their import quotes.
dprint skips `.mdx`. rumdl documents an `mdx` flavor, which this note
couldn't run. The 47 MDX files are 46 briefs, each written by `/brief`
for one decision, and `index.mdx`. Leaving MDX unwrapped keeps the
long lines of the briefs (the table under "The texts today"), which
the docs site renders the same either way.

### Sentence per line

dprint's `sentence` mode and `mdd prose reflow` both kept the content.
Against wrapping at 72, the sentence mode rewrites about 35,000 lines
instead of 19,000, leaves 7,363 prose lines over 80 in the `.md` files
(399 today), which read poorly in a terminal and in a side-by-side
diff, adds 113 Vale warnings, and hit the `sections.mjs` failure
above. Its gain is that a later edit changes only the sentences it
touches. Range formatting gets most of that gain at 72.

### The agent route

A subagent on Haiku that rewraps the touched paragraphs was not
measured. The tuned dprint kept every token in 188 of the 190 files
it changed, and in the other two it changed only list and block quote
markers, which leaves a model nothing to add. A model can change
words while it wraps. Its output would need the same token check
that `measure.py` does, and with that check a model does the job of
dprint at a higher cost.

## Reproducing the runs

Each run formatted a fresh copy of the base tree. `<copy>` is the
copy's directory, and `<list>` a file with the copy's `.md` paths, one
per line, from `git ls-files '*.md'` without `CLAUDE.md`.

Prettier, at 72 and 80 (`<w>`), over `.md` and `.mdx`, without a
configuration file and with an ignore file that doesn't exist, so that
`.gitignore` doesn't hide the copy under `.scratch/`:

```sh
prettier --no-config --ignore-path <copy>/.none --prose-wrap always \
  --print-width <w> --write '<copy>/docs/**/*.{md,mdx}' \
  '<copy>/.claude/**/*.md' \
  '<copy>/packages/wraithbox-doc/src/content/**/*.{md,mdx}' \
  '<copy>/*.md'
```

dprint, over the `.md` files only:

```sh
dprint fmt --no-gitignore --config <config> --allow-no-files \
  $(sed 's|^|<copy>/|' <list>)
```

The tuned configuration at 72. The other tuned runs change only
`lineWidth` (80) or `textWrap` (`maintainAndWrap`, or `sentence` with
`lineWidth` 80). The default run's configuration holds only
`"markdown": { "lineWidth": 72, "textWrap": "always" }` and the
plugin.

```json
{
  "lineWidth": 72,
  "markdown": {
    "textWrap": "always",
    "wrapCodeSpans": false,
    "emphasisKind": "asterisks",
    "strongKind": "asterisks",
    "table.skipFormat": true
  },
  "plugins": [
    "https://plugins.dprint.dev/markdown-0.28.2.wasm@77d0095123be1df7c802ef1b8875433c757f79238c26a58d1c49b8cafca44e2c"
  ]
}
```

The runs named the plugin by a local path to the same downloaded
file. The checksum form above was checked afterwards. With a wrong
checksum, `dprint check` stops with
`The checksum did not match the expected checksum.`, and with this one
it formats.

## Recommendations

- **Fix two parsers and settle the limits first.** Read the whole
  `**Status:**` paragraph in `indexes.mjs`. Pair code spans from left
  to right before blanking them in `sections.mjs`. Decide whether the page and
  section limits of S01-spec-based-development ("Size and structure")
  stay in lines, with the specs at the page limit trimmed once, or move
  to words. The parser fixes help with or without a formatter. A
  writer who wraps a status line, or keeps two quoted code spans on
  one line, meets those failures today.
- **Adopt dprint for `.md`, width 72, `textWrap: "always"`.** Use
  `wrapCodeSpans: false`, `table.skipFormat: true`,
  `emphasisKind: "asterisks"` and `strongKind: "asterisks"`. Exclude
  `*.mdx`, `CODE_OF_CONDUCT.md`, which is adopted text, and `CLAUDE.md`.
  Pin dprint in `.mise.toml` (`aqua:dprint/dprint`).
- **Pin the plugin by URL and checksum.** In `dprint.json`, name the
  plugin as `https://plugins.dprint.dev/markdown-0.28.2.wasm@<sha256>`,
  which dprint verifies ("Reproducing the runs"), as an action is
  pinned to a commit. A copy of the wasm in the repository would be a
  binary nobody reviews, and its updates would be a binary diff. The URL alone makes dprint
  download the plugin on its first run, so `mise run ci` isn't
  offline. An `md:install` task that runs `dprint` once to fill its
  cache, in the `depends` of `install` and `install-frozen`, fetches
  it beforehand, as `mise install` does for the tools. Dependabot
  doesn't watch dprint plugins, as it doesn't watch `.mise.toml`.
  A plugin update is a manual edit of the URL and the checksum,
  checked against the checksum the plugin's `latest.json` publishes,
  with its release notes read.
- **Wire the check into both gates.** Add `md:format` and `md:check`
  tasks, and `md:check` to `lint`, which puts it in `mise run ci`.
  `.github/workflows/ci.yml` doesn't run `lint`: its docs job runs
  `prose:lint`, `prose:spell`, `doc:check` and `doc:build` as separate
  steps. `md:check` needs its own step there, after the plugin is
  fetched, and `dprint.json` needs a glob in the change rules of
  `scripts/ci-changes.mjs` that starts the docs job. `**/*.md` already
  has one.
- **Roll out with one rewrap commit.** After the parser fixes and
  the decision on the limits, one commit runs `md:format` over the
  tree (about 190 files and 19,000 diff lines), and `.git-blame-ignore-revs` names it, which GitHub's
  blame reads. Land it between waves, since it conflicts with every
  open pull request that touches a doc. From then on the check keeps
  the tree formatted, which is what AGENTS.md ("No `.editorconfig`")
  already says formatters do.
- **Without the rewrap commit, wrap by range.** If the maintainer
  rejects a tree-wide commit, a `md:wrap-changed` task passes the lines
  of `git diff -U0 origin/main` to `dprint fmt --lines`. CI then can't
  check the tree, and wrapped and unwrapped files remain side by side.
- **Keep MDX, sentence-per-line and a model out for now.**
  Re-measure `mdd prose reflow` once it is released and handles MDX.
- **Harness.** Once the task exists, AGENTS.md ("Prose") and the
  `/brief` and `/spec-draft` skills say to run `mise run md:format`
  before a commit. A `PostToolUse` hook as in
  `mittwald/developer-portal` is optional, and can follow when agents
  forget the task.

**Status:** Measured 2026-11-01 over 238 Markdown and MDX files at `10b7ab59`, recommends dprint at 72 for `.md` after two parser fixes and a decision on the S01-spec-based-development size limits
