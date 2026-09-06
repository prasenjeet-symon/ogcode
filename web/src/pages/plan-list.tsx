import { useNavigate } from '@solidjs/router';
import { createSignal, createEffect, onMount, For, Show } from 'solid-js';
import { usePlan } from '../context/plan';
import { useServer } from '../context/server';
import PlanSidebar from '../components/plan-sidebar';
import ModelSelector from '../components/model-selector';
import { DrawerToggle } from '../components/sidebar-shell';

const SUGGESTIONS: string[] = [
  'Plan a REST API',
  'Design a database schema',
  'Architect a microservice',
  'Refactor a module',
];

export default function PlanList() {
  return <PlanListContent />;
}

function PlanListContent() {
  const plan = usePlan();
  const server = useServer();
  const navigate = useNavigate();

  const [text, setText] = createSignal('');
  const [submitting, setSubmitting] = createSignal(false);
  let textareaRef: HTMLTextAreaElement | undefined;

  onMount(() => {
    textareaRef?.focus();
  });

  createEffect(() => {
    text();
    if (textareaRef) {
      textareaRef.style.height = 'auto';
      textareaRef.style.height = Math.min(Math.max(textareaRef.scrollHeight, 56), 280) + 'px';
    }
  });

  const startPlan = async (content: string) => {
    if (submitting()) return;
    const trimmed = content.trim();
    if (!trimmed) return;
    setSubmitting(true);
    try {
      // Reuse the active plan if it's still empty and open, rather than creating a new one
      let targetPlan = plan.activePlan();
      const isReusable = targetPlan &&
        targetPlan.status === 'open' &&
        plan.messages().filter((m: any) => m.info.role === 'user').length === 0;

      if (!isReusable) {
        targetPlan = await plan.newPlan(undefined, plan.selectedModel());
      }
      navigate(`/plan/${targetPlan!.id}`);
      requestAnimationFrame(() => {
        plan.sendPrompt(trimmed).catch((e) => {
          console.error('plan prompt failed:', e);
        });
      });
    } catch (e) {
      console.error('start plan failed:', e);
      setSubmitting(false);
    }
  };

  const handleSubmit = (e: Event) => {
    e.preventDefault();
    startPlan(text());
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.metaKey && !e.ctrlKey) {
      e.preventDefault();
      handleSubmit(e);
    }
  };

  const canSend = () => !submitting() && text().trim().length > 0;

  return (
    <div class="flex h-dvh w-full">
      <PlanSidebar />

      <div class="flex-1 flex flex-col overflow-hidden relative bg-[color:var(--bg-base)]">
        {/* Floating drawer toggle: this page has no header bar, and below lg
            the plan sidebar (plans/notes/docindex navigation) is a drawer —
            without this a phone has no way off the page. Pinned outside the
            scroll container so it stays put while the hero scrolls. */}
        <div
          class="lg:hidden absolute top-0 left-0 z-30 p-2"
          style={{ 'padding-top': 'max(0.5rem, env(safe-area-inset-top))' }}
        >
          <DrawerToggle drawer="plans" label="Open navigation" />
        </div>

        <div class="relative flex-1 overflow-y-auto flex flex-col">
          <div class="flex-1 flex flex-col items-center justify-center w-full max-w-2xl mx-auto px-4 sm:px-6 pb-24">
            {/* Brand */}
            <div class="mb-8 flex flex-col items-center animate-scale-in">
              <div class="w-11 h-11 rounded-xl bg-[color:var(--accent)] flex items-center justify-center shadow-md shadow-[color:var(--accent)]/15 ring-1 ring-white/10 mb-4">
                <svg class="w-5 h-5 text-[color:var(--on-primary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                </svg>
              </div>
              <h1 class="text-[22px] sm:text-[26px] md:text-[28px] font-semibold tracking-tight text-zinc-50 text-center">
                What would you like to build?
              </h1>
            </div>

            {/* Prompt input */}
            <form onSubmit={handleSubmit} class="w-full animate-fade-in-up" style={{ 'animation-delay': '60ms' }}>
              <div class="rounded-xl border border-[color:var(--border-default)] bg-[color:var(--bg-surface)]
                          shadow-sm transition-all var(--spring-md) focus-within:border-[color:var(--accent)]/40">
                <textarea
                  ref={textareaRef}
                  value={text()}
                  onInput={(e) => setText(e.currentTarget.value)}
                  onKeyDown={handleKeyDown}
                  placeholder="Describe your project or requirement…"
                  disabled={submitting()}
                  rows={2}
                  class="block w-full resize-none bg-transparent px-5 pt-4 pb-2 text-[15px] text-zinc-100
                         placeholder-zinc-500 focus:outline-none disabled:opacity-60
                         min-h-[56px] max-h-[280px] leading-relaxed"
                />
                <div class="flex items-center gap-2 px-3 pb-2.5 pt-1">
                  <ModelSelector
                    selectedModel={plan.selectedModel}
                    models={plan.models}
                    onSelect={plan.selectModel}
                  />
                  <div class="flex-1" />
                  <button
                    type="submit"
                    disabled={!canSend()}
                    title={canSend() ? 'Send (Enter)' : 'Type a message'}
                    class={`h-9 w-9 rounded-lg flex items-center justify-center transition-all shrink-0
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
            <div class="mt-5 flex flex-wrap items-center justify-center gap-2">
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

            {/* Pre-flight checklist */}
            <div class="mt-6 w-full rounded-xl border border-[color:var(--border-default)] bg-[color:var(--bg-surface)] divide-y divide-[color:var(--border-subtle)]">
              <div class="px-4 py-2.5 flex items-center gap-2">
                <svg class="w-3.5 h-3.5 text-zinc-500 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
                </svg>
                <span class="text-[11px] font-semibold text-zinc-500 uppercase tracking-wider">Before you plan</span>
              </div>

              {/* Git repo */}
              <CheckRow
                ok={server.isGitRepo()}
                label="Project is a git repository"
                okDetail="git initialized"
                warnDetail={
                  <span>Run <code class="font-mono text-[10.5px] bg-[color:var(--bg-elevated)] px-1 py-[1px] rounded">git init</code> in your project directory first</span>
                }
              />

              {/* Remote */}
              <CheckRow
                ok={server.hasRemote()}
                label="Remote repository connected"
                okDetail="remote configured"
                warnDetail={
                  <span>Add a remote — <code class="font-mono text-[10.5px] bg-[color:var(--bg-elevated)] px-1 py-[1px] rounded">git remote add origin &lt;url&gt;</code></span>
                }
              />

              {/* GitHub CLI */}
              <CheckRow
                ok={server.ghInstalled()}
                label="GitHub CLI installed"
                okDetail="gh is available"
                warnDetail={
                  <span>Install from <code class="font-mono text-[10.5px] bg-[color:var(--bg-elevated)] px-1 py-[1px] rounded">cli.github.com</code> — required for PR creation</span>
                }
              />

              {/* Branch */}
              <div class="px-4 py-2.5 flex items-start gap-2.5">
                <svg class="w-3.5 h-3.5 text-amber-400 shrink-0 mt-[1px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                <span class="text-[11.5px] text-zinc-400 leading-snug">
                  Make sure you're on your intended working branch
                  <Show when={server.branch()}>
                    {' '}— currently on{' '}
                    <code class="font-mono text-[10.5px] text-zinc-200 bg-[color:var(--bg-elevated)] px-1 py-[1px] rounded">{server.branch()}</code>
                  </Show>
                </span>
              </div>

              {/* Pull */}
              <div class="px-4 py-2.5 flex items-start gap-2.5">
                <svg class="w-3.5 h-3.5 text-amber-400 shrink-0 mt-[1px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                <span class="text-[11.5px] text-zinc-400 leading-snug">
                  Pull the latest changes before starting —{' '}
                  <code class="font-mono text-[10.5px] text-zinc-300 bg-[color:var(--bg-elevated)] px-1 py-[1px] rounded">git pull</code>
                </span>
              </div>
            </div>
          </div>

          {/* Footer. Keyboard hints are desktop-only. */}
          <div class="relative pb-5 px-6 composer-hints flex items-center justify-center gap-3 text-[10.5px] text-zinc-600">
            <span class="flex items-center gap-1">
              <kbd class="px-1 py-[1px] rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px]">↵</kbd>
              send
            </span>
            <span class="text-zinc-700">·</span>
            <span class="flex items-center gap-1">
              <kbd class="px-1 py-[1px] rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px]">⇧ ↵</kbd>
              newline
            </span>
          </div>
          <Show when={server.directory()}>
            <div class="relative pb-5 px-6 flex items-center justify-center">
              <span class="font-mono text-[10.5px] text-zinc-600 truncate max-w-[280px]">
                {server.directory()}
              </span>
            </div>
          </Show>
        </div>
      </div>
    </div>
  );
}

function CheckRow(props: { ok: boolean; label: string; okDetail: string; warnDetail: any }) {
  return (
    <div class="px-4 py-2.5 flex items-start gap-2.5">
      <Show
        when={props.ok}
        fallback={
          <svg class="w-3.5 h-3.5 text-red-400 shrink-0 mt-[1px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
          </svg>
        }
      >
        <svg class="w-3.5 h-3.5 text-emerald-400 shrink-0 mt-[1px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
        </svg>
      </Show>
      <div class="min-w-0 text-[11.5px] leading-snug">
        <Show
          when={props.ok}
          fallback={
            <span class="text-red-300">{props.label} — </span>
          }
        >
          <span class="text-zinc-400">{props.label} — </span>
        </Show>
        <Show
          when={props.ok}
          fallback={<span class="text-zinc-400">{props.warnDetail}</span>}
        >
          <span class="text-emerald-500/70">{props.okDetail}</span>
        </Show>
      </div>
    </div>
  );
}