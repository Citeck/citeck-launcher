import { defineConfig } from 'astro/config';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  site: 'https://citeck.github.io',
  base: '/citeck-launcher',
  trailingSlash: 'always',
  // One small stylesheet: inlining it saves the render-blocking round trip on slow mobile links.
  build: { inlineStylesheets: 'always' },
  vite: { plugins: [tailwindcss()] },
});
