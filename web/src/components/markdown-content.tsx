import { createMemo, createEffect, createSignal, onCleanup, Show } from 'solid-js';
import { Portal } from 'solid-js/web';
import { marked } from 'marked';
import { markedHighlight } from 'marked-highlight';
import hljs from 'highlight.js';
import DOMPurify from 'dompurify';
import mermaid from 'mermaid';
import katex from 'katex';
import 'katex/dist/katex.min.css';
// Plotly is ~1MB minified — lazy-load it so the chat bundle stays lean until
// a message actually contains a chart. The resolved module is cached here so
// teardown helpers (purge of last run's plots) can reach it without importing.
type PlotlyModule = typeof import('plotly.js-dist-min');
let plotlyModule: PlotlyModule['default'] | null = null;
function loadPlotly(): Promise<PlotlyModule> {
  if (plotlyModule) return Promise.resolve({ default: plotlyModule } as unknown as PlotlyModule);
  return import('plotly.js-dist-min').then((m) => {
    plotlyModule = m.default;
    return m;
  });
}
import { renderRoughDiagram, type RoughSpec } from './rough-renderer';
import DiagramZoomDialog, { DIAGRAM_ZOOM_ICON, type DiagramZoomState } from './diagram-zoom';

// Mermaid ships light and dark themes; the chat follows the page theme
// (documentElement.dataset.theme, falling back to prefers-color-scheme),
// re-initialized per render in the effect below so a mid-session theme
// flip is picked up without a reload.
function mermaidTheme(): 'dark' | 'default' {
  const t = document.documentElement?.dataset?.theme;
  if (t === 'light') return 'default';
  if (t === 'dark') return 'dark';
  return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'default' : 'dark';
}

mermaid.initialize({
  startOnLoad: false,
  theme: 'dark',
  securityLevel: 'antiscript',
});

marked.use({
  extensions: [
    {
      name: 'blockMath',
      level: 'block' as const,
      start(src: string) { return src.indexOf('$$'); },
      tokenizer(src: string) {
        const match = /^\$\$([\s\S]+?)\$\$/.exec(src);
        if (match) return { type: 'blockMath', raw: match[0], text: match[1].trim() };
      },
      renderer(token: any) {
        try {
          return `<div class="math-block">${katex.renderToString(token.text, { displayMode: true, throwOnError: false, output: 'html' })}</div>\n`;
        } catch {
          return `<div class="math-block">${token.text}</div>\n`;
        }
      },
    },
    {
      // Same rule as the document viewer (markdown-document.tsx), and for the
      // same reason: chat replies quote prices too. The opening $ must hug its
      // first character, the closing $ its last, and no digit may follow — the
      // lax "anything up to the next $" rule paired the currency signs in
      // "at $3/1M … at $0.05/1M" into one math span and KaTeX typeset the prose
      // (spaces collapsed, ** shown as asterisks).
      name: 'inlineMath',
      level: 'inline' as const,
      start(src: string) { return src.indexOf('$'); },
      tokenizer(src: string) {
        const match = /^\$(?=\S)((?:\\\$|[^$\n])+?)(?<=\S)\$(?!\d)/.exec(src);
        if (match) return { type: 'inlineMath', raw: match[0], text: match[1].trim() };
      },
      renderer(token: any) {
        try {
          return katex.renderToString(token.text, { displayMode: false, throwOnError: false, output: 'html' });
        } catch {
          return `<span class="math-inline">${token.text}</span>`;
        }
      },
    },
  ],
});

marked.use(
  markedHighlight({
    emptyLangClass: 'hljs',
    langPrefix: 'hljs language-',
    highlight(code, lang) {
      const language = hljs.getLanguage(lang) ? lang : 'plaintext';
      return hljs.highlight(code, { language }).value;
    },
  })
);

marked.setOptions({
  breaks: true,
  gfm: true,
});

// Base64 over UTF-8 bytes (chunked so large blocks don't blow the argument
// list, and surrogate-safe — the old btoa(unescape(...)) path threw on lone
// surrogates and aborted every later diagram render in the same pass).
function encodeHtmlForSrcdoc(html: string): string {
  const bytes = new TextEncoder().encode(html);
  let bin = '';
  const CHUNK = 0x8000;
  for (let i = 0; i < bytes.length; i += CHUNK) {
    bin += String.fromCharCode.apply(null, bytes.subarray(i, i + CHUNK) as unknown as number[]);
  }
  return btoa(bin);
}

function decodeHtmlFromSrcdoc(encoded: string): string {
  const bin = atob(encoded);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new TextDecoder().decode(bytes);
}

