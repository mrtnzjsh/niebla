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
            src: '/niebla/scripts/mermaid.min.js',
            defer: true,
          },
        },
        {
          tag: 'script',
          attrs: {
            src: '/niebla/scripts/mermaid-init.js',
            defer: true,
          },
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
          items: [
            { label: 'Overview & Philosophy', slug: 'getting-started/philosophy' },
            { label: 'Installation & Setup', slug: 'getting-started/installation' },
            { label: 'Quickstart Walkthrough', slug: 'getting-started/quickstart' },
          ],
        },
        {
          label: 'Design & Components',
          items: [
            { label: 'System Architecture', slug: 'architecture/tech-stack' },
            { label: 'NieblaFS (Virtual Filesystem)', slug: 'architecture/fuse-internals' },
            { label: 'CAS & Merkle Trees', slug: 'components/cas-storage' },
            { label: 'Repository Server', slug: 'components/server' },
          ],
        },
        {
          label: 'Workflows',
          items: [
            { label: 'Workspaces & Storage', slug: 'workflows/workspaces' },
            { label: 'Development Lifecycle', slug: 'workflows/development-lifecycle' },
            { label: 'Direct Materialization (--no-fuse)', slug: 'workflows/direct-materialization' },
            { label: 'IDE Setup', slug: 'workflows/ide-setup' },
            { label: 'In-Tree Governance (OWNERS.toml)', slug: 'workflows/in-tree-governance' },
            { label: 'Offline Development', slug: 'workflows/offline-mode' },
            { label: 'Troubleshooting', slug: 'workflows/troubleshooting' },
          ],
        },
        {
          label: 'Cotejo Code Review',
          items: [
            { label: 'Overview & Attention Sets', slug: 'cotejo/overview' },
            { label: 'Review Lifecycle & Diffs', slug: 'cotejo/review-workflows' },
            { label: 'AI Hints & Suggestions', slug: 'cotejo/ai-assistance' },
            { label: 'Authentication & Theming', slug: 'cotejo/deployment-auth' },
          ],
        },
        {
          label: 'How-To Guides',
          items: [
            { label: 'Local & Workstation Setup', slug: 'recipes/local-setup' },
            { label: 'Tailscale Private Mesh', slug: 'recipes/tailscale' },
            { label: 'Production & Cloud Deployments', slug: 'recipes/cloud-deployments' },
            { label: 'ZFS Backups & Disaster Recovery', slug: 'recipes/disaster-recovery' },
            { label: 'Logging & Observability', slug: 'recipes/logging-observability' },
          ],
        },
        {
          label: 'Reference',
          items: [
            { label: 'CLI Command Reference', slug: 'reference/cli' },
            { label: 'Configuration Schema', slug: 'reference/configuration' },
            { label: 'AI Agent Skills', slug: 'reference/skills' },
            { label: 'gRPC Wire Protocol', slug: 'reference/grpc-api' },
            { label: 'Cotejo HTTP & REST API', slug: 'reference/cotejo-api' },
            { label: 'CloudEvents & Webhooks', slug: 'reference/events' },
            { label: 'Security & Authentication', slug: 'reference/security-auth' },
          ],
        },
      ],
    }),
  ],
});
