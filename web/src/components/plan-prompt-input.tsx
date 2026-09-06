import { createSignal, createEffect, Show, onCleanup, onMount } from 'solid-js';
import { usePlan } from '../context/plan';
import ModelSelector from './model-selector';

export default function PlanPromptInput() {
  const plan = usePlan();
  const [text, setText] = createSignal('');
  const [focused, setFocused] = createSignal(false);
  let textareaRef: HTMLTextAreaElement | undefined;

  createEffect(() => {
    text();
    if (textareaRef) {
      textareaRef.style.height = 'auto';
      textareaRef.style.height = Math.min(textareaRef.scrollHeight, 240) + 'px';
    }
  });

  const isRunning = () => plan.loading();
  const isLocked = () => plan.activePlan()?.status === 'locked';
  const isDisabled = () => isRunning() || isLocked();

  const handleSubmit = (e: Event) => {
    e.preventDefault();
    const content = text().trim();
    if (!content || isDisabled()) return;
    setText('');
    if (textareaRef) textareaRef.style.height = 'auto';
    plan.sendPrompt(content);
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.metaKey && !e.ctrlKey) {
      e.preventDefault();
      handleSubmit(e);
    }
  };

  const handleInput = (e: Event) => {
    const target = e.target as HTMLTextAreaElement;
    setText(target.value);
  };

  const canSend = () => !isDisabled() && text().trim().length > 0;

  const handleGlobalKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && isRunning()) {
      e.preventDefault();
      e.stopPropagation();
      plan.abort();
    }
  };

  onMount(() => {
    document.addEventListener('keydown', handleGlobalKeyDown);
  });
  onCleanup(() => {
    document.removeEventListener('keydown', handleGlobalKeyDown);
  });

  return (
    <div class="shrink-0 bg-gradient-to-t from-[color:var(--bg-base)] via-[color:var(--bg-base)] to-transparent pt-4 composer-safe">
      <Show when={plan.lockError()}>
        <div class="max-w-3xl mx-auto px-4 md:px-6 mb-2">
          <div class="flex items-start gap-2 px-3 py-2 rounded-lg bg-red-500/10 border border-red-500/30 text-[12px] text-red-300">
            <svg class="w-3.5 h-3.5 mt-0.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
            </svg>
            {plan.lockError()}
          </div>
        </div>
      </Show>
      <form onSubmit={handleSubmit} class="max-w-3xl mx-auto px-4 md:px-6 pb-4">
        <div
          class={`rounded-xl border bg-[color:var(--bg-surface)] transition-colors duration-150
            ${focused()
              ? 'border-[color:var(--border-strong)] shadow-md'
              : 'border-[color:var(--border-default)] shadow-sm'
            }`}
        >
          <textarea
            ref={textareaRef}
            value={text()}
            onInput={handleInput}
            onKeyDown={handleKeyDown}
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
            placeholder={isLocked() ? 'Plan is locked' : isRunning() ? 'Agent is working…' : 'Describe what you want to build…'}
            disabled={isDisabled()}
            rows={1}
            class="block w-full resize-none bg-transparent px-4 pt-3.5 pb-1 text-[14px] text-zinc-100
                   placeholder-zinc-500 focus:outline-none disabled:opacity-60
                   min-h-[44px] max-h-[240px] leading-relaxed"
          />

          <div class="flex items-center gap-2 px-2.5 pb-2.5 pt-1">
            <ModelSelector
              selectedModel={plan.selectedModel}
              models={plan.models}
              onSelect={plan.selectModel}
            />

            <div class="flex-1" />

            <Show when={isLocked()}>
              <span class="flex items-center gap-1.5 text-[12px] text-amber-400 font-medium">
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                </svg>
                Locked
              </span>
            </Show>

            <Show when={!isRunning() && !isLocked() && plan.activePlan()?.status === 'open' && plan.messages().length > 0}>
              <button
                type="button"
                onClick={() => plan.lockPlan()}
                class="h-8 px-3 rounded-lg flex items-center gap-1.5 text-[12px] font-medium
                       text-amber-300 hover:text-amber-200 hover:bg-amber-500/10 border border-amber-500/30
                       transition-colors duration-150"
                title="Lock plan and break into tasks"
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                </svg>
                Lock Plan
              </button>
            </Show>

            <Show when={isRunning()}>
              <button
                type="button"
                onClick={() => plan.abort()}
                class="h-8 px-3 rounded-lg flex items-center gap-1.5 text-[12px] font-medium
                       text-red-400 hover:text-red-300 hover:bg-red-500/10 border border-red-500/30
                       transition-colors duration-150"
                title="Cancel agent (Esc)"
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
                Cancel
              </button>
            </Show>

            <Show when={!isRunning() && !isLocked()}>
              <button
                type="submit"
                disabled={!canSend()}
                title={canSend() ? 'Send (Enter)' : 'Type a message'}
                class={`h-8 w-8 rounded-lg flex items-center justify-center transition-all
                  ${canSend()
                    ? 'bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)] text-[color:var(--on-primary)] shadow-sm scale-100'
                    : 'bg-[color:var(--bg-elevated)] text-zinc-600 cursor-not-allowed scale-95'
                  }`}
              >
                <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 19V5m0 0l-6 6m6-6l6 6" />
                </svg>
              </button>
            </Show>
          </div>
        </div>

        {/* Keyboard hints are desktop-only; touch keyboards have their own. */}
        <div class="mt-2 flex items-center justify-center gap-4 text-[10.5px] text-zinc-600 composer-hints">
          <span class="flex items-center gap-1">
            <kbd class="px-1 py-[1px] rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px]">↵</kbd>
            send
          </span>
          <span class="flex items-center gap-1">
            <kbd class="px-1 py-[1px] rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px]">⇧ ↵</kbd>
            newline
          </span>
          <Show when={isRunning()}>
            <span class="flex items-center gap-1">
              <kbd class="px-1 py-[1px] rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px]">Esc</kbd>
              cancel
            </span>
          </Show>
        </div>
      </form>
    </div>
  );
}