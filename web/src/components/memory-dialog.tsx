import { createSignal, Show, createMemo, onCleanup, onMount } from 'solid-js';
import { Portal } from 'solid-js/web';
import { reindexMemory, resetMemory } from '../api/client';

interface MemoryDialogProps {
  savedTokens: number;
  totalTokens: number;
  model: string;
  dynamicPrices: Record<string, number>;
  models: { id: string; inputPricePerM: number; outputPricePerM: number }[];
}

function formatTokens(n: number): string {
  if (n === 0) return '0';
  const abs = Math.abs(n);
  if (abs < 1_000) return n.toString();
  if (abs < 10_000) return (n / 1000).toFixed(2).replace(/\.?0+$/, '') + 'K';
  if (abs < 1_000_000) return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'K';
  return (n / 1_000_000).toFixed(2).replace(/\.?0+$/, '') + 'M';
}

function estimateCost(tokens: number, model: string, dynamicPrices: Record<string, number>, models: { id: string; inputPricePerM: number }[]): string | null {
  const base = model.split('/').pop() ?? model;
  const dynPrice = dynamicPrices[model] ?? dynamicPrices[base];
  if (dynPrice) {
    const cost = (tokens / 1_000_000) * dynPrice;
    if (cost < 0.0001) return null;
    return cost < 0.01 ? `$${cost.toFixed(4)}` : `$${cost.toFixed(3)}`;
  }
  const info = models.find((m) => m.id === model) ?? models.find((m) => m.id === base);
  if (info && info.inputPricePerM > 0) {
    const cost = (tokens / 1_000_000) * info.inputPricePerM;
    if (cost < 0.0001) return null;
    return cost < 0.01 ? `$${cost.toFixed(4)}` : `$${cost.toFixed(3)}`;
  }
  return null;
}

function getModelLabel(model: string | undefined): string {
  if (!model) return '';
  const parts = model.split('/');
  const name = parts[parts.length - 1];
  return name.replace(/-\d{4}-\d{2}-\d{2}$/, '').replace(/-preview$/, '');
}

