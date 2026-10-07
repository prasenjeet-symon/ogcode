import { createSignal, For, Show, createMemo, onCleanup, createEffect } from 'solid-js';
import { Portal } from 'solid-js/web';
import { useSession } from '../context/session';
import type { ModelInfo } from '../api/client';

// The reasoning-effort picker that sits beside the model picker in the
// composer. Effort is how long the model thinks before it acts: the levels on
// offer are the selected model's own, as the server reports them for its
// provider (ModelInfo.efforts), so Claude offers Low → Max, gpt-oss Low → High,
// and a model that only switches thinking on or off offers Off / On. A model
// with no effort control shows nothing — unless it reasons and simply can't be
// asked to through this provider, in which case the server says why
// (effortNote) and the picker shows that instead of disappearing silently.

const LABEL: Record<string, string> = {
  none: 'Off',
  on: 'On',
  minimal: 'Minimal',
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  xhigh: 'Extra high',
  max: 'Max',
};

const HINT: Record<string, string> = {
  none: 'No thinking — fastest and cheapest',
  on: 'Thinks before it acts',
  minimal: 'Barely thinks — very fast',
  low: 'Quick — routine edits and questions',
  medium: 'Balances speed and depth',
  high: 'Thorough — harder problems',
  xhigh: 'Deeper still — long, tricky tasks',
  max: 'Thinks as long as it needs — slowest',
};

// How many of the gauge's four bars a level fills.
const BARS: Record<string, number> = {
  none: 0, minimal: 1, low: 1, on: 2, medium: 2, high: 3, xhigh: 4, max: 4,
};

export function effortLabel(level: string): string {
  return LABEL[level] ?? level;
}

function EffortGauge(props: { level: string; class?: string }) {
  const filled = () => BARS[props.level] ?? 2;
  return (
    <svg
      class={props.class ?? 'w-3 h-3'}
      classList={{ 'text-[color:var(--accent)]': props.level === 'max' }}
      viewBox="0 0 12 12"
      fill="currentColor"
      aria-hidden="true"
    >
      <For each={[0, 1, 2, 3]}>
        {(i) => (
          <rect
            x={i * 3}
            y={8 - i * 2}
            width="2"
            height={4 + i * 2}
            rx="0.6"
            opacity={i < filled() ? 1 : 0.28}
          />
        )}
      </For>
    </svg>
  );
}

interface EffortSelectorProps {
  /** The model the levels belong to; defaults to the session's selection. */
  modelId?: () => string;
  providerId?: () => string;
  /** The current level ('' = the model's default); defaults to the session's. */
  value?: () => string;
  onSelect?: (level: string) => void;
  placement?: 'top' | 'bottom';
}

