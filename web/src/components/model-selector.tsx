import { createSignal, For, Show, createMemo } from 'solid-js';
import { Portal } from 'solid-js/web';
import { useSession } from '../context/session';
import type { ModelInfo } from '../api/client';
import { modelGroup, subProviderLabel } from '../lib/providers';

const PROVIDER_LABELS: Record<string, string> = {
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  openrouter: 'OpenRouter',
  ollama: 'Ollama',
  google: 'Google',
  mistral: 'Mistral',
};

const PROVIDER_DOT: Record<string, string> = {
  anthropic: 'bg-orange-400',
  openai: 'bg-emerald-400',
  openrouter: 'bg-violet-400',
  ollama: 'bg-sky-400',
  google: 'bg-blue-400',
  mistral: 'bg-rose-400',
};

const PROVIDER_TEXT: Record<string, string> = {
  anthropic: 'text-orange-400',
  openai: 'text-emerald-400',
  openrouter: 'text-violet-400',
  ollama: 'text-sky-400',
  google: 'text-blue-400',
  mistral: 'text-rose-400',
};

// Collection/group color and label overrides so OpenAI-compatible providers
// (Gemini, DeepSeek, Groq, …) get their own visual identity in the picker
// instead of all appearing as "OpenAI".
const COLLECTION_DOT: Record<string, string> = {
  ogcode: 'bg-emerald-400',
  Gemini: 'bg-blue-400',
  DeepSeek: 'bg-indigo-400',
  Groq: 'bg-amber-400',
  Together: 'bg-teal-400',
  Mistral: 'bg-rose-400',
};

const COLLECTION_TEXT: Record<string, string> = {
  ogcode: 'text-emerald-400',
  Gemini: 'text-blue-400',
  DeepSeek: 'text-indigo-400',
  Groq: 'text-amber-400',
  Together: 'text-teal-400',
  Mistral: 'text-rose-400',
};

function groupLabel(group: string): string {
  return PROVIDER_LABELS[group] || group;
}
function groupDot(group: string): string {
  return COLLECTION_DOT[group] || PROVIDER_DOT[group] || 'bg-zinc-500';
}
function groupText(group: string): string {
  return COLLECTION_TEXT[group] || PROVIDER_TEXT[group] || 'text-zinc-500';
}

// Per-model tag styling for the underlying free-pool provider (Groq, OpenRouter,
// …) shown inside the aggregated "ogcode" group.
const SUBPROVIDER_STYLE: Record<string, string> = {
  Groq: 'text-amber-300 bg-amber-500/10',
  OpenRouter: 'text-violet-300 bg-violet-500/10',
  Cerebras: 'text-orange-300 bg-orange-500/10',
  SambaNova: 'text-sky-300 bg-sky-500/10',
  NVIDIA: 'text-green-300 bg-green-500/10',
};
function subProviderStyle(label: string): string {
  return SUBPROVIDER_STYLE[label] || 'text-zinc-400 bg-zinc-500/10';
}

interface ModelSelectorProps {
  selectedModel?: () => string;
  models?: () => ModelInfo[];
  onSelect?: (modelId: string) => void;
  placement?: 'top' | 'bottom';
}

