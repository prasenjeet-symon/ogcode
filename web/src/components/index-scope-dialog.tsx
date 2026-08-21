import { createSignal, createResource, createMemo, onMount, onCleanup, Show, For } from 'solid-js';
import { useServer } from '../context/server';
import { useDocIndex } from '../context/docindex';
import { getGitignoreInfo } from '../api/client';

/**
 * The scope panel explains what the index reads, and it leads with .gitignore
 * because that is what actually decides. The indexer has no built-in skip list;
 * it consults .gitignore and the handful of patterns a user adds here. A panel
 * that opened straight onto an "add pattern" box would teach the opposite —
 * that this list is the mechanism — and every project would end up maintaining
 * its ignore rules twice, in two places that drift apart.
 */

// Six patterns chosen to cover the shapes people actually get wrong: what a
// trailing slash means, where a leading slash anchors, the difference between
// one level and any level, and that exclusion is reversible.
const SYNTAX: { pattern: string; meaning: string }[] = [
  { pattern: 'dist/', meaning: 'That folder, wherever it appears in the tree' },
  { pattern: '*.min.js', meaning: 'Every file with that extension, at any depth' },
  { pattern: '/secrets.env', meaning: 'Only at the repo root — a leading slash anchors it' },
  { pattern: 'docs/*.pdf', meaning: 'One level inside docs/, not deeper' },
  { pattern: 'build/**/tmp', meaning: '** spans any number of folders' },
  { pattern: '!keep.md', meaning: 'Puts back a file an earlier rule excluded' },
];

// Offered only when the workspace has no .gitignore at all, and offered as a
// starting point rather than applied: which of these a project wants is a
// question about that project, and answering it on their behalf is how an
// index ends up quietly missing a directory somebody meant to track.
const STARTER = ['node_modules/', 'dist/', 'build/', '.env', '*.log'].join('\n');

const RULES: { title: string; body: string }[] = [
  {
    title: 'Read on every index run',
    body: 'Edit .gitignore, then rebuild — the index picks the file up as it walks.',
  },
  {
    title: 'The last matching rule wins',
    body: 'Within one file, a later line overrides an earlier one. Put ! re-includes below the rule they undo.',
  },
  {
    title: 'Deeper files outrank shallower ones',
    body: 'A .gitignore inside a folder decides that subtree, overriding the root file.',
  },
  {
    title: 'An excluded folder is never opened',
    body: 'So nothing inside it can be re-included — un-exclude the folder first, then narrow.',
  },
  {
    title: '.git/ is always skipped',
    body: 'It sits outside the working tree, and no rule can bring it back.',
  },
];

