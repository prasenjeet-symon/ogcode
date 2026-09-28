import { createResource, createMemo, createSignal, Show, For } from 'solid-js';
import { useServer } from '../context/server';
import { useDocIndex } from '../context/docindex';
import { getIndexPlan, type ModelInfo } from '../api/client';
import { modelGroup } from '../lib/providers';
import { groupLabel, groupDot } from './model-selector';
import DialogShell, { DialogHeader } from './dialog-shell';
import { revealWithin } from '../lib/reveal';

/**
 * The run dialog is where indexing gets paid for, so it opens by saying what it
 * will cost: how many files, of what kind, and which of them are already done.
 * The old modal asked only which model to use, which put the expensive question
 * — is this run worth starting — somewhere the person clicking could not see it.
 *
 * The plan is drawn as one bar split into what is already indexed, what this run
 * will read and what it will drop, so "1 of 652" and "652 of 652" look as
 * different as they are. The model list is a radio group: arrows move the
 * choice, Enter starts the run.
 */

const price = (n: number) => (n % 1 === 0 ? String(n) : n.toFixed(2));

export default function IndexRunDialog(props: {
  rebuild: boolean;
  onClose: () => void;
  onConfirm: () => void;
  onOpenScope: () => void;
  returnFocus?: () => HTMLElement | undefined;
}) {
  const server = useServer();
  const docIndex = useDocIndex();

  const [plan] = createResource(
    () => server.directory() || '',
    (dir) => getIndexPlan(dir || undefined),
  );

  const [query, setQuery] = createSignal('');
  const enabled = createMemo(() => docIndex.models().filter((m) => m.enabled));
  const visible = createMemo(() => {
    const q = query().trim().toLowerCase();
    return q ? enabled().filter((m) => `${m.name} ${m.id} ${modelGroup(m)}`.toLowerCase().includes(q)) : enabled();
  });
  const groups = createMemo(() => [...new Set(visible().map((m) => modelGroup(m)))]);
  const inGroup = (group: string) => visible().filter((m) => modelGroup(m) === group);
  const selected = () => enabled().find((m) => m.id === docIndex.selectedModel());

  // A rebuild re-reads everything; an incremental run only touches what is not
  // in the index yet. Naming the right number is the whole point of the panel.
  const workCount = () => (props.rebuild ? plan()?.total ?? 0 : plan()?.pending ?? 0);
  const staleCount = () => plan()?.stale ?? 0;
  const doneCount = () => (props.rebuild ? 0 : plan()?.indexed ?? 0);

  // With nothing new to index and nothing stale to drop, the run would walk the
  // tree and do nothing. Saying so beats letting someone spend a minute finding
  // out.
  const isNoop = () =>
    !props.rebuild && !plan.loading && !plan.error && !!plan() && workCount() === 0 && staleCount() === 0;

  const canRun = () => !!docIndex.selectedModel() && !isNoop() && !plan.loading;

  // The breakdown has to describe the same files the headline counts, or it
  // reads as a second, larger answer to the same question.
  const types = createMemo(() => {
    const p = plan();
    if (!p) return [] as { label: string; count: number }[];
    const set = props.rebuild
      ? [{ label: 'text', count: p.text }, { label: 'pdf', count: p.pdf }, { label: 'docx', count: p.docx }]
      : [{ label: 'text', count: p.pendingText }, { label: 'pdf', count: p.pendingPdf }, { label: 'docx', count: p.pendingDocx }];
    return set.filter((t) => t.count > 0);
  });

  // Segment widths for the plan bar. A sliver of work next to a large finished
  // index still gets a visible minimum, since "some" and "none" must not look
  // the same.
  const bar = createMemo(() => {
    const done = doneCount();
    const work = workCount();
    const stale = staleCount();
    const total = Math.max(1, done + work + stale);
    const pct = (n: number) => (n <= 0 ? 0 : Math.max(1.5, (n / total) * 100));
    return { done: pct(done), work: pct(work), stale: pct(stale) };
  });

  const confirm = () => {
    if (canRun()) props.onConfirm();
  };

  // ---- Model radio group -------------------------------------------------

  let list: HTMLDivElement | undefined;
  let filterInput: HTMLInputElement | undefined;

  const step = (delta: number) => {
    const all = visible();
    if (!all.length) return;
    const i = all.findIndex((m) => m.id === docIndex.selectedModel());
    const next = all[i < 0 ? 0 : (i + delta + all.length) % all.length];
    docIndex.selectModel(next.id);
    queueMicrotask(() => {
      const el = list?.querySelector<HTMLElement>(`[data-model="${CSS.escape(next.id)}"]`);
      if (!el || !list) return;
      if (document.activeElement !== filterInput) el.focus({ preventScroll: true });
      revealWithin(list, el, { margin: 4 });
    });
  };

  const focusSelected = () => {
    const el = list?.querySelector<HTMLElement>('[role="radio"][aria-checked="true"]');
    if (!el || !list) return;
    el.focus({ preventScroll: true });
    revealWithin(list, el, { margin: 4 });
  };

  // Enter starts the run only from the list. From the filter it takes the
  // match — typing a model's name and pressing Enter must pick that model,
  // not start a paid run with the one that was selected before.
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.repeat && e.key === 'Enter') return;
    const t = e.target as HTMLElement;
    const inRadio = t.getAttribute('role') === 'radio';
    const inFilter = t === filterInput;
    if ((inRadio || inFilter) && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      e.preventDefault();
      step(e.key === 'ArrowDown' ? 1 : -1);
    } else if (inFilter && e.key === 'Enter') {
      e.preventDefault();
      const shown = visible();
      if (!shown.length) return;
      if (!shown.some((m) => m.id === docIndex.selectedModel())) docIndex.selectModel(shown[0].id);
      queueMicrotask(focusSelected);
    } else if ((inRadio || t.getAttribute('role') === 'dialog') && e.key === 'Enter') {
      e.preventDefault();
      confirm();
    } else if (inRadio && filterInput && e.key.length === 1 && e.key !== ' ' && !e.metaKey && !e.ctrlKey && !e.altKey) {
      // Type-to-filter: the keystroke lands in the filter once it has focus.
      filterInput.focus();
    }
  };

  const ModelRow = (p: { model: ModelInfo }) => {
    const isSel = () => docIndex.selectedModel() === p.model.id;
    return (
      <div
        role="radio"
        aria-checked={isSel()}
        tabindex={isSel() ? 0 : -1}
        data-model={p.model.id}
        onClick={() => docIndex.selectModel(p.model.id)}
        class="group h-8 px-2.5 rounded-lg flex items-center gap-2.5 cursor-pointer select-none outline-none transition-colors focus-visible:shadow-[inset_0_0_0_1px_var(--accent-ring)]"
        classList={{
          'bg-[color:var(--bg-elevated)] shadow-[inset_0_0_0_1px_var(--border-default)]': isSel(),
          'hover:bg-[color:var(--bg-elevated)]/70': !isSel(),
        }}
      >
        <span
          class="w-3.5 h-3.5 rounded-full shrink-0 flex items-center justify-center border transition-colors"
          classList={{
            'border-[color:var(--accent)] bg-[color:var(--accent)]': isSel(),
            'border-[color:var(--border-strong)] group-hover:border-[color:var(--text-muted)]': !isSel(),
          }}
        >
          <Show when={isSel()}>
            <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--on-primary)]" />
          </Show>
        </span>
        <span
          class="flex-1 min-w-0 truncate text-ui"
          classList={{ 'text-[color:var(--text-primary)] font-medium': isSel(), 'text-[color:var(--text-secondary)]': !isSel() }}
        >
          {p.model.name}
        </span>
        <Show when={p.model.default}>
          <span class="shrink-0 text-[0.5625rem] uppercase tracking-wider text-[color:var(--text-muted)]">default</span>
        </Show>
        <span class="shrink-0 w-[5.5rem] text-right text-[0.625rem] font-mono tabular-nums text-[color:var(--text-muted)]">
          <Show when={p.model.inputPricePerM > 0 || p.model.outputPricePerM > 0} fallback={<span class="text-[color:var(--success)]">free</span>}>
            ${price(p.model.inputPricePerM)}<span class="opacity-50"> / </span>${price(p.model.outputPricePerM)}
          </Show>
        </span>
      </div>
    );
  };

  const Legend = (p: { color: string; ring?: boolean; count: number; label: string }) => (
    <span class="inline-flex items-center gap-1.5">
      <span
        class="w-2 h-2 rounded-full shrink-0"
        style={p.ring ? { 'box-shadow': `inset 0 0 0 1.5px ${p.color}` } : { background: p.color }}
      />
      <span class="tabular-nums text-[color:var(--text-secondary)]">{p.count.toLocaleString()}</span>
      {p.label}
    </span>
  );

  return (
    <DialogShell
      labelledBy="idx-run-title"
      describedBy="idx-run-desc"
      onClose={props.onClose}
      class="sm:max-w-[500px]"
      initialFocus={() => list?.querySelector<HTMLElement>('[aria-checked="true"]') ?? filterInput ?? null}
      onKeyDown={onKeyDown}
      returnFocus={props.returnFocus}
    >
      <DialogHeader
        id="idx-run-title"
        descriptionId="idx-run-desc"
        title={props.rebuild ? 'Rebuild index' : 'Index documents'}
        description={props.rebuild
          ? 'Discards the current index and reads every file again with the model you pick.'
          : 'Reads each new file and records what it is about, so agents can find it later.'}
        tone={props.rebuild ? 'warning' : 'accent'}
        onClose={props.onClose}
        icon={
          <Show when={props.rebuild} fallback={
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
            </svg>
          }>
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99" />
            </svg>
          </Show>
        }
      />

      {/* ---- What this run will do ---- */}
      <div class="shrink-0 px-5 py-4 border-b border-[color:var(--border-subtle)]">
        <Show when={!plan.loading} fallback={
          <div class="flex flex-col gap-3" aria-busy="true" aria-label="Scanning the workspace">
            <div class="flex items-center justify-between">
              <div class="skel h-5 w-36" />
              <div class="skel h-4 w-14" />
            </div>
            <div class="skel h-1.5 w-full rounded-full" />
            <div class="skel h-3 w-56" />
          </div>
        }>
          <Show when={!plan.error} fallback={
            <p class="text-meta text-[color:var(--danger)] leading-relaxed">
              Couldn't scan the workspace — the run will work it out as it goes.
            </p>
          }>
            <div class="flex items-baseline gap-2">
              <Show when={!isNoop()} fallback={
                <span class="flex items-center gap-2 text-sm font-semibold text-[color:var(--text-primary)]">
                  <svg class="w-4 h-4 text-[color:var(--success)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                  Up to date
                </span>
              }>
                <span class="text-[1.375rem] font-semibold tabular-nums leading-none tracking-[-0.02em] text-[color:var(--text-primary)]">
                  {workCount().toLocaleString()}
                </span>
                <span class="text-ui text-[color:var(--text-secondary)]">
                  {workCount() === 1 ? 'file' : 'files'} {props.rebuild ? 'to re-read' : 'to index'}
                </span>
              </Show>
              <div class="flex-1" />
              <div class="flex items-center gap-1">
                <For each={types()}>
                  {(t) => (
                    <span class="text-[0.625rem] font-mono tabular-nums text-[color:var(--text-tertiary)] px-1.5 py-0.5 rounded bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)]">
                      {t.count.toLocaleString()} {t.label}
                    </span>
                  )}
                </For>
              </div>
            </div>

            <div class="mt-3 h-1.5 rounded-full bg-[color:var(--bg-elevated)] overflow-hidden flex gap-px" aria-hidden="true">
              <Show when={bar().done > 0}>
                <span class="h-full bg-[color:var(--success)]/55" style={{ width: `${bar().done}%` }} />
              </Show>
              <Show when={bar().work > 0}>
                <span class="h-full bg-[color:var(--accent)]" style={{ width: `${bar().work}%` }} />
              </Show>
              <Show when={bar().stale > 0}>
                <span class="h-full bg-[color:var(--warning)]" style={{ width: `${bar().stale}%` }} />
              </Show>
            </div>

            <div class="mt-2.5 flex flex-wrap items-center gap-x-3.5 gap-y-1 text-micro text-[color:var(--text-tertiary)]">
              <Show when={props.rebuild} fallback={
                <>
                  <Show when={doneCount() > 0}>
                    <Legend color="color-mix(in srgb, var(--success) 55%, transparent)" count={doneCount()} label="already indexed" />
                  </Show>
                  <Show when={workCount() > 0}>
                    <Legend color="var(--accent)" count={workCount()} label="new or changed" />
                  </Show>
                  <Show when={staleCount() > 0}>
                    <Legend color="var(--warning)" count={staleCount()} label={staleCount() === 1 ? 'entry to drop' : 'entries to drop'} />
                  </Show>
                </>
              }>
                <span class="text-[color:var(--warning)]">Every existing entry is deleted first, so this costs the full run again.</span>
              </Show>
            </div>
          </Show>
        </Show>

        <div class="mt-3 flex items-center gap-1.5 text-micro text-[color:var(--text-muted)]">
          <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 3c2.755 0 5.455.232 8.083.678.533.09.917.556.917 1.096v1.044a2.25 2.25 0 01-.659 1.591l-5.432 5.432a2.25 2.25 0 00-.659 1.591v2.927a2.25 2.25 0 01-1.244 2.013L9.75 21v-6.568a2.25 2.25 0 00-.659-1.591L3.659 7.409A2.25 2.25 0 013 5.818V4.774c0-.54.384-1.006.917-1.096A48.32 48.32 0 0112 3z" />
          </svg>
          <span class="truncate">
            Scope: <span class="font-mono">.gitignore</span>
            <Show when={docIndex.excludes().length > 0}>
              {' '}+ {docIndex.excludes().length} {docIndex.excludes().length === 1 ? 'pattern' : 'patterns'}
            </Show>
          </span>
          <button type="button" onClick={props.onOpenScope} class="shrink-0 text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] underline underline-offset-2 decoration-[color:var(--border-strong)]">
            Review
          </button>
        </div>
      </div>

      {/* ---- Model ---- */}
      <div class="flex-1 min-h-0 flex flex-col">
        <div class="shrink-0 px-5 pt-3 pb-2 flex items-center gap-2">
          <h3 id="idx-run-model" class="text-[0.625rem] font-semibold uppercase tracking-[0.06em] text-[color:var(--text-muted)]">Model</h3>
          <div class="flex-1" />
          <span class="text-[0.625rem] text-[color:var(--text-muted)] font-mono">in / out per 1M tokens</span>
        </div>

        <Show when={enabled().length > 7}>
          <div class="shrink-0 px-3 pb-2">
            <div class="relative">
              <svg class="absolute left-2.5 top-1/2 -translate-y-1/2 w-3 h-3 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" />
              </svg>
              <input
                ref={filterInput}
                type="text"
                value={query()}
                onInput={(e) => setQuery(e.currentTarget.value)}
                placeholder={`Filter ${enabled().length} models — just start typing`}
                aria-label="Filter models"
                class="h-8 w-full pl-7 pr-3 rounded-lg text-meta bg-[color:var(--bg-base)] border border-[color:var(--border-subtle)] text-[color:var(--text-primary)] placeholder-[color:var(--text-muted)] focus:outline-none focus:border-[color:var(--border-strong)] transition"
              />
            </div>
          </div>
        </Show>

        <div ref={list} role="radiogroup" aria-labelledby="idx-run-model" class="flex-1 min-h-[120px] overflow-y-auto px-3 pb-2">
          <Show when={enabled().length > 0} fallback={
            <div class="px-3 py-8 text-center text-meta text-[color:var(--text-tertiary)] leading-relaxed">
              No models available.<br />Configure a provider in Settings first.
            </div>
          }>
            <Show when={visible().length > 0} fallback={
              <p class="px-3 py-6 text-center text-meta text-[color:var(--text-muted)]">No model matches “{query()}”</p>
            }>
              <For each={groups()}>
                {(group) => (
                  <div class="mb-1.5 last:mb-0">
                    <div class="flex items-center gap-2 px-2.5 pt-1 pb-1">
                      <span class={`w-1.5 h-1.5 rounded-full ${groupDot(group)}`} />
                      <span class="text-[0.625rem] font-semibold uppercase tracking-[0.06em] text-[color:var(--text-tertiary)]">
                        {groupLabel(group)}
                      </span>
                      <span class="text-[0.625rem] text-[color:var(--text-muted)] tabular-nums">{inGroup(group).length}</span>
                    </div>
                    <div class="flex flex-col gap-px">
                      <For each={inGroup(group)}>{(model) => <ModelRow model={model} />}</For>
                    </div>
                  </div>
                )}
              </For>
            </Show>
          </Show>
        </div>
      </div>

      {/* ---- Footer ---- */}
      <div class="shrink-0 px-5 py-3 border-t border-[color:var(--border-subtle)] bg-[color:var(--bg-base)]/40 flex items-center justify-between gap-3">
        <p class="text-micro text-[color:var(--text-muted)] leading-snug min-w-0 truncate">
          <Show when={selected()} fallback="Runs in the background — you can keep working.">
            {(m) => <>With <span class="text-[color:var(--text-secondary)]">{m().name}</span> · runs in the background</>}
          </Show>
        </p>
        <div class="flex items-center gap-2 shrink-0">
          <button
            type="button"
            onClick={props.onClose}
            class="h-8 px-3 rounded-lg text-meta text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={confirm}
            disabled={!canRun()}
            title={isNoop() ? 'Nothing new to index' : undefined}
            class="h-8 pl-3 pr-2 rounded-lg text-meta font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition flex items-center gap-2 shadow-[var(--shadow-sm)]"
          >
            <Show when={isNoop()} fallback={
              !plan() || plan.loading
                ? (props.rebuild ? 'Rebuild' : 'Index')
                : (props.rebuild ? `Rebuild ${workCount().toLocaleString()}` : `Index ${workCount().toLocaleString()}`)
            }>
              Up to date
            </Show>
            <span class="hidden sm:inline-flex items-center justify-center h-4 min-w-4 px-1 rounded text-[0.625rem] font-mono bg-black/15" aria-hidden="true">↵</span>
          </button>
        </div>
      </div>
    </DialogShell>
  );
}
