import { createSignal, Show, For, onMount, onCleanup, createMemo, createEffect, on } from 'solid-js';
import { useServer } from '../context/server';
import { useDocIndex } from '../context/docindex';
import { type IndexFile } from '../api/client';
import SessionSidebar from '../components/session-sidebar';
import PlanSidebar from '../components/plan-sidebar';
import IndexScopeDialog from '../components/index-scope-dialog';
import IndexRunDialog from '../components/index-run-dialog';
import FileViewer from '../components/index-file-viewer';
import Popover from '../components/popover';
import { createGitChanges, ChangesPanel, DiffPane } from '../components/index-changes';
import { DrawerToggle } from '../components/sidebar-shell';
import { revealWithin } from '../lib/reveal';
import {
  buildTree, flattenTree, allDirIds, defaultExpanded,
  basename, relPath, TreeRow, type TreeNode,
} from '../components/file-tree';

function Sidebar() {
  const server = useServer();
  return (
    <Show when={server.mode() === 'plan'} fallback={<SessionSidebar />}>
      <PlanSidebar />
    </Show>
  );
}

const WIDTH_KEY = 'ogcode.docindex.treeWidth';
const MIN_TREE = 240;
const MAX_TREE = 640;

function savedWidth(): number {
  try {
    const n = Number(localStorage.getItem(WIDTH_KEY));
    return n >= MIN_TREE && n <= MAX_TREE ? n : 340;
  } catch {
    return 340;
  }
}

function findNode(nodes: TreeNode[], id: string): TreeNode | null {
  for (const n of nodes) {
    if (n.id === id) return n;
    if (n.kind === 'dir') {
      const hit = findNode(n.children, id);
      if (hit) return hit;
    }
  }
  return null;
}

/** Whether the viewport is phone-width; reactive, unlike reading innerWidth once. */
function createNarrow() {
  const mq = window.matchMedia('(max-width: 767px)');
  const [narrow, setNarrow] = createSignal(mq.matches);
  const onChange = () => setNarrow(mq.matches);
  mq.addEventListener('change', onChange);
  onCleanup(() => mq.removeEventListener('change', onChange));
  return narrow;
}

const Spinner = (props: { class?: string }) => (
  <div class={`border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin ${props.class ?? 'w-3.5 h-3.5'}`} />
);

const Kbd = (props: { children: string }) => <span class="kbd">{props.children}</span>;

