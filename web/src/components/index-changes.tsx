import { createSignal, createResource, createMemo, Show, For, type JSX } from 'solid-js';
import {
  getGitStatus, getGitFileDiff, getGitCommits, getGitCommitDiff, stageGitFiles, unstageGitFiles, commitGitChanges,
  type GitFileStatus, type GitCommit,
} from '../api/client';
import GitDiff from './git-diff';
import { basename } from './file-tree';

/**
 * The Project Index's git side panel: the working tree the agent has been
 * editing, the recent commits, and a commit box. State lives in one store the
 * page owns, because it spans both panes — the list on the left and the diff in
 * the viewer on the right.
 */
export function createGitChanges(directory: () => string | undefined) {
  const [files, setFiles] = createSignal<GitFileStatus[]>([]);
  const [isRepo, setIsRepo] = createSignal(false);
  const [loading, setLoading] = createSignal(false);
  // Whether the first status fetch has come back, so "Not a git repository"
  // does not flash up before the API has answered.
  const [checked, setChecked] = createSignal(false);
  const [mode, setMode] = createSignal<'wt' | 'log'>('wt');
  const [commits, setCommits] = createSignal<GitCommit[]>([]);
  const [commitsLoading, setCommitsLoading] = createSignal(false);
  const [selectedChange, setSelectedChange] = createSignal<{ path: string; staged: boolean } | null>(null);
  const [selectedCommit, setSelectedCommit] = createSignal<GitCommit | null>(null);
  const [message, setMessage] = createSignal('');
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal('');

  const [changeDiff] = createResource(selectedChange, (sel) => getGitFileDiff(sel.path, sel.staged, directory()));
  const [commitDiff] = createResource(selectedCommit, (c) => getGitCommitDiff(c.sha, directory()));

  const refreshStatus = async () => {
    setLoading(true);
    try {
      const res = await getGitStatus(directory());
      setIsRepo(res.isRepo);
      setFiles(res.files || []);
    } catch {
      setIsRepo(false);
      setFiles([]);
    } finally {
      setLoading(false);
      setChecked(true);
    }
  };

  const refreshCommits = async () => {
    setCommitsLoading(true);
    try {
      setCommits((await getGitCommits(directory(), 20)) || []);
    } catch {
      setCommits([]);
    } finally {
      setCommitsLoading(false);
    }
  };

  const run = async (op: () => Promise<unknown>, failure: string) => {
    setError('');
    try {
      await op();
      await refreshStatus();
    } catch {
      setError(failure);
    }
  };

  const stage = (path: string) => run(() => stageGitFiles([path], directory()), `Couldn't stage ${basename(path)}.`);
  const unstage = (path: string) => run(() => unstageGitFiles([path], directory()), `Couldn't unstage ${basename(path)}.`);

  const commit = async () => {
    const msg = message().trim();
    if (!msg || busy()) return;
    setBusy(true);
    setError('');
    try {
      await commitGitChanges(msg, directory());
      setMessage('');
      await Promise.all([refreshStatus(), refreshCommits()]);
    } catch {
      setError('Commit failed — check the message and what is staged.');
    } finally {
      setBusy(false);
    }
  };

  const staged = createMemo(() => files().filter((f) => f.staged));
  const unstaged = createMemo(() => files().filter((f) => !f.staged));

  return {
    files, staged, unstaged, isRepo, loading, checked, mode, setMode,
    commits, commitsLoading, selectedChange, setSelectedChange, selectedCommit, setSelectedCommit,
    changeDiff, commitDiff, message, setMessage, busy, error,
    refreshStatus, refreshCommits, stage, unstage, commit,
  };
}

export type GitChanges = ReturnType<typeof createGitChanges>;

