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

  // Variant drives color theming across the chip, header icon, and stat card.
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
              class="relative w-full max-w-[440px] bg-[color:var(--bg-surface)] border border-[color:var(--border-default)] rounded-2xl shadow-[0_24px_64px_rgba(0,0,0,0.6)] flex flex-col overflow-hidden max-h-[86vh] animate-scale-in"
              onClick={(e) => e.stopPropagation()}
            >
              {/* ---- Header ---- */}
              <div class="shrink-0 px-5 pt-4 pb-3.5 border-b border-[color:var(--border-subtle)] flex items-start justify-between gap-3">
                <div class="flex items-start gap-2.5 min-w-0">
                  {/* Icon */}
                  <div
                    class="w-7 h-7 rounded-lg flex items-center justify-center shrink-0 mt-0.5"
                    classList={{
                      'bg-[color:color-mix(in_srgb,var(--success)_12%,transparent)]': variant() === 'savings',
                      'bg-[color:color-mix(in_srgb,var(--warning)_12%,transparent)]': variant() === 'overhead',
                      'bg-[color:var(--accent-soft)]': variant() === 'idle',
                    }}
                  >
                    <svg
                      class="w-4 h-4"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke="currentColor"
                      stroke-width="1.8"
                      classList={{
                        'text-[color:var(--success)]': variant() === 'savings',
                        'text-[color:var(--warning)]': variant() === 'overhead',
                        'text-[color:var(--accent)]': variant() === 'idle',
                      }}
                    >
                      <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.847.813a4.5 4.5 0 00-3.09 3.091zM18.25 4.5l-1.5 1.5M18.25 19.5l-1.5-1.5M4.5 18.25l1.5-1.5M19.5 18.25l-1.5-1.5" />
                    </svg>
                  </div>

                  <div class="min-w-0">
                    <h2 class="text-[14px] font-semibold text-[color:var(--text-primary)] leading-tight">
                      Agentic Memory
                    </h2>
                    <p class="text-[11.5px] text-[color:var(--text-tertiary)] mt-1 leading-relaxed">
                      <Show when={hasSavings()}>
                        Summarizes conversation history so only relevant context is sent to the model.
                      </Show>
                      <Show when={!hasSavings() && !hasOverhead()}>
                        Active and will start saving tokens as your conversation grows.
                      </Show>
                      <Show when={hasOverhead()}>
                        Adding a small overhead while it builds up context — savings grow as history expands.
                      </Show>
                    </p>
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() => setOpen(false)}
                  class="w-7 h-7 rounded-lg flex items-center justify-center text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition shrink-0"
                  title="Close"
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </div>

              {/* ---- Stats ---- */}
              <div class="flex-1 overflow-y-auto px-5 py-4">
                {/* Savings highlight card */}
                <Show when={hasSavings()}>
                  <div class="rounded-xl border border-[color:color-mix(in_srgb,var(--success)_18%,var(--border-subtle))] bg-[color:color-mix(in_srgb,var(--success)_6%,transparent)] px-4 py-3.5">
                    <div class="flex items-center justify-between mb-2.5">
                      <span class="text-[10px] uppercase tracking-wider font-semibold text-[color:var(--success)] opacity-80">
                        Tokens saved this session
                      </span>
                      <Show when={costSaved()}>
                        <span class="text-[10px] font-semibold text-[color:var(--success)] bg-[color:color-mix(in_srgb,var(--success)_14%,transparent)] px-2 py-0.5 rounded-full tabular-nums">
                          ~{costSaved()} saved
                        </span>
                      </Show>
                    </div>
                    <div class="flex items-baseline gap-1.5">
                      <span class="text-[26px] font-semibold text-[color:var(--success)] tabular-nums leading-none">
                        {formatTokens(props.savedTokens)}
                      </span>
                      <span class="text-[12px] text-[color:var(--text-tertiary)]">tokens</span>
                    </div>

                    {/* Progress bar showing savings vs total */}
                    <Show when={savingsPercent() > 0}>
                      <div class="mt-3 flex items-center gap-3">
                        <div class="flex-1 h-1.5 rounded-full bg-[color:color-mix(in_srgb,var(--success)_10%,transparent)] overflow-hidden">
                          <div
                            class="h-full rounded-full bg-[color:var(--success)] transition-all duration-500"
                            style={{ width: `${Math.max(savingsPercent(), 2)}%` }}
                          />
                        </div>
                        <span class="text-[10px] text-[color:var(--text-secondary)] font-medium tabular-nums shrink-0">
                          {savingsPercent()}% smaller context
                        </span>
                      </div>
                    </Show>
                  </div>
                </Show>

                <Show when={hasOverhead()}>
                  <div class="rounded-xl border border-[color:color-mix(in_srgb,var(--warning)_18%,var(--border-subtle))] bg-[color:color-mix(in_srgb,var(--warning)_6%,transparent)] px-4 py-3.5">
                    <div class="flex items-baseline gap-1.5">
                      <span class="text-[26px] font-semibold text-[color:var(--warning)] tabular-nums leading-none">
                        {formatTokens(-props.savedTokens)}
                      </span>
                      <span class="text-[12px] text-[color:var(--text-tertiary)]">tokens overhead</span>
                    </div>
                    <p class="text-[11px] text-[color:var(--text-tertiary)] mt-2 leading-relaxed">
                      Memory is indexing your conversation. This overhead decreases as your session grows and savings compound.
                    </p>
                  </div>
                </Show>

                <Show when={!hasSavings() && !hasOverhead()}>
                  <div class="rounded-xl border border-[color:color-mix(in_srgb,var(--accent)_18%,var(--border-subtle))] bg-[color:var(--accent-soft)] px-4 py-3.5">
                    <div class="flex items-center gap-2 mb-1.5">
                      <svg class="w-4 h-4 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                      </svg>
                      <span class="text-[12px] text-[color:var(--accent)] font-medium">Waiting for conversation</span>
                    </div>
                    <p class="text-[11px] text-[color:var(--text-tertiary)] leading-relaxed">
                      Send a message to start building context. Memory will begin saving tokens as the conversation grows.
                    </p>
                  </div>
                </Show>

                {/* Session meta row */}
                <Show when={props.model}>
                  <div class="mt-4 pt-3.5 border-t border-[color:var(--border-subtle)] flex items-center justify-between gap-3">
                    <div class="flex items-center gap-1.5 text-[11px] text-[color:var(--text-tertiary)] min-w-0">
                      <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
                      </svg>
                      <span class="truncate">Model: <span class="text-[color:var(--text-secondary)] font-medium">{getModelLabel(props.model)}</span></span>
                    </div>
                    <div class="flex items-center gap-1.5 text-[11px] text-[color:var(--text-tertiary)] shrink-0">
                      <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M7 8h10M7 12h4m1 8l-4-4H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-3l-4 4z" />
                      </svg>
                      <span class="shrink-0">Session: <span class="text-[color:var(--text-secondary)] font-medium tabular-nums">{formatTokens(props.totalTokens)}</span></span>
                    </div>
                  </div>
                </Show>

                {/* ---- Maintenance ---- */}
                <div class="mt-4 pt-3.5 border-t border-[color:var(--border-subtle)]">
                  <div class="flex items-center gap-1.5 mb-2 text-[10px] uppercase tracking-wider text-[color:var(--text-muted)] font-semibold">
                    <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M11.42 15.17L17.25 21A2.652 2.652 0 0021 17.25l-5.877-5.877M11.42 15.17l-2.496-3.396c-.3-.4-.7-.5-1.1-.5a3.5 3.5 0 11-5 5c0 .4.1.8.5 1.1l3.396 2.496M11.42 15.17l-3.396-2.496M3 21l3-3m6-6l6-6m-3-3l3 3" />
                    </svg>
                    Maintenance
                  </div>
                  <p class="text-[11px] text-[color:var(--text-tertiary)] mb-3 leading-relaxed">
                    Re-embed all stored memory against the current embedding model — run this after switching embedding providers, which invalidates existing vectors. Reset wipes every memory table.
                  </p>

                  {/* Status messages */}
                  <Show when={actionMsg()}>
                    <div class="mb-3 text-[11px] text-[color:var(--success)] bg-[color:color-mix(in_srgb,var(--success)_10%,transparent)] border border-[color:color-mix(in_srgb,var(--success)_20%,transparent)] rounded-lg px-2.5 py-1.5">
                      {actionMsg()}
                    </div>
                  </Show>
                  <Show when={actionErr()}>
                    <div class="mb-3 text-[11px] text-[color:var(--danger)] bg-[color:color-mix(in_srgb,var(--danger)_10%,transparent)] border border-[color:color-mix(in_srgb,var(--danger)_20%,transparent)] rounded-lg px-2.5 py-1.5 break-words">
                      {actionErr()}
                    </div>
                  </Show>

                  <div class="flex flex-wrap items-center gap-2">
                    {/* Re-embed all memory */}
                    <button
                      type="button"
                      onClick={runReindex}
                      disabled={busy() !== null}
                      class="flex items-center gap-1.5 text-[11px] px-2.5 py-1.5 rounded-lg border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] text-[color:var(--text-secondary)] hover:bg-[color:var(--bg-hover)] hover:text-[color:var(--text-primary)] disabled:opacity-50 disabled:cursor-not-allowed transition"
                    >
                      <Show when={busy() === 'reindex'}
                        fallback={
                          <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M16.023 9.348h4.992V4.356M2.985 19.644v-4.992h4.992m0 0L3 21l4.992-4.992M21 3l-4.992 4.992M21 3v4.992h-4.992" />
                          </svg>
                        }
                      >
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
                        class="flex items-center gap-1.5 text-[11px] px-2.5 py-1.5 rounded-lg border border-[color:color-mix(in_srgb,var(--danger)_30%,var(--border-default))] bg-[color:color-mix(in_srgb,var(--danger)_10%,transparent)] text-[color:var(--danger)] hover:bg-[color:color-mix(in_srgb,var(--danger)_16%,transparent)] disabled:opacity-50 disabled:cursor-not-allowed transition"
                      >
                        <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                          <path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                        </svg>
                        Reset memory
                      </button>
                    </Show>
                    <Show when={confirmReset()}>
                      <span class="text-[11px] text-[color:var(--danger)]">Erase everything?</span>
                      <button
                        type="button"
                        onClick={runReset}
                        disabled={busy() !== null}
                        class="flex items-center gap-1.5 text-[11px] px-2.5 py-1.5 rounded-lg border border-[color:color-mix(in_srgb,var(--danger)_50%,var(--border-default))] bg-[color:color-mix(in_srgb,var(--danger)_30%,transparent)] text-[color:var(--on-primary)] hover:bg-[color:color-mix(in_srgb,var(--danger)_40%,transparent)] disabled:opacity-50 disabled:cursor-not-allowed transition"
                      >
                        <Show when={busy() === 'reset'}>
                          <svg class="w-3 h-3 animate-spin" fill="none" viewBox="0 0 24 24">
                            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
                            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v2a6 6 0 00-6 6H4z" />
                          </svg>
                        </Show>
                        Yes, erase
                      </button>
                      <button
                        type="button"
                        onClick={() => setConfirmReset(false)}
                        disabled={busy() !== null}
                        class="text-[11px] px-2.5 py-1.5 rounded-lg border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] text-[color:var(--text-secondary)] hover:bg-[color:var(--bg-hover)] disabled:opacity-50 disabled:cursor-not-allowed transition"
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