export default function DocIndexPage() {
  const server = useServer();
  const docIndex = useDocIndex();
  const git = createGitChanges(() => server.directory() || undefined);
  const narrow = createNarrow();

  const [showRunDialog, setShowRunDialog] = createSignal(false);
  const [isRebuild, setIsRebuild] = createSignal(false);
  const [showScopeDialog, setShowScopeDialog] = createSignal(false);
  const [runMenuOpen, setRunMenuOpen] = createSignal(false);
  const [runMenuAnchor, setRunMenuAnchor] = createSignal<HTMLButtonElement>();

  const [search, setSearch] = createSignal('');
  const [pendingOnly, setPendingOnly] = createSignal(false);
  const [expanded, setExpanded] = createSignal<Set<string>>(new Set());
  const [selected, setSelected] = createSignal<TreeNode | null>(null);
  const [treeWidth, setTreeWidth] = createSignal(savedWidth());
  const [showChanges, setShowChanges] = createSignal(false);
  // On a phone the tree and the viewer take turns filling the screen.
  const [mobilePane, setMobilePane] = createSignal<'tree' | 'file'>('tree');

  // Tracked apart from tree selection so folding a folder doesn't close the
  // viewer, the way clicking a folder in an editor leaves the open tab alone.
  const [openFile, setOpenFile] = createSignal<IndexFile | null>(null);
  const [openAnchor, setOpenAnchor] = createSignal<string | undefined>();

  let filterInput: HTMLInputElement | undefined;
  let primaryBtn: HTMLButtonElement | undefined;

  onMount(() => {
    // The DocIndex store lives above the router and persists across navigation,
    // so its file/doc list is only pulled on directory change, SSE reconnect or
    // a build event — none of which fire on a plain navigation back to this
    // screen. Without this, returning here shows a stale tree (files added or
    // removed on disk since the last visit are missing). Refresh on entry so the
    // list always reflects the project as it is now.
    docIndex.refresh();
    // Pre-fetch git status so the changed-files count on the tree toolbar
    // populates without needing to open the changes panel first.
    git.refreshStatus();

    // "/" jumps to the filter, as in most file browsers — unless the key is
    // meant for a field or a dialog is up.
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== '/' || e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
      if (document.querySelector('[aria-modal="true"]') || !filterInput) return;
      e.preventDefault();
      setShowChanges(false);
      if (narrow()) setMobilePane('tree');
      queueMicrotask(() => filterInput?.focus({ preventScroll: true }));
    };
    window.addEventListener('keydown', onKey);
    onCleanup(() => window.removeEventListener('keydown', onKey));
  });

  // ---- tree state -------------------------------------------------------

  const pendingCount = createMemo(() => docIndex.files().filter((f) => !f.indexed).length);
  const indexedCount = () => docIndex.files().length - pendingCount();

  const filteredFiles = createMemo(() => {
    const q = search().toLowerCase().trim();
    let list = docIndex.files();
    if (pendingOnly()) list = list.filter((f) => !f.indexed);
    if (q) list = list.filter((f) => f.path.toLowerCase().includes(q));
    return list;
  });
  const filtering = () => search().trim().length > 0 || pendingOnly();

  const fullTree = createMemo(() => buildTree(docIndex.files()));
  const viewTree = createMemo(() => buildTree(filteredFiles()));

  // The pending filter has nothing to show once everything is indexed.
  createEffect(() => { if (pendingCount() === 0 && pendingOnly()) setPendingOnly(false); });

  // Seed the expansion once per file set; later refreshes (a build finishing,
  // a return to the screen) keep what the reader opened, and keep the open file
  // — with its indexed flag brought up to date — instead of closing it.
  let seeded = false;
  createEffect(on(() => docIndex.files(), (files) => {
    if (!seeded || files.length === 0) {
      setExpanded(defaultExpanded(buildTree(files).root));
      seeded = files.length > 0;
    }
    const open = openFile();
    if (open) {
      const now = files.find((f) => f.path === open.path);
      if (now && (now.indexed !== open.indexed || now.pageCount !== open.pageCount)) setOpenFile(now);
    }
  }));

  // The pattern count on the Scope button needs the exclude list. On a fresh
  // load straight onto this screen the directory is still resolving at mount,
  // so load it when the directory is known rather than once on mount.
  createEffect(on(() => server.directory(), (dir) => { if (dir) docIndex.loadExcludes(); }));

  // A different project is a different tree: start over.
  createEffect(on(() => server.directory(), () => {
    seeded = false;
    setSearch('');
    setPendingOnly(false);
    setSelected(null);
    setOpenFile(null);
    setShowChanges(false);
    setMobilePane('tree');
    git.refreshStatus();
  }, { defer: true }));

  const rowEls = new Map<string, HTMLElement>();

  const rows = createMemo(() => {
    const open = expanded();
    const all = filtering();
    rowEls.clear();
    // While filtering, every surviving folder opens so matches are always visible.
    return flattenTree(viewTree().root, (id) => all || open.has(id));
  });

  let treeEl: HTMLDivElement | undefined;
  const revealRow = (id: string, block: 'nearest' | 'center' = 'nearest') => {
    const el = rowEls.get(id);
    if (el && treeEl) revealWithin(treeEl, el, { block, margin: 4 });
  };

  const toggle = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const openInViewer = (file: IndexFile, anchor?: string) => {
    setOpenFile(file);
    setOpenAnchor(anchor);
    if (narrow()) setMobilePane('file');
  };

  const select = (node: TreeNode, fromKeyboard = false) => {
    setSelected(node);
    if (node.kind === 'file') {
      const file = docIndex.files().find((f) => f.path === node.id)
        ?? { path: node.id, indexed: node.indexed, pageCount: node.pageCount, indexedAt: 0 };
      // Walking the tree with the arrows on a phone must not throw the reader
      // into the viewer on every step.
      if (fromKeyboard && narrow()) { setOpenFile(file); setOpenAnchor(undefined); }
      else openInViewer(file);
    }
    queueMicrotask(() => revealRow(node.id));
  };

  const expandAll = () => setExpanded(allDirIds(viewTree().root));
  const collapseAll = () => setExpanded(new Set<string>());

  /** Shows a workspace path in the tree — clearing a filter that hides it — and opens it. */
  const openPath = (abs: string, anchor?: string) => {
    const file = docIndex.files().find((f) => f.path === abs);
    const prefix = fullTree().prefix;
    const rel = relPath(abs, prefix);
    const node = findNode(fullTree().root.children, file ? abs : rel.replace(/\/+$/, ''));

    if (node) {
      if (!filteredFiles().some((f) => f.path === abs) && node.kind === 'file') {
        setSearch('');
        setPendingOnly(false);
      }
      const parts = (node.kind === 'file' ? rel.split('/').slice(0, -1) : rel.replace(/\/+$/, '').split('/'));
      setExpanded((prev) => {
        const next = new Set(prev);
        parts.forEach((_, i) => next.add(parts.slice(0, i + 1).join('/')));
        return next;
      });
      setShowChanges(false);
      setSelected(node);
      queueMicrotask(() => revealRow(node.id, 'center'));
      if (node.kind === 'dir') {
        if (narrow()) setMobilePane('tree');
        return;
      }
    }
    openInViewer(file ?? { path: abs, indexed: false, pageCount: 0, indexedAt: 0 }, anchor);
  };

  // ---- keyboard navigation ---------------------------------------------

  const move = (delta: number) => {
    const list = rows();
    if (!list.length) return;
    const idx = list.findIndex((r) => r.node.id === selected()?.id);
    const next = idx < 0
      ? (delta > 0 ? 0 : list.length - 1)
      : Math.min(list.length - 1, Math.max(0, idx + delta));
    select(list[next].node, true);
  };

  const moveToParent = () => {
    const list = rows();
    const idx = list.findIndex((r) => r.node.id === selected()?.id);
    if (idx <= 0) return;
    const depth = list[idx].depth;
    for (let i = idx - 1; i >= 0; i--) {
      if (list[i].depth < depth) { select(list[i].node, true); return; }
    }
  };

  const onTreeKeyDown = (e: KeyboardEvent) => {
    const node = selected();
    switch (e.key) {
      case 'ArrowDown': e.preventDefault(); move(1); break;
      case 'ArrowUp': e.preventDefault(); move(-1); break;
      case 'ArrowRight':
        e.preventDefault();
        if (node?.kind === 'dir' && !expanded().has(node.id)) toggle(node.id);
        else move(1);
        break;
      case 'ArrowLeft':
        e.preventDefault();
        if (node?.kind === 'dir' && expanded().has(node.id)) toggle(node.id);
        else moveToParent();
        break;
      case 'Enter':
      case ' ':
        if (!node) break;
        e.preventDefault();
        if (node.kind === 'dir') toggle(node.id);
        else select(node);
        break;
      case 'Home': e.preventDefault(); if (rows().length) select(rows()[0].node, true); break;
      case 'End': e.preventDefault(); if (rows().length) select(rows()[rows().length - 1].node, true); break;
    }
  };

  // ---- resizable split --------------------------------------------------

  let dragStartX = 0;
  let dragStartW = 340;

  const onDragMove = (e: PointerEvent) => {
    setTreeWidth(Math.min(MAX_TREE, Math.max(MIN_TREE, dragStartW + (e.clientX - dragStartX))));
  };
  const onDragEnd = () => {
    window.removeEventListener('pointermove', onDragMove);
    window.removeEventListener('pointerup', onDragEnd);
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    try { localStorage.setItem(WIDTH_KEY, String(treeWidth())); } catch { /* private mode */ }
  };
  const startDrag = (e: PointerEvent) => {
    e.preventDefault();
    dragStartX = e.clientX;
    dragStartW = treeWidth();
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    window.addEventListener('pointermove', onDragMove);
    window.addEventListener('pointerup', onDragEnd);
  };
  onCleanup(() => {
    window.removeEventListener('pointermove', onDragMove);
    window.removeEventListener('pointerup', onDragEnd);
  });

  // ---- git changes ------------------------------------------------------

  // Re-fetch when the panel opens — the agent may have edited files while it
  // was closed.
  createEffect(on(showChanges, (open) => {
    if (open) {
      git.refreshStatus();
      git.refreshCommits();
    }
  }, { defer: true }));

  const hasDiff = () => showChanges() && (!!git.selectedChange() || !!git.selectedCommit());

  // ---- derived ----------------------------------------------------------

  const folderCount = () => allDirIds(fullTree().root).size;
  const rootLabel = () => basename(server.directory() || '') || 'workspace';
  const neverIndexed = () => docIndex.docs().length === 0;
  const progress = () => docIndex.progress();
  const building = () => docIndex.building();

  const showLeft = () => !narrow() || mobilePane() === 'tree';
  const showRight = () => !narrow() || mobilePane() === 'file';

  // ---- modal actions ----------------------------------------------------

  const openRunDialog = (rebuild: boolean) => {
    setRunMenuOpen(false);
    setIsRebuild(rebuild);
    setShowRunDialog(true);
  };

  const handleConfirmBuild = () => {
    setShowRunDialog(false);
    docIndex.build(isRebuild());
  };

  return (
    <div class="flex h-dvh w-full">
      <Sidebar />

      <div class="flex-1 min-w-0 flex flex-col overflow-hidden bg-[color:var(--bg-base)]">
        {/* ---- Header ---- */}
        <header
          class="relative shrink-0 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] pl-2 pr-2 sm:pl-3 sm:pr-2.5 h-12 flex items-center gap-2 sm:gap-3"
          style={{ 'padding-top': 'env(safe-area-inset-top)' }}
        >
          <DrawerToggle drawer={server.mode() === 'plan' ? 'plans' : 'sessions'} label="Open navigation" />
          <div class="flex items-center gap-2.5 min-w-0">
            <div class="w-7 h-7 rounded-lg bg-[color:var(--accent-soft)] shadow-[inset_0_0_0_1px_var(--accent-ring)] flex items-center justify-center shrink-0">
              <svg class="w-3.5 h-3.5 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
              </svg>
            </div>
            <div class="min-w-0">
              <h1 class="text-ui font-semibold text-[color:var(--text-primary)] leading-tight whitespace-nowrap">Project Index</h1>
              <p class="hidden sm:block text-micro text-[color:var(--text-muted)] font-mono truncate leading-tight" title={server.directory()}>
                {rootLabel()}
              </p>
            </div>
          </div>

          {/* Status: what the index holds right now, or how far a run has got. */}
          <Show when={building()} fallback={
            <Show when={docIndex.files().length > 0}>
              <button
                type="button"
                onClick={() => { if (pendingCount() > 0 && !neverIndexed()) { setShowChanges(false); setPendingOnly(!pendingOnly()); if (narrow()) setMobilePane('tree'); } }}
                class="hidden md:flex items-center gap-2 h-7 pl-2 pr-2.5 rounded-full border text-micro tabular-nums shrink-0 transition-colors"
                classList={{
                  'border-[color:var(--border-subtle)] text-[color:var(--text-tertiary)] cursor-default': pendingCount() === 0 || neverIndexed(),
                  'border-[color:var(--accent-ring)] text-[color:var(--text-secondary)] hover:bg-[color:var(--accent-soft)]': pendingCount() > 0 && !neverIndexed(),
                  'bg-[color:var(--accent-soft)]': pendingOnly(),
                }}
                title={pendingCount() > 0 && !neverIndexed() ? 'Show only the files not indexed yet' : undefined}
              >
                <Show when={neverIndexed()} fallback={
                  <Show when={pendingCount() > 0} fallback={
                    <>
                      <span class="idx-done-dot" />
                      <span>All {docIndex.files().length.toLocaleString()} files indexed</span>
                    </>
                  }>
                    <span class="idx-pending-dot" />
                    <span>
                      <span class="text-[color:var(--text-primary)] font-medium">{pendingCount().toLocaleString()}</span>
                      {' '}of {docIndex.files().length.toLocaleString()} not indexed
                    </span>
                  </Show>
                }>
                  <span class="idx-pending-dot" style={{ 'box-shadow': 'inset 0 0 0 1.5px var(--text-muted)' }} />
                  <span>{docIndex.files().length.toLocaleString()} files · not indexed yet</span>
                </Show>
              </button>
            </Show>
          }>
            <div class="flex items-center gap-2 h-7 pl-2.5 pr-3 rounded-full border border-[color:var(--accent-ring)] bg-[color:var(--accent-soft)] text-micro text-[color:var(--text-secondary)] tabular-nums shrink-0">
              <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] animate-pulse" />
              <Show when={progress() && progress()!.total > 0} fallback={<span class="sweep-text">Preparing index run…</span>}>
                <span>
                  Indexing <span class="text-[color:var(--text-primary)] font-medium">{(progress()!.completed + progress()!.failed).toLocaleString()}</span>
                  <span class="hidden sm:inline"> of {progress()!.total.toLocaleString()}</span>
                  <span class="text-[color:var(--text-muted)]"> · {progress()!.percent}%</span>
                  <Show when={progress()!.failed > 0}>
                    <span class="text-[color:var(--danger)]"> · {progress()!.failed} failed</span>
                  </Show>
                </span>
              </Show>
            </div>
          </Show>

          <div class="flex-1" />

          <div class="flex items-center gap-1 shrink-0">
            {/* .icon-btn sets its own display, so the phone-width hide lives on a wrapper. */}
            <span class="hidden sm:contents">
              <button
                type="button"
                onClick={() => { docIndex.refresh(); git.refreshStatus(); }}
                disabled={docIndex.loading() || building()}
                class="icon-btn"
                title="Reload the file list"
                aria-label="Reload the file list"
              >
                <Show when={docIndex.loading()} fallback={
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99" />
                  </svg>
                }>
                  <Spinner />
                </Show>
              </button>
            </span>

            <button
              type="button"
              onClick={() => setShowScopeDialog(true)}
              class="tool-btn"
              title="What gets indexed — .gitignore rules and extra patterns"
            >
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 3c2.755 0 5.455.232 8.083.678.533.09.917.556.917 1.096v1.044a2.25 2.25 0 01-.659 1.591l-5.432 5.432a2.25 2.25 0 00-.659 1.591v2.927a2.25 2.25 0 01-1.244 2.013L9.75 21v-6.568a2.25 2.25 0 00-.659-1.591L3.659 7.409A2.25 2.25 0 013 5.818V4.774c0-.54.384-1.006.917-1.096A48.32 48.32 0 0112 3z" />
              </svg>
              <span class="hidden md:inline">Scope</span>
              <Show when={docIndex.excludes().length > 0}>
                <span class="tool-count">{docIndex.excludes().length}</span>
              </Show>
            </button>
          </div>

          {/* Split button: incremental run up front, full rebuild behind the chevron. */}
          <div class="split-btn shrink-0">
            <button
              ref={primaryBtn}
              type="button"
              onClick={() => openRunDialog(false)}
              disabled={building() || docIndex.loading() || docIndex.files().length === 0}
              title={neverIndexed() ? 'Scan this workspace and build the index' : 'Index files added or changed since the last run'}
            >
              <Show when={building()} fallback={
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
                </svg>
              }>
                <div class="w-3.5 h-3.5 border-2 border-current border-t-transparent rounded-full animate-spin" />
              </Show>
              <span class="hidden sm:inline">
                {building() ? 'Indexing…' : neverIndexed() ? 'Index workspace' : 'Update index'}
              </span>
            </button>
            <button
              ref={setRunMenuAnchor}
              type="button"
              onClick={() => setRunMenuOpen(!runMenuOpen())}
              disabled={building() || docIndex.loading() || docIndex.files().length === 0}
              aria-haspopup="menu"
              aria-expanded={runMenuOpen()}
              aria-label="More index actions"
              title="More index actions"
            >
              <svg class="w-3 h-3 transition-transform" classList={{ 'rotate-180': runMenuOpen() }} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.6">
                <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
              </svg>
            </button>
          </div>

          <Show when={building() && progress() && progress()!.total > 0}>
            <div class="idx-progress" aria-hidden="true">
              <span style={{ width: `${progress()!.percent}%` }} />
            </div>
          </Show>
        </header>

        <Popover
          open={runMenuOpen()}
          anchor={runMenuAnchor()}
          onClose={() => setRunMenuOpen(false)}
          label="Index actions"
          role="menu"
          class="w-[300px] p-1.5"
        >
          <button type="button" role="menuitem" data-pop-item class="pop-item" onClick={() => openRunDialog(false)}>
            <span class="mt-0.5 w-6 h-6 rounded-md shrink-0 flex items-center justify-center bg-[color:var(--accent-soft)] text-[color:var(--accent)]">
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
              </svg>
            </span>
            <span class="min-w-0">
              <span class="block text-ui text-[color:var(--text-primary)]">{neverIndexed() ? 'Index workspace…' : 'Update index…'}</span>
              <span class="block text-micro text-[color:var(--text-tertiary)] leading-snug mt-0.5">
                {neverIndexed() ? 'Read every file once and record what it is about' : 'Read only files added or changed since the last run'}
              </span>
            </span>
          </button>
          <Show when={!neverIndexed()}>
            <button type="button" role="menuitem" data-pop-item class="pop-item" onClick={() => openRunDialog(true)}>
              <span class="mt-0.5 w-6 h-6 rounded-md shrink-0 flex items-center justify-center bg-[color:var(--warning)]/[0.12] text-[color:var(--warning)]">
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99" />
                </svg>
              </span>
              <span class="min-w-0">
                <span class="block text-ui text-[color:var(--text-primary)]">Rebuild from scratch…</span>
                <span class="block text-micro text-[color:var(--text-tertiary)] leading-snug mt-0.5">
                  Discard the index and re-read all {docIndex.files().length.toLocaleString()} files
                </span>
              </span>
            </button>
          </Show>
          <div class="pop-sep" />
          <button type="button" role="menuitem" data-pop-item class="pop-item" style={{ 'align-items': 'center' }} onClick={() => { setRunMenuOpen(false); setShowScopeDialog(true); }}>
            <span class="w-6 h-6 shrink-0 flex items-center justify-center text-[color:var(--text-tertiary)]">
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 3c2.755 0 5.455.232 8.083.678.533.09.917.556.917 1.096v1.044a2.25 2.25 0 01-.659 1.591l-5.432 5.432a2.25 2.25 0 00-.659 1.591v2.927a2.25 2.25 0 01-1.244 2.013L9.75 21v-6.568a2.25 2.25 0 00-.659-1.591L3.659 7.409A2.25 2.25 0 013 5.818V4.774c0-.54.384-1.006.917-1.096A48.32 48.32 0 0112 3z" />
              </svg>
            </span>
            <span class="text-ui">Review scope…</span>
          </button>
        </Popover>

        {/* ---- Body ---- */}
        <div class="flex-1 flex overflow-hidden">

          {/* Loading: a tree-shaped skeleton, so the layout does not jump when it lands. */}
          <Show when={docIndex.loading() && docIndex.files().length === 0}>
            <div
              class="shrink-0 flex flex-col border-r border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)]/40"
              style={{ width: narrow() ? '100%' : `${treeWidth()}px` }}
              aria-busy="true"
              aria-label="Scanning workspace"
            >
              <div class="h-10 px-2.5 flex items-center border-b border-[color:var(--border-subtle)]">
                <div class="skel h-7 w-full rounded-md" />
              </div>
              <div class="flex flex-col gap-[10px] px-3 py-3">
                <For each={[62, 48, 70, 40, 55, 66, 36, 58, 44, 72, 50, 38]}>
                  {(w, i) => <div class="skel h-3" style={{ width: `${w}%`, 'margin-left': `${(i() % 3) * 14}px` }} />}
                </For>
              </div>
            </div>
            <div class="hidden md:flex flex-1 items-center justify-center gap-2 text-meta text-[color:var(--text-tertiary)]">
              <Spinner /> Scanning workspace…
            </div>
          </Show>

          {/* Empty — no indexable files at all */}
          <Show when={docIndex.files().length === 0 && !docIndex.loading()}>
            <div class="flex-1 flex flex-col items-center justify-center text-center px-8">
              <div class="w-12 h-12 rounded-2xl bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-4 shadow-[var(--shadow-sm)]">
                <svg class="w-5 h-5 text-[color:var(--text-tertiary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
                </svg>
              </div>
              <p class="text-sm font-semibold text-[color:var(--text-primary)]">No indexable files found</p>
              <p class="text-meta text-[color:var(--text-tertiary)] mt-1.5 max-w-[340px] leading-relaxed">
                An index run reads each file once and records what it is about, so agents can find
                the right one later instead of grepping for it. Nothing here passes the current scope.
              </p>
              <button
                type="button"
                onClick={() => setShowScopeDialog(true)}
                class="mt-5 h-8 px-3.5 rounded-lg text-meta bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)] text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:border-[color:var(--border-strong)] transition flex items-center gap-1.5"
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 3c2.755 0 5.455.232 8.083.678.533.09.917.556.917 1.096v1.044a2.25 2.25 0 01-.659 1.591l-5.432 5.432a2.25 2.25 0 00-.659 1.591v2.927a2.25 2.25 0 01-1.244 2.013L9.75 21v-6.568a2.25 2.25 0 00-.659-1.591L3.659 7.409A2.25 2.25 0 013 5.818V4.774c0-.54.384-1.006.917-1.096A48.32 48.32 0 0112 3z" />
                </svg>
                Review scope
              </button>
              <p class="text-micro text-[color:var(--text-muted)] mt-3 max-w-[330px] leading-relaxed">
                Whatever your .gitignore skips, the index skips too.
              </p>
            </div>
          </Show>

          <Show when={docIndex.files().length > 0}>
            {/* ---- Left pane: tree, or git changes ---- */}
            <Show when={showLeft()}>
              <div
                class="shrink-0 flex flex-col border-r border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)]/40 min-w-0"
                style={{ width: narrow() ? '100%' : `${treeWidth()}px` }}
              >
                <Show when={!showChanges()} fallback={
                  <ChangesPanel
                    git={git}
                    onBack={() => { setShowChanges(false); git.setSelectedChange(null); git.setSelectedCommit(null); }}
                    onPick={() => { if (narrow()) setMobilePane('file'); }}
                  />
                }>
                  {/* Tree toolbar */}
                  <div class="shrink-0 h-10 px-2 border-b border-[color:var(--border-subtle)] flex items-center gap-1">
                    <div class="relative flex-1 min-w-0">
                      <svg class="absolute left-2 top-1/2 -translate-y-1/2 w-3 h-3 text-[color:var(--text-muted)] pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" />
                      </svg>
                      <input
                        ref={filterInput}
                        type="text"
                        placeholder="Filter files"
                        value={search()}
                        onInput={(e) => setSearch(e.currentTarget.value)}
                        onKeyDown={(e) => {
                          if (e.key === 'Escape') {
                            if (search()) { e.stopPropagation(); setSearch(''); }
                            else e.currentTarget.blur();
                          } else if (e.key === 'ArrowDown' || e.key === 'Enter') {
                            // Hand off to the tree so the arrows keep walking the results.
                            e.preventDefault();
                            const first = rows().find((r) => r.node.kind === 'file') ?? rows()[0];
                            if (first) select(first.node, e.key === 'ArrowDown');
                            treeEl?.focus({ preventScroll: true });
                          }
                        }}
                        aria-label="Filter files"
                        spellcheck={false}
                        autocomplete="off"
                        class="peer h-7 w-full pl-7 pr-7 rounded-md text-meta bg-[color:var(--bg-base)] border border-[color:var(--border-subtle)] text-[color:var(--text-primary)] placeholder-[color:var(--text-muted)] focus:outline-none focus:border-[color:var(--border-strong)] transition"
                      />
                      <Show when={search()} fallback={
                        <span class="hidden md:block absolute right-1.5 top-1/2 -translate-y-1/2 pointer-events-none peer-focus:opacity-0 transition-opacity">
                          <Kbd>/</Kbd>
                        </span>
                      }>
                        <button
                          type="button"
                          onClick={() => { setSearch(''); filterInput?.focus(); }}
                          class="absolute right-1 top-1/2 -translate-y-1/2 w-5 h-5 rounded flex items-center justify-center text-[color:var(--text-muted)] hover:text-[color:var(--text-primary)]"
                          aria-label="Clear filter"
                        >
                          <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                          </svg>
                        </button>
                      </Show>
                    </div>

                    <Show when={pendingCount() > 0 && !neverIndexed()}>
                      <button
                        type="button"
                        onClick={() => setPendingOnly(!pendingOnly())}
                        class="tool-btn" style={{ 'padding-inline': '0.375rem' }}
                        aria-pressed={pendingOnly()}
                        title={pendingOnly() ? 'Show all files' : 'Show only files not indexed yet'}
                      >
                        <span class="idx-pending-dot" />
                        <span class="tool-count is-accent">{pendingCount()}</span>
                      </button>
                    </Show>

                    <button type="button" onClick={expandAll} class="icon-btn" title="Expand all" aria-label="Expand all">
                      <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M8.25 15L12 18.75 15.75 15m-7.5-6L12 5.25 15.75 9" />
                      </svg>
                    </button>
                    <button type="button" onClick={collapseAll} class="icon-btn" title="Collapse all" aria-label="Collapse all">
                      <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 19.5L12 15.75 8.25 19.5m7.5-15L12 8.25 8.25 4.5" />
                      </svg>
                    </button>
                    <span class="w-px h-4 mx-0.5 bg-[color:var(--border-subtle)]" />
                    <button
                      type="button"
                      onClick={() => setShowChanges(true)}
                      class="tool-btn" style={{ 'padding-inline': '0.375rem' }}
                      title="Git changes and commits"
                      aria-label={`Git changes${git.files().length ? ` (${git.files().length})` : ''}`}
                    >
                      <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                        <circle cx="6" cy="6" r="2.25" />
                        <circle cx="6" cy="18" r="2.25" />
                        <circle cx="18" cy="8" r="2.25" />
                        <path stroke-linecap="round" stroke-linejoin="round" d="M6 8.25v7.5M18 10.25c0 4-5 3.5-10.5 6.25" />
                      </svg>
                      <Show when={git.files().length > 0}>
                        <span class="tool-count">{git.files().length > 99 ? '99+' : git.files().length}</span>
                      </Show>
                    </button>
                  </div>

                  {/* Rows */}
                  <div
                    ref={treeEl}
                    role="tree"
                    aria-label="Indexed files"
                    class="flex-1 overflow-y-auto overflow-x-hidden py-1 focus:outline-none"
                    tabindex="0"
                    onKeyDown={onTreeKeyDown}
                  >
                    <Show when={rows().length > 0} fallback={
                      <div class="flex flex-col items-center gap-2 text-center py-10 px-4">
                        <p class="text-meta text-[color:var(--text-muted)]">
                          <Show when={search().trim()} fallback="Every file is indexed.">
                            No {pendingOnly() ? 'unindexed ' : ''}files match “{search()}”
                          </Show>
                        </p>
                        <button
                          type="button"
                          onClick={() => { setSearch(''); setPendingOnly(false); }}
                          class="text-micro text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] underline underline-offset-2 decoration-[color:var(--border-strong)]"
                        >
                          Clear filters
                        </button>
                      </div>
                    }>
                      <For each={rows()}>
                        {(row) => (
                          <TreeRow
                            node={row.node}
                            depth={row.depth}
                            expanded={filtering() || expanded().has(row.node.id)}
                            selected={selected()?.id === row.node.id}
                            query={search()}
                            onToggle={toggle}
                            onSelect={(n) => select(n)}
                            attach={(el) => rowEls.set(row.node.id, el)}
                          />
                        )}
                      </For>
                    </Show>
                  </div>

                  {/* Tree footer */}
                  <div class="shrink-0 px-3 h-7 flex items-center gap-2 border-t border-[color:var(--border-subtle)] text-[0.625rem] text-[color:var(--text-muted)] tabular-nums">
                    <Show when={filtering()} fallback={
                      <>
                        <span>{docIndex.files().length.toLocaleString()} files · {folderCount().toLocaleString()} folders</span>
                        <div class="flex-1" />
                        <Show when={!neverIndexed()}>
                          <span>{indexedCount().toLocaleString()} indexed</span>
                        </Show>
                      </>
                    }>
                      <span>
                        {filteredFiles().length.toLocaleString()} of {docIndex.files().length.toLocaleString()} files
                        {pendingOnly() ? ' · not indexed' : ''}
                      </span>
                      <div class="flex-1" />
                      <button
                        type="button"
                        onClick={() => { setSearch(''); setPendingOnly(false); }}
                        class="hover:text-[color:var(--text-secondary)]"
                      >
                        Clear
                      </button>
                    </Show>
                  </div>
                </Show>
              </div>
            </Show>

            {/* Resize handle — desktop affordance; phones stack the panes instead. */}
            <Show when={!narrow()}>
              <div
                onPointerDown={startDrag}
                onDblClick={() => { setTreeWidth(340); try { localStorage.removeItem(WIDTH_KEY); } catch { /* ignore */ } }}
                class="shrink-0 w-1 -ml-[3px] mr-[-1px] cursor-col-resize hover:bg-[color:var(--accent)]/40 active:bg-[color:var(--accent)]/60 transition-colors z-10"
                title="Drag to resize · double-click to reset"
              />
            </Show>

            {/* ---- Right pane: viewer, or diff ---- */}
            <Show when={showRight()}>
              <div class="flex-1 min-w-0 flex flex-col overflow-hidden">
                <Show when={hasDiff()} fallback={
                  <Show when={openFile()} fallback={
                    <div class="h-full flex flex-col items-center justify-center text-center px-8">
                      <div class="w-11 h-11 rounded-xl bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-3.5">
                        <svg class="w-5 h-5 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                          <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
                        </svg>
                      </div>
                      <p class="text-ui text-[color:var(--text-secondary)]">Select a file to view it</p>
                      <p class="text-micro text-[color:var(--text-muted)] mt-1">Markdown opens rendered; the rest opens as source.</p>
                      <div class="hidden md:flex items-center gap-4 mt-5 text-micro text-[color:var(--text-muted)]">
                        <span class="flex items-center gap-1"><Kbd>↑</Kbd><Kbd>↓</Kbd> move</span>
                        <span class="flex items-center gap-1"><Kbd>←</Kbd><Kbd>→</Kbd> fold</span>
                        <span class="flex items-center gap-1"><Kbd>/</Kbd> filter</span>
                      </div>
                    </div>
                  }>
                    {(file) => (
                      <FileViewer
                        file={file()}
                        prefix={fullTree().prefix}
                        directory={server.directory() || ''}
                        anchor={openAnchor()}
                        onOpenFile={openPath}
                        onRunIndex={() => openRunDialog(false)}
                        onBack={narrow() ? () => setMobilePane('tree') : undefined}
                      />
                    )}
                  </Show>
                }>
                  <DiffPane git={git} onBack={narrow() ? () => setMobilePane('tree') : undefined} />
                </Show>
              </div>
            </Show>
          </Show>
        </div>
      </div>

      {/* ---- Index scope dialog ---- */}
      <Show when={showScopeDialog()}>
        <IndexScopeDialog
          returnFocus={() => primaryBtn}
          onClose={() => setShowScopeDialog(false)}
          onRebuild={() => { setShowScopeDialog(false); openRunDialog(true); }}
        />
      </Show>

      {/* ---- Index run dialog ---- */}
      <Show when={showRunDialog()}>
        <IndexRunDialog
          returnFocus={() => primaryBtn}
          rebuild={isRebuild()}
          onClose={() => setShowRunDialog(false)}
          onConfirm={handleConfirmBuild}
          onOpenScope={() => { setShowRunDialog(false); setShowScopeDialog(true); }}
        />
      </Show>
    </div>
  );
}
