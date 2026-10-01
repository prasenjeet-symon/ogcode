---
title: Rich results & preview
description: Diagrams, math, charts, and opening your local services in the browser.
---

Answers are not always prose. Ogcode renders the formats a technical answer actually needs, and it can hand you a link to anything running locally so you can open it in a browser.

## Rich answers

The agent can reply with, and you can write in the composer:

- **Diagrams** — Mermaid flowcharts, sequence and entity-relationship diagrams.
- **Math** — inline and display LaTeX.
- **Documents** — a complete LaTeX document, compiled and shown as pages (with a PDF to download).
- **Charts** — Plotly bar, line, scatter, pie and heatmap plots.
- **Sketches** — hand-drawn-style diagrams for quick layouts.
- **Sandboxed pages** — interactive HTML, CSS and JavaScript rendered in an isolated frame.

Each rendered diagram has a button to open it larger. Nothing to install — the formats render in the chat itself.

## Files you can open

Drop any file into your workspace's **`public/`** folder and it is served by Ogcode at a URL the UI can link to:

```
/public/<filename>
```

Use it for a generated report, a chart export, or a built site. The agent can also write there itself and hand you the link, so anything it produces that is better viewed as a file than read as text becomes a clickable link.

## Your local services, in the browser

When the agent starts a service while it works — a dev server, a dashboard, a preview build — you do not need to find a port and paste it into a new tab. Ogcode serves each one at its own hostname and lists it on the **Preview** page, where each service is a tile you can open in place.

Those preview URLs look like:

```
http://3000.preview.localhost:9595/
```

The first number is the service's port; the trailing number is the port your Ogcode server is running on. The path after the host is passed through unchanged, so apps that read their own address work as they normally would.

> **Browser note.** A `*.localhost` address resolves to your own machine only in Chromium-based browsers (Chrome, Edge, Brave). If a preview link does not open, try one of those — Safari and Firefox do not resolve these hostnames on their own.

## Opening previews from another machine

By default a preview reaches only the machine the browser is on. To open previews from a browser elsewhere, give Ogcode a real domain:

```bash
OGCODE_PREVIEW_DOMAIN=preview.example.com ogcode serve --port 9595
```

That needs a wildcard DNS record and, if you serve over HTTPS, a wildcard certificate. See [Remote deployment](/docs/deployment/) for putting it behind a proxy.
