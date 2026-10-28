// The post comment thread. Comments run on giscus, which mirrors GitHub
// Discussions into the page: every thread is a Discussion in the repo,
// keyed by the page pathname, so the conversation lives next to the code
// it is about. Sign-in and moderation are GitHub's.
//
// The giscus GitHub App must be installed on the repo (and Discussions
// enabled there) or the embed shows an install prompt instead of a thread.
// Threads live at https://github.com/prasenjeet-symon/ogcode/discussions.
//
// Values are pinned here, never read from the environment, so the built
// HTML is identical everywhere. Comments.astro forwards them as data-*
// attributes on the lazily injected client.js.
export const GISCUS_THEME_URL = 'https://ogcode.in/blog/giscus-theme.css';

// `as const` keeps the literals, so a typo in a mapping or loading name
// fails the build instead of silently changing the widget's behaviour.
export const GISCUS = {
  repo: 'prasenjeet-symon/ogcode',
  repoId: 'R_kgDOSRkf9g',
  category: 'General',
  categoryId: 'DIC_kwDOSRkf9s4DAniu',
  mapping: 'pathname',
  strict: '0',
  reactionsEnabled: '1',
  emitMetadata: '0',
  inputPosition: 'top',
  lang: 'en',
  loading: 'lazy',
  theme: GISCUS_THEME_URL,
} as const;