// Chunked base64-to-bytes for large downloads (a PDF page bundle decoded in
// one atob + charCodeAt pass blocks the main thread the same way).
function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  const CHUNK = 0x8000;
  for (let i = 0; i < bin.length; i += CHUNK) {
    const end = Math.min(i + CHUNK, bin.length);
    for (let j = i; j < end; j++) out[j] = bin.charCodeAt(j);
  }
  return out;
}

// A ```latex block is compiled by POSTing its source to the server, which runs
// pdflatex. The markdown effect re-runs on every streaming chunk, so the
// result is cached by source and an in-flight compile is shared: one compile
// per block instead of one per render.
interface LatexResult {
  pages?: { image: string; pageNum: number }[];
  pdfBase64?: string;
  pdfSize?: number;
  title?: string;
}

const latexResults = new Map<string, LatexResult>();
const latexInflight = new Map<string, Promise<LatexResult>>();

function compileLatex(source: string): Promise<LatexResult> {
  const cached = latexResults.get(source);
  if (cached) return Promise.resolve(cached);
  const running = latexInflight.get(source);
  if (running) return running;

  const baseURL = import.meta.env.VITE_API_URL || '';
  const p = (async () => {
    const res = await fetch(`${baseURL}/api/latex/pages`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source }),
    });

    if (!res.ok) {
      let errorMsg = `Error ${res.status}`;
      try {
        const errData = await res.json();
        errorMsg = errData.error || errorMsg;
        if (errData.output) {
          errorMsg += '\n\npdflatex output:\n' + errData.output.slice(0, 500);
        }
      } catch {}
      throw new Error(errorMsg);
    }

    const data = (await res.json()) as LatexResult;
    latexResults.set(source, data);
    // Bound the cache: one entry per distinct source would otherwise grow
    // the heap monotonically over a long session with many documents.
    while (latexResults.size > 30) {
      const oldest = latexResults.keys().next();
      if (oldest.done) break;
      latexResults.delete(oldest.value);
    }
    return data;
  })();
  latexInflight.set(source, p);
  // Settle either way: a failure is not cached, so a later render may retry.
  p.then(() => latexInflight.delete(source), () => latexInflight.delete(source));
  return p;
}