// Status letters in the colours editors use for them, so a scan down the list
// reads added / modified / deleted without reading a word.
function statusOf(f: GitFileStatus): { letter: string; title: string; color: string } {
  if (f.x === '?' || f.y === '?') return { letter: 'U', title: 'Untracked', color: 'var(--success)' };
  if (f.x === 'A') return { letter: 'A', title: 'Added', color: 'var(--success)' };
  if (f.x === 'D' || f.y === 'D') return { letter: 'D', title: 'Deleted', color: 'var(--danger)' };
  if (f.x === 'R') return { letter: 'R', title: 'Renamed', color: '#58a6ff' };
  return { letter: 'M', title: 'Modified', color: 'var(--warning)' };
}

// Git reports an untracked folder as "dir/"; its name keeps the slash and its
// parent is what comes before it.
function nameOf(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  return basename(trimmed) + (trimmed !== path ? '/' : '');
}

function dirOf(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const i = trimmed.lastIndexOf('/');
  return i > 0 ? trimmed.slice(0, i) : '';
}

const Spinner = (props: { class?: string }) => (
  <div class={`border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin ${props.class ?? 'w-3.5 h-3.5'}`} />
);

const Empty = (props: { children: JSX.Element }) => (
  <p class="text-meta text-[color:var(--text-muted)] text-center py-10 px-4 leading-relaxed">{props.children}</p>
);

