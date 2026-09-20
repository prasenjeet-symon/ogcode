import rss from '@astrojs/rss';
import { publishedPosts } from '../lib/posts';

export async function GET(context) {
  const posts = await publishedPosts();
  return rss({
    title: 'Ogcode Blog',
    description:
      'Notes from building Ogcode: context engineering, agent memory, and running coding agents on your own machine.',
    site: context.site,
    items: posts.map((post) => ({
      title: post.data.title,
      description: post.data.description,
      pubDate: post.data.pubDate,
      link: `/blog/${post.id}/`,
    })),
    customData: '<language>en-us</language>',
  });
}
