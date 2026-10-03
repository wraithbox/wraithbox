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
