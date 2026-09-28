import { createSignal, createResource, createMemo, createEffect, on, onMount, onCleanup, Show, For } from 'solid-js';
import { getDocContent, getIndexedDoc, docAssetURL, type IndexFile } from '../api/client';
import CodeViewer, { formatBytes } from './code-viewer';
import MarkdownDocument, { revealHeading, type DocHeading } from './markdown-document';
import Popover from './popover';
import { basename, fileExt, relPath, langColor, tint } from './file-tree';

/**
 * The right-hand pane of the Project Index: one open file, read-only.
 *
 * Markdown opens rendered, the way a README is read, with a Preview/Source
 * switch in the file bar (⇧⌘V, as in editors) — the choice is remembered, so
 * someone who reads raw Markdown sets it once. Rendered documents get an
 * outline for jumping between headings, and a link to another file in the
 * workspace opens that file here instead of leaving the page.
 *
 * The index status in the bar opens what the index actually recorded for the
 * file — the labels an agent searching the index will find it by — instead of
 * leaving "indexed" as a bare yes.
 */

const MARKDOWN_EXT = new Set(['md', 'mdx', 'markdown', 'mdown', 'mkd']);
const VIEW_KEY = 'ogcode.docindex.mdView';

function readView(): 'preview' | 'source' {
  try {
    return localStorage.getItem(VIEW_KEY) === 'source' ? 'source' : 'preview';
  } catch {
    return 'preview';
  }
}

