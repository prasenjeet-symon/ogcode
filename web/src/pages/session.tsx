import { useParams, useNavigate, useLocation } from '@solidjs/router';
import { useSession } from '../context/session';
import { useServer } from '../context/server';
import { createEffect, createSignal, on, Show } from 'solid-js';
import MessageList from '../components/message-list';
import PromptInput from '../components/prompt-input';
import SessionSidebar from '../components/session-sidebar';
import TokenPill from '../components/token-pill';
import { getProviderPricing } from '../api/client';

function getModelLabel(model: string | undefined): string {
  if (!model) return '';
  const parts = model.split('/');
  const name = parts[parts.length - 1];
  return name.replace(/-\d{4}-\d{2}-\d{2}$/, '').replace(/-preview$/, '');
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

function estimateCost(tokens: number, model: string, dynamicPrices: Record<string, number>, models: { id: string; inputPricePerM: number }[]): string | null {
  // Dynamic prices (from real-time API) take precedence for OpenRouter/Ollama.
  const base = model.split('/').pop() ?? model;
  const dynPrice = dynamicPrices[model] ?? dynamicPrices[base];
  if (dynPrice) {
    const cost = (tokens / 1_000_000) * dynPrice;
    if (cost < 0.0001) return null;
    return cost < 0.01 ? `$${cost.toFixed(4)}` : `$${cost.toFixed(3)}`;
  }
  // Fallback to catalog prices from the model list.
  const info = models.find((m) => m.id === model) ?? models.find((m) => m.id === base);
  if (info && info.inputPricePerM > 0) {
    const cost = (tokens / 1_000_000) * info.inputPricePerM;
    if (cost < 0.0001) return null;
    return cost < 0.01 ? `$${cost.toFixed(4)}` : `$${cost.toFixed(3)}`;
  }
  return null;
}

function shortenPath(path: string): string {
  if (!path) return '';
  const home = path.match(/^\/(Users|home)\/[^/]+/);
  const collapsed = home ? path.replace(home[0], '~') : path;
  const segments = collapsed.split('/').filter(Boolean);
  if (segments.length <= 3) return collapsed;
  return `${collapsed.startsWith('~') ? '~' : ''}/…/${segments.slice(-2).join('/')}`;
}

export default function Chat() {
  return <ChatContent />;
}

function ChatContent() {
  const session = useSession();
  const server = useServer();
  const params = useParams();
  const navigate = useNavigate();
  const location = useLocation();

  // Real-time pricing for OpenRouter and Ollama providers.
  const [dynamicPrices, setDynamicPrices] = createSignal<Record<string, number>>({});

  createEffect(on(
    () => {
      const model = session.activeSession()?.model;
      const info = model ? session.models().find(m => m.id === model) : undefined;
      return info?.providerId ?? '';
    },
    (provider) => {
      if (provider === 'openrouter' || provider === 'ollama') {
        getProviderPricing(provider)
          .then(setDynamicPrices)
          .catch(() => {});
      }
    }
  ));

  createEffect(on(() => params.id, (id) => {
    if (id) {
      session.selectSession(id);
    }
  }));

  return (
    <div class="flex h-screen w-full">
      <SessionSidebar />
      <div class="flex-1 flex flex-col min-w-0 bg-[color:var(--bg-base)]">
        {/* Header */}
        <header class="h-11 shrink-0 border-b border-[color:var(--border-subtle)] flex items-center px-4 backdrop-blur-sm overflow-visible" style={{ background: 'linear-gradient(var(--tint), var(--tint)) rgba(15,15,18,0.8)', 'z-index': 100 }}>
          <div class="flex items-center gap-2 min-w-0 flex-1">
            <svg class="w-4 h-4 text-zinc-500 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M8 10h.01M12 10h.01M16 10h.01M9 16H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-5l-5 5v-5z" />
            </svg>
            <h2 class="text-[13px] font-medium text-zinc-100 truncate">
              {session.activeSession()?.title || 'New session'}
            </h2>
            <Show when={session.loading() || session.hasRunningTools()}>
              <span class="flex items-center gap-1 text-[11px] text-[color:var(--accent)] ml-1">
                <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] animate-pulse" />
                {session.hasRunningTools() ? 'running tools' : 'generating'}
              </span>
            </Show>
          </div>

          <div class="flex items-center gap-2 shrink-0">
            <TokenPill />
            <Show when={server.directory()}>
              <span
                title={server.directory()}
                class="hidden sm:flex items-center gap-1.5 text-[11px] text-zinc-500 px-2 py-1 rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] font-mono"
              >
                <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
                </svg>
                {shortenPath(server.directory())}
              </span>
            </Show>
            <Show when={session.activeSession()?.model}>
              <span class="text-[11px] text-zinc-400 bg-[color:var(--bg-elevated)] px-2 py-1 rounded-md border border-[color:var(--border-subtle)] font-medium">
                {getModelLabel(session.activeSession()?.model)}
              </span>
            </Show>
            <Show when={server.memoryEnabled()}>
              <span
                title={(() => {
                  const t = session.memorySavedTokens();
                  if (t > 0) return `Memory is saving ~${formatTokens(t)} tokens vs. sending full history`;
                  if (t < 0) return `Memory is adding ~${formatTokens(-t)} tokens of overhead — savings kick in as history grows`;
                  return 'Agentic memory active';
                })()}
                class={`flex items-center gap-1.5 text-[11px] px-2 py-1 rounded-md border font-medium
                  ${session.memorySavedTokens() > 0
                    ? 'text-emerald-400 bg-emerald-400/10 border-emerald-400/20'
                    : session.memorySavedTokens() < 0
                    ? 'text-amber-400 bg-amber-400/10 border-amber-400/20'
                    : 'text-zinc-400 bg-zinc-400/10 border-zinc-400/20'
                  }`}
              >
                <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.847.813a4.5 4.5 0 00-3.09 3.091z" />
                </svg>
                Memory
                <Show when={session.memorySavedTokens() > 0}>
                  <span class="text-emerald-500/70">·</span>
                  <span class="text-emerald-300">~{formatTokens(session.memorySavedTokens())} saved</span>
                  <Show when={estimateCost(session.memorySavedTokens(), session.activeSession()?.model ?? '', dynamicPrices(), session.models())}>
                    <span class="text-emerald-500/70 font-normal">
                      ({estimateCost(session.memorySavedTokens(), session.activeSession()?.model ?? '', dynamicPrices(), session.models())})
                    </span>
                  </Show>
                </Show>
                <Show when={session.memorySavedTokens() < 0}>
                  <span class="text-amber-500/70">·</span>
                  <span class="text-amber-300">~{formatTokens(-session.memorySavedTokens())} overhead</span>
                </Show>
              </span>
            </Show>
            <button
              type="button"
              onClick={() => navigate('/settings', { state: { from: location.pathname } })}
              class="w-7 h-7 flex items-center justify-center rounded-md text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)] transition-all var(--spring-sm) active:scale-[0.92]"
              title="Settings"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.325.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.241-.438.613-.43.992a7.723 7.723 0 010 .255c-.008.378.137.75.43.991l1.004.827c.424.35.534.955.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.47 6.47 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.281c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.019-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.991a6.932 6.932 0 010-.255c-.007-.38.138-.751.43-.992l1.004-.827a1.125 1.125 0 00.26-1.43l-1.298-2.247a1.125 1.125 0 00-1.37-.491l-1.216.456c-.356.133-.751.072-1.076-.124a6.47 6.47 0 01-.22-.128c-.331-.183-.581-.495-.644-.869l-.214-1.281z" />
                <path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
              </svg>
            </button>
          </div>
        </header>

        {/* Auto-compact notice */}
        <Show when={session.compacted()}>
          <div class="shrink-0 flex items-center gap-2 px-4 py-1.5 text-[12px] text-amber-300 bg-amber-400/10 border-b border-amber-400/20">
            <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            Context auto-compacted — conversation history trimmed to fit model context window.
          </div>
        </Show>

        {/* Messages */}
        <MessageList />

        {/* Input */}
        <PromptInput />
      </div>
    </div>
  );
}
