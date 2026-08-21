import { createResource, onMount, onCleanup, createMemo, Show, For } from 'solid-js';
import { useServer } from '../context/server';
import { useDocIndex } from '../context/docindex';
import { getIndexPlan, type ModelInfo } from '../api/client';
import { modelGroup, subProviderLabel } from '../lib/providers';

/**
 * The run dialog is where indexing gets paid for, so it opens by saying what it
 * will cost: how many files, of what kind, and which of them are already done.
 * The old modal asked only which model to use, which put the expensive question
 * — is this run worth starting — somewhere the person clicking could not see it.
 */

// Models group the way they do everywhere else in the app — by collection
// where there is one — so the free pool reads as "ogcode" rather than leaking
// raw ids like "ogcode-openrouter" into a heading.
const GROUP_LABEL: Record<string, string> = {
  anthropic: 'Anthropic', openai: 'OpenAI', openrouter: 'OpenRouter',
  google: 'Google', mistral: 'Mistral', ollama: 'Ollama', ogcode: 'ogcode free pool',
};

const GROUP_COLOR: Record<string, string> = {
  anthropic: '#fb923c', openai: '#34d399', openrouter: '#a78bfa',
  google: '#60a5fa', mistral: '#f43f5e', ollama: '#14b8a6', ogcode: '#34d399',
};

const price = (n: number) => (n % 1 === 0 ? String(n) : n.toFixed(2));

