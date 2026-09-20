import { defineCollection, z } from 'astro:content';
import { glob } from 'astro/loaders';

// One markdown file per post in src/content/posts/. The filename (minus .md)
// becomes the URL: src/content/posts/why-recall.md -> ogcode.xyz/blog/why-recall/
const posts = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/posts' }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    pubDate: z.coerce.date(),
    updatedDate: z.coerce.date().optional(),
    author: z.string().default('Prasenjeet Kumar'),
    tags: z.array(z.string()).default([]),
    // Drafts render in `astro dev` but are excluded from the built site.
    draft: z.boolean().default(false),
    // Absolute URL or site-rooted path; falls back to the site-wide card.
    ogImage: z.string().default('https://ogcode.xyz/media/og.png'),
    // Site-rooted path to a wide hero image shown under the post header.
    hero: z.string().optional(),
    heroAlt: z.string().optional(),
  }),
});

export const collections = { posts };
