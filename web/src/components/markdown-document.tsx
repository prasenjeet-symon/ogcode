import { createEffect, on, onCleanup } from 'solid-js';
import { Marked, type Tokens } from 'marked';
import hljs from 'highlight.js';
import DOMPurify from 'dompurify';
import mermaid from 'mermaid';
import katex from 'katex';
import 'katex/dist/katex.min.css';
import { hljsLanguage } from './code-viewer';
import { revealWithin, scrollParent } from '../lib/reveal';

/**
 * Renders a Markdown file from the workspace the way a reader expects to see it
 * — the Preview side of the file viewer's Preview/Source switch.
 *
 * This is deliberately not MarkdownContent, the chat renderer. A chat reply and
 * a project document want different things from the same syntax: the chat turns
 * an ```html fence into a live iframe and a ```latex fence into compiled pages,
 * because there the model is showing its work; in a README the same fences are
 * code samples, and running them would be both wrong and surprising. Chat
 * also treats single newlines as line breaks, where a document (and GitHub)
 * joins them into one paragraph. So this has its own Marked instance and its own
 * DOMPurify instance, and neither touches the chat's global configuration.
 *
 * What a document needs that chat does not: relative links and images resolved
 * against the file's own folder (images served by the workspace asset route,
 * links to other files opened in the viewer), heading anchors that in-page links
 * and the outline can jump to, front matter shown as a table, and GitHub's
 * `> [!NOTE]` alerts.
 */

export interface DocHeading {
  id: string;
  text: string;
  level: number;
}