export default function EffortSelector(props: EffortSelectorProps = {}) {
  const session = useSession();
  const [open, setOpen] = createSignal(false);
  const [pos, setPos] = createSignal<{ left: number; top?: number; bottom?: number; maxH: number } | null>(null);
  let triggerRef: HTMLButtonElement | undefined;

  const modelId = () => (props.modelId ? props.modelId() : session.selectedModel());
  const providerId = () => (props.providerId ? props.providerId() : session.selectedProvider());

  const info = createMemo((): ModelInfo | undefined => {
    const id = modelId();
    const pid = providerId();
    const list = session.models();
    return (pid ? list.find((m) => m.id === id && m.providerId === pid) : undefined)
      ?? list.find((m) => m.id === id);
  });
  const levels = () => info()?.efforts ?? [];
  const defaultLevel = () => info()?.defaultEffort ?? '';
  const note = () => info()?.effortNote ?? '';
  const value = () => (props.value ? props.value() : session.selectedEffort());
  // What the model will actually run at: the pick, else its default.
  const effective = () => value() || defaultLevel();

  const triggerLabel = () => (effective() ? effortLabel(effective()) : 'Default');

  const toggleOpen = () => {
    if (open()) { setOpen(false); return; }
    if (window.matchMedia('(max-width: 640px)').matches) { setOpen(true); return; }
    const r = triggerRef?.getBoundingClientRect();
    if (r) {
      const W = 288, GAP = 6, M = 8; // menu width (w-72), gap, viewport margin
      const vw = window.innerWidth, vh = window.innerHeight;
      let left = Math.min(r.left, vw - W - M);
      if (left < M) left = M;
      const below = vh - r.bottom - GAP - M;
      const above = r.top - GAP - M;
      const preferUp = (props.placement ?? 'top') === 'top';
      const openUp = preferUp ? !(above < 200 && below > above) : (below < 200 && above > below);
      if (openUp) setPos({ left, bottom: vh - r.top + GAP, maxH: Math.max(160, above) });
      else setPos({ left, top: r.bottom + GAP, maxH: Math.max(160, below) });
    }
    setOpen(true);
  };

  // Escape closes the menu — and only the menu: the composer reads Escape as
  // "stop the agent", which must not fire while a picker is in the way.
  createEffect(() => {
    if (!open()) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.preventDefault();
      e.stopPropagation();
      setOpen(false);
      triggerRef?.focus();
    };
    window.addEventListener('keydown', onKey, true);
    onCleanup(() => window.removeEventListener('keydown', onKey, true));
  });

  const choose = (level: string) => {
    if (props.onSelect) props.onSelect(level);
    else void session.selectEffort(level);
    setOpen(false);
  };

  // The rows: the model's levels, plus a "Default" row first when the model's
  // own default is not known — the only way back to it then.
  const rows = createMemo((): { level: string; label: string; hint: string }[] => {
    const list = levels().map((l) => ({ level: l, label: effortLabel(l), hint: HINT[l] ?? '' }));
    if (!defaultLevel() && list.length > 0) {
      list.unshift({ level: '', label: 'Default', hint: 'Whatever the model does on its own' });
    }
    return list;
  });

  return (
    <Show when={levels().length > 0 || note()}>
      <div class="relative">
        <button
          ref={triggerRef}
          type="button"
          onClick={toggleOpen}
          aria-haspopup="menu"
          aria-expanded={open()}
          aria-label={`Reasoning effort: ${levels().length > 0 ? triggerLabel() : 'not adjustable'}`}
          title={levels().length > 0
            ? `Reasoning effort: ${triggerLabel()} — how hard the model thinks before it acts`
            : note()}
          class="flex items-center gap-1.5 px-2 py-1 h-8 text-meta font-medium rounded-md transition-colors whitespace-nowrap
                 hover:bg-[color:var(--bg-hover)]"
          classList={{
            'text-zinc-300': levels().length > 0,
            'text-zinc-500': levels().length === 0,
            'bg-[color:var(--bg-hover)]': open(),
          }}
        >
          <EffortGauge level={levels().length > 0 ? effective() : 'none'} class="w-3 h-3 shrink-0" />
          <span class="effort-trigger-label">{levels().length > 0 ? triggerLabel() : 'Effort'}</span>
          <svg class="w-3 h-3 text-zinc-500 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </button>
        <Show when={open()}>
          <Portal>
            <div class="fixed inset-0 z-[210]" onClick={() => setOpen(false)} />
            <div
              role="menu"
              aria-label="Reasoning effort"
              class="model-dropdown fixed w-72 bg-[color:var(--bg-overlay)] border border-[color:var(--border-default)] rounded-xl shadow-[0_16px_40px_rgba(0,0,0,0.5)] z-[211] py-1 overflow-y-auto"
              style={{
                left: `${pos()?.left ?? 0}px`,
                ...(pos()?.top !== undefined ? { top: `${pos()!.top}px` } : { bottom: `${pos()?.bottom ?? 0}px` }),
                'max-height': `${pos()?.maxH ?? 384}px`,
              }}
            >
              <div class="px-3 pt-2 pb-1 text-micro font-semibold uppercase tracking-wider text-zinc-500">
                Reasoning effort
              </div>
              <Show when={levels().length === 0}>
                <p class="px-3 pb-2.5 pt-0.5 text-meta leading-relaxed text-zinc-400">{note()}</p>
              </Show>
              <For each={rows()}>
                {(row) => {
                  // Never picked: the default is what runs, so it is what is
                  // checked — the model's own level when known, else "Default".
                  const isSelected = () => (value()
                    ? row.level === value()
                    : defaultLevel() ? row.level === defaultLevel() : row.level === '');
                  return (
                    <button
                      type="button"
                      role="menuitemradio"
                      aria-checked={isSelected()}
                      aria-label={`${row.label}${row.level !== '' && row.level === defaultLevel() ? ' (default)' : ''}${row.hint ? ` — ${row.hint}` : ''}`}
                      onClick={() => choose(row.level)}
                      class={`w-full text-left px-3 py-1.5 transition-colors flex items-center gap-2
                              ${isSelected()
                                ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]'
                                : 'text-zinc-200 hover:bg-[color:var(--bg-hover)]'
                              }`}
                    >
                      <Show when={isSelected()} fallback={<span class="w-3.5 shrink-0" />}>
                        <svg class="w-3.5 h-3.5 shrink-0 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                          <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                        </svg>
                      </Show>
                      <EffortGauge level={row.level || 'medium'} class="w-3 h-3 shrink-0 opacity-90" />
                      <span class="min-w-0 flex-1">
                        <span class="block text-ui leading-tight">{row.label}</span>
                        <Show when={row.hint}>
                          <span class="block text-micro leading-tight text-zinc-500 truncate">{row.hint}</span>
                        </Show>
                      </span>
                      <Show when={row.level !== '' && row.level === defaultLevel()}>
                        <span class="text-[9.5px] text-zinc-500 uppercase tracking-wider shrink-0">default</span>
                      </Show>
                    </button>
                  );
                }}
              </For>
              <Show when={levels().length > 0}>
                <div class="mt-1 border-t border-[color:var(--border-subtle)] px-3 pt-2 pb-1.5 text-micro leading-snug text-zinc-500">
                  More effort thinks longer: better on hard problems, slower and more tokens. Applies from the next turn.
                </div>
              </Show>
            </div>
          </Portal>
        </Show>
      </div>
    </Show>
  );
}