export default function IndexScopeDialog(props: { onClose: () => void; onRebuild: () => void }) {
  const server = useServer();
  const docIndex = useDocIndex();

  const [tab, setTab] = createSignal<'gitignore' | 'patterns'>('gitignore');
  const [newPattern, setNewPattern] = createSignal('');
  const [copied, setCopied] = createSignal<string | null>(null);

  const [info] = createResource(
    () => server.directory() || '',
    (dir) => getGitignoreInfo(dir || undefined),
  );

  onMount(() => {
    docIndex.loadExcludes();
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') props.onClose(); };
    window.addEventListener('keydown', onKey);
    onCleanup(() => window.removeEventListener('keydown', onKey));
  });

  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  const copy = async (text: string, key: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopied(null), 1400);
    } catch {
      // clipboard unavailable — nothing to do
    }
  };
  onCleanup(() => clearTimeout(copyTimer));

  const addPattern = async () => {
    const p = newPattern().trim();
    if (!p) return;
    await docIndex.addExclude(p);
    setNewPattern('');
  };

  const ruleCount = () => info()?.rules.length ?? 0;
  const negatedCount = createMemo(() => info()?.rules.filter((r) => r.negated).length ?? 0);

  const tabBtn = (id: 'gitignore' | 'patterns') =>
    `h-7 px-3 rounded-md text-[12px] font-medium transition flex items-center gap-1.5 ${
      tab() === id
        ? 'bg-[color:var(--bg-surface)] text-[color:var(--text-primary)] shadow-[var(--shadow-sm)]'
        : 'text-[color:var(--text-tertiary)] hover:text-[color:var(--text-secondary)]'
    }`;

  return (
    <div
      class="fixed inset-0 z-50 bg-black/60 backdrop-blur-[2px] flex items-center justify-center p-4 modal-backdrop"
      onClick={(e) => { if (e.target === e.currentTarget) props.onClose(); }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Index scope"
        class="w-full max-w-[760px] bg-[color:var(--bg-surface)] border border-[color:var(--border-default)] rounded-2xl shadow-[0_24px_64px_rgba(0,0,0,0.6)] flex flex-col overflow-hidden max-h-[84vh] animate-scale-in"
      >
        {/* ---- Header ---- */}
        <div class="shrink-0 px-5 pt-4 pb-3 border-b border-[color:var(--border-subtle)]">
          <div class="flex items-start justify-between gap-3">
            <div class="flex items-start gap-2.5 min-w-0">
              <div class="w-7 h-7 rounded-lg bg-[color:var(--accent-soft)] flex items-center justify-center shrink-0 mt-0.5">
                <svg class="w-4 h-4 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 3c2.755 0 5.455.232 8.083.678.533.09.917.556.917 1.096v1.044a2.25 2.25 0 01-.659 1.591l-5.432 5.432a2.25 2.25 0 00-.659 1.591v2.927a2.25 2.25 0 01-1.244 2.013L9.75 21v-6.568a2.25 2.25 0 00-.659-1.591L3.659 7.409A2.25 2.25 0 013 5.818V4.774c0-.54.384-1.006.917-1.096A48.32 48.32 0 0112 3z" />
                </svg>
              </div>
              <div class="min-w-0">
                <h2 class="text-[14px] font-semibold text-[color:var(--text-primary)] leading-tight">Index scope</h2>
                <p class="text-[11.5px] text-[color:var(--text-tertiary)] mt-1 leading-relaxed">
                  Your <span class="font-mono text-[color:var(--text-secondary)]">.gitignore</span> decides what gets indexed. One list, already under review, shared with git.
                </p>
              </div>
            </div>
            <button
              onClick={props.onClose}
              class="w-7 h-7 rounded-lg flex items-center justify-center text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition shrink-0"
              title="Close"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>

          {/* Tabs */}
          <div class="mt-3 inline-flex items-center gap-0.5 p-0.5 rounded-lg bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)]">
            <button onClick={() => setTab('gitignore')} class={tabBtn('gitignore')}>
              <span class="font-mono text-[11px]">.gitignore</span>
              <Show when={info()?.exists}>
                <span class="text-[10px] tabular-nums text-[color:var(--text-muted)]">{ruleCount()}{info()?.truncated ? '+' : ''}</span>
              </Show>
            </button>
            <button onClick={() => setTab('patterns')} class={tabBtn('patterns')}>
              Extra patterns
              <Show when={docIndex.excludes().length > 0}>
                <span class="text-[10px] tabular-nums text-[color:var(--text-muted)]">{docIndex.excludes().length}</span>
              </Show>
            </button>
          </div>
        </div>

        {/* ---- Body ---- */}
        <div class="flex-1 overflow-y-auto">

          {/* ============ .gitignore ============ */}
          <Show when={tab() === 'gitignore'}>
            <div class="px-5 py-4 flex flex-col gap-4">

              {/* Status */}
              <Show when={!info.loading} fallback={
                <div class="h-[72px] rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] flex items-center justify-center gap-2 text-[12px] text-[color:var(--text-tertiary)]">
                  <div class="w-3.5 h-3.5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                  Reading .gitignore…
                </div>
              }>
                <Show
                  when={info()?.exists}
                  fallback={
                    <div class="rounded-xl border border-[color:var(--warning)]/30 bg-[color:var(--warning)]/[0.06] p-3.5">
                      <div class="flex items-start gap-2.5">
                        <svg class="w-4 h-4 text-[color:var(--warning)] shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                          <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                        </svg>
                        <div class="min-w-0">
                          <p class="text-[12.5px] font-medium text-[color:var(--text-primary)]">This workspace has no .gitignore</p>
                          <p class="text-[11.5px] text-[color:var(--text-tertiary)] mt-1 leading-relaxed">
                            Every readable file is being indexed, apart from <span class="font-mono">.git/</span>. Create one at{' '}
                            <span class="font-mono text-[color:var(--text-secondary)]">{info()?.path}</span> and the next run will respect it.
                          </p>
                          <div class="mt-2.5 rounded-lg border border-[color:var(--border-subtle)] bg-[color:var(--bg-base)] overflow-hidden">
                            <div class="px-2.5 py-1.5 border-b border-[color:var(--border-subtle)] flex items-center justify-between gap-2">
                              <span class="text-[10px] uppercase tracking-wider text-[color:var(--text-muted)]">A common starting point</span>
                              <button
                                onClick={() => copy(STARTER, 'starter')}
                                class="h-5 px-1.5 rounded text-[10px] text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition"
                              >
                                {copied() === 'starter' ? 'copied' : 'copy'}
                              </button>
                            </div>
                            <pre class="px-2.5 py-2 text-[11px] font-mono leading-[1.7] text-[color:var(--text-secondary)] whitespace-pre">{STARTER}</pre>
                          </div>
                          <p class="text-[11px] text-[color:var(--text-muted)] mt-2 leading-relaxed">
                            Keep only the lines that apply to this project — a rule you don't need hides files you meant to keep.
                          </p>
                        </div>
                      </div>
                    </div>
                  }
                >
                  <div class="rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] overflow-hidden">
                    <div class="px-3 py-2.5 flex items-center gap-2.5 border-b border-[color:var(--border-subtle)]">
                      <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--success)] shrink-0" />
                      <div class="min-w-0 flex-1">
                        <p class="text-[12px] font-mono text-[color:var(--text-primary)] truncate" title={info()?.path}>.gitignore</p>
                        <p class="text-[10.5px] text-[color:var(--text-muted)] mt-0.5 tabular-nums">
                          {ruleCount()}{info()?.truncated ? '+' : ''} {ruleCount() === 1 ? 'rule' : 'rules'} in force
                          <Show when={negatedCount() > 0}>{` · ${negatedCount()} re-include${negatedCount() === 1 ? '' : 's'}`}</Show>
                        </p>
                      </div>
                      <button
                        onClick={() => copy(info()!.path, 'path')}
                        class="h-6 px-2 rounded-md text-[11px] text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)] transition shrink-0"
                        title={info()?.path}
                      >
                        {copied() === 'path' ? 'copied' : 'copy path'}
                      </button>
                    </div>

                    <div class="max-h-[150px] overflow-y-auto py-1">
                      <For each={info()?.rules}>
                        {(rule) => (
                          <button
                            onClick={() => copy(rule.pattern, `rule-${rule.line}`)}
                            class="w-full flex items-baseline gap-2.5 px-3 py-[3px] text-left hover:bg-[color:var(--bg-hover)] transition group"
                            title="Copy pattern"
                          >
                            <span class="w-6 shrink-0 text-right text-[10px] font-mono tabular-nums text-[color:var(--text-muted)]">{rule.line}</span>
                            <span
                              class="text-[11.5px] font-mono truncate"
                              classList={{
                                'text-[color:var(--success)]': rule.negated,
                                'text-[color:var(--text-secondary)]': !rule.negated,
                              }}
                            >
                              {rule.pattern}
                            </span>
                            <Show when={copied() === `rule-${rule.line}`}>
                              <span class="text-[10px] text-[color:var(--success)] shrink-0">copied</span>
                            </Show>
                          </button>
                        )}
                      </For>
                    </div>

                    <Show when={info()?.truncated}>
                      <p class="px-3 py-1.5 border-t border-[color:var(--border-subtle)] text-[10.5px] text-[color:var(--text-muted)]">
                        Showing the first {ruleCount()} rules — the file has more.
                      </p>
                    </Show>
                  </div>
                </Show>
              </Show>

              {/* Nested files */}
              <Show when={(info()?.nested.length ?? 0) > 0}>
                <div class="rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)]/60 px-3 py-2.5">
                  <p class="text-[11.5px] text-[color:var(--text-secondary)]">
                    {info()!.nested.length} more .gitignore {info()!.nested.length === 1 ? 'file' : 'files'} below the root — each one overrides this one for its own folder.
                  </p>
                  <div class="mt-2 flex flex-wrap gap-1.5">
                    <For each={info()?.nested}>
                      {(path) => (
                        <span class="px-1.5 py-0.5 rounded bg-[color:var(--bg-base)] border border-[color:var(--border-subtle)] text-[10.5px] font-mono text-[color:var(--text-tertiary)]">
                          {path}
                        </span>
                      )}
                    </For>
                  </div>
                </div>
              </Show>

              <div class="grid grid-cols-1 md:grid-cols-2 gap-4 items-start">
              {/* Syntax */}
              <div>
                <h3 class="text-[10px] font-semibold uppercase tracking-wider text-[color:var(--text-muted)] mb-2">Writing a rule</h3>
                <div class="rounded-xl border border-[color:var(--border-subtle)] overflow-hidden divide-y divide-[color:var(--border-subtle)]">
                  <For each={SYNTAX}>
                    {(row) => (
                      <button
                        onClick={() => copy(row.pattern, `syn-${row.pattern}`)}
                        class="w-full px-2.5 py-2 flex items-center gap-2.5 text-left hover:bg-[color:var(--bg-elevated)] transition"
                        title="Copy pattern"
                      >
                        <code class="w-[96px] shrink-0 text-[11.5px] font-mono text-[color:var(--accent)] truncate">{row.pattern}</code>
                        <span class="flex-1 min-w-0 text-[11.5px] text-[color:var(--text-tertiary)] leading-snug">{row.meaning}</span>
                        <Show when={copied() === `syn-${row.pattern}`}>
                          <span class="text-[10px] text-[color:var(--success)] shrink-0">copied</span>
                        </Show>
                      </button>
                    )}
                  </For>
                </div>
              </div>

              {/* Precedence */}
              <div>
                <h3 class="text-[10px] font-semibold uppercase tracking-wider text-[color:var(--text-muted)] mb-2">How the index reads it</h3>
                <div class="flex flex-col gap-2">
                  <For each={RULES}>
                    {(rule) => (
                      <div class="flex items-start gap-2.5">
                        <svg class="w-3.5 h-3.5 text-[color:var(--accent)] shrink-0 mt-[3px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                          <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                        </svg>
                        <p class="text-[11.5px] leading-relaxed text-[color:var(--text-tertiary)]">
                          <span class="text-[color:var(--text-secondary)] font-medium">{rule.title}.</span>{' '}
                          {rule.body}
                        </p>
                      </div>
                    )}
                  </For>
                </div>
              </div>
              </div>
            </div>
          </Show>

          {/* ============ Extra patterns ============ */}
          <Show when={tab() === 'patterns'}>
            <div class="px-5 py-4 flex flex-col gap-3.5">
              <p class="text-[11.5px] text-[color:var(--text-tertiary)] leading-relaxed">
                For files git tracks but the index shouldn't carry — a committed fixture, a vendored bundle.
                These are matched against <span class="text-[color:var(--text-secondary)]">file and folder names only</span>, so{' '}
                <code class="font-mono text-[color:var(--accent)]">logs</code> and{' '}
                <code class="font-mono text-[color:var(--accent)]">*.test.js</code> work, while a path like{' '}
                <code class="font-mono">web/dist</code> does not. If the whole project should ignore it, it belongs in .gitignore.
              </p>

              <div class="flex gap-2">
                <input
                  type="text"
                  placeholder="e.g. fixtures, *.snap, coverage"
                  value={newPattern()}
                  onInput={(e) => setNewPattern(e.currentTarget.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter') addPattern(); }}
                  class="flex-1 h-8 px-3 rounded-lg text-[12px] font-mono bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-primary)] placeholder-[color:var(--text-muted)] focus:outline-none focus:border-[color:var(--accent)] transition"
                />
                <button
                  onClick={addPattern}
                  disabled={!newPattern().trim()}
                  class="h-8 px-3.5 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition"
                >
                  Add
                </button>
              </div>

              <div class="rounded-xl border border-[color:var(--border-subtle)] overflow-hidden">
                <Show
                  when={docIndex.excludes().length > 0}
                  fallback={
                    <p class="text-[11.5px] text-[color:var(--text-muted)] text-center py-7 px-4 leading-relaxed">
                      No extra patterns. <span class="font-mono">.gitignore</span> is doing all the work — which is where it belongs.
                    </p>
                  }
                >
                  <For each={docIndex.excludes()}>
                    {(entry, i) => (
                      <div
                        class="flex items-center justify-between gap-2 px-3 py-2 hover:bg-[color:var(--bg-elevated)] group transition"
                        classList={{ 'border-t border-[color:var(--border-subtle)]': i() > 0 }}
                      >
                        <span class="text-[11.5px] font-mono text-[color:var(--text-secondary)] truncate">{entry.pattern}</span>
                        <button
                          onClick={() => docIndex.deleteExclude(entry.id)}
                          class="w-6 h-6 rounded flex items-center justify-center text-[color:var(--text-muted)] hover:text-[color:var(--danger)] hover:bg-[color:var(--danger)]/10 opacity-0 group-hover:opacity-100 focus:opacity-100 transition shrink-0"
                          title={`Remove ${entry.pattern}`}
                        >
                          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                          </svg>
                        </button>
                      </div>
                    )}
                  </For>
                </Show>
              </div>
            </div>
          </Show>
        </div>

        {/* ---- Footer ---- */}
        <div class="shrink-0 px-5 py-3 border-t border-[color:var(--border-subtle)] flex items-center justify-between gap-3">
          <p class="text-[11px] text-[color:var(--text-muted)] leading-snug">
            Rules apply on the next index run.
          </p>
          <div class="flex items-center gap-2 shrink-0">
            <button
              onClick={props.onClose}
              class="h-8 px-3.5 rounded-lg text-[12px] text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition"
            >
              Close
            </button>
            <button
              onClick={props.onRebuild}
              disabled={docIndex.building()}
              class="h-8 px-3.5 rounded-lg text-[12px] font-medium bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)] text-[color:var(--text-primary)] hover:border-[color:var(--border-strong)] disabled:opacity-40 disabled:cursor-not-allowed transition flex items-center gap-1.5"
            >
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 12c0-1.232-.046-2.453-.138-3.662a4.006 4.006 0 00-3.7-3.7 48.678 48.678 0 00-7.324 0 4.006 4.006 0 00-3.7 3.7c-.017.22-.032.441-.046.662M19.5 12l3-3m-3 3l-3-3m-12 3c0 1.232.046 2.453.138 3.662a4.006 4.006 0 003.7 3.7 48.656 48.656 0 007.324 0 4.006 4.006 0 003.7-3.7c.017-.22.032-.441.046-.662M4.5 12l3 3m-3-3l-3 3" />
              </svg>
              Rebuild index
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
