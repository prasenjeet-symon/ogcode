# Ogcode docs site

The documentation at [ogcode.in/docs](https://ogcode.in/docs). A sibling of
`blog/`, built the same way: a fully static Astro project that pre-renders every
page to plain HTML, with a small Pagefind search index and a dark/light toggle
as the only client-side JavaScript.

- `src/pages/index.astro` — the docs landing page (hero + group cards).
- `src/pages/[...slug].astro` — every other docs page, rendered from a markdown
  or MDX file in `src/content/docs/`.
- `src/lib/nav.ts` — the sidebar tree; also drives prev/next and the landing
  cards. Hand-authored so ordering and grouping are explicit.
- `src/layouts/Docs.astro` — the three-column shell: sidebar, prose, on-this-page.

## Writing a page

Add a file under `src/content/docs/`. The filename is the URL: `install.mdx` →
`ogcode.in/docs/install/`. Frontmatter:

```yaml
---
title: Install
description: One line shown in search results and the page lede.
---
```

Add it to `src/lib/nav.ts` so it appears in the sidebar and prev/next.

## Build

```sh
npm install --legacy-peer-deps
npm run build      # astro build && node postbuild.mjs
```

`postbuild.mjs` sweeps Astro's content-layer stubs and runs Pagefind over the
finished HTML, writing the search bundle to `../docs/docs/pagefind`.

The output lands in `../docs/docs`, which GitHub Pages serves at `/docs/`.
`npm run dev` serves the site locally at `http://localhost:4321/docs/`.
