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
      head: [
        {
          tag: 'script',
          content: `
            try {
              if (localStorage.getItem('niebla-nav-collapsed') === 'true') {
                document.documentElement.dataset.navCollapsed = 'true';
              }
              if (localStorage.getItem('niebla-toc-collapsed') === 'true') {
                document.documentElement.dataset.tocCollapsed = 'true';
              }
            } catch (e) {}
          `,
        },
        {
          tag: 'script',
          attrs: {
            src: '/niebla/scripts/sidebar-toggles.js',
            defer: true,
          },
        },
      ],
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
          label: 'Architecture & Internals',
          autogenerate: { directory: 'architecture' },
        },
        {
          label: 'Component Manuals',
          autogenerate: { directory: 'components' },
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
