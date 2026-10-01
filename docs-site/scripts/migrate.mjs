// One-shot migration helper (not part of the build).
//
// Copies two long-form reference documents out of the repo root into the docs
// content collection. It strips the source h1 (the page title comes from the
// layout), drops standalone `---` separators (the prose styles draw their own
// heading rules), and repoints the handful of relative links at their new
// homes. Everything else is copied byte for byte.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repo = fileURLToPath(new URL('../../', import.meta.url));
const outDir = fileURLToPath(new URL('../src/content/docs/', import.meta.url));

/** Remove lines that are exactly `---` but only outside fenced code blocks. */
function stripSeparators(text) {
  const lines = text.split('\n');
  const out = [];
  let fence = null;
  for (const line of lines) {
    const m = /^(\s*)(`{3,}|~{3,})/.exec(line);
    if (m) {
      const marker = m[2][0];
      if (fence === null) fence = marker;
      else if (fence === marker) fence = null;
      out.push(line);
      continue;
    }
    if (fence === null && line.trim() === '---') continue;
    out.push(line);
  }
  return out.join('\n');
}

function write(name, frontmatter, body) {
  const text = `---\n${frontmatter.trim()}\n---\n\n${body.trim()}\n`;
  fs.writeFileSync(path.join(outDir, name), text);
  console.log(`wrote ${name} (${text.split('\n').length} lines)`);
}

fs.mkdirSync(outDir, { recursive: true });

// ── docs/OUTLINE.md -> architecture.md ────────────────────────────────
// The source opens with an h1 and a two-line blockquote; the h1 is redundant
// (the layout renders the title) and the blockquote becomes the page note.
{
  const src = fs.readFileSync(path.join(repo, 'docs/OUTLINE.md'), 'utf8');
  const body = stripSeparators(src);
  const idx = body.search(/^## /m);
  const rest = body.slice(idx);
  write(
    'architecture.md',
    'title: "Architecture & configuration"\ndescription: "The full reference: every subsystem, command and environment variable."',
    `> Architecture and configuration reference for the ogcode codebase, regenerated from codebase analysis at **v0.42.0**.\n\n${rest}`,
  );
}

// ── docs/DEPLOY.md -> deployment.md ───────────────────────────────────
// Only the h1 goes; the intro paragraph and its three-item list are kept.
{
  const src = fs.readFileSync(path.join(repo, 'docs/DEPLOY.md'), 'utf8');
  let body = stripSeparators(src);
  body = body.replace(/^# .*\n+/, ''); // drop the h1
  const gh = 'https://github.com/prasenjeet-symon/ogcode/blob/main';
  body = body
    .replaceAll('(../controlplane/docs/deploy.md)', `(${gh}/controlplane/docs/deploy.md)`)
    .replaceAll('(../README.md#remote-deployment-and-security)', `(${gh}/README.md#remote-deployment-and-security)`)
    .replaceAll('(OUTLINE.md)', '(/docs/architecture/)');
  write(
    'deployment.md',
    'title: "Remote deployment"\ndescription: "Reach a remote server safely — SSH tunnel, reverse proxy with HTTPS, or Docker."',
    body,
  );
}