/** Left pane: working-tree changes or recent commits, plus the commit box. */
export function ChangesPanel(props: { git: GitChanges; onBack: () => void; onPick?: () => void }) {
  const git = props.git;
  const canCommit = () => git.staged().length > 0 && !!git.message().trim() && !git.busy();

  const Section = (p: { title: string; count: number; children: JSX.Element }) => (
    <div>
      <div class="sticky top-0 z-[1] h-7 px-3 flex items-center gap-1.5 bg-[color:var(--bg-surface)] text-[0.625rem] font-semibold uppercase tracking-[0.06em] text-[color:var(--text-muted)]">
        {p.title}
        <span class="tabular-nums font-medium">{p.count}</span>
      </div>
      {p.children}
    </div>
  );

  const Row = (p: { f: GitFileStatus }) => {
    const st = statusOf(p.f);
    const isSel = () => {
      const s = git.selectedChange();
      return !!s && s.path === p.f.path && s.staged === p.f.staged;
    };
    return (
      <div
        class="group relative h-7 pl-3 pr-1.5 flex items-center gap-2"
        classList={{ 'tree-row-selected': isSel(), 'hover:bg-[color:var(--bg-elevated)]': !isSel() }}
      >
        <button
          type="button"
          onClick={() => { git.setSelectedChange({ path: p.f.path, staged: p.f.staged }); git.setSelectedCommit(null); props.onPick?.(); }}
          class="flex-1 min-w-0 h-full flex items-center gap-2 text-left"
          title={p.f.path}
        >
          <span
            class="shrink-0 w-3.5 text-center text-[0.625rem] font-mono font-bold"
            style={{ color: st.color }}
            title={st.title}
          >
            {st.letter}
          </span>
          <span
            class="shrink-0 max-w-[60%] truncate text-ui text-[color:var(--text-primary)]"
            classList={{ 'line-through text-[color:var(--text-tertiary)]': st.letter === 'D' }}
          >
            {nameOf(p.f.path)}
          </span>
          <span class="min-w-0 truncate text-micro font-mono text-[color:var(--text-muted)]">{dirOf(p.f.path)}</span>
        </button>
        <button
          type="button"
          onClick={() => (p.f.staged ? git.unstage(p.f.path) : git.stage(p.f.path))}
          class="hover-reveal shrink-0 w-6 h-6 rounded-md flex items-center justify-center text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)] opacity-0 group-hover:opacity-100 focus:opacity-100"
          title={p.f.staged ? 'Unstage' : 'Stage'}
          aria-label={`${p.f.staged ? 'Unstage' : 'Stage'} ${p.f.path}`}
        >
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d={p.f.staged ? 'M19.5 12h-15' : 'M12 4.5v15m7.5-7.5h-15'} />
          </svg>
        </button>
      </div>
    );
  };

  return (
    <>
      <div class="shrink-0 h-10 px-2 border-b border-[color:var(--border-subtle)] flex items-center gap-1.5">
        <button type="button" onClick={props.onBack} class="tool-btn" style={{ 'padding-left': '0.25rem' }} title="Back to the file tree">
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
          </svg>
          Files
        </button>
        <div class="seg" role="tablist" aria-label="Git view">
          <button type="button" role="tab" class="seg-btn" aria-selected={git.mode() === 'wt'} onClick={() => git.setMode('wt')}>
            Changes
            <Show when={git.files().length > 0}>
              <span class="seg-count">{git.files().length}</span>
            </Show>
          </button>
          <button type="button" role="tab" class="seg-btn" aria-selected={git.mode() === 'log'} onClick={() => git.setMode('log')}>
            Commits
          </button>
        </div>
        <div class="flex-1" />
        <button
          type="button"
          onClick={() => (git.mode() === 'wt' ? git.refreshStatus() : git.refreshCommits())}
          disabled={git.mode() === 'wt' ? git.loading() : git.commitsLoading()}
          class="icon-btn"
          title="Refresh"
          aria-label="Refresh"
        >
          <Show when={git.mode() === 'wt' ? git.loading() : git.commitsLoading()} fallback={
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99" />
            </svg>
          }>
            <Spinner />
          </Show>
        </button>
      </div>

      <div class="flex-1 overflow-y-auto overflow-x-hidden">
        <Show when={git.checked()} fallback={<div class="flex justify-center py-10"><Spinner class="w-4 h-4" /></div>}>
          <Show when={git.isRepo()} fallback={<Empty>This workspace is not a git repository.</Empty>}>
            <Show when={git.mode() === 'wt'} fallback={
              <Show when={git.commits().length > 0} fallback={<Empty>{git.commitsLoading() ? 'Loading commits…' : 'No commits yet.'}</Empty>}>
                <div class="py-1">
                  <For each={git.commits()}>
                    {(c) => {
                      const isSel = () => git.selectedCommit()?.sha === c.sha;
                      return (
                        <button
                          type="button"
                          onClick={() => { git.setSelectedCommit(c); git.setSelectedChange(null); props.onPick?.(); }}
                          class="relative w-full text-left pl-3 pr-3 py-1.5 flex items-start gap-2.5"
                          classList={{ 'tree-row-selected': isSel(), 'hover:bg-[color:var(--bg-elevated)]': !isSel() }}
                        >
                          <span class="shrink-0 mt-[3px] text-[0.625rem] font-mono text-[color:var(--text-tertiary)] px-1 rounded bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)]">
                            {c.short}
                          </span>
                          <span class="min-w-0">
                            <span class="block text-ui text-[color:var(--text-primary)] truncate">{c.message}</span>
                            <span class="block text-micro text-[color:var(--text-muted)] truncate">{c.author} · {c.time}</span>
                          </span>
                        </button>
                      );
                    }}
                  </For>
                </div>
              </Show>
            }>
              <Show when={git.files().length > 0} fallback={<Empty>Working tree clean — nothing to commit.</Empty>}>
                <Show when={git.staged().length > 0}>
                  <Section title="Staged" count={git.staged().length}>
                    <For each={git.staged()}>{(f) => <Row f={f} />}</For>
                  </Section>
                </Show>
                <Show when={git.unstaged().length > 0}>
                  <Section title="Changes" count={git.unstaged().length}>
                    <For each={git.unstaged()}>{(f) => <Row f={f} />}</For>
                  </Section>
                </Show>
              </Show>
            </Show>
          </Show>
        </Show>
      </div>

      <Show when={git.mode() === 'wt' && git.isRepo()}>
        <div class="shrink-0 p-2 border-t border-[color:var(--border-subtle)] flex flex-col gap-1.5">
          <textarea
            placeholder={git.staged().length ? 'Commit message' : 'Stage files to commit'}
            value={git.message()}
            onInput={(e) => git.setMessage(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                if (canCommit()) git.commit();
              }
            }}
            rows={2}
            class="w-full text-meta rounded-md bg-[color:var(--bg-base)] border border-[color:var(--border-subtle)] text-[color:var(--text-primary)] placeholder-[color:var(--text-muted)] focus:outline-none focus:border-[color:var(--border-strong)] transition resize-none px-2 py-1.5"
          />
          <Show when={git.error()}>
            <p class="text-micro text-[color:var(--danger)] px-0.5">{git.error()}</p>
          </Show>
          <button
            type="button"
            onClick={() => git.commit()}
            disabled={!canCommit()}
            class="h-7 rounded-md text-meta font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition flex items-center justify-center gap-2"
          >
            <Show when={git.busy()} fallback={
              <>
                {git.staged().length ? `Commit ${git.staged().length} staged` : 'Commit'}
                <span class="hidden md:inline text-[0.625rem] opacity-70 font-mono">⌘↵</span>
              </>
            }>
              <div class="w-3 h-3 border-2 border-current border-t-transparent rounded-full animate-spin" />
              Committing…
            </Show>
          </button>
        </div>
      </Show>
    </>
  );
}

