// Two jobs after the Astro build has written ../docs/docs:
//
// 1. Sweep Astro's content-layer stubs. The content layer leaves a few
//    internal modules in the build output (empty module maps and a dev-time
//    schema). Nothing references them on a fully static site.
//
// 2. Run Pagefind over the finished HTML so the search box has an index.
//    Pagefind reads the rendered pages (not the source), so it sees exactly
//    what a crawler would; the bundle it writes is served at /docs/pagefind/.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const outDir = fileURLToPath(new URL('../docs/docs/', import.meta.url));

for (const name of ['content-assets.mjs', 'content-modules.mjs', 'collections']) {
  fs.rmSync(new URL(name, `file://${outDir}`), { recursive: true, force: true });
}

const bin = fileURLToPath(new URL('node_modules/.bin/pagefind', import.meta.url));
execFileSync(bin, ['--site', outDir], { stdio: 'inherit' });
