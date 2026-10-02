// The docs sidebar, hand-authored.
//
// Every entry names a page under src/content/docs (the slug, minus a leading
// slash). This one file drives all four navigation surfaces — the sidebar in
// Docs.astro, the prev/next pager, the landing-page cards, and the sitemap
// ordering — so a page is "in the docs" exactly when it appears here.
//
// The shape is a Getting-started track that gets a binary running, then a
// Guides group that walks one capability at a time. Empty groups are skipped
// everywhere, so a reserved heading costs nothing until it has pages.

export interface DocPage {
  /** Title shown in the sidebar, pager, cards and <title>. */
  title: string;
  /** Slug with a leading slash; the index is '/'. */
  slug: string;
  /** One line shown on the landing page and the article lede. */
  description: string;
}

export interface DocGroup {
  /** Sidebar heading. */
  group: string;
  /** Icon key; the landing page maps it to an inline SVG. */
  icon: string;
  items: DocPage[];
}

const nav: DocGroup[] = [
  {
    group: 'Getting started',
    icon: 'rocket',
    items: [
      {
        title: 'Introduction',
        slug: '/intro',
        description: 'What Ogcode is and what you can do with it.',
      },
      {
        title: 'Install',
        slug: '/install',
        description: 'One binary on macOS, Linux or Windows — or Docker, or Ollama.',
      },
      {
        title: 'Quick start',
        slug: '/quick-start',
        description: 'Go from a fresh install to an agent editing files in your repo.',
      },
      {
        title: 'Core concepts',
        slug: '/core-concepts',
        description: 'Sessions, agents, modes, context and memory — the mental model.',
      },
    ],
  },
  {
    group: 'Guides',
    icon: 'book',
    items: [
      {
        title: 'Sessions & modes',
        slug: '/sessions-and-modes',
        description: 'The three modes, how a session runs, and steering it mid-turn.',
      },
      {
        title: 'Plan mode & tasks',
        slug: '/plan-mode',
        description: 'Break a feature into tasks, each in its own worktree, and open pull requests.',
      },
      {
        title: 'Permissions',
        slug: '/permissions',
        description: 'Ask, Auto and Yolo — what is gated, and how approvals are remembered.',
      },
      {
        title: 'Memory & context',
        slug: '/memory-and-context',
        description: 'Project instructions, turn-by-turn recall, and a working context that stays a useful size.',
      },
      {
        title: 'Search, skills & MCP',
        slug: '/search-and-skills',
        description: 'Web search, on-demand skills, and connecting external MCP servers.',
      },
      {
        title: 'Rich results & preview',
        slug: '/rich-results',
        description: 'Diagrams, math, charts, and opening your local services in the browser.',
      },
      {
        title: 'Remote deployment',
        slug: '/deployment',
        description: 'Reach a remote server safely: SSH tunnel, reverse proxy, Docker.',
      },
    ],
  },
];

/** Groups with at least one page. */
export function navGroups(): DocGroup[] {
  return nav.filter((g) => g.items.length > 0);
}

/** Every page, in reading order — the source of truth for getStaticPaths. */
export function flatPages(): DocPage[] {
  return nav.flatMap((g) => g.items);
}

/** The page for a slug, or undefined. */
export function pageFor(slug: string): DocPage | undefined {
  return flatPages().find((p) => p.slug === slug);
}

/** The group a page belongs to, or undefined. */
export function groupFor(slug: string): DocGroup | undefined {
  return navGroups().find((g) => g.items.some((p) => p.slug === slug));
}

/** The built URL for a slug (trailingSlash: 'always'). */
export function pageUrl(slug: string): string {
  return `/docs${slug}/`;
}
