// @ts-check
import { defineConfig } from 'astro/config';
import tailwindcss from '@tailwindcss/vite';
import sitemap from '@astrojs/sitemap';
import mdx from '@astrojs/mdx';

// https://astro.build/config
export default defineConfig({
  site: 'https://warmbly.com',
  // Astro 7 changed the default to 'jsx', which also drops the whitespace
  // between inline elements. Keep the HTML-aware compression the site was
  // built with on Astro 6 so rendered pages stay byte-for-byte equivalent.
  compressHTML: true,
  integrations: [
    mdx(),
    sitemap({
      // The 404 page is noindex and should never appear in the sitemap.
      filter: (page) => !page.includes('/404'),
    }),
  ],
  vite: {
    plugins: [tailwindcss()],
    server: {
      // Allow Tailscale MagicDNS names when `make site PUBLIC_HOST=…` exposes
      // the dev server with --host. IPs + localhost are always allowed.
      allowedHosts: ['.ts.net'],
    },
  },
});
