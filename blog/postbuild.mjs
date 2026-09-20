// Astro's content layer leaves a few internal stubs in the build output
// (empty module maps and a dev-time schema). Nothing references them on a
// fully static site — sweep them so they never reach the deployed site.
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const out = fileURLToPath(new URL('../docs/blog/', import.meta.url));
for (const name of ['content-assets.mjs', 'content-modules.mjs', 'collections']) {
  fs.rmSync(new URL(name, `file://${out}`), { recursive: true, force: true });
}