export default function MemoryDialog(props: MemoryDialogProps) {
  const [open, setOpen] = createSignal(false);

  const savingsPercent = createMemo(() => {
    const total = props.totalTokens;
    const saved = props.savedTokens;
    if (total <= 0 || saved <= 0) return 0;
    return Math.min(Math.round((saved / (total + saved)) * 100), 100);
  });

  const costSaved = createMemo(() => {
    if (props.savedTokens <= 0) return null;
    return estimateCost(props.savedTokens, props.model, props.dynamicPrices, props.models);
  });

  const hasSavings = () => props.savedTokens > 0;
  const hasOverhead = () => props.savedTokens < 0;

  // Maintenance actions: re-embed all stored memory against the current
  // embedding model (use after switching providers), and wipe everything.
  const [busy, setBusy] = createSignal<null | 'reindex' | 'reset'>(null);
  const [actionMsg, setActionMsg] = createSignal<string | null>(null);
  const [actionErr, setActionErr] = createSignal<string | null>(null);
  const [confirmReset, setConfirmReset] = createSignal(false);

  const runReindex = async () => {
    setBusy('reindex');
    setActionErr(null);
    setActionMsg(null);
    try {
      await reindexMemory();
      setActionMsg('Re-embedded all memory against the current model.');
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const runReset = async () => {
    setBusy('reset');
    setActionErr(null);
    setActionMsg(null);
    try {
      await resetMemory();
      setConfirmReset(false);
      setActionMsg('Memory store cleared.');
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && open()) {
      setOpen(false);
    }
  };

  onMount(() => {
    document.addEventListener('keydown', handleKeyDown);
  });

  onCleanup(() => {
    document.removeEventListener('keydown', handleKeyDown);
  });

  // Variant drives the color theming of the header chip.
  const variant = () => hasSavings() ? 'savings' : hasOverhead() ? 'overhead' : 'idle';

  return (
    <>
      {/* The clickable chip in the header */}
      <button
        type="button"
        onClick={() => setOpen(true)}
        title={(() => {
          const t = props.savedTokens;
          if (t > 0) return `Memory is saving ~${formatTokens(t)} tokens — click for details`;
          if (t < 0) return `Memory is adding ~${formatTokens(-t)} tokens of overhead — click for details`;
          return 'Agentic memory active — click for details';
        })()}
        class="group flex items-center gap-1.5 h-7 px-2 rounded-md border font-medium cursor-pointer transition-colors select-none"
        classList={{
          'border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] text-[color:var(--text-secondary)] hover:bg-[color:var(--bg-hover)]': variant() === 'idle',
          'border-[color:color-mix(in_srgb,var(--success)_25%,var(--border-default))] bg-[color:color-mix(in_srgb,var(--success)_8%,transparent)] text-[color:var(--success)] hover:bg-[color:color-mix(in_srgb,var(--success)_12%,transparent)]': variant() === 'savings',
          'border-[color:color-mix(in_srgb,var(--warning)_25%,var(--border-default))] bg-[color:color-mix(in_srgb,var(--warning)_8%,transparent)] text-[color:var(--warning)] hover:bg-[color:color-mix(in_srgb,var(--warning)_12%,transparent)]': variant() === 'overhead',
        }}
      >
        <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.847.813a4.5 4.5 0 00-3.09 3.091z" />
        </svg>
        <span class="text-micro">Memory</span>
        <Show when={hasSavings()}>
          <span class="text-micro opacity-50">·</span>
          <span class="text-micro tabular-nums">~{formatTokens(props.savedTokens)} saved</span>
        </Show>
        <Show when={hasOverhead()}>
          <span class="text-micro opacity-50">·</span>
          <span class="text-micro tabular-nums">~{formatTokens(-props.savedTokens)} overhead</span>
        </Show>
      </button>

      {/* Dialog overlay — portaled to <body> so `position: fixed` anchors to the
          viewport. Without this it is contained by the header's `backdrop-blur`
          ancestor and renders off-center with its top (and close button) cut off. */}
      <Show when={open()}>
        <Portal>
          <div
            class="fixed inset-0 z-[200] bg-black/60 backdrop-blur-[2px] flex items-center justify-center p-4 modal-backdrop"
            onClick={(e) => { if (e.target === e.currentTarget) setOpen(false); }}
          >
            <div
              role="dialog"
              aria-modal="true"
              aria-label="Agentic memory"
              class="relative w-full max-w-[420px] bg-[color:var(--bg-surface)] border border-[color:var(--border-default)] rounded-2xl shadow-[0_24px_64px_rgba(0,0,0,0.6)] flex flex-col overflow-hidden max-h-[86vh] animate-scale-in"
              onClick={(e) => e.stopPropagation()}
            >
              {/* ---- Header ---- */}
              <div class="shrink-0 px-5 pt-4 pb-3.5 border-b border-[color:var(--border-subtle)] flex items-start justify-between gap-3">
                <div class="flex items-start gap-3 min-w-0">
                  <div class="w-7 h-7 rounded-lg bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] flex items-center justify-center shrink-0 mt-0.5">
                    <svg class="w-3.5 h-3.5 text-[color:var(--text-secondary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.847.813a4.5 4.5 0 00-3.09 3.091zM18.25 4.5l-1.5 1.5M18.25 19.5l-1.5-1.5M4.5 18.25l1.5-1.5M19.5 18.25l-1.5-1.5" />
                    </svg>
                  </div>
                  <div class="min-w-0">
                    <h2 class="text-[13.5px] font-semibold text-[color:var(--text-primary)] leading-tight">
                      Memory
                    </h2>
                    <p class="text-[11.5px] text-[color:var(--text-tertiary)] mt-1 leading-relaxed">
                      Summarizes conversation history so only relevant context is sent to the model.
                    </p>
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() => setOpen(false)}
                  class="w-7 h-7 rounded-lg flex items-center justify-center text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition shrink-0"
                  title="Close"
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </div>

              {/* ---- Body ---- */}
              <div class="flex-1 overflow-y-auto px-5 py-4">
                {/* Savings — quiet hero number with a colored status dot */}
                <Show when={hasSavings()}>
                  <div>
                    <div class="flex items-center gap-1.5 mb-2.5">
                      <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--success)] shrink-0" />
                      <span class="text-[10px] font-medium uppercase tracking-[0.08em] text-[color:var(--text-muted)]">
                        Saved this session
                      </span>
                      <span class="flex-1" />
                      <Show when={costSaved()}>
                        <span
                          class="text-[10.5px] font-medium text-[color:var(--success)] tabular-nums"
                          title="Estimated input-token cost saved at current model pricing"
                        >
                          ≈ {costSaved()}
                        </span>
                      </Show>
                    </div>
                    <div class="flex items-baseline gap-2">
                      <span class="text-[30px] font-semibold tracking-tight text-[color:var(--text-primary)] tabular-nums leading-none">
                        {formatTokens(props.savedTokens)}
                      </span>
                      <span class="text-[12px] text-[color:var(--text-tertiary)]">tokens</span>
                    </div>
                    <Show when={savingsPercent() > 0}>
                      <div class="mt-4 flex items-center gap-3">
                        <div class="flex-1 h-[3px] rounded-full bg-[color:var(--border-subtle)] overflow-hidden">
                          <div
                            class="h-full rounded-full bg-[color:var(--success)] transition-all duration-500"
                            style={{ width: `${Math.max(savingsPercent(), 2)}%` }}
                          />
                        </div>
                        <span class="text-[10.5px] text-[color:var(--text-tertiary)] tabular-nums shrink-0">
                          {savingsPercent()}% smaller context
                        </span>
                      </div>
                    </Show>
                  </div>
                </Show>

                {/* Overhead — same quiet treatment with a warning dot */}
                <Show when={hasOverhead()}>
                  <div>
                    <div class="flex items-center gap-1.5 mb-2.5">
                      <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--warning)] shrink-0" />
                      <span class="text-[10px] font-medium uppercase tracking-[0.08em] text-[color:var(--text-muted)]">
                        Overhead
                      </span>
                    </div>
                    <div class="flex items-baseline gap-2">
                      <span class="text-[30px] font-semibold tracking-tight text-[color:var(--text-primary)] tabular-nums leading-none">
                        {formatTokens(-props.savedTokens)}
                      </span>
                      <span class="text-[12px] text-[color:var(--text-tertiary)]">tokens</span>
                    </div>
                    <p class="text-[11px] text-[color:var(--text-tertiary)] mt-2.5 leading-relaxed">
                      Memory is indexing your conversation. This decreases as the session grows and savings compound.
                    </p>
                  </div>
                </Show>

                {/* Idle — no numbers yet */}
                <Show when={!hasSavings() && !hasOverhead()}>
                  <div>
                    <div class="flex items-center gap-1.5 mb-2.5">
                      <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] shrink-0" />
                      <span class="text-[10px] font-medium uppercase tracking-[0.08em] text-[color:var(--text-muted)]">
                        Waiting for conversation
                      </span>
                    </div>
                    <p class="text-[11.5px] text-[color:var(--text-tertiary)] leading-relaxed">
                      Send a message to start building context — savings appear here as the conversation grows.
                    </p>
                  </div>
                </Show>

                {/* ---- Session ---- */}
                <div class="mt-5 pt-4 border-t border-[color:var(--border-subtle)] flex flex-col gap-2.5">
                  <Show when={props.model}>
                    <div class="flex items-center justify-between gap-3">
                      <span class="text-[11px] text-[color:var(--text-tertiary)]">Model</span>
                      <span class="text-[10.5px] font-medium text-[color:var(--text-secondary)] font-mono truncate">
                        {getModelLabel(props.model)}
                      </span>
                    </div>
                  </Show>
                  <div class="flex items-center justify-between gap-3">
                    <span class="text-[11px] text-[color:var(--text-tertiary)]">Session</span>
                    <span class="text-[11px] font-medium text-[color:var(--text-secondary)] tabular-nums">
                      {formatTokens(props.totalTokens)} tokens
                    </span>
                  </div>
                </div>

                {/* ---- Maintenance ---- */}
                <div class="mt-5 pt-4 border-t border-[color:var(--border-subtle)]">
                  <div class="text-[10px] font-medium uppercase tracking-[0.08em] text-[color:var(--text-muted)] mb-2">
                    Maintenance
                  </div>
                  <p class="text-[11px] text-[color:var(--text-tertiary)] leading-relaxed mb-3">
                    Re-embed all stored memory after switching embedding providers. Reset wipes everything.
                  </p>

                  {/* Status messages */}
                  <Show when={actionMsg()}>
                    <div class="mb-3 flex items-start gap-1.5 text-[11px] text-[color:var(--success)]">
                      <span class="mt-[5px] w-1 h-1 rounded-full bg-current shrink-0" />
                      <span>{actionMsg()}</span>
                    </div>
                  </Show>
                  <Show when={actionErr()}>
                    <div class="mb-3 flex items-start gap-1.5 text-[11px] text-[color:var(--danger)] break-words">
                      <span class="mt-[5px] w-1 h-1 rounded-full bg-current shrink-0" />
                      <span>{actionErr()}</span>
                    </div>
                  </Show>

                  <div class="flex flex-wrap items-center gap-2">
                    {/* Re-embed all memory */}
                    <button
                      type="button"
                      onClick={runReindex}
                      disabled={busy() !== null}
                      class="flex items-center gap-1.5 h-7 px-2.5 rounded-md border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] text-[11px] font-medium text-[color:var(--text-secondary)] hover:bg-[color:var(--bg-hover)] hover:text-[color:var(--text-primary)] disabled:opacity-50 disabled:cursor-not-allowed transition"
                    >
                      <Show when={busy() === 'reindex'}>
                        <svg class="w-3 h-3 animate-spin" fill="none" viewBox="0 0 24 24">
                          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
                          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v2a6 6 0 00-6 6H4z" />
                        </svg>
                      </Show>
                      {busy() === 'reindex' ? 'Re-embedding…' : 'Re-embed all memory'}
                    </button>

                    {/* Reset memory (two-step confirm) */}
                    <Show when={!confirmReset()}>
                      <button
                        type="button"
                        onClick={() => { setConfirmReset(true); setActionErr(null); setActionMsg(null); }}
                        disabled={busy() !== null}
                        class="ml-auto flex items-center h-7 px-2.5 rounded-md border border-transparent text-[11px] font-medium text-[color:var(--danger)] hover:bg-[color:color-mix(in_srgb,var(--danger)_10%,transparent)] disabled:opacity-50 disabled:cursor-not-allowed transition"
                      >
                        Reset memory
                      </button>
                    </Show>
                    <Show when={confirmReset()}>
                      <span class="ml-auto text-[11px] text-[color:var(--danger)]">Erase everything?</span>
                      <button
                        type="button"
                        onClick={runReset}
                        disabled={busy() !== null}
                        class="flex items-center gap-1.5 h-7 px-2.5 rounded-md bg-[color:var(--danger)] text-[color:var(--on-primary)] text-[11px] font-medium hover:opacity-90 disabled:opacity-50 disabled:cursor-not-allowed transition"
                      >
                        <Show when={busy() === 'reset'}>
                          <svg class="w-3 h-3 animate-spin" fill="none" viewBox="0 0 24 24">
                            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
                            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v2a6 6 0 00-6 6H4z" />
                          </svg>
                        </Show>
                        {busy() === 'reset' ? 'Erasing…' : 'Yes, erase'}
                      </button>
                      <button
                        type="button"
                        onClick={() => setConfirmReset(false)}
                        disabled={busy() !== null}
                        class="flex items-center h-7 px-2.5 rounded-md border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] text-[11px] font-medium text-[color:var(--text-secondary)] hover:bg-[color:var(--bg-hover)] disabled:opacity-50 disabled:cursor-not-allowed transition"
                      >
                        Cancel
                      </button>
                    </Show>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </Portal>
      </Show>
    </>
  );
}