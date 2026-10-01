import { defineCollection, z } from 'astro:content';
import { glob } from 'astro/loaders';

// One markdown/MDX file per docs page in src/content/docs/. The filename (minus
// the extension) is the slug used in src/lib/nav.ts, e.g.
// src/content/docs/quick-start.mdx -> /docs/quick-start/.
const docs = defineCollection({
  loader: glob({ pattern: '**/*.{md,mdx}', base: './src/content/docs' }),
  schema: z.object({
    // The nav (src/lib/nav.ts) carries the title shown in the sidebar; this one
    // is the fallback used for <title> and structured data.
    title: z.string(),
    description: z.string().optional(),
  }),
});

export const collections = { docs };