export default function ModelSelector(props: ModelSelectorProps = {}) {
  const session = useSession();
  const [open, setOpen] = createSignal(false);
  const [pos, setPos] = createSignal<{ left: number; top?: number; bottom?: number; maxH: number } | null>(null);
  let triggerRef: HTMLButtonElement | undefined;

  // Open the dropdown as a viewport-fixed, clamped popover anchored to the trigger
  // so it stays fully visible — even inside a right-edge drawer or near a screen edge.
  const toggleOpen = () => {
    if (open()) { setOpen(false); return; }
    const r = triggerRef?.getBoundingClientRect();
    if (r) {
      const W = 384, GAP = 6, M = 8; // dropdown width (w-96), gap, viewport margin
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

  const allModels = () => (props.models ? props.models() : session.models());
  const enabledModels = createMemo(() => allModels().filter((m) => m.enabled));

  const grouped = createMemo((): Map<string, ModelInfo[]> => {
    const map = new Map<string, ModelInfo[]>();
    for (const m of enabledModels()) {
      const group = modelGroup(m);
      const list = map.get(group) || [];
      list.push(m);
      map.set(group, list);
    }
    return map;
  });

  const selectedModelInfo = (): ModelInfo | undefined => {
    const id = props.selectedModel ? props.selectedModel() : session.selectedModel();
    // Prefer enabled models, but fall back to all models so a session whose
    // model has since been disabled still shows the correct label in the trigger.
    return enabledModels().find((m) => m.id === id) ?? allModels().find((m) => m.id === id);
  };

  const handleSelect = (modelId: string) => {
    if (props.onSelect) {
      props.onSelect(modelId);
    } else {
      session.selectModel(modelId);
    }
    setOpen(false);
  };

  return (
    <div class="relative">
      <button
        ref={triggerRef}
        type="button"
        onClick={toggleOpen}
        class="flex items-center gap-1.5 px-2 py-1 h-8 text-[12px] font-medium text-zinc-300
               hover:bg-[color:var(--bg-hover)] rounded-md
               transition-colors whitespace-nowrap max-w-[200px]"
      >
        <span class={`w-1.5 h-1.5 rounded-full ${
          selectedModelInfo() ? groupDot(modelGroup(selectedModelInfo()!)) : 'bg-zinc-500'
        }`} />
        <span class="truncate">{selectedModelInfo()?.name || 'Select model'}</span>
        <svg class="w-3 h-3 text-zinc-500 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>
      <Show when={open()}>
        <Portal>
          <div class="fixed inset-0 z-[210]" onClick={() => setOpen(false)} />
          <div
            class="fixed w-96 bg-[color:var(--bg-overlay)] border border-[color:var(--border-default)] rounded-xl shadow-[0_16px_40px_rgba(0,0,0,0.5)] z-[211] py-1 overflow-y-auto"
            style={{
              left: `${pos()?.left ?? 0}px`,
              ...(pos()?.top !== undefined ? { top: `${pos()!.top}px` } : { bottom: `${pos()?.bottom ?? 0}px` }),
              'max-height': `${pos()?.maxH ?? 384}px`,
            }}
          >
          <For each={[...grouped().entries()]}>
            {([group, models]) => (
              <div>
                <div class={`px-3 pt-2 pb-1 text-[10px] font-semibold uppercase tracking-wider flex items-center gap-1.5 ${
                  groupText(group)
                }`}>
                  <span class={`w-1.5 h-1.5 rounded-full ${groupDot(group)}`} />
                  {groupLabel(group)}
                </div>
                <For each={models}>
                  {(model) => {
                    const isSelected = () => (props.selectedModel ? props.selectedModel() : session.selectedModel()) === model.id;
                    return (
                      <button
                        type="button"
                        onClick={() => handleSelect(model.id)}
                        class={`w-full text-left px-3 py-1.5 text-[13px] transition-colors
                                flex items-center justify-between gap-2
                                ${isSelected()
                                  ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]'
                                  : 'text-zinc-200 hover:bg-[color:var(--bg-hover)]'
                                }`}
                      >
                        <span class="truncate flex items-center gap-2">
                          <Show when={isSelected()} fallback={<span class="w-3.5" />}>
                            <svg class="w-3.5 h-3.5 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                            </svg>
                          </Show>
                          {model.name}
                        </span>
                        <div class="flex items-center gap-1 shrink-0">
                          <Show when={subProviderLabel(model)}>
                            {(label) => (
                              <span class={`text-[9px] px-1 py-0.5 rounded font-medium ${subProviderStyle(label())}`}>
                                {label()}
                              </span>
                            )}
                          </Show>
                          <Show when={model.inputPricePerM > 0 || model.outputPricePerM > 0}>
                            <span class="text-[9px] text-zinc-500 bg-zinc-500/10 px-1 py-0.5 rounded font-mono tabular-nums">
                              ${fmtPrice(model.inputPricePerM)}/${fmtPrice(model.outputPricePerM)}
                            </span>
                          </Show>
                          <Show when={model.isCustom}>
                            <span class="text-[9px] text-violet-400 bg-violet-500/10 px-1 py-0.5 rounded">custom</span>
                          </Show>
                          <Show when={model.default}>
                            <span class="text-[9.5px] text-zinc-500 uppercase tracking-wider">default</span>
                          </Show>
                        </div>
                      </button>
                    );
                  }}
                </For>
              </div>
            )}
          </For>
          <Show when={enabledModels().length === 0}>
            <div class="px-3 py-4 text-[12px] text-zinc-500 text-center">
              No models available
            </div>
          </Show>
          </div>
        </Portal>
      </Show>
    </div>
  );
}

function fmtPrice(n: number): string {
  if (n === 0) return '0';
  if (n < 0.01) return n.toFixed(2);
  if (n < 1) return n.toFixed(2).replace(/0+$/, '').replace(/\.$/, '');
  if (Number.isInteger(n)) return String(n);
  return n.toFixed(2).replace(/0+$/, '').replace(/\.$/, '');
}