/** Right pane: the diff of the picked change or commit. */
export function DiffPane(props: { git: GitChanges; onBack?: () => void }) {
  const git = props.git;
  const loading = () => (git.selectedChange() ? git.changeDiff.loading : git.commitDiff.loading);
  const failed = () => (git.selectedChange() ? !!git.changeDiff.error : !!git.commitDiff.error);

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 h-10 px-3 flex items-center gap-2 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)]">
        <Show when={props.onBack}>
          <button type="button" onClick={() => props.onBack?.()} class="icon-btn -ml-1.5" title="Back" aria-label="Back">
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
            </svg>
          </button>
        </Show>
        <Show when={git.selectedChange()} fallback={
          <>
            <span class="shrink-0 text-[0.625rem] font-mono text-[color:var(--text-tertiary)] px-1 rounded bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)]">
              {git.selectedCommit()?.short}
            </span>
            <span class="text-ui font-medium text-[color:var(--text-primary)] truncate">{git.selectedCommit()?.message}</span>
          </>
        }>
          {(sel) => (
            <>
              <span class="text-ui font-medium text-[color:var(--text-primary)] truncate shrink-0 max-w-[50%]">{nameOf(sel().path)}</span>
              <span class="min-w-0 truncate text-micro font-mono text-[color:var(--text-muted)]">{dirOf(sel().path)}</span>
              <Show when={sel().staged}>
                <span class="shrink-0 text-[0.625rem] font-medium px-1.5 py-px rounded text-[color:var(--success)] bg-[color:var(--success)]/10">staged</span>
              </Show>
            </>
          )}
        </Show>
        <div class="flex-1" />
        <Show when={loading()}>
          <Spinner />
        </Show>
      </div>
      <div class="flex-1 min-h-0 overflow-auto p-3">
        <Show when={!loading()} fallback={
          <div class="h-full flex items-center justify-center gap-2 text-meta text-[color:var(--text-tertiary)]">
            <Spinner /> Loading diff…
          </div>
        }>
          <Show when={!failed()} fallback={<p class="text-meta text-[color:var(--danger)]">Couldn't load the diff.</p>}>
            <Show when={git.selectedChange()} fallback={
              <GitDiff diff={git.commitDiff()?.diff || ''} filename={git.selectedCommit()?.message || ''} />
            }>
              <GitDiff diff={git.changeDiff()?.diff || ''} filename={git.selectedChange()!.path} />
            </Show>
          </Show>
        </Show>
      </div>
    </div>
  );
}
