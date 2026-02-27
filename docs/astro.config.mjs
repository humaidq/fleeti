// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
	integrations: [
		starlight({
			title: 'Fleeti',
			customCss: ['/src/styles/landing.css'],
			components: {
				Header: './src/components/Header.astro',
				Hero: './src/components/Hero.astro',
			},
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/humaidq/fleeti' }],
			sidebar: [
				{
					label: 'Getting Started',
					items: [
						{ label: 'Overview', slug: 'overview' },
						{ label: 'Quickstart', slug: 'guides/example' },
					],
				},
				{
					label: 'Reference',
					items: [{ label: 'Runtime Reference', slug: 'reference/example' }],
				},
			],
		}),
	],
});
