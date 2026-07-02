import { useNavigate } from '@solidjs/router';
import { createSignal, createEffect, For, Show, onMount } from 'solid-js';
import { useSession } from '../context/session';
import { useServer } from '../context/server';
import SessionSidebar from '../components/session-sidebar';
import ModelSelector from '../components/model-selector';

const SUGGESTIONS: string[] = [
  'Explain this codebase',
  'Find and fix a bug',
  'Write tests for a module',
  'Refactor for clarity',
];

export default function Home() {
  return <HomeContent />;
}

function HomeContent() {
  const session = useSession();
  const server = useServer();
  const navigate = useNavigate();

  // Redirect to plan mode landing page when in plan mode
  createEffect(() => {
    if (server.mode() === 'plan') {
      navigate('/plan', { replace: true });
    }
  });

  const [text, setText] = createSignal('');
  const [submitting, setSubmitting] = createSignal(false);
  let textareaRef: HTMLTextAreaElement | undefined;

  onMount(() => {
    textareaRef?.focus();
  });

  // Auto-resize textarea
  createEffect(() => {
    text();
    if (textareaRef) {
      textareaRef.style.height = 'auto';
      textareaRef.style.height = Math.min(Math.max(textareaRef.scrollHeight, 56), 280) + 'px';
    }
  });

  const startSession = async (content: string) => {
    if (submitting()) return;
    const trimmed = content.trim();
    if (!trimmed) return;
    setSubmitting(true);
    try {
      const sess = await session.newSession();
      // Navigate first so the session context is active, then prompt
      navigate(`/session/${sess.id}`);
      // Wait a tick for the route to settle, then send the prompt
      // through the session context (which handles loading state, polling, etc.)
      requestAnimationFrame(() => {
        session.prompt(trimmed).catch((e) => {
          console.error('prompt failed:', e);
        });
      });
    } catch (e) {
      console.error('start session failed:', e);
      setSubmitting(false);
    }
  };

  const handleSubmit = (e: Event) => {
    e.preventDefault();
    startSession(text());
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.metaKey && !e.ctrlKey) {
      e.preventDefault();
      handleSubmit(e);
    }
  };

  const canSend = () => !submitting() && text().trim().length > 0;

  return (
    <div class="flex h-screen w-full">
      <SessionSidebar />

      <div class="flex-1 flex flex-col overflow-hidden relative bg-[color:var(--bg-base)]">

        <div class="relative flex-1 overflow-y-auto flex flex-col">
          <div class="flex-1 flex flex-col items-center justify-center w-full max-w-2xl mx-auto px-6 pb-24">
            {/* Brand */}
            <div class="mb-8 flex flex-col items-center animate-scale-in">
              <div class="w-11 h-11 rounded-2xl bg-[color:var(--accent)] flex items-center justify-center shadow-lg shadow-[color:var(--accent)]/20 ring-1 ring-white/10 mb-4">
                <svg class="w-5 h-5 text-[color:var(--on-primary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
                </svg>
              </div>
              <h1 class="text-[26px] md:text-[28px] font-semibold tracking-tight text-zinc-50 text-center">
                What can I help you with?
              </h1>
            </div>

            {/* Prompt input */}
            <form onSubmit={handleSubmit} class="w-full animate-fade-in-up" style={{ 'animation-delay': '60ms' }}>
              <div class="rounded-2xl border border-[color:var(--border-default)] bg-[color:var(--bg-surface)]
                          shadow-lg shadow-black/30 transition-all var(--spring-md) focus-within:border-[color:var(--accent)]/50
                          focus-within:shadow-[color:var(--accent)]/5 focus-within:shadow-2xl">
                <textarea
                  ref={textareaRef}
                  value={text()}
                  onInput={(e) => setText(e.currentTarget.value)}
                  onKeyDown={handleKeyDown}
                  placeholder="Message ogcode…"
                  disabled={submitting()}
                  rows={2}
                  class="block w-full resize-none bg-transparent px-5 pt-4 pb-2 text-[14px] text-zinc-100
                         placeholder-zinc-500 focus:outline-none disabled:opacity-60
                         min-h-[56px] max-h-[280px] leading-relaxed"
                />
                <div class="flex items-center gap-2 px-3 pb-2.5 pt-1">
                  <ModelSelector />
                  <div class="flex-1" />
                  <button
                    type="submit"
                    disabled={!canSend()}
                    title={canSend() ? 'Send (Enter)' : 'Type a message'}
                    class={`h-9 w-9 rounded-xl flex items-center justify-center transition-all shrink-0
                      ${canSend()
                        ? 'bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)] text-[color:var(--on-primary)] shadow-sm hover:shadow-md hover:scale-[1.04] active:scale-[0.97]'
                        : 'bg-[color:var(--bg-elevated)] text-zinc-600 cursor-not-allowed'
                      }`}
                  >
                    <Show
                      when={!submitting()}
                      fallback={
                        <svg class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                          <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v4m0 8v4m8-8h-4M8 12H4m13.657-5.657l-2.829 2.829M9.172 14.828l-2.829 2.829m11.314 0l-2.829-2.829M9.172 9.172L6.343 6.343" />
                        </svg>
                      }
                    >
                      <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M12 19V5m0 0l-6 6m6-6l6 6" />
                      </svg>
                    </Show>
                  </button>
                </div>
              </div>
            </form>

            {/* Suggestion chips */}
            <div class="mt-5 flex flex-wrap items-center justify-center gap-2 animate-fade-in-up" style={{ 'animation-delay': '120ms' }}>
              <For each={SUGGESTIONS}>
                {(s) => (
                  <button
                    type="button"
                    onClick={() => {
                      setText(s);
                      textareaRef?.focus();
                    }}
                    class="h-8 px-3 rounded-full border border-[color:var(--border-subtle)]
                           bg-[color:var(--bg-surface)]/60 hover:border-[color:var(--accent)]/40
                           hover:bg-[color:var(--accent-soft)] text-[12px] text-zinc-400 hover:text-[color:var(--accent)]
                           transition-all var(--spring-sm) hover:scale-[1.02] active:scale-[0.98]"
                  >
                    {s}
                  </button>
                )}
              </For>
            </div>
          </div>

          {/* Tiny status footer */}
          <div class="relative pb-5 px-6 flex items-center justify-center gap-3 text-[10.5px] text-zinc-600">
            <span class="flex items-center gap-1">
              <kbd class="px-1 py-[1px] rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px]">↵</kbd>
              send
            </span>
            <span class="text-zinc-700">·</span>
            <span class="flex items-center gap-1">
              <kbd class="px-1 py-[1px] rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px]">⇧ ↵</kbd>
              newline
            </span>
            <Show when={server.directory()}>
              <span class="text-zinc-700">·</span>
              <span class="font-mono text-zinc-600 truncate max-w-[280px]">
                {server.directory()}
              </span>
            </Show>
          </div>
        </div>
      </div>
    </div>
  );
}
