// Astro config for the Ogcode blog.
//
// The blog is a fully static, pre-rendered site: every page is plain HTML at
// build time, no client JS. It builds into ../docs/blog so GitHub Pages (which
// serves the docs/ folder as ogcode.xyz) picks it up at ogcode.xyz/blog/.
// docs/blog is gitignored — CI builds it fresh on every Pages deploy.
import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { remarkReadingTime } from './remark-reading-time.mjs';

const fontsDir = fileURLToPath(new URL('../docs/fonts', import.meta.url));

// The blog's CSS references the same /fonts/*.woff2 files the homepage uses —
// domain-root paths that exist in production but not under this project. This
// dev-only middleware maps /fonts/ onto ../docs/fonts so `astro dev` renders
// with the real typefaces instead of fallback stacks.
function rootFonts() {
  return {
    name: 'ogcode-root-fonts',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        if (!req.url || !req.url.startsWith('/fonts/')) return next();
        const rel = decodeURIComponent(req.url.slice('/fonts/'.length).split('?')[0]);
        const file = path.normalize(path.join(fontsDir, rel));
        if (!file.startsWith(fontsDir + path.sep) || !fs.existsSync(file)) return next();
        res.setHeader('Content-Type', 'font/woff2');
        res.setHeader('Cache-Control', 'max-age=3600');
        fs.createReadStream(file).pipe(res);
      });
    },
  };
}

export default defineConfig({
  site: 'https://ogcode.xyz',
  base: '/blog',
  outDir: '../docs/blog',
  trailingSlash: 'always',
  integrations: [sitemap()],
  markdown: {
    // css-variables lets the code palette come from blog.css, so highlighting
    // uses the site's own tokens instead of a stock theme.
    shikiConfig: { theme: 'css-variables', wrap: true },
    remarkPlugins: [remarkReadingTime],
  },
  vite: { plugins: [rootFonts()] },
});
