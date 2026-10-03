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
