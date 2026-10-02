import { useParams } from '@solidjs/router';
import { useSession } from '../context/session';
import { createEffect, createMemo, createSignal, on, Show } from 'solid-js';
import MessageList from '../components/message-list';
import PromptInput from '../components/prompt-input';
import SessionSidebar from '../components/session-sidebar';
import TokenPill from '../components/token-pill';
import ContextMeter from '../components/context-meter';
import ResourcePill from '../components/resource-pill';
import SubagentIndicator from '../components/subagent-indicator';
import { DrawerToggle } from '../components/sidebar-shell';
import AskUserDialog from '../components/ask-user-dialog';
import { NotFoundPanel } from './not-found';

export default function Chat() {
  return <ChatContent />;
}

function ChatContent() {
  const session = useSession();
  const params = useParams();

  createEffect(on(() => params.id, (id) => {
    if (id) {
      session.selectSession(id);
    }
  }));

  return (
    <div class="flex h-dvh w-full">
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
        {/* Header. On phones the ambient pills (resource sparkline, token
            breakdown) collapse into what fits; everything keeps the same
            order so muscle memory transfers. */}
        <header class="h-11 shrink-0 border-b border-[color:var(--border-subtle)] flex items-center px-2 sm:px-3.5 gap-2 backdrop-blur-md overflow-visible" style={{ background: 'linear-gradient(var(--tint), var(--tint)) rgba(15,15,18,0.82)', 'z-index': 100, [ 'padding-top']: 'env(safe-area-inset-top)' }}>
          <DrawerToggle drawer="sessions" label="Open sessions" />
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
            <ContextMeter />
            <TokenPill />
            <ResourcePill />
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

      {/* The agent's question, if it is blocked on one. Rendered above the chat
          rather than inside it: the batch belongs to the session, not the
          transcript position, and must stay visible wherever the user scrolled. */}
      <AskUserDialog />
    </div>
  );
}