export interface MarkdownDocumentProps {
  text: string;
  /** Absolute path of the file — relative references resolve against its folder. */
  path: string;
  /** Workspace root — a root-relative reference ("/docs/a.md") resolves against it. */
  root: string;
  /** URL an <img> can load a workspace image from. */
  assetURL: (absPath: string) => string;
  /** Opens another workspace file, optionally at a heading. */
  onOpenFile: (absPath: string, anchor?: string) => void;
  /** Called after each render with the article element and its outline. */
  onRendered?: (article: HTMLElement, headings: DocHeading[]) => void;
  /** Heading to bring into view once rendered (a link's #fragment). */
  anchor?: string;
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

const MAX_HIGHLIGHT = 60_000;

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function decodeEntities(s: string): string {
  return s
    .replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'").replace(/&amp;/g, '&');
}

// GitHub's anchor rule, so a link written for github.com lands here too:
// lower-case, drop everything but letters, digits, spaces, hyphens and
// underscores, then turn each space into a hyphen (without collapsing runs).
function slugify(text: string): string {
  return text.trim().toLowerCase().replace(/[^\p{L}\p{N}\s_-]/gu, '').replace(/\s/g, '-');
}

// Per-render state for the heading renderer. Parsing is synchronous, so a
// module-level collector reset before each parse is safe.
let headings: DocHeading[] = [];
let slugCounts = new Map<string, number>();

function uniqueSlug(text: string): string {
  const base = slugify(text) || 'section';
  const n = slugCounts.get(base) ?? 0;
  slugCounts.set(base, n + 1);
  return n === 0 ? base : `${base}-${n}`;
}

function renderMath(tex: string, displayMode: boolean): string {
  try {
    return katex.renderToString(tex, { displayMode, throwOnError: false, output: 'html' });
  } catch {
    return `<code>${escapeHtml(tex)}</code>`;
  }
}

const md = new Marked({ gfm: true, breaks: false });

md.use({
  extensions: [
    {
      name: 'blockMath',
      level: 'block',
      start(src: string) { return src.indexOf('$$'); },
      tokenizer(src: string) {
        const m = /^\$\$([\s\S]+?)\$\$/.exec(src);
        if (m) return { type: 'blockMath', raw: m[0], text: m[1].trim() };
      },
      renderer(token) {
        return `<div class="math-block">${renderMath(String(token.text), true)}</div>\n`;
      },
    },
    {
      // Stricter than chat's rule, because documents talk about money: the
      // opening $ must hug its first character, the closing $ its last, and no
      // digit may follow — so "costs $5 and $10" stays prose.
      name: 'inlineMath',
      level: 'inline',
      start(src: string) { return src.indexOf('$'); },
      tokenizer(src: string) {
        const m = /^\$(?=\S)((?:\\\$|[^$\n])+?)(?<=\S)\$(?!\d)/.exec(src);
        if (m) return { type: 'inlineMath', raw: m[0], text: m[1] };
      },
      renderer(token) {
        return renderMath(String(token.text), false);
      },
    },
  ],
  renderer: {
    heading(this: { parser: any }, { tokens, depth }: Tokens.Heading) {
      const html = this.parser.parseInline(tokens);
      const text = decodeEntities(this.parser.parseInline(tokens, this.parser.textRenderer)).trim();
      const id = uniqueSlug(text);
      headings.push({ id, text, level: depth });
      return `<h${depth} id="md-${id}">${html}</h${depth}>\n`;
    },
    code({ text, lang }: Tokens.Code) {
      const language = (lang || '').trim().split(/\s+/)[0].toLowerCase();
      if (language === 'mermaid') return `<pre class="mermaid">${escapeHtml(text)}</pre>\n`;
      if (language === 'math') return `<div class="math-block">${renderMath(text, true)}</div>\n`;
      const grammar = hljsLanguage(language);
      let body = escapeHtml(text);
      if (grammar && text.length <= MAX_HIGHLIGHT) {
        try { body = hljs.highlight(text, { language: grammar }).value; } catch { /* keep plain */ }
      }
      const cls = language ? `hljs language-${escapeHtml(language)}` : 'hljs';
      return `<pre><code class="${cls}">${body}</code></pre>\n`;
    },
    table(this: { parser: any }, token: Tokens.Table) {
      // Wrapped so a wide table scrolls inside the page column instead of
      // pushing the whole document sideways.
      const cell = (c: Tokens.TableCell, tag: 'th' | 'td') => {
        const align = c.align ? ` align="${c.align}"` : '';
        return `<${tag}${align}>${this.parser.parseInline(c.tokens)}</${tag}>`;
      };
      const head = `<tr>${token.header.map((c) => cell(c, 'th')).join('')}</tr>`;
      const body = token.rows.map((row) => `<tr>${row.map((c) => cell(c, 'td')).join('')}</tr>`).join('');
      return `<div class="md-table"><table><thead>${head}</thead>${body ? `<tbody>${body}</tbody>` : ''}</table></div>\n`;
    },
  },
});

mermaid.initialize({ startOnLoad: false, theme: 'dark', securityLevel: 'strict' });

// ---------------------------------------------------------------------------
// Front matter
// ---------------------------------------------------------------------------

const FRONT_MATTER = /^---\r?\n([\s\S]*?)\r?\n(?:---|\.\.\.)\r?\n?/;

/** Splits YAML front matter off the top of a document and renders it as a table. */
function frontMatter(src: string): { html: string; body: string } {
  const m = FRONT_MATTER.exec(src);
  if (!m) return { html: '', body: src };

  // Only the flat `key: value` shape (plus `- item` lists under a key) is laid
  // out as a table; anything richer is shown verbatim rather than half-parsed.
  const rows: { key: string; value: string[] }[] = [];
  let flat = true;
  for (const line of m[1].split(/\r?\n/)) {
    if (!line.trim() || line.trim().startsWith('#')) continue;
    const kv = /^([A-Za-z0-9_.-][\w .-]*?):\s*(.*)$/.exec(line);
    const item = /^\s+-\s+(.*)$/.exec(line);
    if (kv && !line.startsWith(' ')) {
      rows.push({ key: kv[1], value: kv[2] ? [kv[2]] : [] });
    } else if (item && rows.length) {
      rows[rows.length - 1].value.push(item[1]);
    } else {
      flat = false;
      break;
    }
  }

  const unquote = (v: string) => v.replace(/^(['"])(.*)\1$/, '$2');
  const html = flat && rows.length
    ? `<div class="md-front">${rows.map((r) =>
        `<div class="md-front-row"><div class="md-front-key">${escapeHtml(r.key)}</div>` +
        `<div class="md-front-val">${escapeHtml(r.value.map(unquote).join(', ')) || '&nbsp;'}</div></div>`).join('')}</div>`
    : `<pre><code class="hljs language-yaml">${escapeHtml(m[1])}</code></pre>`;
  return { html, body: src.slice(m[0].length) };
}

// ---------------------------------------------------------------------------
// References
// ---------------------------------------------------------------------------

const IMAGE_EXT = /\.(png|jpe?g|gif|webp|avif|svg|ico|bmp)$/i;

function dirname(p: string): string {
  const i = p.lastIndexOf('/');
  return i > 0 ? p.slice(0, i) : '/';
}

function normalize(p: string): string {
  const out: string[] = [];
  for (const seg of p.split('/')) {
    if (!seg || seg === '.') continue;
    if (seg === '..') out.pop();
    else out.push(seg);
  }
  return '/' + out.join('/');
}

type Ref =
  | { kind: 'external' }
  | { kind: 'anchor'; anchor: string }
  | { kind: 'file'; path: string; anchor?: string };

function resolveRef(href: string, fileDir: string, root: string): Ref {
  if (href.startsWith('#')) return { kind: 'anchor', anchor: safeDecode(href.slice(1)) };
  if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('//')) return { kind: 'external' };
  const [pathPart, ...frag] = href.split('#');
  const clean = safeDecode(pathPart.split('?')[0]);
  const anchor = frag.length ? safeDecode(frag.join('#')) : undefined;
  if (!clean) return anchor ? { kind: 'anchor', anchor } : { kind: 'external' };
  const path = normalize(clean.startsWith('/') ? `${root}/${clean}` : `${fileDir}/${clean}`);
  return { kind: 'file', path, anchor };
}

function safeDecode(s: string): string {
  try { return decodeURIComponent(s); } catch { return s; }
}

// ---------------------------------------------------------------------------
// Sanitizing — a private DOMPurify instance, so these hooks never reach chat
// ---------------------------------------------------------------------------

const purify = DOMPurify(window);
let refContext: { fileDir: string; root: string; assetURL: (p: string) => string } | null = null;

purify.addHook('afterSanitizeAttributes', (node) => {
  const ctx = refContext;
  if (!ctx) return;
  const el = node as Element;
  if (el.tagName === 'IMG') {
    const src = el.getAttribute('src');
    if (src) {
      const ref = resolveRef(src, ctx.fileDir, ctx.root);
      if (ref.kind === 'file') el.setAttribute('src', ctx.assetURL(ref.path));
    }
    el.setAttribute('loading', 'lazy');
    el.setAttribute('decoding', 'async');
  } else if (el.tagName === 'SOURCE') {
    // <picture> sources (GitHub's light/dark logo idiom). One URL per srcset
    // is the case worth handling; descriptors are kept as written.
    const srcset = el.getAttribute('srcset');
    if (srcset && !srcset.includes(',')) {
      const [url, ...desc] = srcset.trim().split(/\s+/);
      const ref = resolveRef(url, ctx.fileDir, ctx.root);
      if (ref.kind === 'file') el.setAttribute('srcset', [ctx.assetURL(ref.path), ...desc].join(' '));
    }
  } else if (el.tagName === 'A') {
    const href = el.getAttribute('href');
    if (!href) return;
    const ref = resolveRef(href, ctx.fileDir, ctx.root);
    if (ref.kind === 'external') {
      el.setAttribute('target', '_blank');
      el.setAttribute('rel', 'noopener noreferrer');
    } else if (ref.kind === 'anchor') {
      el.setAttribute('data-md-anchor', ref.anchor);
    } else if (IMAGE_EXT.test(ref.path)) {
      // A link to an image opens the image, not a "binary file" notice.
      el.setAttribute('href', ctx.assetURL(ref.path));
      el.setAttribute('target', '_blank');
      el.setAttribute('rel', 'noopener noreferrer');
    } else {
      el.setAttribute('data-md-file', ref.path);
      if (ref.anchor) el.setAttribute('data-md-anchor', ref.anchor);
      el.setAttribute('title', el.getAttribute('title') || 'Open in viewer');
    }
  }
});

// ---------------------------------------------------------------------------
// Post-processing
// ---------------------------------------------------------------------------

const ALERTS: Record<string, { label: string; icon: string }> = {
  note: { label: 'Note', icon: 'M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z' },
  tip: { label: 'Tip', icon: 'M12 18v-5.25m0 0a6.01 6.01 0 001.5-.189m-1.5.189a6.01 6.01 0 01-1.5-.189m3.75 7.478a12.06 12.06 0 01-4.5 0m3.75 2.383a14.406 14.406 0 01-3 0M14.25 18v-.192c0-.983.658-1.823 1.508-2.316a7.5 7.5 0 10-7.517 0c.85.493 1.509 1.333 1.509 2.316V18' },
  important: { label: 'Important', icon: 'M7.5 8.25h9m-9 3H12m-9.75 1.51c0 1.6 1.123 2.994 2.707 3.227 1.129.166 2.27.293 3.423.379.35.026.67.21.865.501L12 21l2.755-4.133a1.14 1.14 0 01.865-.501 48.172 48.172 0 003.423-.379c1.584-.233 2.707-1.626 2.707-3.228V6.741c0-1.602-1.123-2.995-2.707-3.228A48.394 48.394 0 0012 3c-2.392 0-4.744.175-7.043.513C3.373 3.746 2.25 5.14 2.25 6.741v6.018z' },
  warning: { label: 'Warning', icon: 'M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z' },
  caution: { label: 'Caution', icon: 'M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z' },
};

const SVG_NS = 'http://www.w3.org/2000/svg';

function alertIcon(d: string): SVGSVGElement {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.8');
  svg.setAttribute('width', '15');
  svg.setAttribute('height', '15');
  const path = document.createElementNS(SVG_NS, 'path');
  path.setAttribute('stroke-linecap', 'round');
  path.setAttribute('stroke-linejoin', 'round');
  path.setAttribute('d', d);
  svg.appendChild(path);
  return svg;
}

/** Turns GitHub's `> [!NOTE]` blockquotes into titled alerts. */
function decorateAlerts(root: DocumentFragment) {
  root.querySelectorAll('blockquote').forEach((bq) => {
    const p = bq.firstElementChild;
    if (!p || p.tagName !== 'P') return;
    const first = p.firstChild;
    if (!first || first.nodeType !== Node.TEXT_NODE) return;
    const m = /^\s*\[!(note|tip|important|warning|caution)\]\s*/i.exec(first.textContent ?? '');
    if (!m) return;
    const kind = m[1].toLowerCase();
    first.textContent = (first.textContent ?? '').slice(m[0].length);
    if (!p.textContent?.trim() && !p.querySelector('img')) p.remove();

    const box = document.createElement('div');
    box.className = `md-alert md-alert-${kind}`;
    const title = document.createElement('p');
    title.className = 'md-alert-title';
    title.append(alertIcon(ALERTS[kind].icon), ALERTS[kind].label);
    box.append(title, ...Array.from(bq.childNodes));
    bq.replaceWith(box);
  });
}

/** Gives each fenced block the chat's language chip and copy button. */
function decorateCodeBlocks(root: DocumentFragment) {
  root.querySelectorAll('pre').forEach((pre) => {
    const code = pre.querySelector('code');
    if (!code) return;
    const wrap = document.createElement('div');
    wrap.className = 'code-block';
    pre.replaceWith(wrap);
    wrap.appendChild(pre);

    const bar = document.createElement('div');
    bar.className = 'code-block-bar';
    const lang = Array.from(code.classList).find((c) => c.startsWith('language-'))?.slice(9) ?? '';
    if (lang && lang !== 'plaintext') {
      const chip = document.createElement('span');
      chip.className = 'code-block-lang';
      chip.textContent = lang;
      bar.appendChild(chip);
    }
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'code-block-copy';
    btn.textContent = 'Copy';
    btn.setAttribute('aria-label', 'Copy code');
    bar.appendChild(btn);
    wrap.appendChild(bar);
  });
}

function renderDocument(text: string, path: string, root: string, assetURL: (p: string) => string) {
  headings = [];
  slugCounts = new Map();
  const { html: front, body } = frontMatter(text);
  const html = front + (md.parse(body, { async: false }) as string);

  refContext = { fileDir: dirname(path), root, assetURL };
  let fragment: DocumentFragment;
  try {
    fragment = purify.sanitize(html, {
      USE_PROFILES: { html: true, svg: true },
      ADD_ATTR: ['target'],
      RETURN_DOM_FRAGMENT: true,
    });
  } finally {
    refContext = null;
  }
  decorateAlerts(fragment);
  decorateCodeBlocks(fragment);
  return { fragment, headings: headings.slice() };
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/**
 * Scrolls a rendered heading to the top of its pane and briefly marks it.
 * Returns false when there is no such heading or nothing to scroll yet.
 */
export function revealHeading(article: HTMLElement, id: string, smooth = true): boolean {
  const el = article.querySelector<HTMLElement>(`[id="md-${CSS.escape(id)}"]`)
    ?? article.querySelector<HTMLElement>(`[id="md-${CSS.escape(id.toLowerCase())}"]`);
  const sc = el && scrollParent(article);
  if (!el || !sc) return false;
  revealWithin(sc, el, { block: 'start', margin: 16, smooth });
  el.classList.remove('md-flash');
  void el.offsetWidth;
  el.classList.add('md-flash');
  return true;
}

export default function MarkdownDocument(props: MarkdownDocumentProps) {
  let article!: HTMLElement;
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => clearTimeout(copyTimer));

  createEffect(on(
    () => [props.text, props.path, props.root] as const,
    ([text, path, root]) => {
      let outline: DocHeading[] = [];
      try {
        const rendered = renderDocument(text, path, root, props.assetURL);
        outline = rendered.headings;
        article.replaceChildren(rendered.fragment);
      } catch (err) {
        // A document the renderer chokes on still gets read, as plain text.
        console.error('markdown preview failed:', err);
        const note = document.createElement('p');
        note.className = 'md-img-missing';
        note.textContent = "Couldn't render this document — showing it as plain text.";
        const pre = document.createElement('pre');
        pre.textContent = text;
        article.replaceChildren(note, pre);
      }

      const diagrams = article.querySelectorAll<HTMLElement>('.mermaid');
      if (diagrams.length) {
        requestAnimationFrame(() => { mermaid.run({ nodes: diagrams }).catch(() => {}); });
      }
      props.onRendered?.(article, outline);
      // Straight away when the pane is already laid out; a frame later when
      // this render is what gives it something to scroll.
      const anchor = props.anchor;
      if (anchor && !revealHeading(article, anchor, false)) {
        requestAnimationFrame(() => revealHeading(article, anchor, false));
      }
    },
  ));

  const onClick = (e: MouseEvent) => {
    const target = e.target as HTMLElement;

    const copy = target.closest<HTMLButtonElement>('.code-block-copy');
    if (copy) {
      const code = copy.closest('.code-block')?.querySelector('code');
      navigator.clipboard.writeText(code?.textContent ?? '').then(() => {
        copy.textContent = 'Copied';
        copy.classList.add('is-copied');
        clearTimeout(copyTimer);
        copyTimer = setTimeout(() => {
          copy.textContent = 'Copy';
          copy.classList.remove('is-copied');
        }, 1500);
      }).catch(() => {});
      return;
    }

    const link = target.closest<HTMLAnchorElement>('a');
    if (!link || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const file = link.getAttribute('data-md-file');
    const anchor = link.getAttribute('data-md-anchor') ?? undefined;
    if (file) {
      e.preventDefault();
      if (file !== props.path) props.onOpenFile(file, anchor);
      else if (anchor) revealHeading(article, anchor);
      else scrollParent(article)?.scrollTo({ top: 0, behavior: 'smooth' });
    } else if (anchor) {
      e.preventDefault();
      revealHeading(article, anchor);
    }
  };

  // A picture that will not load is replaced by a note naming it, so a moved
  // screenshot reads as "Image not found: Architecture" rather than a
  // broken-image glyph.
  const onError = (e: Event) => {
    const img = e.target as HTMLElement;
    if (img.tagName !== 'IMG') return;
    const alt = img.getAttribute('alt')?.trim();
    const note = document.createElement('span');
    note.className = 'md-img-missing';
    note.textContent = alt ? `Image not found: ${alt}` : 'Image not found';
    note.title = img.getAttribute('src') ?? '';
    img.replaceWith(note);
  };

  return (
    <article
      ref={(el) => {
        article = el;
        el.addEventListener('error', onError, true);
      }}
      class="md-doc"
      onClick={onClick}
    />
  );
}
