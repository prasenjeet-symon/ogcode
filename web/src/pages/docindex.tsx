import { createSignal, Show, For, onMount, createMemo } from 'solid-js';
import { useServer } from '../context/server';
import { useDocIndex } from '../context/docindex';
import { type DocSummary } from '../api/client';
import SessionSidebar from '../components/session-sidebar';
import PlanSidebar from '../components/plan-sidebar';

function Sidebar() {
  const server = useServer();
  return (
    <Show when={server.mode() === 'plan'} fallback={<SessionSidebar />}>
      <PlanSidebar />
    </Show>
  );
}

function docBasename(docPath: string): string {
  return docPath.split('/').pop() || docPath;
}

function docExt(docPath: string): string {
  const name = docBasename(docPath);
  const dot = name.lastIndexOf('.');
  return dot >= 0 ? name.slice(dot + 1).toLowerCase() : '';
}

function formatDate(ms: number): string {
  if (!ms) return '';
  return new Date(ms).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

// Find the longest common directory prefix across all doc paths.
function commonPrefix(docs: DocSummary[]): string {
  if (docs.length === 0) return '';
  const parts = docs.map(d => d.docPath.split('/').slice(0, -1));
  const minLen = Math.min(...parts.map(p => p.length));
  let len = 0;
  for (let i = 0; i < minLen; i++) {
    if (parts.every(p => p[i] === parts[0][i])) len = i + 1;
    else break;
  }
  return parts[0].slice(0, len).join('/');
}

interface FolderGroup {
  relPath: string; // relative to project root, '' = root
  files: DocSummary[];
}

function buildFolderGroups(docs: DocSummary[]): FolderGroup[] {
  if (docs.length === 0) return [];
  const prefix = commonPrefix(docs);
  const map = new Map<string, DocSummary[]>();
  for (const doc of docs) {
    const dir = doc.docPath.split('/').slice(0, -1).join('/');
    let rel = dir === prefix ? '' : dir.startsWith(prefix + '/') ? dir.slice(prefix.length + 1) : dir;
    if (!map.has(rel)) map.set(rel, []);
    map.get(rel)!.push(doc);
  }
  return [...map.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([relPath, files]) => ({ relPath, files }));
}

const extColors: Record<string, string> = {
  pdf:  'text-red-400 bg-red-400/10 border-red-400/20',
  docx: 'text-blue-400 bg-blue-400/10 border-blue-400/20',
  md:   'text-zinc-300 bg-zinc-300/10 border-zinc-300/20',
  html: 'text-orange-400 bg-orange-400/10 border-orange-400/20',
  go:   'text-cyan-400 bg-cyan-400/10 border-cyan-400/20',
  ts:   'text-blue-400 bg-blue-400/10 border-blue-400/20',
  tsx:  'text-blue-400 bg-blue-400/10 border-blue-400/20',
  js:   'text-yellow-400 bg-yellow-400/10 border-yellow-400/20',
  jsx:  'text-yellow-400 bg-yellow-400/10 border-yellow-400/20',
  py:   'text-green-400 bg-green-400/10 border-green-400/20',
  rs:   'text-orange-500 bg-orange-500/10 border-orange-500/20',
  sql:  'text-purple-400 bg-purple-400/10 border-purple-400/20',
};

function ExtBadge(props: { ext: string }) {
  const cls = () => extColors[props.ext] || 'text-zinc-500 bg-zinc-500/10 border-zinc-500/20';
  return (
    <span class={`shrink-0 text-[9px] font-bold uppercase tracking-wider px-1.5 py-0.5 rounded border font-mono ${cls()}`}>
      {props.ext || 'file'}
    </span>
  );
}

function FileIcon(props: { ext: string }) {
  if (props.ext === 'pdf') {
    return (
      <svg class="w-3.5 h-3.5 text-red-400 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
        <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
      </svg>
    );
  }
  if (props.ext === 'docx') {
    return (
      <svg class="w-3.5 h-3.5 text-blue-400 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
        <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
      </svg>
    );
  }
  return (
    <svg class="w-3.5 h-3.5 text-zinc-500 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
      <path stroke-linecap="round" stroke-linejoin="round" d="M17.25 6.75L22.5 12l-5.25 5.25m-10.5 0L1.5 12l5.25-5.25m7.5-3l-4.5 16.5" />
    </svg>
  );
}

export default function DocIndexPage() {
  const docIndex = useDocIndex();
  const [showBuildModal, setShowBuildModal] = createSignal(false);
  const [isRebuild, setIsRebuild] = createSignal(false);
  const [showExcludesModal, setShowExcludesModal] = createSignal(false);
  const [newPattern, setNewPattern] = createSignal('');
  const [search, setSearch] = createSignal('');
  const [expanded, setExpanded] = createSignal<Set<string>>(new Set());

  onMount(() => docIndex.loadExcludes());

  const openBuildModal = (rebuild: boolean) => {
    setIsRebuild(rebuild);
    setShowBuildModal(true);
  };

  const handleConfirmBuild = () => {
    setShowBuildModal(false);
    docIndex.build(isRebuild());
  };

  const handleAddPattern = async () => {
    const p = newPattern().trim();
    if (!p) return;
    await docIndex.addExclude(p);
    setNewPattern('');
  };

  const filteredDocs = createMemo(() => {
    const q = search().toLowerCase().trim();
    if (!q) return docIndex.docs();
    return docIndex.docs().filter(d => d.docPath.toLowerCase().includes(q));
  });

  const folderGroups = createMemo(() => buildFolderGroups(filteredDocs()));

  const totalPages = () => docIndex.docs().reduce((s, d) => s + d.pageCount, 0);
  const lastIndexed = () => {
    const dates = docIndex.docs().map(d => d.indexedAt).filter(Boolean);
    if (!dates.length) return null;
    return Math.max(...dates);
  };

  const toggleExpand = (path: string) => {
    setExpanded(prev => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  return (
    <div class="flex h-screen w-full">
      <Sidebar />

      <div class="flex-1 flex flex-col overflow-hidden bg-[color:var(--bg-base)]">
        {/* Header */}
        <div class="shrink-0 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] px-4 py-2.5 flex items-center gap-3">
          <div class="flex items-center gap-2 shrink-0">
            <svg class="w-4 h-4 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
            </svg>
            <h1 class="text-[14px] font-semibold text-zinc-100">Project Index</h1>
          </div>

          <Show when={docIndex.building()}>
            <div class="flex items-center gap-1.5 text-[11px] text-zinc-400">
              <div class="w-2 h-2 rounded-full bg-[color:var(--accent)] animate-pulse" />
              <Show when={docIndex.progress() && docIndex.progress()!.total > 0} fallback={'Indexing…'}>
                {(() => {
                  const p = docIndex.progress()!;
                  return <>{p.completed + p.failed} / {p.total} files</>;
                })()}
              </Show>
            </div>
          </Show>

          <div class="flex-1" />

          <button
            onClick={() => setShowExcludesModal(true)}
            class="h-7 px-2.5 rounded-md text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-zinc-400 hover:text-zinc-200 hover:border-[color:var(--border-default)] transition flex items-center gap-1.5"
          >
            <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" />
            </svg>
            Excludes
            <Show when={docIndex.excludes().length > 0}>
              <span class="text-[10px] bg-[color:var(--bg-base)] px-1.5 py-0.5 rounded text-zinc-500">
                {docIndex.excludes().length}
              </span>
            </Show>
          </button>

          <Show when={docIndex.docs().length > 0}>
            <button
              onClick={() => openBuildModal(true)}
              disabled={docIndex.building() || docIndex.loading()}
              class="h-7 px-2.5 rounded-md text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-zinc-400 hover:text-zinc-200 hover:border-[color:var(--border-default)] disabled:opacity-50 transition flex items-center gap-1.5"
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
              </svg>
              Rebuild
            </button>
          </Show>

          <button
            onClick={() => docIndex.refresh()}
            disabled={docIndex.loading() || docIndex.building()}
            class="h-7 px-2.5 rounded-md text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-zinc-400 hover:text-zinc-200 hover:border-[color:var(--border-default)] disabled:opacity-50 transition flex items-center gap-1.5"
          >
            <Show when={docIndex.loading()} fallback={
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
              </svg>
            }>
              <div class="w-3 h-3 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
            </Show>
            Refresh
          </button>

          <button
            onClick={() => openBuildModal(false)}
            disabled={docIndex.building() || docIndex.loading()}
            class="h-7 px-3 rounded-md text-[12px] font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-50 disabled:cursor-not-allowed transition flex items-center gap-1.5"
          >
            <Show when={docIndex.building()} fallback={
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
              </svg>
            }>
              <div class="w-3 h-3 border-2 border-white border-t-transparent rounded-full animate-spin" />
            </Show>
            {docIndex.building() ? 'Indexing…' : 'Index Docs'}
          </button>
        </div>

        {/* Progress bar during indexing */}
        <Show when={docIndex.building() && docIndex.progress() && docIndex.progress()!.total > 0}>
          {(() => {
            const p = docIndex.progress()!;
            const pct = p.percent;
            return (
              <div class="shrink-0 h-1 bg-[color:var(--bg-elevated)] relative">
                <div
                  class="h-full bg-[color:var(--accent)] transition-all duration-500 ease-out"
                  style={{ width: `${pct}%` }}
                />
                <div class="absolute inset-0 flex items-center justify-center">
                  <span class="text-[9px] font-mono text-zinc-500 tabular-nums">
                    {pct}% · {p.completed} done{p.failed > 0 ? ` · ${p.failed} failed` : ''}
                  </span>
                </div>
              </div>
            );
          })()}
        </Show>

        {/* Content */}
        <div class="flex-1 overflow-y-auto">

          {/* Loading */}
          <Show when={docIndex.loading() && docIndex.docs().length === 0}>
            <div class="flex items-center justify-center h-full">
              <div class="flex flex-col items-center gap-3">
                <div class="w-5 h-5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                <p class="text-[12px] text-zinc-500">Loading…</p>
              </div>
            </div>
          </Show>

          {/* Empty state */}
          <Show when={docIndex.docs().length === 0 && !docIndex.loading()}>
            <div class="flex flex-col items-center justify-center h-full text-center px-8">
              <svg class="w-10 h-10 text-zinc-700 mb-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
              </svg>
              <p class="text-[14px] font-semibold text-zinc-300">No documents indexed</p>
              <p class="text-[12px] text-zinc-500 mt-1.5 max-w-[280px] leading-relaxed">
                Index your workspace so agents can search and navigate files intelligently.
              </p>
              <Show when={!docIndex.building()}>
                <button
                  onClick={() => openBuildModal(false)}
                  class="mt-4 h-8 px-4 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] transition"
                >
                  Index Docs
                </button>
              </Show>
              <Show when={docIndex.building()}>
                <div class="mt-4 flex flex-col items-center gap-3">
                  <div class="w-5 h-5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                  <Show when={docIndex.progress() && docIndex.progress()!.total > 0} fallback={
                    <p class="text-[12px] text-zinc-400">Indexing in progress…</p>
                  }>
                    {(() => {
                      const p = docIndex.progress()!;
                      return (
                        <div class="flex flex-col items-center gap-2">
                          <div class="w-48 h-1.5 bg-[color:var(--bg-elevated)] rounded-full overflow-hidden">
                            <div
                              class="h-full bg-[color:var(--accent)] rounded-full transition-all duration-500 ease-out"
                              style={{ width: `${p.percent}%` }}
                            />
                          </div>
                          <p class="text-[12px] text-zinc-400">
                            {p.completed + p.failed} / {p.total} files · {p.percent}%
                          </p>
                        </div>
                      );
                    })()}
                  </Show>
                </div>
              </Show>
            </div>
          </Show>

          {/* Folder tree */}
          <Show when={docIndex.docs().length > 0}>
            <div class="max-w-3xl mx-auto px-6 py-5">

              {/* Stats + search row */}
              <div class="flex items-center gap-3 mb-5">
                <div class="flex items-center gap-2 text-[11px] text-zinc-500">
                  <span class="text-zinc-300 font-medium">{docIndex.docs().length}</span> files
                  <span class="text-zinc-700">·</span>
                  <span class="text-zinc-300 font-medium">{totalPages()}</span> pages
                  <Show when={lastIndexed()}>
                    <span class="text-zinc-700">·</span>
                    <span>last indexed {formatDate(lastIndexed()!)}</span>
                  </Show>
                </div>
                <div class="flex-1" />
                <div class="relative">
                  <svg class="absolute left-2.5 top-1/2 -translate-y-1/2 w-3 h-3 text-zinc-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" />
                  </svg>
                  <input
                    type="text"
                    placeholder="Filter files…"
                    value={search()}
                    onInput={e => setSearch(e.currentTarget.value)}
                    class="h-7 pl-7 pr-3 w-44 rounded-md text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-zinc-200 placeholder-zinc-600 focus:outline-none focus:border-[color:var(--accent)] transition"
                  />
                </div>
              </div>

              {/* No results */}
              <Show when={search() && folderGroups().length === 0}>
                <p class="text-[12px] text-zinc-600 text-center py-12">No files match "{search()}"</p>
              </Show>

              {/* Folder groups */}
              <div class="space-y-1">
                <For each={folderGroups()}>
                  {(group) => {
                    const isCollapsed = () => !expanded().has(group.relPath);
                    return (
                      <div class="rounded-xl border border-[color:var(--border-subtle)] overflow-hidden">
                        {/* Folder header */}
                        <button
                          onClick={() => toggleExpand(group.relPath)}
                          class="w-full flex items-center gap-2.5 px-4 py-2.5 bg-[color:var(--bg-surface)] hover:bg-[color:var(--bg-elevated)] transition-colors text-left"
                        >
                          <svg
                            class={`w-3 h-3 text-zinc-500 transition-transform shrink-0 ${isCollapsed() ? '-rotate-90' : ''}`}
                            fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5"
                          >
                            <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
                          </svg>
                          <svg class="w-3.5 h-3.5 text-yellow-500/80 shrink-0" fill="currentColor" viewBox="0 0 24 24">
                            <path d="M19.5 21a3 3 0 003-3v-4.5a3 3 0 00-3-3h-15a3 3 0 00-3 3V18a3 3 0 003 3h15zM1.5 10.146V6a3 3 0 013-3h5.379a2.25 2.25 0 011.59.659l2.122 2.121c.14.141.331.22.53.22H19.5a3 3 0 013 3v1.146A4.483 4.483 0 0019.5 9h-15a4.483 4.483 0 00-3 1.146z" />
                          </svg>
                          <span class="text-[12px] font-medium text-zinc-300 truncate flex-1">
                            {group.relPath || <span class="text-zinc-500 italic">project root</span>}
                          </span>
                          <span class="text-[10px] text-zinc-600 shrink-0">
                            {group.files.length} {group.files.length === 1 ? 'file' : 'files'}
                          </span>
                        </button>

                        {/* File rows */}
                        <Show when={!isCollapsed()}>
                          <div class="bg-[color:var(--bg-base)]">
                            <For each={group.files}>
                              {(doc, i) => {
                                const ext = docExt(doc.docPath);
                                const name = docBasename(doc.docPath);
                                const isLast = () => i() === group.files.length - 1;
                                return (
                                  <div class={`flex items-center gap-3 px-4 py-2 hover:bg-[color:var(--bg-surface)] transition-colors ${!isLast() ? 'border-b border-[color:var(--border-subtle)]/40' : ''}`}>
                                    <div class="w-4 shrink-0 flex justify-center">
                                      <FileIcon ext={ext} />
                                    </div>
                                    <span class="flex-1 text-[12px] text-zinc-300 truncate font-mono" title={doc.docPath}>
                                      {name}
                                    </span>
                                    <ExtBadge ext={ext} />
                                    <Show when={doc.pageCount > 1}>
                                      <span class="text-[11px] text-zinc-600 font-mono tabular-nums shrink-0">
                                        {doc.pageCount}p
                                      </span>
                                    </Show>
                                    <span class="text-[11px] text-zinc-600 w-14 text-right shrink-0">
                                      {formatDate(doc.indexedAt)}
                                    </span>
                                  </div>
                                );
                              }}
                            </For>
                          </div>
                        </Show>
                      </div>
                    );
                  }}
                </For>
              </div>
            </div>
          </Show>
        </div>
      </div>

      {/* Excludes modal */}
      <Show when={showExcludesModal()}>
        <div
          class="fixed inset-0 z-50 bg-black/60 backdrop-blur-[2px] flex items-center justify-center p-4"
          onClick={(e) => { if (e.target === e.currentTarget) setShowExcludesModal(false); }}
        >
          <div class="w-full max-w-[480px] bg-[color:var(--bg-surface)] border border-[color:var(--border-default)] rounded-2xl shadow-[0_24px_64px_rgba(0,0,0,0.6)] flex flex-col overflow-hidden max-h-[80vh]">
            <div class="px-6 pt-6 pb-4 border-b border-[color:var(--border-subtle)] flex items-start justify-between gap-3">
              <div>
                <h2 class="text-[15px] font-semibold text-zinc-100">Exclude Patterns</h2>
                <p class="text-[12px] text-zinc-500 mt-1 leading-relaxed">
                  Folders and files matching these patterns are skipped during indexing.
                </p>
              </div>
              <button
                onClick={() => setShowExcludesModal(false)}
                class="w-7 h-7 rounded-lg flex items-center justify-center text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-elevated)] transition shrink-0"
              >
                <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>

            <div class="px-6 py-3 border-b border-[color:var(--border-subtle)] flex gap-2">
              <input
                type="text"
                placeholder="e.g. logs, *.test.js, coverage"
                value={newPattern()}
                onInput={(e) => setNewPattern(e.currentTarget.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') handleAddPattern(); }}
                class="flex-1 h-8 px-3 rounded-lg text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-zinc-200 placeholder-zinc-600 focus:outline-none focus:border-[color:var(--accent)] transition"
              />
              <button
                onClick={handleAddPattern}
                disabled={!newPattern().trim()}
                class="h-8 px-3 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition"
              >
                Add
              </button>
            </div>

            <div class="flex-1 overflow-y-auto px-3 py-2">
              <Show when={docIndex.excludes().length === 0}>
                <p class="text-[12px] text-zinc-600 text-center py-6">No exclude patterns yet.</p>
              </Show>
              <For each={docIndex.excludes()}>
                {(entry) => (
                  <div class="flex items-center justify-between gap-2 px-3 py-2 rounded-lg hover:bg-[color:var(--bg-elevated)] group transition">
                    <span class="text-[12px] font-mono text-zinc-300">{entry.pattern}</span>
                    <button
                      onClick={() => docIndex.deleteExclude(entry.id)}
                      class="w-6 h-6 rounded flex items-center justify-center text-zinc-600 hover:text-red-400 hover:bg-red-400/10 opacity-0 group-hover:opacity-100 transition"
                    >
                      <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </div>
                )}
              </For>
            </div>
          </div>
        </div>
      </Show>

      {/* Build modal */}
      <Show when={showBuildModal()}>
        <div
          class="fixed inset-0 z-50 bg-black/60 backdrop-blur-[2px] flex items-center justify-center p-4"
          onClick={(e) => { if (e.target === e.currentTarget) setShowBuildModal(false); }}
        >
          <div class="w-full max-w-[440px] bg-[color:var(--bg-surface)] border border-[color:var(--border-default)] rounded-2xl shadow-[0_24px_64px_rgba(0,0,0,0.6)] flex flex-col overflow-hidden">
            <div class="px-6 pt-6 pb-4 border-b border-[color:var(--border-subtle)]">
              <div class="flex items-start justify-between gap-3">
                <div>
                  <h2 class="text-[15px] font-semibold text-zinc-100">
                    {isRebuild() ? 'Rebuild Index' : 'Index Documents'}
                  </h2>
                  <p class="text-[12px] text-zinc-500 mt-1 leading-relaxed">
                    {isRebuild()
                      ? 'Re-analyze all documents from scratch.'
                      : 'Scan the workspace and extract semantic labels per page.'}
                  </p>
                </div>
                <button
                  onClick={() => setShowBuildModal(false)}
                  class="w-7 h-7 rounded-lg flex items-center justify-center text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-elevated)] transition shrink-0"
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </div>
            </div>

            <div class="px-3 py-2 max-h-[320px] overflow-y-auto">
              <Show when={docIndex.models().filter(m => m.enabled).length === 0}>
                <div class="px-3 py-8 text-center text-[12px] text-zinc-500">
                  No models available. Configure a provider in Settings.
                </div>
              </Show>
              <For each={[...new Set(docIndex.models().filter(m => m.enabled).map(m => m.providerId))]}>
                {(providerId) => {
                  const pModels = () => docIndex.models().filter(m => m.enabled && m.providerId === providerId);
                  const providerLabel: Record<string, string> = {
                    anthropic: 'Anthropic', openai: 'OpenAI', openrouter: 'OpenRouter',
                    google: 'Google', mistral: 'Mistral', ollama: 'Ollama',
                  };
                  const providerColor: Record<string, string> = {
                    anthropic: '#fb923c', openai: '#34d399', openrouter: '#a78bfa',
                    google: '#60a5fa', mistral: '#f43f5e', ollama: '#14b8a6',
                  };
                  return (
                    <div class="mb-1">
                      <div class="flex items-center gap-2 px-2 py-1.5">
                        <span class="w-1.5 h-1.5 rounded-full" style={{ background: providerColor[providerId] || '#71717a' }} />
                        <span class="text-[10px] font-semibold uppercase tracking-wider" style={{ color: providerColor[providerId] || '#71717a' }}>
                          {providerLabel[providerId] || providerId}
                        </span>
                      </div>
                      <For each={pModels()}>
                        {(model) => {
                          const isSel = () => docIndex.selectedModel() === model.id;
                          return (
                            <button
                              onClick={() => docIndex.selectModel(model.id)}
                              class={`w-full text-left px-3 py-2.5 rounded-lg mb-0.5 flex items-center justify-between gap-3 transition-colors
                                ${isSel() ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]' : 'text-zinc-200 hover:bg-[color:var(--bg-elevated)]'}`}
                            >
                              <div class="flex items-center gap-2.5 min-w-0">
                                <div class={`w-4 h-4 rounded-full border-2 flex items-center justify-center shrink-0
                                  ${isSel() ? 'border-[color:var(--accent)] bg-[color:var(--accent)]' : 'border-zinc-600'}`}>
                                  <Show when={isSel()}>
                                    <svg class="w-2.5 h-2.5 text-white" fill="currentColor" viewBox="0 0 24 24">
                                      <path d="M9 16.17L4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41L9 16.17z" />
                                    </svg>
                                  </Show>
                                </div>
                                <span class="text-[13px] font-medium truncate">{model.name}</span>
                                <Show when={model.default}>
                                  <span class="text-[9px] text-zinc-500 uppercase tracking-wider">default</span>
                                </Show>
                              </div>
                              <Show when={model.inputPricePerM > 0}>
                                <span class="text-[10px] text-zinc-500 font-mono">
                                  ${model.inputPricePerM % 1 === 0 ? model.inputPricePerM : model.inputPricePerM.toFixed(2)}
                                  <span class="text-zinc-700">/</span>
                                  ${model.outputPricePerM % 1 === 0 ? model.outputPricePerM : model.outputPricePerM.toFixed(2)}
                                </span>
                              </Show>
                            </button>
                          );
                        }}
                      </For>
                    </div>
                  );
                }}
              </For>
            </div>

            <div class="px-6 py-4 border-t border-[color:var(--border-subtle)] flex items-center justify-end gap-3">
              <button
                onClick={() => setShowBuildModal(false)}
                class="h-8 px-4 rounded-lg text-[12px] text-zinc-400 hover:text-zinc-200 hover:bg-[color:var(--bg-elevated)] transition"
              >
                Cancel
              </button>
              <button
                onClick={handleConfirmBuild}
                disabled={!docIndex.selectedModel()}
                class="h-8 px-4 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-50 disabled:cursor-not-allowed transition flex items-center gap-1.5"
              >
                <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
                </svg>
                {isRebuild() ? 'Rebuild' : 'Index Docs'}
              </button>
            </div>
          </div>
        </div>
      </Show>
    </div>
  );
}
