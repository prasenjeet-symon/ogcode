import { createMemo, createEffect } from 'solid-js';
import { marked } from 'marked';
import { markedHighlight } from 'marked-highlight';
import hljs from 'highlight.js';
import DOMPurify from 'dompurify';
import mermaid from 'mermaid';
import katex from 'katex';
import 'katex/dist/katex.min.css';
import Plotly from 'plotly.js-dist-min';
import { renderRoughDiagram, type RoughSpec } from './rough-renderer';

mermaid.initialize({
  startOnLoad: false,
  theme: 'dark',
  securityLevel: 'strict',
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
      name: 'inlineMath',
      level: 'inline' as const,
      start(src: string) { return src.indexOf('$'); },
      tokenizer(src: string) {
        const match = /^\$(?!\$)((?:[^$\n]|\\.)+?)\$/.exec(src);
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

// Encode HTML content for use in a data attribute, preserving all characters
// including <script>, event handlers, etc. We use base64 to avoid any
// encoding issues with DOMPurify or HTML attribute escaping.
function encodeHtmlForSrcdoc(html: string): string {
  return btoa(unescape(encodeURIComponent(html)));
}

function decodeHtmlFromSrcdoc(encoded: string): string {
  return decodeURIComponent(escape(atob(encoded)));
}

export default function MarkdownContent(props: { text: string; class?: string }) {
  let containerRef: HTMLDivElement | undefined;
  // Holds encoded HTML blocks from the latest memo run. The memo always
  // executes before the effect (memo is a dependency), so this is always
  // current by the time the effect reads it.
  let currentHtmlBlocks: string[] = [];

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
        const rawHtml = codeContent
          .replace(/<[^>]+>/g, '')
          .replace(/&amp;/g, '&')
          .replace(/&lt;/g, '<')
          .replace(/&gt;/g, '>')
          .replace(/&quot;/g, '"')
          .replace(/&#39;/g, "'");
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
        /<pre><code class="hljs language-latex">([\s\S]*?)<\/code><\/pre>/g,
        (_match: string, codeContent: string) => {
          // Decode HTML entities from highlight.js
          const rawLatex = codeContent
            .replace(/<[^>]+>/g, '')
            .replace(/&amp;/g, '&')
            .replace(/&lt;/g, '<')
            .replace(/&gt;/g, '>')
            .replace(/&quot;/g, '"')
            .replace(/&#39;/g, "'");
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

    const blocksData = currentHtmlBlocks;

    // Render mermaid diagrams
    const mermaidNodes = containerRef.querySelectorAll('.mermaid');
    if (mermaidNodes.length > 0) {
      requestAnimationFrame(() => {
        mermaid.run({ nodes: mermaidNodes }).catch(() => {});
      });
    }

    // Render Plotly charts
    const plotlyNodes = containerRef.querySelectorAll<HTMLElement>('.plotly-chart');
    plotlyNodes.forEach(el => {
      try {
        const spec = JSON.parse(el.textContent || '{}');
        el.textContent = '';
        Plotly.newPlot(el, spec.data ?? [], spec.layout ?? {}, { responsive: true, displayModeBar: false, ...spec.config });
      } catch {
        el.textContent = 'Invalid Plotly spec';
      }
    });

    // Render Rough diagrams
    const roughNodes = containerRef.querySelectorAll<HTMLElement>('.rough-diagram');
    roughNodes.forEach(el => {
      try {
        const spec = JSON.parse(el.textContent || '{}') as RoughSpec;
        el.textContent = '';
        renderRoughDiagram(el, spec);
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

      // Create a sandboxed iframe
      const iframe = document.createElement('iframe');
      iframe.style.cssText = 'width:100%;border:none;overflow:hidden;background:transparent;';
      iframe.sandbox.add('allow-scripts', 'allow-same-origin');
      // Note: allow-same-origin is needed for scripts within the iframe to
      // execute properly (e.g., to access their own DOM). The iframe is still
      // sandboxed — it cannot access the parent page's DOM.

      // Wrap the content in a transparent template that blends seamlessly
      // into the chat. No background or card styling — the HTML content
      // should appear as part of the conversation, not as a separate widget.
      const darkWrap = `<!DOCTYPE html><html style="color-scheme:dark;"><head><meta charset="utf-8"><style>
body { margin: 0; padding: 0; background: transparent; color: #ededef; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; color-scheme: dark; }
a { color: #8b9cf7; }
table { border-collapse: collapse; margin: 0.75em 0; }
th, td { border: 1px solid #2a2a30; padding: 6px 12px; text-align: left; }
th { background: rgba(255, 255, 255, 0.05); }
img { max-width: 100%; height: auto; }
</style></head><body>${rawHtml}</body></html>`;

      // Set height via resize observer after content loads
      iframe.addEventListener('load', () => {
        try {
          const doc = iframe.contentDocument || iframe.contentWindow?.document;
          if (doc?.body) {
            // Add a small delay for scripts to render
            const setHeight = () => {
              const height = doc.body.scrollHeight;
              if (height > 0) {
                iframe.style.height = height + 'px';
              }
            };
            setHeight();
            // Also set height after a short delay for async rendering
            setTimeout(setHeight, 200);
            setTimeout(setHeight, 1000);
          }
        } catch {
          // Cross-origin restrictions — set a reasonable default
          iframe.style.height = '300px';
        }
      });

      iframe.srcdoc = darkWrap;
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

      // Header bar
      const header = document.createElement('div');
      header.className = 'latex-render-header';
      header.innerHTML = `
        <div class="latex-render-info">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
            <polyline points="14 2 14 8 20 8"/>
            <line x1="16" y1="13" x2="8" y2="13"/>
            <line x1="16" y1="17" x2="8" y2="17"/>
            <polyline points="10 9 9 9 8 9"/>
          </svg>
          <span class="latex-render-type">LaTeX (${docClass})</span>
          ${docTitle ? `<span class="latex-render-title">— ${docTitle}</span>` : ''}
        </div>
        <div class="latex-render-actions">
          <button class="latex-src-toggle" title="Show/hide source code">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="16 18 22 12 16 6"/>
              <polyline points="8 6 2 12 8 18"/>
            </svg>
          </button>
          <button class="latex-download-btn" title="Download PDF">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
              <polyline points="7 10 12 15 17 10"/>
              <line x1="12" y1="15" x2="12" y2="3"/>
            </svg>
            PDF
          </button>
        </div>
      `;

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

      // Compile and render inline
      const baseURL = import.meta.env.VITE_API_URL || '';

      const compileAndRender = async () => {
        loadingDiv.style.display = 'flex';
        statusDiv.style.display = 'none';
        statusDiv.className = 'latex-render-status';

        try {
          const res = await fetch(`${baseURL}/api/latex/pages`, {
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

          const data = await res.json();

          // Hide loading spinner
          loadingDiv.style.display = 'none';

          // Render page images inline
          if (data.pages && data.pages.length > 0) {
            pagesDiv.style.display = 'block';
            pagesDiv.innerHTML = ''; // clear previous

            data.pages.forEach((page: { image: string; pageNum: number }) => {
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
            loadingDiv.style.display = 'none';
            statusDiv.className = 'latex-render-status latex-success';
            statusDiv.style.display = 'block';
            statusDiv.textContent = '✓ PDF compiled (page rendering unavailable — download to view)';
          }
        } catch (err) {
          loadingDiv.style.display = 'none';
          statusDiv.className = 'latex-render-status latex-error';
          statusDiv.style.display = 'block';
          statusDiv.textContent = `✗ ${err instanceof Error ? err.message : String(err)}`;

          // Show source code so the user can see what was attempted
          sourceDiv.style.display = 'block';
        }
      };

      // Auto-compile on render
      compileAndRender();

      // Download button handler
      const downloadBtn = container.querySelector('.latex-download-btn');
      if (downloadBtn) {
        downloadBtn.addEventListener('click', async () => {
          // If we already have the PDF data, download directly
          const pdfBase64 = (container as any)._pdfBase64;
          if (pdfBase64) {
            const byteCharacters = atob(pdfBase64);
            const byteNumbers = new Array(byteCharacters.length);
            for (let i = 0; i < byteCharacters.length; i++) {
              byteNumbers[i] = byteCharacters.charCodeAt(i);
            }
            const byteArray = new Uint8Array(byteNumbers);
            const blob = new Blob([byteArray], { type: 'application/pdf' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `${(container as any)._docTitle || 'output'}.pdf`;
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
            a.download = 'output.pdf';
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
    <div
      ref={containerRef}
      class={`prose-chat break-words min-w-0 ${props.class ?? ''}`}
      innerHTML={html()}
    />
  );
}