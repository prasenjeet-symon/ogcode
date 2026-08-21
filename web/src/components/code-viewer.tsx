import { Show, createMemo } from 'solid-js';
import hljs from 'highlight.js';

// Extensions highlight.js doesn't know by name, mapped to the grammar it does.
const LANG_ALIASES: Record<string, string> = {
  tsx: 'typescript',
  jsx: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  h: 'cpp',
  hpp: 'cpp',
  cc: 'cpp',
  mm: 'objectivec',
  m: 'objectivec',
  yml: 'yaml',
  sh: 'bash',
  zsh: 'bash',
  bash: 'bash',
  ps1: 'powershell',
  kt: 'kotlin',
  rs: 'rust',
  py: 'python',
  rb: 'ruby',
  md: 'markdown',
  mdx: 'markdown',
  txt: 'plaintext',
  svg: 'xml',
  html: 'xml',
  vue: 'xml',
  cs: 'csharp',
  gradle: 'groovy',
  mod: 'go',
  sum: 'plaintext',
};

function hljsLanguage(ext: string): string | null {
  const name = LANG_ALIASES[ext] ?? ext;
  return name && hljs.getLanguage(name) ? name : null;
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

// Highlighting is the only part of this viewer that can stall the tab: it turns
// each line into several DOM nodes, so a large file becomes hundreds of
// thousands of them and the main thread is gone for seconds. Plain text stays
// one text node however big it is, so falling back to it is always cheap.
const MAX_HIGHLIGHT_BYTES = 150_000;

// A minified bundle is small enough to pass the byte cap while being one
// enormous line, which is the pathological case for highlight.js.
const MAX_LINE_FOR_HIGHLIGHT = 2_000;

function hasVeryLongLine(code: string): boolean {
  let start = 0;
  for (;;) {
    const nl = code.indexOf('\n', start);
    if (nl < 0) return code.length - start > MAX_LINE_FOR_HIGHLIGHT;
    if (nl - start > MAX_LINE_FOR_HIGHLIGHT) return true;
    start = nl + 1;
  }
}

export interface CodeViewerProps {
  content: string;
  ext: string;
}

export default function CodeViewer(props: CodeViewerProps) {
  const lines = createMemo(() => props.content.split('\n'));

  // One text node of newline-separated numbers, rather than a div per line:
  // it stays aligned with the code because both sides share a line-height,
  // and a 20k-line file costs one node instead of 20k.
  const gutter = createMemo(() => lines().map((_, i) => i + 1).join('\n'));

  const html = createMemo(() => {
    const code = props.content;
    if (!code) return '';
    if (code.length > MAX_HIGHLIGHT_BYTES || hasVeryLongLine(code)) return escapeHtml(code);
    const lang = hljsLanguage(props.ext);
    try {
      return lang
        ? hljs.highlight(code, { language: lang }).value
        : escapeHtml(code);
    } catch {
      return escapeHtml(code);
    }
  });

  return (
    <div class="code-view h-full overflow-auto bg-[color:var(--bg-base)]">
      <div class="flex min-w-max text-[12px] leading-[19px] font-mono">
        <pre class="sticky left-0 z-10 shrink-0 select-none text-right px-3 py-3 text-[color:var(--text-muted)] bg-[color:var(--bg-base)] border-r border-[color:var(--border-subtle)]">
          {gutter()}
        </pre>
        <pre class="px-4 py-3">
          <code class="hljs" innerHTML={html()} />
        </pre>
      </div>
      <Show when={lines().length === 1 && !props.content}>
        <p class="px-4 py-3 text-[12px] text-[color:var(--text-muted)] font-sans">Empty file.</p>
      </Show>
    </div>
  );
}
