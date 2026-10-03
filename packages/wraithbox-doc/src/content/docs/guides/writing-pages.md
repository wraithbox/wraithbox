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

That page is served at `/guides/my-page/`.

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
(`/guides/my-page/`, `/diagram.png`). The site is served from the root of
its own domain (`https://wraithbox.nl`), so the same Markdown works in
local dev and in production.

```markdown
See the [getting started guide](/guides/getting-started/).

![A diagram](/diagram.png)
```

## The landing page

`index.mdx` uses Starlight's `splash` template to render a hero and card grid
instead of the usual docs layout. It is a separate landing page - not the first
sidebar entry - so the sidebar starts with your actual content.
