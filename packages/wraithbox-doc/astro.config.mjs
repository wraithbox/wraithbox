// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// This is a *project* site: it deploys under a subpath of https://lsimons.github.io
// matching the monorepo's GitHub Pages path
// (https://lsimons.github.io/wraithbox/), so it sets `base`.
const base = '/wraithbox';

/**
 * Content links and image sources are written root-relative (`/guides/foo/`,
 * `/guides/foo.png`) so the markdown stays portable. This rehype plugin
 * prefixes those with the deploy base path at render time, for both
 * `<a href>` and `<img src>` (including raw HTML `<img>` tags).
 */
function rehypeBaseLinks() {
	/** @param {any} node */
	const visit = (node) => {
		const attr =
			node.type === 'element' && node.tagName === 'a'
				? 'href'
				: node.type === 'element' && node.tagName === 'img'
					? 'src'
					: null;
		if (attr) {
			const value = node.properties?.[attr];
			if (
				typeof value === 'string' &&
				value.startsWith('/') &&
				!value.startsWith('//') &&
				!value.startsWith(`${base}/`)
			) {
				node.properties[attr] = base + value;
			}
		}
		for (const child of node.children ?? []) visit(child);
	};
	return (/** @type {any} */ tree) => {
		visit(tree);
	};
}

// https://astro.build/config
export default defineConfig({
	site: 'https://lsimons.github.io',
	base,
	markdown: {
		rehypePlugins: [rehypeBaseLinks],
	},
	// The Quarto slide deck is a static file at /presentations/example.html.
	// Starlight strips the `.html` from the sidebar link (rendering
	// /presentations/example), so redirect that extensionless URL to the real
	// file. Works in dev, preview, and on GitHub Pages. Astro applies `base` to
	// the redirect source but not the target, so the target carries `${base}`.
	redirects: {
		'/presentations/example': `${base}/presentations/example.html`,
	},
	integrations: [
		starlight({
			title: 'Wraith Box',
			description: 'Run Claude Code inside an isolated macOS VM: drop-in, fast, default-deny.',
			favicon: '/favicon.svg',
			head: [
				{
					tag: 'link',
					attrs: {
						rel: 'apple-touch-icon',
						sizes: '180x180',
						href: `${base}/apple-touch-icon.png`,
					},
				},
				// Fonts: Merriweather = long-form/body, Merriweather Sans = on-screen/UI.
				{ tag: 'link', attrs: { rel: 'preconnect', href: 'https://fonts.googleapis.com' } },
				{
					tag: 'link',
					attrs: { rel: 'preconnect', href: 'https://fonts.gstatic.com', crossorigin: true },
				},
				{
					tag: 'link',
					attrs: {
						rel: 'stylesheet',
						href: 'https://fonts.googleapis.com/css2?family=Merriweather:wght@400;700&family=Merriweather+Sans:wght@400;700&display=swap',
					},
				},
			],
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/lsimons/wraithbox' },
			],
			editLink: {
				baseUrl:
					'https://github.com/lsimons/wraithbox/edit/main/packages/wraithbox-doc/',
			},
			customCss: ['./src/styles/custom.css'],
			sidebar: [
				{
					label: 'Overview',
					items: [{ slug: 'design', label: 'Design overview' }],
				},
				{
					label: 'Guides',
					items: [
						{ slug: 'guides/getting-started', label: 'Getting started' },
						{ slug: 'guides/writing-pages', label: 'Writing pages' },
						{ slug: 'guides/slides', label: 'Slide decks with Quarto' },
					],
				},
				{
					label: 'Reference',
					items: [
						{ slug: 'contributing', label: 'Contributing' },
						// Starlight prepends the deploy `base` to sidebar link values, so
						// these are written without it (unlike head/content links).
						{
							label: 'Example slides',
							items: [
								{ label: 'HTML', link: '/presentations/example.html', attrs: { target: '_blank' } },
								{ label: 'PDF', link: '/presentations/example.pdf', attrs: { target: '_blank' } },
								{
									label: 'Quarto source',
									link: '/presentations/example.qmd',
									attrs: { target: '_blank' },
								},
							],
						},
					],
				},
			],
		}),
	],
});
