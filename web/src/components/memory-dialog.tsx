import { createSignal, Show, createMemo, onCleanup } from 'solid-js';

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

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && open()) {
      setOpen(false);
    }
  };

  // Listen for Escape on document when open
  createMemo(() => {
    if (open()) {
      document.addEventListener('keydown', handleKeyDown);
    } else {
      document.removeEventListener('keydown', handleKeyDown);
    }
  });

  onCleanup(() => {
    document.removeEventListener('keydown', handleKeyDown);
  });

  return (
    <>
      {/* The clickable chip */}
      <button
        type="button"
        onClick={() => setOpen(true)}
        title={(() => {
          const t = props.savedTokens;
          if (t > 0) return `Memory is saving ~${formatTokens(t)} tokens — click for details`;
          if (t < 0) return `Memory is adding ~${formatTokens(-t)} tokens of overhead — click for details`;
          return 'Agentic memory active — click for details';
        })()}
        class={`flex items-center gap-1.5 text-[11px] px-2 py-1 rounded-md border font-medium cursor-pointer transition-opacity hover:opacity-90
          ${props.savedTokens > 0
            ? 'text-emerald-400 bg-emerald-400/10 border-emerald-400/20'
            : props.savedTokens < 0
            ? 'text-amber-400 bg-amber-400/10 border-amber-400/20'
            : 'text-zinc-400 bg-zinc-400/10 border-zinc-400/20'
          }`}
      >
        <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.847.813a4.5 4.5 0 00-3.09 3.091z" />
        </svg>
        Memory
        <Show when={hasSavings()}>
          <span class="text-emerald-500/70">·</span>
          <span class="text-emerald-300">~{formatTokens(props.savedTokens)} saved</span>
        </Show>
        <Show when={hasOverhead()}>
          <span class="text-amber-500/70">·</span>
          <span class="text-amber-300">~{formatTokens(-props.savedTokens)} overhead</span>
        </Show>
      </button>

      {/* Dialog overlay */}
      <Show when={open()}>
        <div
          class="fixed inset-0 z-[200] flex items-center justify-center"
          onClick={() => setOpen(false)}
        >
          {/* Backdrop */}
          <div class="absolute inset-0 bg-black/50 backdrop-blur-sm" />

          {/* Dialog */}
          <div
            class="relative w-full max-w-md mx-4 bg-[color:var(--bg-overlay)] border border-[color:var(--border-default)] rounded-xl shadow-2xl overflow-hidden"
            style={{ animation: 'popIn 200ms cubic-bezier(0.16, 1, 0.3, 1) forwards' }}
            onClick={(e) => e.stopPropagation()}
          >
            {/* Close button */}
            <button
              type="button"
              onClick={() => setOpen(false)}
              class="absolute top-3 right-3 w-7 h-7 flex items-center justify-center rounded-md text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)] transition"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>

            {/* Header section with gradient */}
            <div class="px-6 pt-6 pb-4">
              <div class="flex items-start gap-4">
                {/* Icon */}
                <div class={`w-12 h-12 rounded-xl flex items-center justify-center shrink-0 ${hasSavings() ? 'bg-emerald-500/15 border border-emerald-500/20' : hasOverhead() ? 'bg-amber-500/15 border border-amber-500/20' : 'bg-violet-500/15 border border-violet-500/20'}`}>
                  <svg class={`w-6 h-6 ${hasSavings() ? 'text-emerald-400' : hasOverhead() ? 'text-amber-400' : 'text-violet-400'}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.847.813a4.5 4.5 0 00-3.09 3.091zM18.25 4.5l-1.5 1.5M18.25 19.5l-1.5-1.5M4.5 18.25l1.5-1.5M19.5 18.25l-1.5-1.5" />
                  </svg>
                </div>

                <div class="flex-1 min-w-0">
                  <h3 class="text-[15px] font-semibold text-zinc-100 mb-0.5">Agentic Memory</h3>
                  <p class="text-[12px] text-zinc-500 leading-relaxed">
                    <Show when={hasSavings()}>
                      Memory summarizes conversation history so only relevant context is sent to the model.
                    </Show>
                    <Show when={!hasSavings() && !hasOverhead()}>
                      Memory is active and will start saving tokens as your conversation grows.
                    </Show>
                    <Show when={hasOverhead()}>
                      Memory is adding a small overhead while it builds up context — savings grow as history expands.
                    </Show>
                  </p>
                </div>
              </div>
            </div>

            {/* Stats section */}
            <div class="px-6 pb-5">
              {/* Savings highlight card */}
              <Show when={hasSavings()}>
                <div class="rounded-lg bg-emerald-500/8 border border-emerald-500/15 p-4 mb-4">
                  <div class="flex items-center justify-between mb-3">
                    <span class="text-[11px] uppercase tracking-wider text-emerald-400/70 font-semibold">Tokens Saved This Session</span>
                    <Show when={costSaved()}>
                      <span class="text-[11px] font-semibold text-emerald-300 bg-emerald-500/15 px-2 py-0.5 rounded-full">
                        ~{costSaved()} saved
                      </span>
                    </Show>
                  </div>
                  <div class="flex items-baseline gap-2">
                    <span class="text-[28px] font-bold text-emerald-400 tabular-nums leading-none">
                      {formatTokens(props.savedTokens)}
                    </span>
                    <span class="text-[12px] text-emerald-500/70">tokens</span>
                  </div>

                  {/* Progress bar showing savings vs total */}
                  <Show when={savingsPercent() > 0}>
                    <div class="mt-3 flex items-center gap-3">
                      <div class="flex-1 h-1.5 rounded-full bg-emerald-500/10 overflow-hidden">
                        <div
                          class="h-full rounded-full bg-gradient-to-r from-emerald-500 to-emerald-400 transition-all duration-500"
                          style={{ width: `${Math.max(savingsPercent(), 2)}%` }}
                        />
                      </div>
                      <span class="text-[11px] text-emerald-400/80 font-medium tabular-nums shrink-0">
                        {savingsPercent()}% smaller context
                      </span>
                    </div>
                  </Show>
                </div>
              </Show>

              <Show when={hasOverhead()}>
                <div class="rounded-lg bg-amber-500/8 border border-amber-500/15 p-4 mb-4">
                  <div class="flex items-baseline gap-2">
                    <span class="text-[28px] font-bold text-amber-400 tabular-nums leading-none">
                      {formatTokens(-props.savedTokens)}
                    </span>
                    <span class="text-[12px] text-amber-500/70">tokens overhead</span>
                  </div>
                  <p class="text-[11px] text-amber-500/60 mt-2 leading-relaxed">
                    Memory is indexing your conversation. This overhead decreases as your session grows and savings compound.
                  </p>
                </div>
              </Show>

              <Show when={!hasSavings() && !hasOverhead()}>
                <div class="rounded-lg bg-violet-500/8 border border-violet-500/15 p-4 mb-4">
                  <div class="flex items-center gap-2 mb-2">
                    <svg class="w-4 h-4 text-violet-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                    <span class="text-[12px] text-violet-300 font-medium">Waiting for conversation</span>
                  </div>
                  <p class="text-[11px] text-violet-500/60 leading-relaxed">
                    Send a message to start building context. Memory will begin saving tokens as the conversation grows.
                  </p>
                </div>
              </Show>

              {/* Model info */}
              <Show when={props.model}>
                <div class="mt-4 pt-3 border-t border-[color:var(--border-subtle)] flex items-center justify-between">
                  <div class="flex items-center gap-1.5 text-[11px] text-zinc-500">
                    <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
                    </svg>
                    Model: <span class="text-zinc-300 font-medium">{getModelLabel(props.model)}</span>
                  </div>
                  <div class="flex items-center gap-1.5 text-[11px] text-zinc-500">
                    <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M7 8h10M7 12h4m1 8l-4-4H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-3l-4 4z" />
                    </svg>
                    Session tokens: <span class="text-zinc-300 font-medium tabular-nums">{formatTokens(props.totalTokens)}</span>
                  </div>
                </div>
              </Show>
            </div>
          </div>
        </div>
      </Show>
    </>
  );
}