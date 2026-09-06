import { useNavigate, useLocation } from '@solidjs/router';
import { createSignal, createEffect, For, Show, onMount } from 'solid-js';
import { useSession } from '../context/session';
import { useServer } from '../context/server';
import SessionSidebar from '../components/session-sidebar';
import { DrawerToggle } from '../components/sidebar-shell';
import ModelSelector from '../components/model-selector';
import Logo from '../components/logo';

const SUGGESTIONS: string[] = [
  'Explain this codebase',
  'Find and fix a bug',
  'Write tests for a module',
  'Refactor for clarity',
];

// ── Value pillars ──────────────────────────────────────────────
// Each pillar ties the SpaceX-of-coding-agents narrative to a
// concrete technical differentiator from the README.
const PILLARS = [
  {
    icon: 'memory',
    title: 'Agentic Loop Memory',
    desc: 'A persistent knowledge graph recalls only the facts relevant to each turn — never replaying the whole transcript.',
    stat: '70%+',
    statLabel: 'tokens saved on long sessions',
  },
  {
    icon: 'accuracy',
    title: 'Sharper Accuracy',
    desc: 'A short, on-point context window lets the model focus — so it produces more correct results per turn, not fewer.',
    stat: '↑',
    statLabel: 'accuracy vs naive replay',
  },
  {
    icon: 'infinity',
    title: 'Near-Infinite Context',
    desc: 'The per-turn prompt stays flat no matter how long the session runs. Chat for days — on any model.',
    stat: '∞',
    statLabel: 'no hard context limit',
  },
] as const;

// ── Workflow steps (Plan Mode narrative) ───────────────────────
const STEPS = [
  { n: '01', title: 'Describe', desc: 'Open a plan and state your goal. The agent reads your codebase and refines the approach with you.' },
  { n: '02', title: 'Lock', desc: 'Lock the plan. A breakdown agent turns it into structured tasks with effort estimates and a dependency graph.' },
  { n: '03', title: 'Execute', desc: 'Each task gets its own git branch and isolated agent. Independent tasks run in parallel — conflict-free PRs.' },
] as const;

function PillarIcon(props: { name: string }) {
  const common = 'w-5 h-5';
  switch (props.name) {
    case 'memory':
      return (
        <svg class={common} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 2a10 10 0 100 20 10 10 0 000-20zM12 6v6l4 2" />
        </svg>
      );
    case 'accuracy':
      return (
        <svg class={common} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 3l2.5 5.5L20 9l-4 4 1 6-5-3-5 3 1-6-4-4 5.5-.5L12 3z" />
        </svg>
      );
    case 'infinity':
      return (
        <svg class={common} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
          <path stroke-linecap="round" stroke-linejoin="round" d="M6.5 8a4 4 0 100 8c2.2 0 3.5-1.8 5.5-4s3.3-4 5.5-4a4 4 0 110 8c-2.2 0-3.5-1.8-5.5-4S8.7 8 6.5 8z" />
        </svg>
      );
    default:
      return null;
  }
}

export default function Home() {
  return <HomeContent />;
}