export default function IndexRunDialog(props: {
  rebuild: boolean;
  onClose: () => void;
  onConfirm: () => void;
  onOpenScope: () => void;
}) {
  const server = useServer();
  const docIndex = useDocIndex();

  const [plan] = createResource(
    () => server.directory() || '',
    (dir) => getIndexPlan(dir || undefined),
  );

  onMount(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') props.onClose(); };
    window.addEventListener('keydown', onKey);
    onCleanup(() => window.removeEventListener('keydown', onKey));
  });

  const enabled = createMemo(() => docIndex.models().filter((m) => m.enabled));
  const groups = createMemo(() => [...new Set(enabled().map((m) => modelGroup(m)))]);
  const inGroup = (group: string) => enabled().filter((m) => modelGroup(m) === group);

  // A rebuild re-reads everything; an incremental run only touches what is not
  // in the index yet. Naming the right number is the whole point of the panel.
  const workCount = () => (props.rebuild ? plan()?.total ?? 0 : plan()?.pending ?? 0);
  const staleCount = () => plan()?.stale ?? 0;

  // With nothing new to index and nothing stale to drop, the run would walk the
  // tree and do nothing. Saying so beats letting someone spend a minute finding
  // out.
  const isNoop = () =>
    !props.rebuild && !plan.loading && !plan.error && !!plan() && workCount() === 0 && staleCount() === 0;

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

  return (
    <div
      class="fixed inset-0 z-50 bg-black/60 backdrop-blur-[2px] flex items-center justify-center p-4 modal-backdrop"
      onClick={(e) => { if (e.target === e.currentTarget) props.onClose(); }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label={props.rebuild ? 'Rebuild index' : 'Index documents'}
        class="w-full max-w-[520px] bg-[color:var(--bg-surface)] border border-[color:var(--border-default)] rounded-2xl shadow-[0_24px_64px_rgba(0,0,0,0.6)] flex flex-col overflow-hidden max-h-[84vh] animate-scale-in"
      >
        {/* ---- Header ---- */}
        <div class="shrink-0 px-5 pt-4 pb-3.5 border-b border-[color:var(--border-subtle)] flex items-start justify-between gap-3">
          <div class="flex items-start gap-2.5 min-w-0">
            <div
              class="w-7 h-7 rounded-lg flex items-center justify-center shrink-0 mt-0.5"
              classList={{
                'bg-[color:var(--warning)]/[0.12]': props.rebuild,
                'bg-[color:var(--accent-soft)]': !props.rebuild,
              }}
            >
              <Show when={props.rebuild} fallback={
                <svg class="w-3.5 h-3.5 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
                </svg>
              }>
                <svg class="w-4 h-4 text-[color:var(--warning)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 12c0-1.232-.046-2.453-.138-3.662a4.006 4.006 0 00-3.7-3.7 48.678 48.678 0 00-7.324 0 4.006 4.006 0 00-3.7 3.7c-.017.22-.032.441-.046.662M19.5 12l3-3m-3 3l-3-3m-12 3c0 1.232.046 2.453.138 3.662a4.006 4.006 0 003.7 3.7 48.656 48.656 0 007.324 0 4.006 4.006 0 003.7-3.7c.017-.22.032-.441.046-.662M4.5 12l3 3m-3-3l-3 3" />
                </svg>
              </Show>
            </div>
            <div class="min-w-0">
              <h2 class="text-[14px] font-semibold text-[color:var(--text-primary)] leading-tight">
                {props.rebuild ? 'Rebuild index' : 'Index documents'}
              </h2>
              <p class="text-[11.5px] text-[color:var(--text-tertiary)] mt-1 leading-relaxed">
                {props.rebuild
                  ? 'Discards the current index and reads every file again with the model you pick.'
                  : 'Reads each new file and records what it is about, so agents can find it later.'}
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

        {/* ---- What this run will do ---- */}
        <div class="shrink-0 px-5 py-3.5 border-b border-[color:var(--border-subtle)]">
          <Show when={!plan.loading} fallback={
            <div class="h-[58px] rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] flex items-center justify-center gap-2 text-[11.5px] text-[color:var(--text-tertiary)]">
              <div class="w-3.5 h-3.5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
              Scanning the workspace…
            </div>
          }>
            <Show when={!plan.error} fallback={
              <p class="text-[11.5px] text-[color:var(--danger)]">Couldn't scan the workspace — the run will work it out as it goes.</p>
            }>
              <div class="rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] px-3.5 py-3">
                <div class="flex items-baseline gap-2">
                  <span class="text-[22px] font-semibold tabular-nums leading-none text-[color:var(--text-primary)]">
                    {workCount()}
                  </span>
                  <span class="text-[12px] text-[color:var(--text-secondary)]">
                    {workCount() === 1 ? 'file' : 'files'} {props.rebuild ? 'to re-analyze' : 'to index'}
                  </span>
                  <div class="flex-1" />
                  <For each={types()}>
                    {(t) => (
                      <span class="text-[10px] font-mono tabular-nums text-[color:var(--text-muted)] px-1.5 py-0.5 rounded bg-[color:var(--bg-base)]">
                        {t.count} {t.label}
                      </span>
                    )}
                  </For>
                </div>

                <p class="text-[11px] text-[color:var(--text-tertiary)] mt-2 leading-relaxed">
                  <Show when={props.rebuild} fallback={
                    <>
                      <Show when={(plan()?.indexed ?? 0) > 0}>
                        {plan()!.indexed} already indexed and skipped.{' '}
                      </Show>
                      <Show when={staleCount() > 0}>
                        {staleCount()} {staleCount() === 1 ? 'entry' : 'entries'} for deleted files will be dropped.{' '}
                      </Show>
                      <Show when={isNoop()}>
                        Nothing on disk has changed since the last run.{' '}
                      </Show>
                    </>
                  }>
                    Every existing entry is deleted first, so this costs the full run again.{' '}
                  </Show>
                  <button
                    onClick={props.onOpenScope}
                    class="text-[color:var(--accent)] hover:underline"
                  >
                    Scope is set by .gitignore
                  </button>
                </p>
              </div>
            </Show>
          </Show>
        </div>

        {/* ---- Model ---- */}
        <div class="flex-1 min-h-0 flex flex-col">
          <div class="shrink-0 px-5 pt-3 pb-1.5 flex items-center justify-between gap-2">
            <h3 class="text-[10px] font-semibold uppercase tracking-wider text-[color:var(--text-muted)]">Model</h3>
            <span class="text-[10px] text-[color:var(--text-muted)] font-mono">in / out per 1M</span>
          </div>

          <div class="flex-1 overflow-y-auto px-3 pb-2">
            <Show when={enabled().length > 0} fallback={
              <div class="px-3 py-8 text-center text-[11.5px] text-[color:var(--text-tertiary)] leading-relaxed">
                No models available.<br />Configure a provider in Settings first.
              </div>
            }>
              <For each={groups()}>
                {(group) => (
                  <div class="mb-1">
                    <div class="flex items-center gap-2 px-2 py-1.5">
                      <span class="w-1.5 h-1.5 rounded-full" style={{ background: GROUP_COLOR[group] || '#71717a' }} />
                      <span
                        class="text-[9.5px] font-semibold uppercase tracking-wider"
                        style={{ color: GROUP_COLOR[group] || '#71717a' }}
                      >
                        {GROUP_LABEL[group] || group}
                      </span>
                      <span class="text-[9.5px] text-[color:var(--text-muted)] tabular-nums">{inGroup(group).length}</span>
                    </div>
                    <For each={inGroup(group)}>
                      {(model) => {
                        const isSel = () => docIndex.selectedModel() === model.id;
                        return (
                          <button
                            onClick={() => docIndex.selectModel(model.id)}
                            class="w-full text-left px-2.5 py-2 rounded-lg mb-0.5 flex items-center justify-between gap-3 transition-colors"
                            classList={{
                              'bg-[color:var(--accent-soft)] text-[color:var(--accent)]': isSel(),
                              'text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)]': !isSel(),
                            }}
                          >
                            <div class="flex items-center gap-2.5 min-w-0">
                              <div
                                class="w-4 h-4 rounded-full border-2 flex items-center justify-center shrink-0"
                                classList={{
                                  'border-[color:var(--accent)] bg-[color:var(--accent)]': isSel(),
                                  'border-[color:var(--border-strong)]': !isSel(),
                                }}
                              >
                                <Show when={isSel()}>
                                  <svg class="w-2.5 h-2.5 text-white" fill="currentColor" viewBox="0 0 24 24">
                                    <path d="M9 16.17L4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41L9 16.17z" />
                                  </svg>
                                </Show>
                              </div>
                              <span class="text-[12.5px] font-medium truncate">{model.name}</span>
                              <Show when={subProviderLabel(model as ModelInfo)}>
                                {(label) => (
                                  <span class="text-[9px] px-1 py-0.5 rounded bg-[color:var(--bg-base)] text-[color:var(--text-muted)] shrink-0">
                                    {label()}
                                  </span>
                                )}
                              </Show>
                              <Show when={model.default}>
                                <span class="text-[9px] text-[color:var(--text-muted)] uppercase tracking-wider shrink-0">default</span>
                              </Show>
                            </div>
                            <span class="text-[10px] font-mono tabular-nums shrink-0 text-[color:var(--text-muted)]">
                              <Show when={model.inputPricePerM > 0} fallback={<span class="text-[color:var(--success)]">free</span>}>
                                ${price(model.inputPricePerM)}<span class="opacity-50">/</span>${price(model.outputPricePerM)}
                              </Show>
                            </span>
                          </button>
                        );
                      }}
                    </For>
                  </div>
                )}
              </For>
            </Show>
          </div>
        </div>

        {/* ---- Footer ---- */}
        <div class="shrink-0 px-5 py-3 border-t border-[color:var(--border-subtle)] flex items-center justify-between gap-3">
          <p class="text-[11px] text-[color:var(--text-muted)] leading-snug min-w-0 truncate">
            Runs in the background — you can keep working.
          </p>
          <div class="flex items-center gap-2 shrink-0">
            <button
              onClick={props.onClose}
              class="h-8 px-3.5 rounded-lg text-[12px] text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition"
            >
              Cancel
            </button>
            <button
              onClick={props.onConfirm}
              disabled={!docIndex.selectedModel() || isNoop()}
              title={isNoop() ? 'Nothing new to index' : undefined}
              class="h-8 px-3.5 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition flex items-center gap-1.5"
            >
              <Show when={props.rebuild} fallback={
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
                </svg>
              }>
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 12c0-1.232-.046-2.453-.138-3.662a4.006 4.006 0 00-3.7-3.7 48.678 48.678 0 00-7.324 0 4.006 4.006 0 00-3.7 3.7c-.017.22-.032.441-.046.662M19.5 12l3-3m-3 3l-3-3m-12 3c0 1.232.046 2.453.138 3.662a4.006 4.006 0 003.7 3.7 48.656 48.656 0 007.324 0 4.006 4.006 0 003.7-3.7c.017-.22.032-.441.046-.662M4.5 12l3 3m-3-3l-3 3" />
                </svg>
              </Show>
              <Show when={isNoop()} fallback={
                !plan()
                  ? (props.rebuild ? 'Rebuild' : 'Index')
                  : (props.rebuild ? `Rebuild ${workCount()}` : `Index ${workCount()}`)
              }>
                Up to date
              </Show>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
