// The docs sidebar, hand-authored.
//
// Every entry names a page under src/content/docs (the slug, minus a leading
// slash). This one file drives all four navigation surfaces — the sidebar in
// Docs.astro, the prev/next pager, the landing-page cards, and the sitemap
// ordering — so a page is "in the docs" exactly when it appears here.
//
// v1 ships the Getting-started track, the two migrated references
// (arch/deployment), and empty groups reserved for growth. Empty groups are
// skipped everywhere, so a reserved heading costs nothing until it has pages.

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
        description: 'What Ogcode is, what it runs on, and how it is put together.',
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
        title: 'Remote deployment',
        slug: '/deployment',
        description: 'Reach a remote server safely: SSH tunnel, reverse proxy, Docker.',
      },
    ],
  },
  {
    group: 'Reference',
    icon: 'layers',
    items: [
      {
        title: 'Architecture & configuration',
        slug: '/architecture',
        description: 'The full reference: every subsystem, command and environment variable.',
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
