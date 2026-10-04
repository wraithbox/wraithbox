// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import { unified } from '@astrojs/markdown-remark';
import { refsDigest, remarkRefs } from './src/plugins/remark-refs.mjs';
import { specsIntegration } from './src/plugins/specs.mjs';

// The site is served from the root of its own domain, https://wraithbox.nl
// (GitHub Pages with a custom domain), so there is no `base` path and
// root-relative links work as written.

// https://astro.build/config
export default defineConfig({
	site: 'https://wraithbox.nl',
	// IDs in page text (S07-egress-gateway, SEC06-repo-writes, I36) become
	// links with a hover card (src/plugins/remark-refs.mjs).
	markdown: { processor: unified({ remarkPlugins: [[remarkRefs, { digest: refsDigest() }]] }) },
	integrations: [
		// Publishes the design documents in docs/ (src/plugins/specs.mjs).
		specsIntegration(),
		starlight({
			title: 'Wraith Box',
			description: 'Run coding agents inside isolated VMs: one command, fast, default-deny.',
			// BRAND ASSETS: the masters are the SVGs in design/. The favicons and
			// apple-touch-icon in public/ are derived from them by
			// scripts/gen-favicon.mjs (mise run doc:favicon). The header logo
			// uses the masters directly: the inverted icon (white tile) on the
			// dark theme, the dark-tile icon on the light theme. The logo has no
			// wordmark, so the title text stays, and the alt text is empty so
			// screen readers do not read the name twice.
			logo: {
				dark: './design/icon-inverted.svg',
				light: './design/icon.svg',
				alt: '',
				replacesTitle: false,
			},
			//
			// HERO HOOK: the landing page's hero image is set in the frontmatter
			// of src/content/docs/index.mdx, not here. Add under `hero:`
			//   image:
			//     dark: ../../assets/hero-dark.webp
			//     light: ../../assets/hero-light.webp
			//     alt: ''   (decorative; the tagline carries the meaning)
			// Starlight renders it beside the title on wide screens.
			favicon: '/favicon.svg',
			head: [
				// Fallback for browsers without SVG favicons. sizes="32x32" (not
				// "any") keeps browsers that do support SVG on favicon.svg.
				{
					tag: 'link',
					attrs: { rel: 'icon', href: '/favicon.ico', sizes: '32x32' },
				},
				{
					tag: 'link',
					attrs: {
						rel: 'apple-touch-icon',
						sizes: '180x180',
						href: '/apple-touch-icon.png',
					},
				},
				// No webfont links: the theme uses the system font stack
				// (src/styles/custom.css), so pages make no font-service requests.
			],
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/wraithbox/wraithbox' },
			],
			editLink: {
				baseUrl:
					'https://github.com/wraithbox/wraithbox/edit/main/packages/wraithbox-doc/',
			},
			customCss: ['./src/styles/custom.css', './src/styles/brief.css', './src/styles/refs.css'],
			components: {
				// Dark is the default theme; an explicit choice in the theme
				// toggle (dark, light or auto) still wins. See the two files.
				ThemeProvider: './src/components/ThemeProvider.astro',
				ThemeSelect: './src/components/ThemeSelect.astro',
			},
			sidebar: [
				{
					label: 'Overview',
					items: [{ slug: 'design', label: 'Design overview' }],
				},
				// The design documents from docs/ (src/plugins/specs.mjs), one
				// group per kind of ID (S01-spec-based-development).
				...[
					['Specs', 'spec'],
					['Requirements', 'requirements'],
					['Threats', 'threats'],
					['Spikes', 'spikes'],
					['Research', 'research'],
					['Versions', 'versions'],
				].map(([label, directory]) => ({
					label,
					collapsed: true,
					items: [{ autogenerate: { directory } }],
				})),
				{
					label: 'Review',
					items: [{ slug: 'review', label: 'Review briefs' }],
				},
				{
					label: 'Guides',
					items: [
						{ slug: 'guides/getting-started', label: 'Getting started' },
						{ slug: 'guides/writing-pages', label: 'Writing pages' },
					],
				},
				{
					label: 'Reference',
					items: [
						{ slug: 'contributing', label: 'Contributing' },
					],
				},
			],
		}),
	],
});
