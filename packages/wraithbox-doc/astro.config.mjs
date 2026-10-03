// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// The site is served from the root of its own domain, https://wraithbox.nl
// (GitHub Pages with a custom domain), so there is no `base` path and
// root-relative links work as written.

// https://astro.build/config
export default defineConfig({
	site: 'https://wraithbox.nl',
	integrations: [
		starlight({
			title: 'Wraith Box',
			description: 'Run coding agents inside isolated VMs: one command, fast, default-deny.',
			// BRAND ASSETS: the favicon and apple-touch-icon below are placeholders
			// from scripts/gen-favicon.mjs. The master for the brand icon is
			// design/icon.svg.
			//
			// LOGO HOOK: once src/assets/logo-dark.svg (light artwork for the dark
			// theme) and src/assets/logo-light.svg exist, uncomment this. Keep
			// replacesTitle false unless the logo includes the wordmark.
			// logo: {
			// 	dark: './src/assets/logo-dark.svg',
			// 	light: './src/assets/logo-light.svg',
			// 	alt: 'Wraith Box',
			// 	replacesTitle: false,
			// },
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
			customCss: ['./src/styles/custom.css'],
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
