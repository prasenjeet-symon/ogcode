import { getCollection, type CollectionEntry } from 'astro:content';

export type Post = CollectionEntry<'posts'>;

// Published posts, newest first. Drafts stay visible during `astro dev` so
// they can be previewed, and disappear from production builds.
export async function publishedPosts(): Promise<Post[]> {
  const posts = await getCollection('posts', ({ data }) => import.meta.env.DEV || !data.draft);
  return posts.sort((a, b) => b.data.pubDate.valueOf() - a.data.pubDate.valueOf());
}

export function postUrl(post: Post): string {
  return `/blog/${post.id}/`;
}

// Dates in frontmatter are date-only and parse as UTC midnight; format in UTC
// so the shown day never shifts with the build machine's timezone.
export function formatDate(d: Date): string {
  return d.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    timeZone: 'UTC',
  });
}

export function isoDate(d: Date): string {
  return d.toISOString().split('T')[0];
}
