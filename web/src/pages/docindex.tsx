import { createSignal, Show, For, onMount, onCleanup, createMemo, createEffect, createResource, on } from 'solid-js';
import { useServer } from '../context/server';
import { useDocIndex } from '../context/docindex';
import { getDocContent, getGitStatus, getGitFileDiff, getGitCommits, getGitCommitDiff, stageGitFiles, unstageGitFiles, commitGitChanges, type IndexFile, type GitFileStatus, type GitCommit } from '../api/client';
import SessionSidebar from '../components/session-sidebar';
import PlanSidebar from '../components/plan-sidebar';
import CodeViewer, { formatBytes } from '../components/code-viewer';
import GitDiff from '../components/git-diff';
import IndexScopeDialog from '../components/index-scope-dialog';
import IndexRunDialog from '../components/index-run-dialog';
import { DrawerToggle } from '../components/sidebar-shell';
import {
  buildTree, flattenTree, allDirIds, defaultExpanded,
  basename, fileExt, relPath, langColor, tint, TreeRow, type TreeNode,
} from '../components/file-tree';

function Sidebar() {
  const server = useServer();
  return (
    <Show when={server.mode() === 'plan'} fallback={<SessionSidebar />}>
      <PlanSidebar />
    </Show>
  );
}

export default function DocIndexPage() {
  const server = useServer();
  const docIndex = useDocIndex();

  const [showRunDialog, setShowRunDialog] = createSignal(false);
  const [isRebuild, setIsRebuild] = createSignal(false);
  const [showScopeDialog, setShowScopeDialog] = createSignal(false);
  const [search, setSearch] = createSignal('');
  const [expanded, setExpanded] = createSignal<Set<string>>(new Set());
  const [selected, setSelected] = createSignal<TreeNode | null>(null);
  const [treeWidth, setTreeWidth] = createSignal(340);
  const [copied, setCopied] = createSignal(false);

  onMount(() => {
    // The DocIndex store lives above the router and persists across navigation,
    // so its file/doc list is only pulled on directory change, SSE reconnect or
    // a build event — none of which fire on a plain navigation back to this
    // screen. Without this, returning here shows a stale tree (files added or
    // removed on disk since the last visit are missing). Refresh on entry so the
    // list always reflects the project as it is now.
    docIndex.refresh();
    docIndex.loadExcludes();
    // Pre-fetch git status so the changed-files badge on the toolbar
    // populates without needing to open the changes panel first.
    refreshGitStatus();
  });

  // ---- tree state -------------------------------------------------------

  const filteredFiles = createMemo(() => {
    const q = search().toLowerCase().trim();
    if (!q) return docIndex.files();
    return docIndex.files().filter((f) => f.path.toLowerCase().includes(q));
  });

  const fullTree = createMemo(() => buildTree(docIndex.files()));
  const viewTree = createMemo(() => buildTree(filteredFiles()));

  // Re-seed the expansion state whenever the underlying file set changes.
  createEffect(on(() => docIndex.files(), (files) => {
    setExpanded(defaultExpanded(buildTree(files).root));
    setSelected(null);
    setOpenFile(null);
  }));

  const rowEls = new Map<string, HTMLElement>();

  const rows = createMemo(() => {
    const filtering = search().trim().length > 0;
    const open = expanded();
    rowEls.clear();
    // While filtering, every surviving folder opens so matches are always visible.
    return flattenTree(viewTree().root, (id) => filtering || open.has(id));
  });

  const toggle = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const select = (node: TreeNode) => {
    setSelected(node);
    if (node.kind === 'file') setOpenFile({ path: node.id, indexed: node.indexed, pageCount: node.pageCount, indexedAt: 0 });
    queueMicrotask(() => rowEls.get(node.id)?.scrollIntoView({ block: 'nearest' }));
  };

  const expandAll = () => setExpanded(allDirIds(viewTree().root));
  const collapseAll = () => setExpanded(new Set<string>());

  // ---- keyboard navigation ---------------------------------------------

  const move = (delta: number) => {
    const list = rows();
    if (!list.length) return;
    const idx = list.findIndex((r) => r.node.id === selected()?.id);
    const next = idx < 0
      ? (delta > 0 ? 0 : list.length - 1)
      : Math.min(list.length - 1, Math.max(0, idx + delta));
    select(list[next].node);
  };

  const moveToParent = () => {
    const list = rows();
    const idx = list.findIndex((r) => r.node.id === selected()?.id);
    if (idx <= 0) return;
    const depth = list[idx].depth;
    for (let i = idx - 1; i >= 0; i--) {
      if (list[i].depth < depth) { select(list[i].node); return; }
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
      case 'Home': e.preventDefault(); if (rows().length) select(rows()[0].node); break;
      case 'End': e.preventDefault(); if (rows().length) select(rows()[rows().length - 1].node); break;
    }
  };

  // ---- resizable split --------------------------------------------------

  let dragStartX = 0;
  let dragStartW = 340;

  const onDragMove = (e: PointerEvent) => {
    setTreeWidth(Math.min(640, Math.max(240, dragStartW + (e.clientX - dragStartX))));
  };
  const onDragEnd = () => {
    window.removeEventListener('pointermove', onDragMove);
    window.removeEventListener('pointerup', onDragEnd);
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
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
  onCleanup(onDragEnd);

  // The saved/derived pane width is right for a desktop window. On a phone it
  // would leave the viewer ~50px, so below md the pane is capped at 45vw —
  // enough for file names, still half the screen for content. The resize
  // handle is hidden there too (touch users drag nothing by 4px).
  const isNarrow = () => window.innerWidth < 768;
  const effectiveTreeWidth = () => (isNarrow() ? Math.min(treeWidth(), Math.round(window.innerWidth * 0.45)) : treeWidth());

  // ---- git changes ------------------------------------------------------

  const [showChanges, setShowChanges] = createSignal(false);
  const [gitStatus, setGitStatus] = createSignal<GitFileStatus[]>([]);
  const [gitIsRepo, setGitIsRepo] = createSignal(false);
  const [gitLoading, setGitLoading] = createSignal(false);
  // Tracks whether the first git-status fetch has completed, so the
  // "Not a git repository" fallback doesn't flash before the API responds.
  const [gitChecked, setGitChecked] = createSignal(false);
  const [selectedChange, setSelectedChange] = createSignal<{ path: string; staged: boolean } | null>(null);

  // "wt" = working tree, "log" = commit history.
  const [changesMode, setChangesMode] = createSignal<'wt' | 'log'>('wt');
  const [gitCommits, setGitCommits] = createSignal<GitCommit[]>([]);
  const [commitsLoading, setCommitsLoading] = createSignal(false);
  const [selectedCommit, setSelectedCommit] = createSignal<GitCommit | null>(null);
  const [commitMessage, setCommitMessage] = createSignal('');
  const [commitBusy, setCommitBusy] = createSignal(false);

  const [changeDiff] = createResource(
    () => selectedChange(),
    (sel) => getGitFileDiff(sel.path, sel.staged, server.directory() || undefined),
  );

  const [commitDiff] = createResource(
    () => selectedCommit(),
    (c) => getGitCommitDiff(c.sha, server.directory() || undefined),
  );

  const refreshGitStatus = async () => {
    setGitLoading(true);
    try {
      const res = await getGitStatus(server.directory() || undefined);
      setGitIsRepo(res.isRepo);
      setGitStatus(res.files || []);
    } catch {
      setGitIsRepo(false);
      setGitStatus([]);
    } finally {
      setGitLoading(false);
      setGitChecked(true);
    }
  };

  const refreshGitCommits = async () => {
    setCommitsLoading(true);
    try {
      const res = await getGitCommits(server.directory() || undefined, 20);
      setGitCommits(res || []);
    } catch {
      setGitCommits([]);
    } finally {
      setCommitsLoading(false);
    }
  };

  // Stage a single file.
  const stageFile = async (path: string) => {
    try {
      await stageGitFiles([path], server.directory() || undefined);
      await refreshGitStatus();
    } catch { /* surface in UI later */ }
  };

  // Unstage a single file.
  const unstageFile = async (path: string) => {
    try {
      await unstageGitFiles([path], server.directory() || undefined);
      await refreshGitStatus();
    } catch { /* surface in UI later */ }
  };

  // Commit all staged files with the entered message.
  const handleCommit = async () => {
    const msg = commitMessage().trim();
    if (!msg) return;
    setCommitBusy(true);
    try {
      await commitGitChanges(msg, server.directory() || undefined);
      setCommitMessage('');
      await refreshGitStatus();
      await refreshGitCommits();
    } catch { /* surface in UI later */ } finally {
      setCommitBusy(false);
    }
  };

  // Re-fetch when the panel is first opened — the agent may have edited files
  // while it was closed.
  createEffect(on(showChanges, (open) => {
    if (open) {
      refreshGitStatus();
      refreshGitCommits();
    }
  }));

  // ---- open file --------------------------------------------------------

  // Tracked apart from tree selection so folding a folder doesn't close the
  // editor, the way clicking a folder in VS Code leaves the open tab alone.
  const [openFile, setOpenFile] = createSignal<IndexFile | null>(null);

  const [content] = createResource(
    () => openFile()?.path,
    (path) => getDocContent(path, server.directory() || undefined),
  );

  const lineCount = () => {
    if (content.error || !content()) return 0;
    return content()!.content.split('\n').length;
  };

  const copyPath = async (path: string) => {
    try {
      await navigator.clipboard.writeText(path);
      setCopied(true);
      setTimeout(() => setCopied(false), 1400);
    } catch {
      // clipboard unavailable — nothing to do
    }
  };

  // ---- derived stats ----------------------------------------------------

  const folderCount = () => allDirIds(fullTree().root).size;
  const rootLabel = () => basename(server.directory() || '') || 'workspace';
  const matchCount = () => filteredFiles().length;

  // ---- modal actions ----------------------------------------------------

  const openRunDialog = (rebuild: boolean) => {
    setIsRebuild(rebuild);
    setShowRunDialog(true);
  };

  const handleConfirmBuild = () => {
    setShowRunDialog(false);
    docIndex.build(isRebuild());
  };

  const iconBtn =
    'h-7 w-7 rounded-md flex items-center justify-center text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] disabled:opacity-40 disabled:cursor-not-allowed transition';

  // The header actions sit in one recessed group, the way an editor's toolbar
  // does: they are all things you do to the index, and reading them as a set is
  // easier than picking four differently-styled buttons out of a row. The one
  // button that starts work stays outside it, filled, because it is the only
  // one that costs anything to press.
  const toolIcon =
    'h-7 w-7 rounded-[7px] flex items-center justify-center text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition';
  const toolBtn =
    'h-7 pl-2 pr-2.5 rounded-[7px] text-[12px] flex items-center gap-1.5 text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition';
  const toolCount =
    'min-w-[15px] h-[15px] px-1 rounded text-[9.5px] font-medium tabular-nums flex items-center justify-center bg-[color:var(--accent-soft)] text-[color:var(--accent)]';

  return (
    <div class="flex h-dvh w-full">
      <Sidebar />

      <div class="flex-1 flex flex-col overflow-hidden bg-[color:var(--bg-base)]">
        {/* ---- Header ---- */}
        <header class="shrink-0 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] pl-2 pr-2 sm:pl-3 sm:pr-2.5 h-12 flex items-center gap-2 sm:gap-3"
                style={{ [ 'padding-top']: 'env(safe-area-inset-top)' }}>
          <DrawerToggle drawer={server.mode() === 'plan' ? 'plans' : 'sessions'} label="Open navigation" />
          <div class="flex items-center gap-2.5 min-w-0">
            <div class="w-6 h-6 rounded-md bg-[color:var(--accent-soft)] flex items-center justify-center shrink-0">
              <svg class="w-3.5 h-3.5 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
              </svg>
            </div>
            <div class="min-w-0">
              <h1 class="text-[13px] font-semibold text-[color:var(--text-primary)] leading-tight">Project Index</h1>
              <p class="text-[10px] text-[color:var(--text-muted)] font-mono truncate leading-tight" title={server.directory()}>
                {rootLabel()}
              </p>
            </div>
          </div>

          {/* Indexed-at-a-glance, so the header says what the index holds and
              not only what can be done to it. */}
          <Show when={docIndex.files().length > 0 && !docIndex.building()}>
            <div class="hidden sm:flex items-center gap-1.5 pl-3 border-l border-[color:var(--border-subtle)] text-[11px] text-[color:var(--text-tertiary)] tabular-nums shrink-0">
              <span>{docIndex.docs().length} indexed</span>
              <span class="text-[color:var(--text-muted)]">/</span>
              <span>{docIndex.files().length} files</span>
              <span class="text-[color:var(--text-muted)]">·</span>
              <span>{folderCount()} folders</span>
            </div>
          </Show>

          <Show when={docIndex.building()}>
            <div class="flex items-center gap-1.5 text-[11px] text-[color:var(--text-secondary)] px-2 py-1 rounded-md bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] shrink-0">
              <div class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] animate-pulse" />
              <Show when={docIndex.progress() && docIndex.progress()!.total > 0} fallback={'Indexing…'}>
                {(() => {
                  const p = docIndex.progress()!;
                  return <span class="tabular-nums">{p.completed + p.failed} / {p.total} files</span>;
                })()}
              </Show>
            </div>
          </Show>

          <div class="flex-1" />

          {/* Toolbar group */}
          <div class="flex items-center gap-0.5 p-0.5 rounded-[9px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] shrink-0">
            <button
              onClick={() => docIndex.refresh()}
              disabled={docIndex.loading() || docIndex.building()}
              class={toolIcon}
              title="Reload the indexed file list"
              aria-label="Reload the indexed file list"
            >
              <Show when={docIndex.loading()} fallback={
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                </svg>
              }>
                <div class="w-3.5 h-3.5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
              </Show>
            </button>

            <span class="w-px h-4 bg-[color:var(--border-subtle)]" />

            <button
              onClick={() => setShowScopeDialog(true)}
              class={toolBtn}
              title="What gets indexed — .gitignore rules and extra patterns"
            >
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 3c2.755 0 5.455.232 8.083.678.533.09.917.556.917 1.096v1.044a2.25 2.25 0 01-.659 1.591l-5.432 5.432a2.25 2.25 0 00-.659 1.591v2.927a2.25 2.25 0 01-1.244 2.013L9.75 21v-6.568a2.25 2.25 0 00-.659-1.591L3.659 7.409A2.25 2.25 0 013 5.818V4.774c0-.54.384-1.006.917-1.096A48.32 48.32 0 0112 3z" />
              </svg>
              <span class="hidden md:inline">Scope</span>
              <Show when={docIndex.excludes().length > 0}>
                <span class={toolCount}>{docIndex.excludes().length}</span>
              </Show>
            </button>

            <Show when={docIndex.docs().length > 0}>
              <span class="w-px h-4 bg-[color:var(--border-subtle)]" />
              <button
                onClick={() => openRunDialog(true)}
                disabled={docIndex.building() || docIndex.loading()}
                class={toolBtn}
                title="Discard the index and re-analyze every file from scratch"
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 12c0-1.232-.046-2.453-.138-3.662a4.006 4.006 0 00-3.7-3.7 48.678 48.678 0 00-7.324 0 4.006 4.006 0 00-3.7 3.7c-.017.22-.032.441-.046.662M19.5 12l3-3m-3 3l-3-3m-12 3c0 1.232.046 2.453.138 3.662a4.006 4.006 0 003.7 3.7 48.656 48.656 0 007.324 0 4.006 4.006 0 003.7-3.7c.017-.22.032-.441.046-.662M4.5 12l3 3m-3-3l-3 3" />
                </svg>
                <span class="hidden md:inline">Rebuild</span>
              </button>
            </Show>
          </div>

          <button
            onClick={() => openRunDialog(false)}
            disabled={docIndex.building() || docIndex.loading()}
            class="h-8 pl-2.5 pr-3 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-50 disabled:cursor-not-allowed transition flex items-center gap-1.5 shadow-[var(--shadow-sm)] shrink-0"
            title={docIndex.docs().length > 0 ? 'Index files added or changed since the last run' : 'Scan this workspace and build the index'}
          >
            <Show when={docIndex.building()} fallback={
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
              </svg>
            }>
              <div class="w-3.5 h-3.5 border-2 border-white border-t-transparent rounded-full animate-spin" />
            </Show>
            {docIndex.building() ? 'Indexing…' : docIndex.docs().length > 0 ? 'Update Index' : docIndex.files().length > 0 ? 'Index Docs' : 'Index Docs'}
          </button>
        </header>

        {/* ---- Build progress ---- */}
        <Show when={docIndex.building() && docIndex.progress() && docIndex.progress()!.total > 0}>
          {(() => {
            const p = docIndex.progress()!;
            return (
              <div class="shrink-0 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] px-4 py-2 flex items-center gap-3">
                <div class="flex-1 h-1 rounded-full bg-[color:var(--bg-elevated)] overflow-hidden">
                  <div
                    class="h-full bg-[color:var(--accent)] rounded-full transition-all duration-500 ease-out"
                    style={{ width: `${p.percent}%` }}
                  />
                </div>
                <span class="text-[10px] font-mono tabular-nums text-[color:var(--text-tertiary)] shrink-0">
                  {p.percent}% · {p.completed} done{p.failed > 0 ? ` · ${p.failed} failed` : ''}
                </span>
              </div>
            );
          })()}
        </Show>

        {/* ---- Body ---- */}
        <div class="flex-1 flex overflow-hidden">

          {/* Loading */}
          <Show when={docIndex.loading() && docIndex.files().length === 0}>
            <div class="flex-1 flex items-center justify-center">
              <div class="flex flex-col items-center gap-3">
                <div class="w-5 h-5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                <p class="text-[12px] text-[color:var(--text-tertiary)]">Scanning workspace…</p>
              </div>
            </div>
          </Show>

          {/* Empty — no indexable files at all */}
          <Show when={docIndex.files().length === 0 && !docIndex.loading()}>
            <div class="flex-1 flex flex-col items-center justify-center text-center px-8">
              <div class="w-14 h-14 rounded-2xl bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-4">
                <svg class="w-6 h-6 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.4">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
                </svg>
              </div>
              <p class="text-[14px] font-semibold text-[color:var(--text-primary)]">No indexable files found</p>
              <p class="text-[12px] text-[color:var(--text-tertiary)] mt-1.5 max-w-[330px] leading-relaxed">
                A run reads each file once and records what it is about, so agents can find
                the right one later instead of grepping for it.
              </p>
              <Show when={!docIndex.building()}>
                <div class="mt-5 flex items-center gap-2">
                  <button
                    onClick={() => openRunDialog(false)}
                    class="h-8 pl-2.5 pr-3.5 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] transition flex items-center gap-1.5 shadow-[var(--shadow-sm)]"
                  >
                    <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
                    </svg>
                    Index this workspace
                  </button>
                  <button
                    onClick={() => setShowScopeDialog(true)}
                    class="h-8 px-3.5 rounded-lg text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:border-[color:var(--border-default)] transition"
                  >
                    Review scope
                  </button>
                </div>
                <p class="text-[11px] text-[color:var(--text-muted)] mt-3 max-w-[330px] leading-relaxed">
                  Whatever your .gitignore skips, the index skips too.
                </p>
              </Show>
              <Show when={docIndex.building()}>
                <div class="mt-5 flex flex-col items-center gap-3">
                  <Show when={docIndex.progress() && docIndex.progress()!.total > 0} fallback={
                    <>
                      <div class="w-5 h-5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                      <p class="text-[12px] text-[color:var(--text-secondary)]">Indexing in progress…</p>
                    </>
                  }>
                    {(() => {
                      const p = docIndex.progress()!;
                      return (
                        <div class="flex flex-col items-center gap-2">
                          <div class="w-52 h-1.5 bg-[color:var(--bg-elevated)] rounded-full overflow-hidden">
                            <div class="h-full bg-[color:var(--accent)] rounded-full transition-all duration-500 ease-out" style={{ width: `${p.percent}%` }} />
                          </div>
                          <p class="text-[12px] text-[color:var(--text-secondary)] tabular-nums">
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

          {/* Changes panel (replaces the tree pane when active) */}
          <Show when={showChanges()}>
            <div
              class="shrink-0 flex flex-col border-r border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)]/40 min-w-0"
              style={{ width: `${effectiveTreeWidth()}px` }}
            >
              {/* Header: back + mode toggle + refresh */}
              <div class="shrink-0 px-2.5 py-2 border-b border-[color:var(--border-subtle)] flex items-center gap-1.5">
                <button
                  onClick={() => setShowChanges(false)}
                  class={iconBtn}
                  title="Back to file tree"
                  aria-label="Back to file tree"
                >
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M9 15L3 12m0 0l6-3m-6 3h18m0 0l-6-3m6 3l-6 3" />
                  </svg>
                </button>
                <div class="flex items-center rounded-md bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] p-0.5 text-[11px]">
                  <button
                    onClick={() => setChangesMode('wt')}
                    class="px-2 py-0.5 rounded font-medium transition"
                    classList={{ 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]': changesMode() === 'wt', 'text-[color:var(--text-secondary)]': changesMode() !== 'wt' }}
                  >
                    Working tree
                  </button>
                  <button
                    onClick={() => setChangesMode('log')}
                    class="px-2 py-0.5 rounded font-medium transition"
                    classList={{ 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]': changesMode() === 'log', 'text-[color:var(--text-secondary)]': changesMode() !== 'log' }}
                  >
                    Commits
                  </button>
                </div>
                <div class="flex-1" />
                <button
                  onClick={() => changesMode() === 'wt' ? refreshGitStatus() : refreshGitCommits()}
                  disabled={changesMode() === 'wt' ? gitLoading() : commitsLoading()}
                  class={iconBtn}
                  title="Refresh"
                >
                  <Show when={(changesMode() === 'wt' ? gitLoading() : commitsLoading())} fallback={
                    <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                    </svg>
                  }>
                    <div class="w-3.5 h-3.5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                  </Show>
                </button>
              </div>

              {/* Body */}
              <div class="flex-1 overflow-y-auto overflow-x-hidden">
                <Show when={gitChecked()} fallback={
                  <div class="flex items-center justify-center py-10">
                    <div class="w-4 h-4 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                  </div>
                }>
                <Show when={gitIsRepo()} fallback={
                  <p class="text-[12px] text-[color:var(--text-muted)] text-center py-10 px-4">
                    Not a git repository.
                  </p>
                }>
                  <Show when={changesMode() === 'wt'} fallback={
                    /* ---- Commits list ---- */
                    <Show when={gitCommits().length > 0} fallback={
                      <p class="text-[12px] text-[color:var(--text-muted)] text-center py-10 px-4">
                        No commits.
                      </p>
                    }>
                      <For each={gitCommits()}>
                        {(c) => {
                          const isSel = () => selectedCommit()?.sha === c.sha;
                          return (
                            <button
                              onClick={() => { setSelectedCommit(c); setSelectedChange(null); }}
                              class="w-full text-left px-3 py-1.5 flex items-start gap-2 transition border-b border-[color:var(--border-subtle)]/50"
                              classList={{
                                'bg-[color:var(--accent-soft)]': isSel(),
                                'hover:bg-[color:var(--bg-elevated)]': !isSel(),
                              }}
                            >
                              <span class="shrink-0 mt-px text-[9px] font-mono text-[color:var(--text-muted)]">
                                {c.short}
                              </span>
                              <div class="min-w-0">
                                <div class="text-[12px] font-medium text-[color:var(--text-primary)] truncate">
                                  {c.message}
                                </div>
                                <div class="text-[10px] text-[color:var(--text-muted)] truncate">
                                  {c.author} · {c.time}
                                </div>
                              </div>
                            </button>
                          );
                        }}
                      </For>
                    </Show>
                  }>
                    {/* ---- Working-tree file list ---- */}
                    <Show when={gitStatus().length > 0} fallback={
                      <p class="text-[12px] text-[color:var(--text-muted)] text-center py-10 px-4">
                        No working-tree changes.
                      </p>
                    }>
                      <For each={gitStatus()}>
                        {(f) => {
                          const label = () => {
                            const x = f.x, y = f.y;
                            if (x === '?' || y === '?') return '??';
                            if (x === 'A') return 'A';
                            if (x === 'D' || y === 'D') return 'D';
                            if (x === 'R') return 'R';
                            return 'M';
                          };
                          const isDeleted = () => f.x === 'D' || f.y === 'D';
                          const isSel = () => {
                            const s = selectedChange();
                            return s !== null && s.path === f.path && s.staged === f.staged;
                          };
                          return (
                            <div
                              class="px-3 py-1.5 flex items-start gap-2 transition border-b border-[color:var(--border-subtle)]/50"
                              classList={{
                                'bg-[color:var(--accent-soft)]': isSel(),
                                'hover:bg-[color:var(--bg-elevated)]': !isSel(),
                              }}
                            >
                              <button
                                onClick={() => { setSelectedChange({ path: f.path, staged: f.staged }); setSelectedCommit(null); }}
                                class="flex-1 min-w-0 text-left flex items-start gap-2"
                              >
                                <span
                                  class="shrink-0 mt-px text-[9px] font-mono font-bold w-5 text-center rounded px-0.5 py-px"
                                  classList={{
                                    'bg-[color:var(--success-soft,var(--accent-soft))] text-[color:var(--success)]': f.staged,
                                  }}
                                  style={
                                    f.staged
                                      ? undefined
                                      : { color: 'var(--text-secondary)', border: '1px solid var(--border-default)' }
                                  }
                                >
                                  {label()}
                                </span>
                                <div class="min-w-0">
                                  <div class="text-[12px] font-medium text-[color:var(--text-primary)] truncate" classList={{ 'line-through': isDeleted() }}>
                                    {basename(f.path)}
                                  </div>
                                  <div class="text-[10px] font-mono text-[color:var(--text-muted)] truncate">
                                    {relPath(f.path, '')}
                                  </div>
                                </div>
                              </button>
                              {/* Per-file stage / unstage button */}
                              <button
                                onClick={() => f.staged ? unstageFile(f.path) : stageFile(f.path)}
                                class="shrink-0 mt-px w-5 h-5 rounded flex items-center justify-center text-[color:var(--text-muted)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition"
                                title={f.staged ? 'Unstage' : 'Stage'}
                              >
                                <Show when={f.staged} fallback={
                                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
                                  </svg>
                                }>
                                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 12H4.5m15 0l-5.625-5.625M19.5 12l-5.625 5.625" />
                                  </svg>
                                </Show>
                              </button>
                            </div>
                          );
                        }}
                      </For>
                    </Show>
                  </Show>
                </Show>
                </Show>
              </div>

              {/* Footer */}
              <Show when={changesMode() === 'wt' && gitIsRepo()}>
                <div class="shrink-0 px-2.5 py-2 border-t border-[color:var(--border-subtle)] flex flex-col gap-1.5">
                  <textarea
                    placeholder="Commit message…"
                    value={commitMessage()}
                    onInput={(e) => setCommitMessage(e.currentTarget.value)}
                    rows={2}
                    class="w-full text-[12px] rounded-md bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-primary)] placeholder-[color:var(--text-muted)] focus:outline-none focus:border-[color:var(--accent)] transition resize-none px-2 py-1"
                  />
                  <button
                    onClick={handleCommit}
                    disabled={commitBusy() || !commitMessage().trim()}
                    class="h-7 rounded-md text-[12px] font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-50 disabled:cursor-not-allowed transition"
                  >
                    {commitBusy() ? 'Committing…' : 'Commit staged'}
                  </button>
                </div>
              </Show>
              <div class="shrink-0 px-3 h-7 flex items-center border-t border-[color:var(--border-subtle)] text-[10px] text-[color:var(--text-muted)] tabular-nums">
                <Show when={changesMode() === 'wt'} fallback={<span>{gitCommits().length} commits</span>}>
                  <span>{gitStatus().length} changed</span>
                </Show>
              </div>
            </div>
          </Show>

          {/* Tree + detail */}
          <Show when={docIndex.files().length > 0}>
            <Show when={!showChanges()}>
            <div
              class="shrink-0 flex flex-col border-r border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)]/40 min-w-0"
              style={{ width: `${effectiveTreeWidth()}px` }}
            >
              {/* Tree toolbar */}
              <div class="shrink-0 px-2.5 py-2 border-b border-[color:var(--border-subtle)] flex items-center gap-1.5">
                <div class="relative flex-1 min-w-0">
                  <svg class="absolute left-2 top-1/2 -translate-y-1/2 w-3 h-3 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" />
                  </svg>
                  <input
                    type="text"
                    placeholder="Filter files…"
                    value={search()}
                    onInput={(e) => setSearch(e.currentTarget.value)}
                    onKeyDown={(e) => { if (e.key === 'Escape') setSearch(''); }}
                    class="h-7 w-full pl-7 pr-6 rounded-md text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-primary)] placeholder-[color:var(--text-muted)] focus:outline-none focus:border-[color:var(--accent)] transition"
                  />
                  <Show when={search()}>
                    <button
                      onClick={() => setSearch('')}
                      class="absolute right-1.5 top-1/2 -translate-y-1/2 w-4 h-4 rounded flex items-center justify-center text-[color:var(--text-muted)] hover:text-[color:var(--text-primary)]"
                    >
                      <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </Show>
                </div>
                <button onClick={expandAll} class={iconBtn} title="Expand all">
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M4 8V4h4M16 4h4v4M20 16v4h-4M8 20H4v-4" />
                  </svg>
                </button>
                <button onClick={collapseAll} class={iconBtn} title="Collapse all">
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M9 3v6H3M15 3v6h6M15 21v-6h6M9 21v-6H3" />
                  </svg>
                </button>
                <span class="w-px h-4 bg-[color:var(--border-subtle)]" />
                <button
                  onClick={() => setShowChanges(true)}
                  class={iconBtn + ' relative'}
                  title="View git changes"
                  aria-label="View git changes"
                >
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6 3v12a6 6 0 006 6 6 6 0 006-6V9a3 3 0 00-3-3 3 3 0 00-3 3v6a3 3 0 01-3 3" />
                  </svg>
                  <Show when={gitStatus().length > 0}>
                    <span class="absolute -top-1 -right-1 min-w-[14px] h-[14px] px-0.5 rounded-full text-[8px] font-medium tabular-nums flex items-center justify-center bg-[color:var(--accent)] text-[color:var(--on-primary)]">
                      {gitStatus().length > 99 ? '99+' : gitStatus().length}
                    </span>
                  </Show>
                </button>
              </div>

              {/* Rows */}
              <div
                class="flex-1 overflow-y-auto overflow-x-hidden py-1 focus:outline-none"
                tabindex="0"
                onKeyDown={onTreeKeyDown}
              >
                <Show when={rows().length > 0} fallback={
                  <p class="text-[12px] text-[color:var(--text-muted)] text-center py-10 px-4">
                    No files match “{search()}”
                  </p>
                }>
                  <For each={rows()}>
                    {(row) => (
                      <TreeRow
                        node={row.node}
                        depth={row.depth}
                        expanded={search().trim().length > 0 || expanded().has(row.node.id)}
                        selected={selected()?.id === row.node.id}
                        query={search()}
                        onToggle={toggle}
                        onSelect={select}
                        attach={(el) => rowEls.set(row.node.id, el)}
                      />
                    )}
                  </For>
                </Show>
              </div>

              {/* Tree footer */}
              <div class="shrink-0 px-3 h-7 flex items-center border-t border-[color:var(--border-subtle)] text-[10px] text-[color:var(--text-muted)] tabular-nums">
                <Show when={search().trim()} fallback={<span>{docIndex.docs().length} indexed · {docIndex.files().length} files · {folderCount()} folders</span>}>
                  <span>{matchCount()} of {docIndex.files().length} files match</span>
                </Show>
              </div>
            </div>
            </Show>

            {/* Resize handle — desktop affordance; touch screens get the
                capped pane width instead. */}
            <div
              onPointerDown={startDrag}
              class="hidden md:block shrink-0 w-1 -ml-px cursor-col-resize hover:bg-[color:var(--accent)]/40 active:bg-[color:var(--accent)]/60 transition-colors z-10"
            />

            {/* File viewer / Diff viewer */}
            <div class="flex-1 min-w-0 flex flex-col overflow-hidden">
              <Show when={showChanges() && (selectedChange() || selectedCommit())} fallback={
              <Show when={openFile()} fallback={
                <div class="h-full flex flex-col items-center justify-center text-center px-8">
                  <svg class="w-8 h-8 text-[color:var(--text-muted)]/50 mb-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.4">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
                  </svg>
                  <p class="text-[12px] text-[color:var(--text-tertiary)]">Select a file to view its contents</p>
                  <p class="text-[11px] text-[color:var(--text-muted)] mt-1.5">
                    Use ↑ ↓ to walk the tree, ← → to fold and unfold
                  </p>
                </div>
              }>
                {(file) => (
                  <>
                    {/* File bar */}
                    <div class="shrink-0 h-9 px-3 flex items-center gap-2 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)]">
                      <span
                        class="shrink-0 w-4 h-4 rounded flex items-center justify-center text-[8px] font-bold font-mono uppercase"
                        style={{
                          color: langColor(fileExt(basename(file().path))),
                          background: tint(langColor(fileExt(basename(file().path))), 0.14),
                        }}
                      >
                        {fileExt(basename(file().path)).slice(0, 2) || '?'}
                      </span>
                      <span class="text-[12px] font-medium text-[color:var(--text-primary)] shrink-0">
                        {basename(file().path)}
                      </span>
                      <Show when={file().indexed} fallback={
                        <span class="shrink-0 text-[9px] font-medium px-1.5 py-px rounded text-[color:var(--text-muted)] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)]">
                          not indexed
                        </span>
                      }>
                        <span class="shrink-0 text-[9px] font-medium px-1.5 py-px rounded text-[color:var(--success)] bg-[color:var(--success-soft,var(--accent-soft))]">
                          indexed
                        </span>
                      </Show>
                      <button
                        onClick={() => copyPath(file().path)}
                        class="group min-w-0 flex items-center gap-1.5 text-[11px] font-mono text-[color:var(--text-muted)] hover:text-[color:var(--text-secondary)] transition"
                        title="Copy path"
                      >
                        <span class="truncate">{relPath(file().path, fullTree().prefix)}</span>
                        <Show when={copied()} fallback={
                          <svg class="w-3 h-3 shrink-0 opacity-0 group-hover:opacity-100 transition" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M8 5H6a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2v-1M8 5a2 2 0 002 2h2a2 2 0 002-2M8 5a2 2 0 012-2h2a2 2 0 012 2m0 0h2a2 2 0 012 2v3" />
                          </svg>
                        }>
                          <span class="text-[10px] text-[color:var(--success)] shrink-0">copied</span>
                        </Show>
                      </button>
                      <div class="flex-1" />
                      <Show when={content() && !content()!.binary}>
                        <span class="shrink-0 text-[10px] font-mono tabular-nums text-[color:var(--text-muted)]">
                          {lineCount()} lines · {formatBytes(content()!.size)}
                        </span>
                      </Show>
                    </div>

                    {/* Contents */}
                    <div class="flex-1 min-h-0">
                      <Show when={!content.loading} fallback={
                        <div class="h-full flex items-center justify-center gap-2 text-[12px] text-[color:var(--text-tertiary)]">
                          <div class="w-3.5 h-3.5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                          Opening…
                        </div>
                      }>
                        <Show when={!content.error} fallback={
                          <p class="p-4 text-[12px] text-[color:var(--danger)]">
                            Couldn't open this file.
                          </p>
                        }>
                          <Show when={!content()?.binary} fallback={
                            <div class="h-full flex flex-col items-center justify-center text-center px-8">
                              <p class="text-[12px] text-[color:var(--text-tertiary)]">Binary file</p>
                              <p class="text-[11px] text-[color:var(--text-muted)] mt-1">
                                {formatBytes(content()?.size || 0)} · not shown
                              </p>
                            </div>
                          }>
                            <CodeViewer
                              content={content()?.content || ''}
                              ext={fileExt(basename(file().path))}
                            />
                          </Show>
                        </Show>
                      </Show>
                    </div>

                    <Show when={content()?.truncated}>
                      <div class="shrink-0 px-3 py-1.5 border-t border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] text-[10px] text-[color:var(--warning)]">
                        Large file — showing the first 2 MB.
                      </div>
                    </Show>
                  </>
                )}
              </Show>
              }>
                <div class="flex flex-col h-full">
                  <div class="shrink-0 h-9 px-3 flex items-center gap-2 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)]">
                    <Show when={selectedChange()} fallback={
                      <span class="text-[12px] font-medium text-[color:var(--text-primary)] truncate">
                        {selectedCommit()?.short} · {selectedCommit()?.message}
                      </span>
                    }>
                      <span class="text-[12px] font-medium text-[color:var(--text-primary)] truncate">
                        {basename(selectedChange()!.path)}
                      </span>
                      <Show when={selectedChange()!.staged}>
                        <span class="shrink-0 text-[9px] font-medium px-1.5 py-px rounded text-[color:var(--success)] bg-[color:var(--success-soft,var(--accent-soft))]">
                          staged
                        </span>
                      </Show>
                    </Show>
                    <div class="flex-1" />
                    <Show when={selectedChange() ? changeDiff.loading : commitDiff.loading}>
                      <div class="w-3.5 h-3.5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                    </Show>
                  </div>
                  <div class="flex-1 min-h-0 overflow-auto p-3">
                    <Show
                      when={selectedChange() ? !changeDiff.loading : !commitDiff.loading}
                      fallback={
                        <div class="h-full flex items-center justify-center gap-2 text-[12px] text-[color:var(--text-tertiary)]">
                          <div class="w-3.5 h-3.5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                          Loading diff…
                        </div>
                      }
                    >
                      <Show
                        when={selectedChange() ? !changeDiff.error : !commitDiff.error}
                        fallback={
                          <p class="text-[12px] text-[color:var(--danger)]">Couldn't load the diff.</p>
                        }
                      >
                        <Show when={selectedChange()} fallback={
                          <GitDiff diff={commitDiff()?.diff || ''} filename={selectedCommit()?.message || ''} />
                        }>
                          <GitDiff diff={changeDiff()?.diff || ''} filename={selectedChange()!.path} />
                        </Show>
                      </Show>
                    </Show>
                  </div>
                </div>
              </Show>
            </div>
          </Show>
        </div>
      </div>

      {/* ---- Index scope dialog ---- */}
      <Show when={showScopeDialog()}>
        <IndexScopeDialog
          onClose={() => setShowScopeDialog(false)}
          onRebuild={() => { setShowScopeDialog(false); openRunDialog(true); }}
        />
      </Show>

      {/* ---- Index run dialog ---- */}
      <Show when={showRunDialog()}>
        <IndexRunDialog
          rebuild={isRebuild()}
          onClose={() => setShowRunDialog(false)}
          onConfirm={handleConfirmBuild}
          onOpenScope={() => { setShowRunDialog(false); setShowScopeDialog(true); }}
        />
      </Show>
    </div>
  );
}