export default function MarkdownContent(props: { text: string; class?: string }) {
  let containerRef: HTMLDivElement | undefined;
  // Holds encoded HTML blocks from the latest memo run. The memo always
  // executes before the effect (memo is a dependency), so this is always
  // current by the time the effect reads it.
  let currentHtmlBlocks: string[] = [];

  // Window listeners attached for the inline HTML frames of the latest render.
  // The frames (and their listeners) are rebuilt on every render, so the
  // previous batch is detached before the DOM is replaced and on teardown.
  const detachFrameListeners: Array<() => void> = [];
  onCleanup(() => {
    for (const detach of detachFrameListeners.splice(0)) detach();
  });

  // A diagram can be wider than the chat column; the magnifier under each one
  // opens the same diagram redrawn at the width of a large dialog. The dialog
  // holds no state of its own — it is handed a `render` callback the owning
  // branch below already knows how to draw with.
  const [zoom, setZoom] = createSignal<DiagramZoomState | null>(null);
  let zoomSeq = 0;

  const attachZoom = (
    target: HTMLElement,
    title: string,
    render: (host: HTMLElement) => void | Promise<void>,
  ) => {
    let frame = target.parentElement;
    if (!frame || !frame.classList.contains('diagram-frame')) {
      frame = document.createElement('div');
      frame.className = 'diagram-frame';
      target.replaceWith(frame);
      frame.appendChild(target);
    }
    if (frame.querySelector(':scope > .diagram-zoom')) return;
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'diagram-zoom';
    btn.title = 'Zoom diagram';
    btn.setAttribute('aria-label', 'Zoom diagram');
    btn.innerHTML = DIAGRAM_ZOOM_ICON;
    btn.addEventListener('click', () => setZoom({ title, render }));
    frame.appendChild(btn);
  };

  // highlight.js escapes quotes as &quot; and &#x27; (see escapeHTML in
  // highlight.js/lib/core.js), so decode every remaining entity after
  // stripping its <span> wrappers — not just &amp;/&lt;/&gt;/&quot;/&#39;.
  // A leftover like &#x27; inside a <script> breaks the script, and the
  // iframe then renders blank while still reporting its static height.
  const decodeHljsEntities = (s: string) =>
    s
      .replace(/<[^>]+>/g, '')
      .replace(/&#(\d+);/g, (_m, n: string) => String.fromCodePoint(parseInt(n, 10)))
      .replace(/&#x([0-9a-fA-F]+);/g, (_m, n: string) => String.fromCodePoint(parseInt(n, 16)))
      .replace(/&quot;/g, '"')
      .replace(/&#39;/g, "'")
      .replace(/&lt;/g, '<')
      .replace(/&gt;/g, '>')
      .replace(/&amp;/g, '&');

  const html = createMemo(() => {
    const raw = marked.parse(props.text, { async: false }) as string;

    // Extract HTML code blocks and replace them with placeholder divs BEFORE
    // DOMPurify runs, preserving <script> tags and event handlers for the
    // sandboxed iframe. Blocks are stored in currentHtmlBlocks (closure) so
    // the effect can access them directly — no DOM-based data transfer needed.
    const htmlBlocks: string[] = [];
    const withPlaceholders = raw.replace(
      /<pre><code class="hljs language-html">([\s\S]*?)<\/code><\/pre>/g,
      (_match, codeContent: string) => {
        // highlight.js wraps tokens in <span> tags before HTML-escaping, so
        // we must strip those span wrappers first. Otherwise <<span>html</span>>
        // reaches the iframe and the browser misparsed it as garbled markup.
        const rawHtml = decodeHljsEntities(codeContent);
        const encoded = encodeHtmlForSrcdoc(rawHtml);
        htmlBlocks.push(encoded);
        const idx = htmlBlocks.length - 1;
        return `<div class="html-render" data-srcdoc-idx="${idx}"></div>`;
      }
    );

    currentHtmlBlocks = htmlBlocks;

    const wrapped = withPlaceholders
      .replace(/<table\b([^>]*)>/g, '<div class="overflow-auto max-w-full"><table$1>')
      .replace(/<\/table>/g, '</table></div>')
      .replace(
        /<pre><code class="hljs language-mermaid">([\s\S]*?)<\/code><\/pre>/g,
        '<pre class="mermaid">$1</pre>'
      )
      .replace(
        /<pre><code class="hljs language-plotly">([\s\S]*?)<\/code><\/pre>/g,
        '<div class="plotly-chart" style="min-height:300px">$1</div>'
      )
      .replace(
        /<pre><code class="hljs language-rough">([\s\S]*?)<\/code><\/pre>/g,
        '<div class="rough-diagram" style="min-height:200px">$1</div>'
      )
      .replace(
        /<pre><code class="hljs language-math">([\s\S]*?)<\/code><\/pre>/g,
        (_match: string, codeContent: string) => {
          // Same entity/tag decode as the latex branch below; the fence
          // carries display TeX, which the document viewer also renders.
          const rawTex = decodeHljsEntities(codeContent);
          try {
            return `<div class="math-block">${katex.renderToString(rawTex, { displayMode: true, throwOnError: false, output: 'html' })}</div>`;
          } catch {
            return _match;
          }
        }
      )
      .replace(
        /<pre><code class="hljs language-latex">([\s\S]*?)<\/code><\/pre>/g,
        (_match: string, codeContent: string) => {
          // Decode HTML entities from highlight.js
          const rawLatex = decodeHljsEntities(codeContent);
          const encoded = encodeHtmlForSrcdoc(rawLatex);
          htmlBlocks.push(encoded);
          const idx = htmlBlocks.length - 1;
          return `<div class="latex-document" data-srcdoc-idx="${idx}"></div>`;
        }
      );

    return DOMPurify.sanitize(wrapped, {
      USE_PROFILES: { html: true, svg: true },
      ADD_ATTR: ['style', 'aria-hidden', 'data-srcdoc-idx'],
    });
  });

  createEffect(() => {
    const _ = html();
    if (!containerRef) return;
    for (const detach of detachFrameListeners.splice(0)) detach();

    const blocksData = currentHtmlBlocks;

    // Render mermaid diagrams. The source is read off each node before run(),
    // because run() replaces the node's children with the rendered <svg> and
    // the definition would otherwise be gone by the time the zoom opens.
    const mermaidNodes = containerRef.querySelectorAll<HTMLElement>('.mermaid');
    if (mermaidNodes.length > 0) {
      // Re-init per render so a theme flip is picked up without a reload.
      mermaid.initialize({ startOnLoad: false, theme: mermaidTheme(), securityLevel: 'antiscript' });
      requestAnimationFrame(() => {
        const sources = new Map<Element, string>();
        mermaidNodes.forEach((node) => sources.set(node, node.textContent || ''));
        mermaid
          .run({ nodes: mermaidNodes })
          .then(() => {
            mermaidNodes.forEach((node) => {
              const src = sources.get(node) ?? '';
              if (!src.trim()) return;
              attachZoom(node, 'Mermaid diagram', async (host) => {
                const { svg } = await mermaid.render(`diagram-zoom-${++zoomSeq}`, src);
                host.innerHTML = svg;
              });
            });
          })
          .catch(() => {
            mermaidNodes.forEach((node) => {
              if (!node.querySelector('svg')) node.textContent = 'Invalid Mermaid diagram';
            });
          });
      });
    }

    // Render Plotly charts. A JSON.parse failure is *not* an error here:
    // while streaming, the fence body is truncated JSON and the next chunk
    // re-runs this effect, so only a parsed-but-misshapen spec (or a render
    // rejection) shows "Invalid Plotly spec". spec.config is deliberately
    // ignored — responsive/displayModeBar stay ours.
    const plotlyNodes = containerRef.querySelectorAll<HTMLElement>('.plotly-chart');
    const plotIsLight = document.documentElement?.dataset?.theme === 'light' ||
      (!document.documentElement?.dataset?.theme && window.matchMedia?.('(prefers-color-scheme: light)').matches);
    plotlyNodes.forEach(el => {
      let spec: unknown;
      try {
        spec = JSON.parse(el.textContent || '{}');
      } catch {
        return;
      }
      if (typeof spec !== 'object' || spec === null || Array.isArray(spec)) {
        el.textContent = 'Invalid Plotly spec';
        return;
      }
      const s = spec as { data?: unknown; layout?: Record<string, unknown> };
      if (!Array.isArray(s.data)) {
        el.textContent = 'Invalid Plotly spec';
        return;
      }
      // The chat chrome is dark: default to a transparent canvas plus a
      // readable font unless the spec sets its own.
      const layout = {
        paper_bgcolor: 'rgba(0,0,0,0)',
        plot_bgcolor: 'rgba(0,0,0,0)',
        font: { color: plotIsLight ? '#1a1a1e' : '#ededef' },
        ...(s.layout ?? {}),
      } as Record<string, unknown>;
      const data = s.data as unknown[];
      el.textContent = '';
      void loadPlotly()
        .then((m) => m.default.react(el, data, layout, { responsive: true, displayModeBar: false }))
        .then(() => {
          attachZoom(el, 'Plotly chart', (host) => {
            // The zoom host starts out an empty div with no height, so
            // size the plot off the dialog body (which already has its
            // flex height); resize hooks keep it full-bleed on rotation.
            host.style.width = '100%';
            host.style.minHeight = '420px';
            host.style.height = 'min(60vh, 560px)';
            void loadPlotly()
              .then((m) => m.default.react(host, data, layout, { responsive: true, displayModeBar: false }))
              .then(() => {
                if (!window.ResizeObserver) {
                  try { plotlyModule?.Plots.resize(host); } catch { /* not plotted yet */ }
                  return;
                }
                const zro = new ResizeObserver(() => {
                  try { plotlyModule?.Plots.resize(host); } catch { /* torn down */ }
                });
                zro.observe(host);
                onCleanup(() => zro.disconnect());
              });
          });
        })
        .catch(() => {
          el.textContent = 'Invalid Plotly spec';
        });
      // The effect re-runs per chunk and innerHTML discards old plot DOM,
      // so purge each plot on re-run/unmount (react() registers
      // window-resize listeners that purge() removes).
      onCleanup(() => {
        try { plotlyModule?.purge(el); } catch { /* already discarded */ }
      });
      // responsive:true only tracks window resizes; follow the container too.
      if (window.ResizeObserver) {
        const ro = new ResizeObserver(() => {
          try { plotlyModule?.Plots.resize(el); } catch { /* not plotted yet */ }
        });
        ro.observe(el);
        onCleanup(() => ro.disconnect());
      }
    });

    // Render Rough diagrams
    const roughNodes = containerRef.querySelectorAll<HTMLElement>('.rough-diagram');
    roughNodes.forEach(el => {
      try {
        const spec = JSON.parse(el.textContent || '{}') as RoughSpec;
        el.textContent = '';
        renderRoughDiagram(el, spec);
        // The dialog is wider, so the SVG simply scales up to it — no need to
        // rewrite the spec's viewBox.
        attachZoom(el, 'Rough diagram', (host) => renderRoughDiagram(host, spec));
      } catch {
        el.textContent = 'Invalid Rough diagram spec';
      }
    });

    // Give every fenced code block a language chip and a copy button.
    //
    // This is a coding agent: half of what the model writes is meant to be
    // taken away and run somewhere. Selecting a block by hand loses the last
    // newline as often as not, so the copy affordance is not a nicety.
    // Only `pre > code` is enhanced — mermaid, plotly, rough and latex all use
    // a bare <pre>/<div> and must keep their own chrome.
    containerRef.querySelectorAll('pre').forEach((pre) => {
      const code = pre.querySelector('code');
      if (!code || pre.parentElement?.classList.contains('code-block')) return;

      const wrap = document.createElement('div');
      wrap.className = 'code-block';
      pre.replaceWith(wrap);
      wrap.appendChild(pre);

      const lang = Array.from(code.classList)
        .find((c) => c.startsWith('language-'))?.slice('language-'.length) ?? '';

      const bar = document.createElement('div');
      bar.className = 'code-block-bar';

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
      // The button lives in the wrapper, never inside <pre>, so a manual
      // select-all over the block never picks up the word "Copy".
      btn.addEventListener('click', () => {
        navigator.clipboard.writeText(code.textContent ?? '').then(() => {
          btn.textContent = 'Copied';
          btn.classList.add('is-copied');
          setTimeout(() => {
            btn.textContent = 'Copy';
            btn.classList.remove('is-copied');
          }, 1500);
        }).catch(() => {});
      });
      bar.appendChild(btn);
      wrap.appendChild(bar);
    });

    // Render HTML blocks in sandboxed iframes
    const htmlNodes = containerRef.querySelectorAll('.html-render');
    htmlNodes.forEach(el => {
      const idx = parseInt(el.getAttribute('data-srcdoc-idx') || '-1', 10);
      if (idx < 0 || idx >= blocksData.length) {
        el.textContent = 'Invalid HTML block';
        return;
      }

      const rawHtml = decodeHtmlFromSrcdoc(blocksData[idx]);

      // Create a sandboxed iframe. It runs the block's scripts but is NOT given
      // allow-same-origin, so the document keeps an opaque origin and cannot
      // reach this page's DOM, cookies or storage. Its height therefore arrives
      // by postMessage from a script injected into the frame, because the
      // parent cannot measure it — which is exactly the point.
      const iframe = document.createElement('iframe');
      iframe.style.cssText = 'width:100%;border:none;overflow:hidden;background:transparent;';
      iframe.sandbox.add('allow-scripts');

      // Wrap the content in a transparent template that blends seamlessly
      // into the chat. No background or card styling — the HTML content
      // should appear as part of the conversation, not as a separate widget.
      const htmlIsLight = document.documentElement?.dataset?.theme === 'light' ||
        (!document.documentElement?.dataset?.theme && window.matchMedia?.('(prefers-color-scheme: light)').matches);
      const htmlScheme = htmlIsLight ? 'light' : 'dark';
      const htmlFg = htmlIsLight ? '#1a1a1e' : '#ededef';
      const htmlLink = htmlIsLight ? '#0b57d0' : '#ffb38a';
      const htmlBorder = htmlIsLight ? '#d5d5da' : '#2a2a30';
      const htmlThBg = htmlIsLight ? 'rgba(0, 0, 0, 0.04)' : 'rgba(255, 255, 255, 0.05)';
      const frameDoc = `<!DOCTYPE html><html style="color-scheme:${htmlScheme};"><head><meta charset="utf-8"><style>
body { margin: 0; padding: 0; background: transparent; color: ${htmlFg}; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; color-scheme: ${htmlScheme}; }
a { color: ${htmlLink}; }
table { border-collapse: collapse; margin: 0.75em 0; }
th, td { border: 1px solid ${htmlBorder}; padding: 6px 12px; text-align: left; }
th { background: ${htmlThBg}; }
img { max-width: 100%; height: auto; }
</style></head><body>${rawHtml}<script>
(function () {
  function post() {
    var h = Math.max(
      document.body ? document.body.scrollHeight : 0,
      document.documentElement ? document.documentElement.scrollHeight : 0
    );
    parent.postMessage({ type: 'ogcode-html-height', height: h }, '*');
  }
  window.addEventListener('load', function () { post(); setTimeout(post, 200); setTimeout(post, 1000); });
  if (window.ResizeObserver) { new ResizeObserver(post).observe(document.documentElement); }
})();
</script></body></html>`;

      const onFrameMessage = (e: MessageEvent) => {
        if (e.source !== iframe.contentWindow) return;
        // The frame is sandboxed without allow-same-origin, so it posts
        // from an opaque origin. Nested frames inside the model's HTML
        // share the source check via contentWindow, so require the opaque
        // origin too before trusting a height message.
        if (e.origin !== 'null' && e.origin !== '') return;
        const d = e.data as { type?: string; height?: number } | null;
        if (d?.type !== 'ogcode-html-height' || typeof d.height !== 'number' || d.height <= 0) return;
        // A runaway frame must not push the transcript thousands of pixels tall.
        iframe.style.height = `${Math.min(d.height, 4000)}px`;
      };
      window.addEventListener('message', onFrameMessage);
      detachFrameListeners.push(() => window.removeEventListener('message', onFrameMessage));

      iframe.srcdoc = frameDoc;
      el.textContent = '';
      el.appendChild(iframe);
    });

    // Render LaTeX document blocks — compile to PDF and display pages inline
    const latexNodes = containerRef.querySelectorAll('.latex-document');
    latexNodes.forEach(el => {
      const idx = parseInt(el.getAttribute('data-srcdoc-idx') || '-1', 10);
      if (idx < 0 || idx >= blocksData.length) {
        el.textContent = 'Invalid LaTeX block';
        return;
      }

      const rawLatex = decodeHtmlFromSrcdoc(blocksData[idx]);

      // Extract document class and title for display
      const docClassMatch = rawLatex.match(/\\documentclass(?:\[.*?\])?\{(.+?)\}/);
      const titleMatch = rawLatex.match(/\\title\{(.+?)\}/);
      const docClass = docClassMatch ? docClassMatch[1] : 'article';
      const docTitle = titleMatch ? titleMatch[1].replace(/\\[a-zA-Z]+(?:\{.*?\})?/g, '').trim() : '';

      // Build the container
      const container = document.createElement('div');
      container.className = 'latex-render-container';

      // Header bar — built with DOM APIs and textContent so the
      // regex-extracted docClass/docTitle (raw model output) can never
      // inject markup. Only static icon SVGs use innerHTML, with no
      // interpolated data.
      const header = document.createElement('div');
      header.className = 'latex-render-header';
      const info = document.createElement('div');
      info.className = 'latex-render-info';
      info.innerHTML = `
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
            <polyline points="14 2 14 8 20 8"/>
            <line x1="16" y1="13" x2="8" y2="13"/>
            <line x1="16" y1="17" x2="8" y2="17"/>
            <polyline points="10 9 9 9 8 9"/>
          </svg>`;
      const typeSpan = document.createElement('span');
      typeSpan.className = 'latex-render-type';
      typeSpan.textContent = `LaTeX (${docClass})`;
      info.appendChild(typeSpan);
      if (docTitle) {
        const titleSpan = document.createElement('span');
        titleSpan.className = 'latex-render-title';
        titleSpan.textContent = `\u2014 ${docTitle}`;
        info.appendChild(titleSpan);
      }
      header.appendChild(info);
      const actions = document.createElement('div');
      actions.className = 'latex-render-actions';
      const zoomBtnEl = document.createElement('button');
      zoomBtnEl.type = 'button';
      zoomBtnEl.className = 'latex-zoom-btn';
      zoomBtnEl.title = 'Zoom document';
      zoomBtnEl.setAttribute('aria-label', 'Zoom document');
      zoomBtnEl.innerHTML = `
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="11" cy="11" r="7"/>
              <line x1="21" y1="21" x2="16.65" y2="16.65"/>
              <line x1="11" y1="7.5" x2="11" y2="14.5"/>
              <line x1="7.5" y1="11" x2="14.5" y2="11"/>
            </svg>
            Zoom`;
      actions.appendChild(zoomBtnEl);
      const srcToggleEl = document.createElement('button');
      srcToggleEl.type = 'button';
      srcToggleEl.className = 'latex-src-toggle';
      srcToggleEl.title = 'Show/hide source code';
      srcToggleEl.setAttribute('aria-label', 'Show/hide LaTeX source');
      srcToggleEl.innerHTML = `
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="16 18 22 12 16 6"/>
              <polyline points="8 6 2 12 8 18"/>
            </svg>`;
      actions.appendChild(srcToggleEl);
      const dlBtnEl = document.createElement('button');
      dlBtnEl.type = 'button';
      dlBtnEl.className = 'latex-download-btn';
      dlBtnEl.title = 'Download PDF';
      dlBtnEl.setAttribute('aria-label', 'Download PDF');
      dlBtnEl.innerHTML = `
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
              <polyline points="7 10 12 15 17 10"/>
              <line x1="12" y1="15" x2="12" y2="3"/>
            </svg>
            PDF`;
      actions.appendChild(dlBtnEl);
      header.appendChild(actions);

      // Page images container (shown after compilation)
      const pagesDiv = document.createElement('div');
      pagesDiv.className = 'latex-pages';
      pagesDiv.style.display = 'none'; // hidden until pages are loaded

      // Source code preview (toggleable)
      const sourceDiv = document.createElement('div');
      sourceDiv.className = 'latex-source-preview';
      sourceDiv.style.display = 'none'; // hidden by default, toggle via button
      const lines = rawLatex.split('\n');
      const previewText = lines.join('\n');
      sourceDiv.textContent = previewText;

      // Status indicator
      const statusDiv = document.createElement('div');
      statusDiv.className = 'latex-render-status';

      // Loading spinner (shown while compiling)
      const loadingDiv = document.createElement('div');
      loadingDiv.className = 'latex-loading';
      loadingDiv.innerHTML = `
        <div class="latex-spinner"></div>
        <span>Compiling LaTeX...</span>
      `;

      container.appendChild(header);
      container.appendChild(loadingDiv);
      container.appendChild(pagesDiv);
      container.appendChild(sourceDiv);
      container.appendChild(statusDiv);
      el.textContent = '';
      el.appendChild(container);

      // Toggle source code visibility
      const srcToggle = container.querySelector('.latex-src-toggle');
      if (srcToggle) {
        srcToggle.addEventListener('click', () => {
          const isVisible = sourceDiv.style.display !== 'none';
          sourceDiv.style.display = isVisible ? 'none' : 'block';
          srcToggle.classList.toggle('active', !isVisible);
        });
      }

      // Compile and render inline (cached by source — see compileLatex)
      const applyLatexResult = (data: LatexResult) => {
        // Hide loading spinner
        loadingDiv.style.display = 'none';

        // Render page images inline
        if (data.pages && data.pages.length > 0) {
          pagesDiv.style.display = 'block';
          pagesDiv.innerHTML = ''; // clear previous

          data.pages.forEach((page) => {
            const pageDiv = document.createElement('div');
            pageDiv.className = 'latex-page';

            const img = document.createElement('img');
            img.src = `data:image/jpeg;base64,${page.image}`;
            img.alt = `Page ${page.pageNum}`;
            img.className = 'latex-page-img';
            img.loading = 'lazy';

            pageDiv.appendChild(img);
            pagesDiv.appendChild(pageDiv);
          });

          // Store pdfBase64 for download
          (container as any)._pdfBase64 = data.pdfBase64;
          (container as any)._pdfSize = data.pdfSize;
          (container as any)._docTitle = data.title || 'output';
        } else if (data.pdfBase64) {
          // Fallback: no page images but have PDF (fitz unavailable)
          pagesDiv.style.display = 'none';
          (container as any)._pdfBase64 = data.pdfBase64;
          (container as any)._pdfSize = data.pdfSize;
          (container as any)._docTitle = data.title || 'output';

          // Show source since we can't render pages
          sourceDiv.style.display = 'block';
          statusDiv.className = 'latex-render-status latex-success';
          statusDiv.style.display = 'block';
          statusDiv.textContent = '✓ PDF compiled (page rendering unavailable — download to view)';
        }
      };

      const compileAndRender = async () => {
        // Disabled while the auto-compile is in flight so an early click
        // cannot fire a second full compile via POST /api/latex.
        const dlBtn = container.querySelector<HTMLButtonElement>('.latex-download-btn');
        dlBtn?.setAttribute('disabled', 'true');
        loadingDiv.style.display = 'flex';
        statusDiv.style.display = 'none';
        statusDiv.className = 'latex-render-status';

        try {
          applyLatexResult(await compileLatex(rawLatex));
        } catch (err) {
          loadingDiv.style.display = 'none';
          statusDiv.className = 'latex-render-status latex-error';
          statusDiv.style.display = 'block';
          statusDiv.textContent = `✗ ${err instanceof Error ? err.message : String(err)}`;

          // Show source code so the user can see what was attempted
          sourceDiv.style.display = 'block';
        } finally {
          dlBtn?.removeAttribute('disabled');
        }
      };

      // Zoom: the header button shows the compiled page images at full width.
      const zoomBtn = container.querySelector('.latex-zoom-btn');
      if (zoomBtn) {
        zoomBtn.addEventListener('click', () => {
          const imgs = Array.from(pagesDiv.querySelectorAll<HTMLImageElement>('.latex-page-img')).map((i) => i.src);
          setZoom({
            title: docTitle ? `LaTeX document — ${docTitle}` : `LaTeX document (${docClass})`,
            render: (host) => {
              if (!imgs.length) {
                host.innerHTML = '<p class="diagram-zoom-empty">No rendered pages — download the PDF to view.</p>';
                return;
              }
              host.innerHTML = '';
              imgs.forEach((src, i) => {
                const img = document.createElement('img');
                img.src = src;
                img.alt = `Page ${i + 1}`;
                img.className = 'latex-page-img';
                host.appendChild(img);
              });
            },
          });
        });
      }

      // Auto-compile on render, gated and debounced: while streaming, this
      // effect re-runs per chunk and each partial fence would otherwise burn
      // a server pdflatex run. Only compile once the source looks complete,
      // and coalesce rapid chunks into one compile.
      const downloadBtn = container.querySelector<HTMLButtonElement>('.latex-download-btn');
      if (/\\end\{document\}/.test(rawLatex)) {
        // Disabled from setup (not just in-flight) so a click during the
        // debounce wait cannot fire a fallback compile alongside the timer.
        downloadBtn?.setAttribute('disabled', 'true');
        const timer = window.setTimeout(() => { void compileAndRender(); }, 400);
        onCleanup(() => window.clearTimeout(timer));
      } else {
        loadingDiv.style.display = 'none';
        statusDiv.className = 'latex-render-status';
        statusDiv.style.display = 'block';
        statusDiv.textContent = 'Composing LaTeX\u2026';
      }

      // Download button handler
      if (downloadBtn) {
        downloadBtn.addEventListener('click', async () => {
          // If we already have the PDF data, download directly
          const pdfBase64 = (container as any)._pdfBase64;
          if (pdfBase64) {
            const byteArray = base64ToBytes(pdfBase64);
            const blob = new Blob([byteArray.buffer as ArrayBuffer], { type: 'application/pdf' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            const cachedTitle = String((container as any)._docTitle || 'output').replace(/[^\w\-]+/g, '_').slice(0, 80) || 'output';
            a.download = `${cachedTitle}.pdf`;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
            return;
          }

          // Fallback: compile again for download
          downloadBtn.setAttribute('disabled', 'true');
          downloadBtn.innerHTML = `
            <svg class="latex-spin" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10" stroke-dasharray="32" stroke-dashoffset="12"/>
            </svg>
            Compiling...
          `;

          try {
            const baseURL = import.meta.env.VITE_API_URL || '';
            const res = await fetch(`${baseURL}/api/latex`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ source: rawLatex }),
            });

            if (!res.ok) {
              let errorMsg = `Error ${res.status}`;
              try {
                const errData = await res.json();
                errorMsg = errData.error || errorMsg;
                if (errData.output) {
                  errorMsg += '\n\npdflatex output:\n' + errData.output.slice(0, 500);
                }
              } catch {}
              throw new Error(errorMsg);
            }

            const blob = await res.blob();
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            const fallbackName = String(docTitle || (container as any)._docTitle || 'output').replace(/[^\w\-]+/g, '_').slice(0, 80) || 'output';
            a.download = `${fallbackName}.pdf`;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);

            downloadBtn.innerHTML = `
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="20 6 9 17 4 12"/>
              </svg>
              Done
            `;
            setTimeout(() => {
              downloadBtn.removeAttribute('disabled');
              downloadBtn.innerHTML = `
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
                  <polyline points="7 10 12 15 17 10"/>
                  <line x1="12" y1="15" x2="12" y2="3"/>
                </svg>
                PDF
              `;
            }, 2000);
          } catch (err) {
            downloadBtn.removeAttribute('disabled');
            downloadBtn.innerHTML = `
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
                <polyline points="7 10 12 15 17 10"/>
                <line x1="12" y1="15" x2="12" y2="3"/>
              </svg>
              PDF
            `;
            statusDiv.className = 'latex-render-status latex-error';
            statusDiv.style.display = 'block';
            statusDiv.textContent = `✗ Download failed: ${err instanceof Error ? err.message : String(err)}`;
          }
        });
      }
    });
  });

  return (
    <>
      <div
        ref={containerRef}
        class={`prose-chat break-words min-w-0 ${props.class ?? ''}`}
        innerHTML={html()}
      />
      <Show when={zoom()}>{(state) => (
        <Portal>
          <DiagramZoomDialog state={state()} onClose={() => setZoom(null)} />
        </Portal>
      )}</Show>
    </>
  );
}