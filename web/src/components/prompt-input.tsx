import { createSignal, createEffect, Show, onCleanup, onMount } from 'solid-js';
import { useSession } from '../context/session';
import ModelSelector from './model-selector';

export default function PromptInput() {
  const session = useSession();
  const [text, setText] = createSignal('');
  const [focused, setFocused] = createSignal(false);
  let textareaRef: HTMLTextAreaElement | undefined;

  // Auto-resize textarea
  createEffect(() => {
    text();
    if (textareaRef) {
      textareaRef.style.height = 'auto';
      textareaRef.style.height = Math.min(textareaRef.scrollHeight, 240) + 'px';
    }
  });

  // The agent loop is "running" if we're loading (LLM streaming) OR tools are executing
  const isRunning = () => session.loading() || session.hasRunningTools();

  const handleSubmit = (e: Event) => {
    e.preventDefault();
    const content = text().trim();
    if (!content || isRunning()) return;
    setText('');
    if (textareaRef) textareaRef.style.height = 'auto';
    session.prompt(content);
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

  const canSend = () => !isRunning() && text().trim().length > 0;
  const isDisabled = () => isRunning();

  // Global Escape key handler to cancel the running agent loop
  const handleGlobalKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && isRunning()) {
      e.preventDefault();
      e.stopPropagation();
      session.abort();
    }
  };

  onMount(() => {
    document.addEventListener('keydown', handleGlobalKeyDown);
  });
  onCleanup(() => {
    document.removeEventListener('keydown', handleGlobalKeyDown);
  });

  return (
    <div class="shrink-0 bg-gradient-to-t from-[color:var(--bg-base)] via-[color:var(--bg-base)] to-transparent pt-4">
      <form onSubmit={handleSubmit} class="max-w-3xl mx-auto px-4 md:px-6 pb-4">
        <div
          class={`rounded-2xl border bg-[color:var(--bg-surface)] transition-all var(--spring-md)
            ${focused()
              ? 'border-[color:var(--accent)]/40 shadow-lg shadow-black/30 shadow-[color:var(--accent)]/5'
              : 'border-[color:var(--border-default)] shadow-md shadow-black/20'
            }`}
        >
          {/* Textarea */}
          <textarea
            ref={textareaRef}
            value={text()}
            onInput={handleInput}
            onKeyDown={handleKeyDown}
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
            placeholder={isRunning() ? "Agent is working…" : "Ask anything, paste an error, or describe a task…"}
            disabled={isDisabled()}
            rows={1}
            class="block w-full resize-none bg-transparent px-4 pt-3.5 pb-1 text-[14px] text-zinc-100
                   placeholder-zinc-500 focus:outline-none disabled:opacity-60
                   min-h-[44px] max-h-[240px] leading-relaxed"
          />

          {/* Toolbar */}
          <div class="flex items-center gap-2 px-2.5 pb-2.5 pt-1">
            <ModelSelector />

            <div class="flex-1" />

            <Show when={isRunning()}>
              <button
                type="button"
                onClick={() => session.abort()}
                class="h-8 px-3 rounded-lg flex items-center gap-1.5 text-[12px] font-medium
                       text-red-400 hover:text-red-300 hover:bg-red-500/10 border border-red-500/30
                       transition-all var(--spring-sm) active:scale-[0.97]"
                title="Cancel agent (Esc)"
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
                Cancel
              </button>
            </Show>

            <Show when={!isRunning()}>
              <button
                type="submit"
                disabled={!canSend()}
                title={canSend() ? 'Send (Enter)' : 'Type a message'}
                class={`h-8 w-8 rounded-lg flex items-center justify-center transition-all var(--spring-sm)
                  ${canSend()
                    ? 'bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)] text-[color:var(--on-primary)] shadow-sm hover:shadow-md hover:scale-[1.06] active:scale-[0.95]'
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

        {/* Footer hint */}
        <div class="mt-2 flex items-center justify-center gap-4 text-[10.5px] text-zinc-600">
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
          <span class="text-zinc-700">ogcode may make mistakes — verify important output.</span>
        </div>
      </form>
    </div>
  );
}
