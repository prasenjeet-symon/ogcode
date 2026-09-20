# Ogcode blog

The blog at [ogcode.xyz/blog](https://ogcode.xyz/blog/). Fully static — every
page is pre-rendered HTML with zero client-side JavaScript, built with
[Astro](https://astro.build) and styled to match the homepage design system
(`docs/index.html`).

## Writing a post

Add one markdown file to `src/content/posts/`. The filename becomes the URL:
`why-recall.md` → `ogcode.xyz/blog/why-recall/`.

```markdown
---
title: "Why recall beats replay"
description: "One or two sentences. Shown on the index, in search results, and on social cards."
pubDate: 2026-09-21
tags: ["context-engine"]        # optional
draft: true                     # optional — visible in dev, excluded from builds
# updatedDate: 2026-09-25      # optional
# author: "Prasenjeet Kumar"   # optional, this is the default
# ogImage: "https://ogcode.xyz/media/og.png"  # optional, this is the default
---

Post body in markdown. Code blocks are highlighted with the site palette.
```

Flip `draft: true` off (or remove it) to publish. Reading time is computed
automatically.

## Developing

```sh
npm install
npm run dev        # http://localhost:4321/blog/ — drafts are visible here
npm run build      # writes static HTML to ../docs/blog (gitignored)
```

`astro dev` serves the real site fonts by mapping `/fonts/` onto `../docs/fonts`.

## How it deploys

GitHub Pages serves the `docs/` folder. The Pages workflow
(`.github/workflows/pages.yml`) runs `npm ci && npm run build` here first, which
writes the blog into `docs/blog/` before the folder is uploaded — so the built
blog is never committed, and every push to `main` touching `docs/**` or
`blog/**` redeploys it.

Feeds and metadata come for free: `/blog/rss.xml`, `/blog/sitemap-index.xml`
(referenced from `docs/robots.txt`), canonical URLs, OpenGraph/Twitter cards,
and JSON-LD `BlogPosting` on every post.
