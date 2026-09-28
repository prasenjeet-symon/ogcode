import { createSignal, createResource, createMemo, onMount, onCleanup, Show, For, type JSX } from 'solid-js';
import { useServer } from '../context/server';
import { useDocIndex } from '../context/docindex';
import { getGitignoreInfo, type ExcludeEntry } from '../api/client';
import DialogShell, { DialogHeader } from './dialog-shell';

/**
 * The scope panel explains what the index reads, and it leads with .gitignore
 * because that is what decides for a project's own files. Alongside it ogcode
 * ships a short list of defaults — dependency folders, build output, generated
 * bundles and lockfiles — seeded into the patterns list on the other tab, where
 * each one is marked and any of them can be removed.
 *
 * The panel still leads with .gitignore rather than with that list: the defaults
 * are a floor for the generated directory a project forgot, not the mechanism.
 * A panel that opened straight onto an "add pattern" box would teach the
 * opposite, and every project would end up maintaining its ignore rules twice,
 * in two places that drift apart.
 *
 * The one thing skipped without being asked, and without appearing in any list,
 * is ogcode's own state directory: it is not a project file at all, and it is
 * called out under the rules so the panel is not describing a narrower index
 * than the one that runs.
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
//
// These are the names ogcode's own defaults deliberately leave to the project —
// a tracked vendor tree, a target directory, a framework cache. The generated
// directories the defaults already cover (node_modules, build output, lockfiles)
// are not repeated here, so this is not a second, drifting copy of that list.
const STARTER = ['vendor/', 'target/', '.next/', '.cache/', 'Pods/'].join('\n');

// How rules combine — the four facts that explain every "why is this file
// (not) indexed" question.
const PRECEDENCE: { title: string; body: string }[] = [
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
];

// Skipped whatever the rules say.
const ALWAYS: { name: string; why: string }[] = [
  { name: '.git/', why: 'It sits outside the working tree, and no rule can bring it back.' },
  {
    name: '.ogcode/',
    why: "It holds ogcode's own state — the project database, notes, plan archives, and the worktrees tasks are checked out into. It is not part of your project, so a rule neither hides nor reveals it.",
  },
];

export default function IndexScopeDialog(props: {
  onClose: () => void;
  onRebuild: () => void;
  returnFocus?: () => HTMLElement | undefined;
}) {
  const server = useServer();
  const docIndex = useDocIndex();

  const [tab, setTab] = createSignal<'gitignore' | 'patterns'>('gitignore');
  const [newPattern, setNewPattern] = createSignal('');
  const [adding, setAdding] = createSignal(false);
  const [copied, setCopied] = createSignal<string | null>(null);
  let patternInput: HTMLInputElement | undefined;

  const [info] = createResource(
    () => server.directory() || '',
    (dir) => getGitignoreInfo(dir || undefined),
  );

  onMount(() => docIndex.loadExcludes());

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

  // Extra patterns are compared with one file or folder name at a time (an
  // exact match or a shell glob), so some patterns that look reasonable can
  // never match anything. Saying so before they are added beats a pattern that
  // silently does nothing.
  const problem = createMemo(() => {
    const p = newPattern().trim();
    if (!p) return '';
    if (p.startsWith('!')) return "Extra patterns can't re-include — put a ! rule in .gitignore instead.";
    if (p.includes('/')) {
      const name = p.replace(/^\/+|\/+$/g, '');
      return name && !name.includes('/')
        ? `Patterns match a name, not a path — use “${name}” without the slash.`
        : 'Patterns match a single file or folder name, never a path — this would match nothing. A path belongs in .gitignore.';
    }
    if (docIndex.excludes().some((e) => e.pattern === p)) return 'Already in the list.';
    return '';
  });

  const addPattern = async () => {
    const p = newPattern().trim();
    if (!p || problem() || adding()) return;
    setAdding(true);
    try {
      await docIndex.addExclude(p);
      setNewPattern('');
      patternInput?.focus();
    } finally {
      setAdding(false);
    }
  };

  const ruleCount = () => info()?.rules.length ?? 0;
  const negatedCount = createMemo(() => info()?.rules.filter((r) => r.negated).length ?? 0);
  // Plain code-point order, so the list reads the same before and after an
  // add — the store's locale-aware sort skips punctuation and reshuffles it.
  const byPattern = (a: ExcludeEntry, b: ExcludeEntry) => (a.pattern < b.pattern ? -1 : a.pattern > b.pattern ? 1 : 0);
  const mine = createMemo(() => docIndex.excludes().filter((e) => !e.default).sort(byPattern));
  const defaults = createMemo(() => docIndex.excludes().filter((e) => e.default).sort(byPattern));

  const CopyGlyph = (p: { done: boolean }) => (
    <Show when={p.done} fallback={
      <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
        <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25m12 6.625v-1.875a3.375 3.375 0 00-3.375-3.375h-1.5a1.125 1.125 0 01-1.125-1.125v-1.5a3.375 3.375 0 00-3.375-3.375H9.75" />
      </svg>
    }>
      <svg class="w-3 h-3 text-[color:var(--success)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
        <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
      </svg>
    </Show>
  );

  const SectionTitle = (p: { children: JSX.Element; aside?: JSX.Element }) => (
    <div class="flex items-center gap-2 mb-2">
      <h3 class="text-[0.625rem] font-semibold uppercase tracking-[0.06em] text-[color:var(--text-muted)]">{p.children}</h3>
      <div class="flex-1" />
      {p.aside}
    </div>
  );

  const PatternRow = (p: { entry: ExcludeEntry }) => (
    <div class="group h-7 pl-2.5 pr-0.5 flex items-center gap-2 rounded-md border border-transparent bg-[color:var(--bg-elevated)]/60 hover:bg-[color:var(--bg-elevated)] hover:border-[color:var(--border-subtle)] transition-colors">
      <span class="flex-1 min-w-0 truncate text-meta font-mono text-[color:var(--text-secondary)]" title={p.entry.pattern}>
        {p.entry.pattern}
      </span>
      <button
        type="button"
        onClick={() => docIndex.deleteExclude(p.entry.id)}
        class="hover-reveal w-6 h-6 rounded-md flex items-center justify-center text-[color:var(--text-muted)] hover:text-[color:var(--danger)] hover:bg-[color:var(--danger)]/10 opacity-0 group-hover:opacity-100 focus:opacity-100 transition shrink-0"
        title={p.entry.default ? `Remove ${p.entry.pattern} — it stays removed` : `Remove ${p.entry.pattern}`}
        aria-label={`Remove ${p.entry.pattern}`}
      >
        <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </div>
  );

  return (
    <DialogShell
      labelledBy="idx-scope-title"
      describedBy="idx-scope-desc"
      onClose={props.onClose}
      class="sm:max-w-[740px]"
      returnFocus={props.returnFocus}
    >
      <DialogHeader
        id="idx-scope-title"
        descriptionId="idx-scope-desc"
        title="Index scope"
        description={<>Your <span class="font-mono text-[color:var(--text-secondary)]">.gitignore</span> decides what gets indexed, plus a few defaults ogcode ships for generated files.</>}
        onClose={props.onClose}
        icon={
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 3c2.755 0 5.455.232 8.083.678.533.09.917.556.917 1.096v1.044a2.25 2.25 0 01-.659 1.591l-5.432 5.432a2.25 2.25 0 00-.659 1.591v2.927a2.25 2.25 0 01-1.244 2.013L9.75 21v-6.568a2.25 2.25 0 00-.659-1.591L3.659 7.409A2.25 2.25 0 013 5.818V4.774c0-.54.384-1.006.917-1.096A48.32 48.32 0 0112 3z" />
          </svg>
        }
      >
        <div class="mt-3.5 seg" role="tablist" aria-label="Scope source">
          <button type="button" role="tab" class="seg-btn" aria-selected={tab() === 'gitignore'} onClick={() => setTab('gitignore')}>
            <span class="font-mono">.gitignore</span>
            <Show when={info()?.exists}>
              <span class="seg-count">{ruleCount()}{info()?.truncated ? '+' : ''}</span>
            </Show>
          </button>
          <button type="button" role="tab" class="seg-btn" aria-selected={tab() === 'patterns'} onClick={() => setTab('patterns')}>
            Extra patterns
            <Show when={docIndex.excludes().length > 0}>
              <span class="seg-count">{docIndex.excludes().length}</span>
            </Show>
          </button>
        </div>
      </DialogHeader>

      {/* ---- Body ---- */}
      <div class="flex-1 min-h-0 overflow-y-auto">

        {/* ============ .gitignore ============ */}
        <Show when={tab() === 'gitignore'}>
          <div class="px-5 py-4 flex flex-col gap-5">
            <Show when={!info.loading} fallback={
              <div class="rounded-xl border border-[color:var(--border-subtle)] overflow-hidden" aria-busy="true">
                <div class="h-11 px-3 flex items-center gap-2.5 border-b border-[color:var(--border-subtle)]">
                  <div class="skel h-3 w-20" /><div class="flex-1" /><div class="skel h-3 w-16" />
                </div>
                <div class="p-3 flex flex-col gap-2">
                  <div class="skel h-3 w-40" /><div class="skel h-3 w-28" /><div class="skel h-3 w-48" /><div class="skel h-3 w-32" />
                </div>
              </div>
            }>
              <Show
                when={info()?.exists}
                fallback={
                  <div class="rounded-xl border border-[color:var(--warning)]/30 bg-[color:var(--warning)]/[0.05] p-3.5">
                    <div class="flex items-start gap-2.5">
                      <svg class="w-4 h-4 text-[color:var(--warning)] shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                      </svg>
                      <div class="min-w-0 flex-1">
                        <p class="text-ui font-medium text-[color:var(--text-primary)]">This workspace has no .gitignore</p>
                        <p class="text-meta text-[color:var(--text-tertiary)] mt-1 leading-relaxed">
                          Every readable file is being indexed, apart from <span class="font-mono">.git/</span> and ogcode's own defaults. Create one at{' '}
                          <span class="font-mono text-[color:var(--text-secondary)] break-all">{info()?.path}</span> and the next run will respect it.
                        </p>
                        <div class="mt-3 rounded-lg border border-[color:var(--border-subtle)] bg-[color:var(--bg-base)] overflow-hidden">
                          <div class="h-8 pl-3 pr-1.5 border-b border-[color:var(--border-subtle)] flex items-center justify-between gap-2">
                            <span class="text-[0.625rem] font-semibold uppercase tracking-[0.06em] text-[color:var(--text-muted)]">A common starting point</span>
                            <button
                              type="button"
                              onClick={() => copy(STARTER, 'starter')}
                              class="h-6 px-2 rounded-md flex items-center gap-1.5 text-micro text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition"
                            >
                              <CopyGlyph done={copied() === 'starter'} />
                              {copied() === 'starter' ? 'Copied' : 'Copy'}
                            </button>
                          </div>
                          <pre class="px-3 py-2 text-meta font-mono leading-[1.7] text-[color:var(--text-secondary)] whitespace-pre">{STARTER}</pre>
                        </div>
                        <p class="text-micro text-[color:var(--text-muted)] mt-2 leading-relaxed">
                          Keep only the lines that apply to this project — a rule you don't need hides files you meant to keep.
                        </p>
                      </div>
                    </div>
                  </div>
                }
              >
                <div class="rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-base)] overflow-hidden">
                  <div class="h-11 pl-3 pr-1.5 flex items-center gap-2.5 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)]/50">
                    <span class="idx-done-dot" title="Found" />
                    <span class="text-ui font-mono text-[color:var(--text-primary)]">.gitignore</span>
                    <span class="min-w-0 truncate text-micro text-[color:var(--text-muted)] tabular-nums">
                      {ruleCount()}{info()?.truncated ? '+' : ''} {ruleCount() === 1 ? 'rule' : 'rules'} in force
                      <Show when={negatedCount() > 0}>{` · ${negatedCount()} re-include${negatedCount() === 1 ? '' : 's'}`}</Show>
                    </span>
                    <div class="flex-1" />
                    <button
                      type="button"
                      onClick={() => copy(info()!.path, 'path')}
                      class="shrink-0 h-7 px-2 rounded-md flex items-center gap-1.5 text-micro text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)] transition"
                      title={info()?.path}
                    >
                      <CopyGlyph done={copied() === 'path'} />
                      {copied() === 'path' ? 'Copied' : 'Copy path'}
                    </button>
                  </div>

                  <div class="max-h-[216px] overflow-y-auto py-1.5">
                    <For each={info()?.rules}>
                      {(rule) => (
                        <button
                          type="button"
                          onClick={() => copy(rule.pattern, `rule-${rule.line}`)}
                          class="group w-full h-[22px] flex items-center gap-3 pl-2 pr-3 text-left hover:bg-[color:var(--bg-elevated)] transition-colors"
                          title="Copy pattern"
                        >
                          <span class="w-7 shrink-0 text-right text-[0.625rem] font-mono tabular-nums text-[color:var(--text-muted)]">{rule.line}</span>
                          <span
                            class="min-w-0 truncate text-meta font-mono"
                            classList={{
                              'text-[color:var(--success)]': rule.negated,
                              'text-[color:var(--text-secondary)]': !rule.negated,
                            }}
                          >
                            {rule.pattern}
                          </span>
                          <Show when={rule.negated}>
                            <span class="shrink-0 text-[0.5625rem] uppercase tracking-wider text-[color:var(--success)]/80">re-include</span>
                          </Show>
                          <span class="flex-1" />
                          <span
                            class="shrink-0 text-[color:var(--text-muted)] transition-opacity"
                            classList={{ 'opacity-0 group-hover:opacity-100': copied() !== `rule-${rule.line}` }}
                          >
                            <CopyGlyph done={copied() === `rule-${rule.line}`} />
                          </span>
                        </button>
                      )}
                    </For>
                  </div>

                  <Show when={info()?.truncated}>
                    <p class="px-3 py-1.5 border-t border-[color:var(--border-subtle)] text-micro text-[color:var(--text-muted)]">
                      Showing the first {ruleCount()} rules — the file has more.
                    </p>
                  </Show>
                </div>
              </Show>
            </Show>

            {/* Nested files */}
            <Show when={(info()?.nested.length ?? 0) > 0}>
              <div class="-mt-2 flex items-start gap-2.5 px-1">
                <svg class="w-3.5 h-3.5 mt-0.5 shrink-0 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 12.75V12A2.25 2.25 0 014.5 9.75h15A2.25 2.25 0 0121.75 12v.75m-8.69-6.44l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z" />
                </svg>
                <div class="min-w-0">
                  <p class="text-meta text-[color:var(--text-tertiary)] leading-relaxed">
                    {info()!.nested.length} more .gitignore {info()!.nested.length === 1 ? 'file' : 'files'} below the root — each one overrides this one for its own folder.
                  </p>
                  <div class="mt-1.5 flex flex-wrap gap-1.5">
                    <For each={info()?.nested}>
                      {(path) => (
                        <button
                          type="button"
                          onClick={() => copy(path, `nested-${path}`)}
                          class="px-1.5 py-0.5 rounded-md bg-[color:var(--bg-base)] border border-[color:var(--border-subtle)] hover:border-[color:var(--border-default)] text-micro font-mono text-[color:var(--text-tertiary)] hover:text-[color:var(--text-secondary)] transition-colors"
                          title="Copy path"
                        >
                          {copied() === `nested-${path}` ? 'Copied' : path}
                        </button>
                      )}
                    </For>
                  </div>
                </div>
              </div>
            </Show>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-5 items-start">
              {/* Syntax */}
              <section>
                <SectionTitle>Writing a rule</SectionTitle>
                <div class="rounded-xl border border-[color:var(--border-subtle)] overflow-hidden divide-y divide-[color:var(--border-subtle)]">
                  <For each={SYNTAX}>
                    {(row) => (
                      <button
                        type="button"
                        onClick={() => copy(row.pattern, `syn-${row.pattern}`)}
                        class="group w-full px-3 py-2 flex items-center gap-3 text-left hover:bg-[color:var(--bg-elevated)] transition-colors"
                        title="Copy pattern"
                      >
                        <code class="w-[92px] shrink-0 text-meta font-mono text-[color:var(--text-primary)] truncate">{row.pattern}</code>
                        <span class="flex-1 min-w-0 text-meta text-[color:var(--text-tertiary)] leading-snug">{row.meaning}</span>
                        <span
                          class="shrink-0 text-[color:var(--text-muted)] transition-opacity"
                          classList={{ 'opacity-0 group-hover:opacity-100': copied() !== `syn-${row.pattern}` }}
                        >
                          <CopyGlyph done={copied() === `syn-${row.pattern}`} />
                        </span>
                      </button>
                    )}
                  </For>
                </div>
              </section>

              {/* Precedence */}
              <section>
                <SectionTitle>How the index reads it</SectionTitle>
                <ol class="flex flex-col gap-2.5">
                  <For each={PRECEDENCE}>
                    {(rule, i) => (
                      <li class="flex items-start gap-2.5">
                        <span class="w-[18px] h-[18px] mt-px shrink-0 rounded-md flex items-center justify-center text-[0.625rem] font-semibold tabular-nums text-[color:var(--text-tertiary)] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)]">
                          {i() + 1}
                        </span>
                        <p class="text-meta leading-relaxed text-[color:var(--text-tertiary)]">
                          <span class="text-[color:var(--text-secondary)] font-medium">{rule.title}.</span>{' '}
                          {rule.body}
                        </p>
                      </li>
                    )}
                  </For>
                </ol>

                <div class="mt-4 pt-3.5 border-t border-[color:var(--border-subtle)] flex flex-col gap-2">
                  <div class="flex items-center gap-2 flex-wrap">
                    <span class="text-micro text-[color:var(--text-muted)]">Always skipped</span>
                    <For each={ALWAYS}>
                      {(a) => (
                        <span
                          class="px-1.5 py-0.5 rounded-md text-micro font-mono text-[color:var(--text-secondary)] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] cursor-help"
                          title={a.why}
                        >
                          {a.name}
                        </span>
                      )}
                    </For>
                  </div>
                  <p class="text-micro leading-relaxed text-[color:var(--text-muted)]">
                    No rule can bring either back. A few defaults for dependency folders, build output and lockfiles also ship with ogcode —{' '}
                    <button
                      type="button"
                      onClick={() => setTab('patterns')}
                      class="text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] underline underline-offset-2 decoration-[color:var(--border-strong)]"
                    >
                      see Extra patterns
                    </button>
                    .
                  </p>
                </div>
              </section>
            </div>
          </div>
        </Show>

        {/* ============ Extra patterns ============ */}
        <Show when={tab() === 'patterns'}>
          <div class="px-5 py-4 flex flex-col gap-4">
            <p class="text-meta text-[color:var(--text-tertiary)] leading-relaxed">
              For files git tracks but the index shouldn't carry — a committed fixture, a vendored bundle.
              Each pattern is matched against a <span class="text-[color:var(--text-secondary)]">single file or folder name</span>, anywhere
              in the tree, so <code class="font-mono text-[color:var(--text-secondary)]">logs</code> and{' '}
              <code class="font-mono text-[color:var(--text-secondary)]">*.test.js</code> work while a path like{' '}
              <code class="font-mono">web/dist</code> never matches. If the whole project should ignore it, it belongs in .gitignore.
            </p>

            <div>
              <form
                class="flex gap-2"
                onSubmit={(e) => { e.preventDefault(); addPattern(); }}
              >
                <div class="relative flex-1">
                  <input
                    ref={patternInput}
                    type="text"
                    placeholder="e.g. fixtures, *.snap, coverage"
                    value={newPattern()}
                    onInput={(e) => setNewPattern(e.currentTarget.value)}
                    aria-label="New exclude pattern"
                    aria-invalid={!!problem()}
                    spellcheck={false}
                    autocomplete="off"
                    class="w-full h-8 px-3 rounded-lg text-meta font-mono bg-[color:var(--bg-base)] border text-[color:var(--text-primary)] placeholder-[color:var(--text-muted)] focus:outline-none transition"
                    classList={{
                      'border-[color:var(--border-subtle)] focus:border-[color:var(--border-strong)]': !problem(),
                      'border-[color:var(--warning)]/50': !!problem(),
                    }}
                  />
                </div>
                <button
                  type="submit"
                  disabled={!newPattern().trim() || !!problem() || adding()}
                  class="h-8 px-3.5 rounded-lg text-meta font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition"
                >
                  Add
                </button>
              </form>
              <Show when={problem()}>
                <p class="mt-1.5 px-0.5 text-micro text-[color:var(--warning)] leading-relaxed">{problem()}</p>
              </Show>
            </div>

            <section>
              <SectionTitle aside={<span class="text-micro text-[color:var(--text-muted)] tabular-nums">{mine().length}</span>}>
                Added for this project
              </SectionTitle>
              <Show when={mine().length > 0} fallback={
                <p class="px-3 py-3 rounded-lg border border-dashed border-[color:var(--border-subtle)] text-micro text-[color:var(--text-muted)]">
                  None yet — anything you add above lands here.
                </p>
              }>
                <div class="grid grid-cols-1 sm:grid-cols-2 gap-1.5">
                  <For each={mine()}>{(entry) => <PatternRow entry={entry} />}</For>
                </div>
              </Show>
            </section>

            <section>
              <SectionTitle aside={<span class="text-micro text-[color:var(--text-muted)]">Remove one and it stays removed</span>}>
                Shipped with ogcode · {defaults().length}
              </SectionTitle>
              <Show when={defaults().length > 0} fallback={
                <p class="px-3 py-3 rounded-lg border border-dashed border-[color:var(--border-subtle)] text-micro text-[color:var(--text-muted)] leading-relaxed">
                  Every default has been removed, so <span class="font-mono">.gitignore</span> is doing all the work — which is where it belongs.
                </p>
              }>
                <div class="grid grid-cols-2 sm:grid-cols-3 gap-1.5">
                  <For each={defaults()}>{(entry) => <PatternRow entry={entry} />}</For>
                </div>
              </Show>
            </section>
          </div>
        </Show>
      </div>

      {/* ---- Footer ---- */}
      <div class="shrink-0 px-5 py-3 border-t border-[color:var(--border-subtle)] bg-[color:var(--bg-base)]/40 flex items-center justify-between gap-3">
        <p class="text-micro text-[color:var(--text-muted)] leading-snug min-w-0 truncate">
          Changes apply on the next index run.
        </p>
        <div class="flex items-center gap-2 shrink-0">
          <button
            type="button"
            onClick={props.onClose}
            class="h-8 px-3 rounded-lg text-meta text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition"
          >
            Close
          </button>
          <button
            type="button"
            onClick={props.onRebuild}
            disabled={docIndex.building()}
            class="h-8 px-3 rounded-lg text-meta font-medium bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)] text-[color:var(--text-primary)] hover:border-[color:var(--border-strong)] hover:bg-[color:var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition flex items-center gap-1.5"
          >
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99" />
            </svg>
            Rebuild index…
          </button>
        </div>
      </div>
    </DialogShell>
  );
}
