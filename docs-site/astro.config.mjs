import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://mrtnzjsh.github.io',
  base: '/niebla',
  server: {
    host: '0.0.0.0',
    port: 4321,
  },
  integrations: [
    starlight({
      title: 'Niebla',
      description: 'Unified Monorepo & Code Review Toolchain',
      logo: {
        src: './src/assets/logo.svg',
      },
      customCss: [
        './src/styles/custom.css',
      ],
      social: {
        github: 'https://github.com/mrtnzjsh/niebla',
      },
      sidebar: [
        {
          label: 'Getting Started',
          autogenerate: { directory: 'getting-started' },
        },
        {
          label: 'Workflows',
          autogenerate: { directory: 'workflows' },
        },
        {
          label: 'Cotejo Review',
          autogenerate: { directory: 'cotejo' },
        },
        {
          label: 'Component Deep Dives',
          autogenerate: { directory: 'components' },
        },
        {
          label: 'Reference & Schemas',
          autogenerate: { directory: 'reference' },
        },
        {
          label: 'Recipes & How-Tos',
          autogenerate: { directory: 'recipes' },
        },
      ],
    }),
  ],
});
