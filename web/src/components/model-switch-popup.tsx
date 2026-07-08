import { Show, createMemo } from 'solid-js';
import { Portal } from 'solid-js/web';
import { useSession } from '../context/session';
import type { ModelInfo } from '../api/client';
import { modelGroup } from '../lib/providers';

const PROVIDER_DOT: Record<string, string> = {
  anthropic: 'bg-orange-400',
  openai: 'bg-emerald-400',
  openrouter: 'bg-violet-400',
  ollama: 'bg-sky-400',
  google: 'bg-blue-400',
  mistral: 'bg-rose-400',
};

const COLLECTION_DOT: Record<string, string> = {
  ogcode: 'bg-emerald-400',
  Gemini: 'bg-blue-400',
  DeepSeek: 'bg-indigo-400',
  Groq: 'bg-amber-400',
  Together: 'bg-teal-400',
  Mistral: 'bg-rose-400',
};

function groupDot(group: string): string {
  return COLLECTION_DOT[group] || PROVIDER_DOT[group] || 'bg-zinc-500';
}

export default function ModelSwitchPopup() {
  const session = useSession();
  const popup = session.modelSwitchPopup;

  const modelInfo = createMemo((): ModelInfo | undefined => {
    const info = popup();
    if (!info) return undefined;
    // Prefer enabled models, fall back to all so disabled-but-selected still shows.
    return session.models().find((m) => m.id === info.modelId && m.enabled)
      ?? session.models().find((m) => m.id === info.modelId);
  });

  const slot = () => popup()?.slot;

  // Auto-clear is handled by the context (setTimeout). Nothing to clean up here.

  return (
    <Show when={popup()}>
      <Portal>
        <div class="fixed inset-0 z-[300] flex items-start justify-center pointer-events-none">
          <div
            class="mt-24 animate-switch-popup"
          >
            <div
              class="flex items-center gap-3 px-4 py-2.5 rounded-xl
                     backdrop-blur-xl border border-[color:var(--border-strong)]
                     shadow-[0_8px_32px_rgba(0,0,0,0.45),0_0_20px_var(--glow)]
                     min-w-[220px]"
              style={{ 'background-color': 'rgba(27, 27, 31, 0.82)' }}
            >
              {/* Slot number badge */}
              <Show when={slot()}>
                {(n) => (
                  <div
                    class="flex items-center justify-center w-7 h-7 rounded-lg shrink-0
                           bg-[color:var(--accent-soft)] text-[color:var(--accent)]
                           font-mono font-semibold text-[13px] border border-[color:var(--accent-ring)]"
                  >
                    {n()}
                  </div>
                )}
              </Show>
              {/* Provider color dot */}
              <span
                class={`w-2 h-2 rounded-full shrink-0 ${
                  modelInfo() ? groupDot(modelGroup(modelInfo()!)) : 'bg-zinc-500'
                }`}
              />
              {/* Model name */}
              <span class="text-[13.5px] font-medium text-zinc-100 truncate max-w-[280px]">
                {modelInfo()?.name || popup()?.modelId || 'Unknown model'}
              </span>
              <span class="text-[10.5px] text-zinc-500 uppercase tracking-wider font-mono shrink-0 ml-auto">
                active
              </span>
            </div>
          </div>
        </div>
      </Portal>
    </Show>
  );
}