function HomeContent() {
  const session = useSession();
  const server = useServer();
  const navigate = useNavigate();
  const location = useLocation();

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
    // Don't auto-focus — the hero is a landing experience, not a bare input.
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

  const scrollToInput = () => {
    textareaRef?.focus();
    textareaRef?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  };

  return (
    <div class="flex h-dvh w-full">
      <SessionSidebar />

      <div class="page-enter flex-1 flex flex-col overflow-hidden relative bg-[color:var(--bg-base)]">
        {/* Ambient background glow — subtle accent radial */}
        <div
          class="pointer-events-none absolute inset-0 opacity-60"
          style={{
            background:
              'radial-gradient(ellipse 70% 50% at 50% -8%, rgba(94,106,210,0.10), transparent 70%)',
          }}
        />
        {/* Dot grid texture */}
        <div class="pointer-events-none absolute inset-0 bg-grid opacity-40" />

        <div class="relative flex-1 overflow-y-auto">
          {/* ── Nav bar ─────────────────────────────────────────── */}
          <header class="sticky top-0 z-20 flex items-center justify-between px-3 sm:px-6 py-3 sm:py-4
                          backdrop-blur-md bg-[color:var(--bg-base)]/70 border-b border-[color:var(--border-subtle)]"
                  style={{ 'padding-top': 'max(0.75rem, env(safe-area-inset-top))' }}>
            <div class="flex items-center gap-2.5">
              <DrawerToggle drawer="sessions" label="Open sessions" />
              <div class="w-7 h-7 rounded-lg bg-[color:var(--accent)] flex items-center justify-center shadow-md shadow-[color:var(--accent)]/20 ring-1 ring-white/10">
                <Logo class="w-3.5 h-3.5 text-[color:var(--on-primary)]" small />
              </div>
              <span class="text-[14px] font-semibold tracking-tight text-zinc-100">ogcode</span>
              <span class="ml-1.5 px-1.5 py-[1px] rounded text-[9px] font-mono font-medium text-[color:var(--accent)] bg-[color:var(--accent-soft)] border border-[color:var(--accent)]/20">
                v{''}0.19.1
              </span>
            </div>
            <div class="flex items-center gap-3">
              <button
                type="button"
                onClick={() => navigate('/plan')}
                class="h-8 px-3.5 rounded-lg text-[12px] font-medium text-zinc-400 hover:text-zinc-100
                       border border-[color:var(--border-default)] hover:border-[color:var(--border-strong)]
                       bg-[color:var(--bg-surface)]/50 transition-all var(--spring-sm)"
              >
                Plan Mode
              </button>
              <button
                type="button"
                onClick={() => navigate('/settings', { state: { from: location.pathname } })}
                class="h-8 w-8 rounded-lg flex items-center justify-center text-zinc-500 hover:text-zinc-200
                       border border-[color:var(--border-default)] hover:border-[color:var(--border-strong)]
                       bg-[color:var(--bg-surface)]/50 transition-all var(--spring-sm)"
                title="Settings"
              >
                <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M10.3 3.2a1 1 0 011.4 0l.6.6a1 1 0 001 .2l.8-.2a1 1 0 011.2.7l.3.9a1 1 0 00.7.7l.9.3a1 1 0 01.7 1.2l-.2.8a1 1 0 00.2 1l.6.6a1 1 0 010 1.4l-.6.6a1 1 0 00-.2 1l.2.8a1 1 0 01-.7 1.2l-.9.3a1 1 0 00-.7.7l-.3.9a1 1 0 01-1.2.7l-.8-.2a1 1 0 00-1 .2l-.6.6a1 1 0 01-1.4 0l-.6-.6a1 1 0 00-1-.2l-.8.2a1 1 0 01-1.2-.7l-.3-.9a1 1 0 00-.7-.7l-.9-.3a1 1 0 01-.7-1.2l.2-.8a1 1 0 00-.2-1l-.6-.6a1 1 0 010-1.4l.6-.6a1 1 0 00.2-1l-.2-.8a1 1 0 01.7-1.2l.9-.3a1 1 0 00.7-.7l.3-.9a1 1 0 011.2-.7l.8.2a1 1 0 001-.2l.6-.6zM12 9a3 3 0 100 6 3 3 0 000-6z" />
                </svg>
              </button>
            </div>
          </header>

          {/* ── Hero ───────────────────────────────────────────── */}
          <section class="relative flex flex-col items-center pt-10 sm:pt-20 pb-10 px-4 sm:px-6">
            {/* Badge */}
            <div class="mb-7 animate-fade-in-up flex items-center gap-2 px-3 py-1.5 rounded-full
                        border border-[color:var(--border-default)] bg-[color:var(--bg-surface)]/60
                        text-[11px] text-zinc-400 text-center">
              <span class="relative flex h-1.5 w-1.5 shrink-0">
                <span class="absolute inline-flex h-full w-full rounded-full bg-[color:var(--accent)] opacity-60 animate-ping" />
                <span class="relative inline-flex h-1.5 w-1.5 rounded-full bg-[color:var(--accent)]" />
              </span>
              The token-efficient agentic coding workbench
            </div>

            {/* Headline */}
            <h1 class="text-center text-[32px] sm:text-[42px] md:text-[54px] font-bold tracking-tight text-zinc-50 leading-[1.05]
                       animate-fade-in-up max-w-3xl" style={{ 'animation-delay': '40ms' }}>
              Where everyone is a
              <span class="block mt-1 bg-gradient-to-r from-[color:var(--accent)] via-[#8b9cf7] to-[color:var(--accent)]
                           bg-clip-text text-transparent">
                software developer.
              </span>
            </h1>

            {/* Subheadline */}
            <p class="mt-5 max-w-xl text-center text-[15px] md:text-[16px] text-zinc-400 leading-relaxed
                      animate-fade-in-up" style={{ 'animation-delay': '80ms' }}>
              No gatekeepers, no boilerplate. Describe what you want in plain English and ogcode builds it —
              curating <em class="not-italic text-zinc-200 font-medium">relevant</em> context per turn, cutting
              <strong class="text-zinc-100 font-semibold"> 70%+ of tokens</strong>, and letting conversations run
              <strong class="text-zinc-100 font-semibold"> effectively forever</strong>.
            </p>
          </section>

          {/* ── Prompt input ────────────────────────────────────── */}
          <section class="relative flex flex-col items-center px-4 sm:px-6 pb-2">
            <form onSubmit={handleSubmit} class="w-full max-w-2xl animate-fade-in-up" style={{ 'animation-delay': '120ms' }}>
              <div class="rounded-2xl border border-[color:var(--border-default)] bg-[color:var(--bg-surface)]
                          shadow-lg shadow-black/30 transition-all var(--spring-md) focus-within:border-[color:var(--accent)]/50
                          focus-within:shadow-[color:var(--accent)]/5 focus-within:shadow-2xl">
                <textarea
                  ref={textareaRef}
                  value={text()}
                  onInput={(e) => setText(e.currentTarget.value)}
                  onKeyDown={handleKeyDown}
                  placeholder="Describe what you want to build…"
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
            <div class="mt-5 flex flex-wrap items-center justify-center gap-2 animate-fade-in-up" style={{ 'animation-delay': '160ms' }}>
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

            {/* Keyboard hint + workspace path. Hint keys are desktop-only. */}
            <div class="mt-5 flex items-center justify-center gap-3 text-[10.5px] text-zinc-600 composer-hints">
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
              <div class="mt-3 flex items-center justify-center max-w-full px-4">
                <span class="font-mono text-[10.5px] text-zinc-600 truncate max-w-[280px]">
                  {server.directory()}
                </span>
              </div>
            </Show>
          </section>

          {/* ── Value pillars ───────────────────────────────────── */}
          <section class="relative px-6 pt-16 pb-12 max-w-4xl mx-auto w-full">
            <div class="text-center mb-8 animate-fade-in-up" style={{ 'animation-delay': '200ms' }}>
              <h2 class="text-[13px] font-semibold uppercase tracking-[0.18em] text-zinc-500">
                Powered by Agentic Loop Memory
              </h2>
            </div>
            <div class="grid grid-cols-1 md:grid-cols-3 gap-4 anim-stagger">
              <For each={PILLARS}>
                {(p) => (
                  <div class="group relative rounded-2xl border border-[color:var(--border-default)]
                              bg-[color:var(--bg-surface)]/50 p-5 transition-all var(--spring-md)
                              hover:border-[color:var(--accent)]/30 hover:bg-[color:var(--bg-surface)]">
                    <div class="absolute top-5 right-5 text-right">
                      <div class="text-[28px] font-bold leading-none bg-gradient-to-br from-[color:var(--accent)] to-[#8b9cf7] bg-clip-text text-transparent">
                        {p.stat}
                      </div>
                      <div class="text-[9.5px] text-zinc-600 mt-1 max-w-[90px] leading-tight">{p.statLabel}</div>
                    </div>
                    <div class="w-9 h-9 rounded-xl bg-[color:var(--accent-soft)] border border-[color:var(--accent)]/15
                                flex items-center justify-center text-[color:var(--accent)] mb-4
                                group-hover:scale-110 transition-transform var(--spring-md)">
                      <PillarIcon name={p.icon} />
                    </div>
                    <h3 class="text-[14px] font-semibold text-zinc-100 mb-1.5">{p.title}</h3>
                    <p class="text-[12px] text-zinc-500 leading-relaxed pr-16">{p.desc}</p>
                  </div>
                )}
              </For>
            </div>
          </section>

          {/* ── Workflow steps ──────────────────────────────────── */}
          <section class="relative px-6 pb-16 max-w-4xl mx-auto w-full">
            <div class="rounded-2xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)]/30 overflow-hidden">
              <div class="flex items-center justify-between px-6 py-4 border-b border-[color:var(--border-subtle)]">
                <div class="flex items-center gap-2">
                  <svg class="w-4 h-4 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-3 7l2 2 4-4" />
                  </svg>
                  <h2 class="text-[13px] font-semibold text-zinc-300">Plan Mode → Parallel PRs</h2>
                </div>
                <button
                  type="button"
                  onClick={() => navigate('/plan')}
                  class="text-[11px] font-medium text-[color:var(--accent)] hover:text-[#8b9cf7] transition-colors var(--spring-sm)"
                >
                  Start a plan →
                </button>
              </div>
              <div class="grid grid-cols-1 md:grid-cols-3 divide-y md:divide-y-0 md:divide-x divide-[color:var(--border-subtle)]">
                <For each={STEPS}>
                  {(s) => (
                    <div class="p-5">
                      <div class="flex items-center gap-2.5 mb-2">
                        <span class="text-[10px] font-mono font-semibold text-[color:var(--accent)] bg-[color:var(--accent-soft)] px-1.5 py-0.5 rounded">
                          {s.n}
                        </span>
                        <span class="text-[13px] font-semibold text-zinc-200">{s.title}</span>
                      </div>
                      <p class="text-[12px] text-zinc-500 leading-relaxed">{s.desc}</p>
                    </div>
                  )}
                </For>
              </div>
            </div>
          </section>

          {/* ── Footer ──────────────────────────────────────────── */}
          <footer class="relative px-6 pb-8 pt-2 flex flex-col items-center gap-3">
            <div class="flex items-center gap-5 text-[11px] text-zinc-600">
              <a href="https://github.com/prasenjeet-symon/ogcode" target="_blank" rel="noopener"
                 class="hover:text-zinc-300 transition-colors var(--spring-sm) flex items-center gap-1.5">
                <svg class="w-3.5 h-3.5" fill="currentColor" viewBox="0 0 24 24">
                  <path d="M12 .5A11.5 11.5 0 00.5 12a11.5 11.5 0 007.86 10.94c.58.1.79-.25.79-.56v-2c-3.2.7-3.88-1.37-3.88-1.37-.53-1.34-1.3-1.7-1.3-1.7-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.2 1.77 1.2 1.03 1.77 2.7 1.26 3.36.96.1-.75.4-1.26.73-1.55-2.55-.3-5.24-1.28-5.24-5.67 0-1.25.45-2.27 1.18-3.07-.12-.3-.51-1.46.11-3.05 0 0 .96-.3 3.15 1.17a10.9 10.9 0 015.74 0c2.19-1.47 3.15-1.17 3.15-1.17.62 1.59.23 2.75.11 3.05.74.8 1.18 1.82 1.18 3.07 0 4.4-2.69 5.37-5.25 5.66.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0023.5 12 11.5 11.5 0 0012 .5z" />
                </svg>
                GitHub
              </a>
              <span class="text-zinc-700">·</span>
              <a href="https://discord.gg/JQP9t8y2Zv" target="_blank" rel="noopener"
                 class="hover:text-zinc-300 transition-colors var(--spring-sm) flex items-center gap-1.5">
                <svg class="w-3.5 h-3.5" fill="currentColor" viewBox="0 0 24 24">
                  <path d="M20.3 4.4A19.8 19.8 0 0015.4 3l-.3.5c1.5.4 2.7.9 4 1.7a13.6 13.6 0 00-11.9 0c1.2-.8 2.6-1.3 4-1.7L10.6 3a19.8 19.8 0 00-4.9 1.4C2.5 9.1 1.7 13.7 2.1 18.3a20 20 0 006 3l.5-.7c-1-.4-2-1-2.8-1.6l.7-.5a14.2 14.2 0 0012.7 0l.7.5c-.9.6-1.8 1.2-2.8 1.6l.5.7a20 20 0 006-3c.5-5.3-.8-9.9-3.3-14zM8.5 15c-.9 0-1.7-.9-1.7-2s.7-2 1.7-2 1.7.9 1.7 2-.8 2-1.7 2zm7 0c-.9 0-1.7-.9-1.7-2s.7-2 1.7-2 1.7.9 1.7 2-.8 2-1.7 2z" />
                </svg>
                Discord
              </a>
              <span class="text-zinc-700">·</span>
              <span class="font-mono">MIT</span>
            </div>
            <div class="text-[10px] text-zinc-700">
              Single binary · Self-hosted · Your code never leaves your machine
            </div>
          </footer>
        </div>
      </div>
    </div>
  );
}