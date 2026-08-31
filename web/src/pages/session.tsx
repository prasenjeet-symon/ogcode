import { useParams, useNavigate, useLocation } from '@solidjs/router';
import { useSession } from '../context/session';
import { useServer } from '../context/server';
import { createEffect, createMemo, createSignal, on, Show } from 'solid-js';
import MessageList from '../components/message-list';
import PromptInput from '../components/prompt-input';
import SessionSidebar from '../components/session-sidebar';
import TokenPill from '../components/token-pill';
import ResourcePill from '../components/resource-pill';
import MemoryDialog from '../components/memory-dialog';
import SubagentIndicator from '../components/subagent-indicator';
import { getProviderPricing } from '../api/client';
import { NotFoundPanel } from './not-found';

function getModelLabel(model: string | undefined): string {
  if (!model) return '';
  const parts = model.split('/');
  const name = parts[parts.length - 1];
  return name.replace(/-\d{4}-\d{2}-\d{2}$/, '').replace(/-preview$/, '');
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

  // Compute total tokens consumed this session (for MemoryDialog)
  const sessionTotalTokens = createMemo(() => {
    let total = 0;
    for (const m of session.messages()) {
      const t = m.info.tokens;
      if (!t) continue;
      total += (t.input ?? 0) + (t.cacheRead ?? 0) + (t.cacheWrite ?? 0) + (t.output ?? 0);
    }
    return total;
  });

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
      <Show
        when={!session.sessionMissing()}
        fallback={
          <NotFoundPanel
            title="Session not found"
            message="This session no longer exists. It may have been deleted from another window."
          />
        }
      >
      <div class="page-enter flex-1 flex flex-col min-w-0 bg-[color:var(--bg-base)]">
        {/* Header */}
        <header class="h-11 shrink-0 border-b border-[color:var(--border-subtle)] flex items-center px-3.5 gap-3 backdrop-blur-md overflow-visible" style={{ background: 'linear-gradient(var(--tint), var(--tint)) rgba(15,15,18,0.82)', 'z-index': 100 }}>
          <div class="flex items-baseline gap-2.5 min-w-0 flex-1">
            <h2 class="text-ui font-medium text-[color:var(--text-primary)] truncate">
              {session.activeSession()?.title || 'New session'}
            </h2>
            {/* Live state lives next to the title rather than in the transcript
                header so it stays visible when the user has scrolled away from
                the tail of the conversation. */}
            <Show when={session.loading() || session.hasRunningTools()}>
              <span class="flex items-baseline gap-1.5 shrink-0">
                <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] animate-pulse self-center" />
                <span class="sweep-text text-micro font-medium">
                  {session.hasRunningTools() ? 'running tools' : 'generating'}
                </span>
              </span>
            </Show>
          </div>

          <div class="flex items-center gap-1.5 shrink-0">
            <SubagentIndicator />
            <TokenPill />
            <ResourcePill />
            <Show when={session.activeSession()?.model}>
              <span
                class="text-micro text-[color:var(--text-secondary)] bg-[color:var(--bg-elevated)] h-7 inline-flex items-center px-2 rounded-md border border-[color:var(--border-subtle)] font-medium max-w-[11rem] truncate"
                title={session.activeSession()?.model}
              >
                {getModelLabel(session.activeSession()?.model)}
              </span>
            </Show>
            <Show when={server.memoryEnabled()}>
              <MemoryDialog
                savedTokens={session.memorySavedTokens()}
                totalTokens={sessionTotalTokens()}
                model={session.activeSession()?.model ?? ''}
                dynamicPrices={dynamicPrices()}
                models={session.models()}
              />
            </Show>
            <button
              type="button"
              onClick={() => navigate('/settings', { state: { from: location.pathname } })}
              class="icon-btn transition-colors"
              title="Settings"
              aria-label="Settings"
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
          <div class="shrink-0 flex items-center gap-2 px-4 py-1.5 text-meta text-amber-300 bg-amber-400/10 border-b border-amber-400/20 animate-slide-down">
            <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            Context auto-compacted — conversation history trimmed to fit model context window.
          </div>
        </Show>

        {/* Messages */}
        <MessageList />

        {/* Input (the tool-permission prompt now surfaces inside the composer) */}
        <PromptInput />
      </div>
      </Show>
    </div>
  );
}
