---
title: Writing pages
description: Add Markdown pages, wire them into the sidebar, and link between them.
---

Pages are Markdown (`.md`) or MDX (`.mdx`) files under
`packages/wraithbox-doc/src/content/docs/`. The path under that directory becomes the URL, and
each page needs at least a `title` in its frontmatter.

## Add a page

Create `packages/wraithbox-doc/src/content/docs/guides/my-page.md`:

```markdown
---
title: My page
description: A short summary used for SEO and social cards.
---

Your content here.
```

That page is served at `/guides/my-page/` (under the deploy base path).

## Wire it into the sidebar

The sidebar is defined explicitly in `packages/wraithbox-doc/astro.config.mjs`. Add your page to
a group's `items`:

```js
{
	label: 'Guides',
	items: [
		{ slug: 'guides/getting-started', label: 'Getting started' },
		{ slug: 'guides/my-page', label: 'My page' },
	],
},
```

## Links and images

Write internal links and image sources **root-relative**
(`/guides/my-page/`, `/diagram.png`) rather than with the deploy base path.
A small rehype plugin in `astro.config.mjs` prepends the base path
(`/wraithbox`) at render time, so the same Markdown works in local
dev and on GitHub Pages.

```markdown
See the [getting started guide](/guides/getting-started/).

![A diagram](/diagram.png)
```

The one exception is the landing page's hero action links and any raw HTML
`<a>` tags, which are used verbatim and must include the base path.

## The landing page

`index.mdx` uses Starlight's `splash` template to render a hero and card grid
instead of the usual docs layout. It is a separate landing page - not the first
sidebar entry - so the sidebar starts with your actual content.