function ago(ts: number): string {
  if (!ts) return '';
  const s = Math.max(0, (Date.now() - ts) / 1000);
  if (s < 60) return 'just now';
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d} day${d === 1 ? '' : 's'} ago`;
  return new Date(ts).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
}

const Spinner = (props: { class?: string }) => (
  <div class={`border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin ${props.class ?? 'w-3.5 h-3.5'}`} />
);

export default function FileViewer(props: {
  file: IndexFile;
  /** Common path prefix of the tree, for showing the path relative to the project. */
  prefix: string;
  directory: string;
  /** Heading to open the file at (a link's #fragment). */
  anchor?: string;
  onOpenFile: (absPath: string, anchor?: string) => void;
  onRunIndex: () => void;
  /** Phones only: back to the tree. */
  onBack?: () => void;
}) {
  const [content] = createResource(
    () => props.file.path,
    (path) => getDocContent(path, props.directory || undefined),
  );

  const name = () => basename(props.file.path);
  const ext = () => fileExt(name());
  const rel = () => relPath(props.file.path, props.prefix);
  const folder = () => {
    const r = rel();
    const i = r.lastIndexOf('/');
    return i > 0 ? r.slice(0, i + 1) : '';
  };
  const isMarkdown = () => MARKDOWN_EXT.has(ext());
  const text = () => (content.error || !content() || content()!.binary ? '' : content()!.content);
  const lineCount = createMemo(() => (text() ? text().split('\n').length : 0));

  // ---- Preview / Source ------------------------------------------------

  const [view, setView] = createSignal<'preview' | 'source'>(readView());
  const setViewRemembered = (v: 'preview' | 'source') => {
    setView(v);
    try { localStorage.setItem(VIEW_KEY, v); } catch { /* private mode */ }
  };
  const showPreview = () => isMarkdown() && view() === 'preview';

  onMount(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.key === 'v' || e.key === 'V') || !e.shiftKey || !(e.metaKey || e.ctrlKey) || e.altKey) return;
      if (!isMarkdown() || document.querySelector('[aria-modal="true"]')) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
      e.preventDefault();
      setViewRemembered(view() === 'preview' ? 'source' : 'preview');
    };
    window.addEventListener('keydown', onKey);
    onCleanup(() => window.removeEventListener('keydown', onKey));
  });

  // ---- Scroll, outline -------------------------------------------------

  let scroller: HTMLDivElement | undefined;
  let article: HTMLElement | undefined;
  const [headings, setHeadings] = createSignal<DocHeading[]>([]);
  const [activeHeading, setActiveHeading] = createSignal('');
  const [outlineOpen, setOutlineOpen] = createSignal(false);
  const [outlineBtn, setOutlineBtn] = createSignal<HTMLButtonElement>();
  const [detailsOpen, setDetailsOpen] = createSignal(false);
  const [detailsBtn, setDetailsBtn] = createSignal<HTMLButtonElement>();

  // A different file starts at the top; the outline belongs to the old one.
  createEffect(on(() => props.file.path, () => {
    setHeadings([]);
    setOutlineOpen(false);
    setDetailsOpen(false);
    scroller?.scrollTo({ top: 0 });
  }, { defer: true }));

  const trackActive = () => {
    if (!article || !scroller || !outlineOpen()) return;
    const top = scroller.getBoundingClientRect().top + 72;
    let current = headings()[0]?.id ?? '';
    for (const h of headings()) {
      const el = article.querySelector(`[id="md-${CSS.escape(h.id)}"]`);
      if (!el) continue;
      if (el.getBoundingClientRect().top <= top) current = h.id;
      else break;
    }
    setActiveHeading(current);
  };

  const outlineBase = createMemo(() => Math.min(...headings().map((h) => h.level), 6));
  const outline = createMemo(() => headings().filter((h) => h.level <= outlineBase() + 2));

  // ---- Index entry -----------------------------------------------------

  const [entry] = createResource(
    () => (detailsOpen() && props.file.indexed ? props.file.path : null),
    (path) => getIndexedDoc(path, props.directory || undefined),
  );
  const keywords = createMemo(() => {
    const seen = new Set<string>();
    for (const p of entry()?.pages ?? []) for (const k of p.keywords ?? []) seen.add(k);
    return [...seen];
  });

  // ---- Copy path -------------------------------------------------------

  const [copied, setCopied] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => clearTimeout(copyTimer));
  const copyPath = async () => {
    try {
      await navigator.clipboard.writeText(props.file.path);
      setCopied(true);
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopied(false), 1400);
    } catch { /* clipboard unavailable */ }
  };

  return (
    <div class="@container flex flex-col h-full min-w-0">
      {/* ---- File bar ---- */}
      <div class="shrink-0 h-10 pl-3 pr-2 flex items-center gap-2 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)]">
        <Show when={props.onBack}>
          <button type="button" onClick={() => props.onBack?.()} class="icon-btn -ml-1.5" title="Back to files" aria-label="Back to files">
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
            </svg>
          </button>
        </Show>

        <span
          class="shrink-0 w-4 h-4 rounded flex items-center justify-center text-[0.5rem] font-bold font-mono uppercase"
          style={{ color: langColor(ext()), background: tint(langColor(ext()), 0.14) }}
        >
          {ext().slice(0, 2) || '?'}
        </span>

        <div class="min-w-0 flex items-baseline text-ui" title={props.file.path}>
          <Show when={folder()}>
            <span class="hidden @xl:block min-w-0 truncate font-mono text-micro text-[color:var(--text-muted)]">{folder()}</span>
          </Show>
          <span class="shrink-0 max-w-full truncate font-medium text-[color:var(--text-primary)]">{name()}</span>
        </div>

        {/* Index status — opens what the index recorded */}
        <button
          ref={setDetailsBtn}
          type="button"
          onClick={() => setDetailsOpen(!detailsOpen())}
          class="shrink-0 h-5 pl-1.5 pr-1 rounded-md flex items-center gap-1.5 text-[0.625rem] font-medium border transition-colors"
          classList={{
            'text-[color:var(--text-secondary)] border-[color:var(--border-default)] hover:border-[color:var(--border-strong)] hover:text-[color:var(--text-primary)]': props.file.indexed,
            'text-[color:var(--accent)] border-[color:var(--accent-ring)] hover:bg-[color:var(--accent-soft)]': !props.file.indexed,
            'bg-[color:var(--bg-hover)]': detailsOpen(),
          }}
          aria-haspopup="dialog"
          aria-expanded={detailsOpen()}
          aria-label={props.file.indexed ? 'Indexed — show what the index recorded' : 'Not indexed yet'}
          title={props.file.indexed ? 'What the index recorded for this file' : 'This file is not in the index yet'}
        >
          <span classList={{ 'idx-done-dot': props.file.indexed, 'idx-pending-dot': !props.file.indexed }} />
          <span class="hidden @md:inline">{props.file.indexed ? 'Indexed' : 'Not indexed'}</span>
          <svg class="w-2.5 h-2.5 opacity-60" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3">
            <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </button>

        <div class="flex-1" />

        <Show when={isMarkdown()}>
          <div class="seg shrink-0" role="tablist" aria-label="Markdown view">
            <button
              type="button"
              role="tab"
              class="seg-btn"
              aria-selected={view() === 'preview'}
              onClick={() => setViewRemembered('preview')}
              title="Rendered document (⇧⌘V)"
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z" />
                <path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
              </svg>
              <span class="hidden @lg:inline">Preview</span>
            </button>
            <button
              type="button"
              role="tab"
              class="seg-btn"
              aria-selected={view() === 'source'}
              onClick={() => setViewRemembered('source')}
              title="Markdown source (⇧⌘V)"
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M17.25 6.75L22.5 12l-5.25 5.25m-10.5 0L1.5 12l5.25-5.25m7.5-3l-4.5 16.5" />
              </svg>
              <span class="hidden @lg:inline">Source</span>
            </button>
          </div>
        </Show>

        <Show when={showPreview() && headings().length > 1}>
          <button
            ref={setOutlineBtn}
            type="button"
            onClick={() => { setOutlineOpen(!outlineOpen()); queueMicrotask(trackActive); }}
            class="icon-btn shrink-0"
            classList={{ 'is-open': outlineOpen() }}
            title="Outline"
            aria-label="Outline"
            aria-haspopup="dialog"
            aria-expanded={outlineOpen()}
          >
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75zm0 5.25h.007v.008H3.75V12zm0 5.25h.007v.008H3.75v-.008z" />
            </svg>
          </button>
        </Show>

        <button
          type="button"
          onClick={copyPath}
          class="icon-btn shrink-0"
          title={copied() ? 'Copied' : 'Copy path'}
          aria-label="Copy path"
        >
          <Show when={copied()} fallback={
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25m12 6.625v-1.875a3.375 3.375 0 00-3.375-3.375h-1.5a1.125 1.125 0 01-1.125-1.125v-1.5a3.375 3.375 0 00-3.375-3.375H9.75" />
            </svg>
          }>
            <svg class="w-3.5 h-3.5 text-[color:var(--success)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
            </svg>
          </Show>
        </button>

        <Show when={content() && !content()!.binary}>
          <span class="hidden @2xl:inline shrink-0 pl-1 text-[0.625rem] font-mono tabular-nums text-[color:var(--text-muted)]">
            {lineCount().toLocaleString()} lines · {formatBytes(content()!.size)}
          </span>
        </Show>
      </div>

      {/* ---- Contents ---- */}
      <div class="flex-1 min-h-0 relative">
        <Show when={!content.loading} fallback={
          <div class="h-full flex items-center justify-center gap-2 text-meta text-[color:var(--text-tertiary)]">
            <Spinner /> Opening…
          </div>
        }>
          <Show when={!content.error} fallback={
            <div class="h-full flex flex-col items-center justify-center text-center px-8 gap-1">
              <p class="text-ui text-[color:var(--text-secondary)]">Couldn't open this file</p>
              <p class="text-micro text-[color:var(--text-muted)] font-mono break-all">{rel()}</p>
            </div>
          }>
            <Show when={!content()?.binary} fallback={
              <div class="h-full flex flex-col items-center justify-center text-center px-8">
                <div class="w-11 h-11 rounded-xl bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-3">
                  <svg class="w-5 h-5 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25M9 16.5v.75m3-3v3M15 12v5.25m-4.5-15H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
                  </svg>
                </div>
                <p class="text-ui text-[color:var(--text-secondary)]">Binary file</p>
                <p class="text-micro text-[color:var(--text-muted)] mt-1">
                  {formatBytes(content()?.size || 0)} · not shown here
                  <Show when={props.file.indexed && props.file.pageCount > 0}>
                    {' '}· {props.file.pageCount} {props.file.pageCount === 1 ? 'page' : 'pages'} in the index
                  </Show>
                </p>
              </div>
            }>
              <Show when={showPreview()} fallback={<CodeViewer content={text()} ext={ext()} />}>
                <div
                  ref={scroller}
                  class="h-full overflow-y-auto overflow-x-hidden bg-[color:var(--bg-base)]"
                  onScroll={trackActive}
                >
                  <Show when={text().trim()} fallback={
                    <p class="px-6 py-8 text-meta text-[color:var(--text-muted)]">This document is empty.</p>
                  }>
                    <MarkdownDocument
                      text={text()}
                      path={props.file.path}
                      root={props.directory}
                      anchor={props.anchor}
                      assetURL={(p) => docAssetURL(p, props.directory || undefined)}
                      onOpenFile={props.onOpenFile}
                      onRendered={(el, hs) => { article = el; setHeadings(hs); }}
                    />
                  </Show>
                </div>
              </Show>
            </Show>
          </Show>
        </Show>
      </div>

      <Show when={content()?.truncated}>
        <div class="shrink-0 px-3 py-1.5 border-t border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] text-micro text-[color:var(--warning)]">
          Large file — showing the first 2 MB.
        </div>
      </Show>

      {/* ---- Outline popover ---- */}
      <Popover
        open={outlineOpen()}
        anchor={outlineBtn()}
        onClose={() => setOutlineOpen(false)}
        label="Outline"
        class="w-[288px]"
      >
        <div class="pop-label flex items-center justify-between">
          <span>Outline</span>
          <span class="tabular-nums normal-case tracking-normal font-normal">{headings().length} headings</span>
        </div>
        <div class="overflow-y-auto px-1.5 pb-1.5 min-h-0">
          <For each={outline()}>
            {(h) => (
              <button
                type="button"
                data-pop-item
                onClick={() => {
                  if (article) revealHeading(article, h.id);
                  setOutlineOpen(false);
                }}
                class="pop-item relative text-meta"
                classList={{ 'text-[color:var(--text-primary)] bg-[color:var(--bg-hover)]': activeHeading() === h.id }}
                style={{
                  'padding-left': `${0.625 + (h.level - outlineBase()) * 0.875}rem`,
                  'padding-top': '0.3125rem',
                  'padding-bottom': '0.3125rem',
                }}
                title={h.text}
              >
                <Show when={activeHeading() === h.id}>
                  <span class="absolute left-0 top-1.5 bottom-1.5 w-[2px] rounded-full bg-[color:var(--accent)]" />
                </Show>
                <span
                  class="truncate"
                  classList={{ 'font-medium text-[color:var(--text-primary)]': h.level === outlineBase() }}
                >
                  {h.text}
                </span>
              </button>
            )}
          </For>
        </div>
      </Popover>

      {/* ---- Index entry popover ---- */}
      <Popover
        open={detailsOpen()}
        anchor={detailsBtn()}
        onClose={() => setDetailsOpen(false)}
        align="start"
        label={props.file.indexed ? 'Index entry' : 'Not indexed'}
        class="w-[320px]"
      >
        <Show when={props.file.indexed} fallback={
          <div class="p-3.5 flex flex-col gap-2">
            <div class="flex items-center gap-2">
              <span class="idx-pending-dot" />
              <p class="text-ui font-medium text-[color:var(--text-primary)]">Not in the index yet</p>
            </div>
            <p class="text-meta text-[color:var(--text-tertiary)] leading-relaxed">
              Agents can still open it by path, but a search of the index won't find it. The next index run
              reads it and files it under labels they can search.
            </p>
            <button
              type="button"
              data-autofocus
              onClick={() => { setDetailsOpen(false); props.onRunIndex(); }}
              class="mt-1 self-start h-7 px-2.5 rounded-md text-meta font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] transition flex items-center gap-1.5"
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
              </svg>
              Update index…
            </button>
          </div>
        }>
          <div class="min-h-0 overflow-y-auto">
            <Show when={!entry.loading} fallback={
              <div class="p-3.5 flex flex-col gap-2.5">
                <div class="skel h-3 w-24" />
                <div class="skel h-3 w-40" />
              </div>
            }>
              <Show when={!entry.error && entry()} fallback={
                <p class="p-3.5 text-meta text-[color:var(--text-tertiary)]">Couldn't read this file's index entry.</p>
              }>
                <div class="p-3.5 pb-3 flex flex-col gap-3">
                  <Show when={keywords().length > 0}>
                    <div>
                      <p class="text-[0.625rem] font-semibold uppercase tracking-[0.06em] text-[color:var(--text-muted)] mb-1">Keywords</p>
                      <p class="text-micro font-mono text-[color:var(--text-tertiary)] leading-relaxed">
                        {keywords().slice(0, 40).join(' · ')}{keywords().length > 40 ? ' …' : ''}
                      </p>
                    </div>
                  </Show>
                </div>
                <div class="px-3.5 py-2 border-t border-[color:var(--border-subtle)] flex items-center gap-1.5 text-micro text-[color:var(--text-muted)] tabular-nums">
                  <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                  <span>Indexed {ago(entry()!.indexedAt)}</span>
                  <span>·</span>
                  <span>{entry()!.pageCount} {entry()!.pageCount === 1 ? 'page' : 'pages'}</span>
                </div>
              </Show>
            </Show>
          </div>
        </Show>
      </Popover>
    </div>
  );
}
