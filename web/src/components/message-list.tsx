import { Index, Show, createEffect, on, createSignal } from 'solid-js';
import { useSession } from '../context/session';
import MessageItem from './message-item';
import { createChatScroll } from '../lib/chat-scroll';
import JumpToLatest from './jump-to-latest';
import Logo from './logo';

function isToolResultMessage(msg: any): boolean {
  if (msg.info.role !== 'user') return false;
  const parts = msg.parts || [];
  return parts.length > 0 && parts.every((p: any) => p.type === 'tool');
}

function isEmptyInProgress(msg: any): boolean {
  if (msg.info.role !== 'assistant') return false;
  if (msg.info.finish || msg.info.error) return false;
  return (msg.parts || []).length === 0;
}

export default function MessageList() {
  const session = useSession();
  const [unreadCount, setUnreadCount] = createSignal(0);
  // Messages already seen. Unread is (total - readMarker); without this the
  // badge showed the whole conversation's length the moment you scrolled up.
  const [readMarker, setReadMarker] = createSignal(0);

  const visibleMessages = () => {
    const activeId = session.activeSession()?.id;
    return session.messages()
      .filter((msg: any) => msg.info.sessionId === activeId)
      .filter((msg: any) => !isToolResultMessage(msg) && !isEmptyInProgress(msg));
  };

  // Follows new content only while the view is at the bottom, and holds the
  // reading position anywhere else. See lib/chat-scroll.ts.
  const scroll = createChatScroll({
    key: () => {
      const id = session.activeSession()?.id || '';
      return id ? `chat:${id}` : '';
    },
    onAtBottom: () => {
      setUnreadCount(0);
      setReadMarker(visibleMessages().length);
    },
  });

  // Restore scroll once messages first appear after mount/navigation.
  createEffect(on(
    () => visibleMessages().length,
    (count) => { if (count > 0) scroll.restore(); },
  ));

  // When the session changes, reset state and stick to bottom.
  createEffect(on(
    () => session.activeSession()?.id,
    () => {
      scroll.reset();
      setUnreadCount(0);
      setReadMarker(0);
    },
  ));

  // Follow new content during streaming, or count what arrived while away.
  createEffect(on(
    () => {
      const msgs = session.messages();
      const last = msgs[msgs.length - 1];
      let tailMark = 0;
      if (last?.parts) {
        for (const p of last.parts) {
          if (p.updatedAt > tailMark) tailMark = p.updatedAt;
        }
      }
      const loadingKey = session.loading() || session.hasRunningTools() ? '1' : '0';
      return msgs.length + ':' + tailMark + ':' + loadingKey;
    },
    (_curr, prev) => {
      if (prev === undefined && !scroll.hasRestored()) return;
      if (scroll.stickToBottom()) {
        scroll.follow();
        setReadMarker(visibleMessages().length);
      } else {
        // User scrolled up — count only messages that arrived since they left.
        setUnreadCount(Math.max(0, visibleMessages().length - readMarker()));
      }
    },
  ));


  const scrollToBottom = () => {
    scroll.jumpToBottom();
    setUnreadCount(0);
    setReadMarker(visibleMessages().length);
  };

  return (
    <div class="flex-1 min-h-0 relative flex flex-col">
      <div ref={scroll.attachScroll} class="chat-scroll flex-1 overflow-y-auto">
        {/* Spacing is rhythmic rather than uniform (see .chat-flow): a wide gap
            opens before each new user prompt, a medium one under it, and the
            agent's own run of tool calls and replies stays tightly packed —
            so the transcript reads as turns, not as an evenly spaced list. */}
        <div ref={scroll.attachContent} class="chat-col chat-flow px-4 md:px-8 pt-6 pb-4">
          <Show when={visibleMessages().length === 0 && !session.loading()}>
            <div class="flex flex-col items-center justify-center py-24 text-center animate-fade-in-up">
              <div class="w-11 h-11 rounded-xl bg-[color:var(--accent-soft)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-3.5">
                <Logo class="w-6 h-6 text-[color:var(--accent)]" />
              </div>
              <p class="text-ui font-medium text-[color:var(--text-primary)] mb-1">Ready when you are</p>
              <p class="text-meta text-[color:var(--text-tertiary)]">Describe a task, ask a question, or paste an error.</p>
            </div>
          </Show>

          <Index each={visibleMessages()}>
            {(msg) => <MessageItem msg={msg()} />}
          </Index>

          {/* Working indicator — a swept label rather than a spinner or avatar,
              so it sits in the flow of the transcript at the exact spot the
              answer will appear, and says which of the two states we're in. */}
          <Show when={session.loading() || session.hasRunningTools()}>
            <div class="flex items-center gap-2 h-7 animate-fade-in" aria-live="polite">
              <div class="thinking-dots">
                <span></span>
                <span></span>
                <span></span>
              </div>
              <span class="sweep-text text-meta font-medium">
                {session.hasRunningTools() ? 'Running tools' : 'Thinking'}
              </span>
            </div>
          </Show>

          {/* The loop stopped and nothing in the transcript says why — a server
              error before any assistant message existed, or a panic. Sits at the
              same spot as the working indicator it replaces, so the eye lands on
              it where it was already waiting for the answer. */}
          <Show when={session.loopError()}>
            {(failure) => (
              <div
                class="flex items-start gap-2 rounded-md border border-red-800/40 bg-red-950/30 px-3 py-2 text-meta text-red-300 animate-fade-in"
                role="alert"
              >
                <svg class="w-3.5 h-3.5 shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                </svg>
                <div class="min-w-0 flex-1">
                  <div class="font-medium">
                    {failure().reason === 'panic'
                      ? 'The agent loop crashed'
                      : 'The agent loop stopped early'}
                  </div>
                  <div class="mt-0.5 break-words opacity-90">{failure().message}</div>
                </div>
                <button
                  type="button"
                  class="shrink-0 opacity-60 hover:opacity-100"
                  aria-label="Dismiss"
                  onClick={() => session.dismissLoopError()}
                >
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </div>
            )}
          </Show>

        </div>

      </div>

      {/* Anchored to the message column and just above the composer, so it
          centres on the conversation and never overlaps a grown input. */}
      <Show when={scroll.isScrolledUp()}>
        <div class="pointer-events-none absolute inset-x-0 bottom-3 flex justify-center">
          <JumpToLatest count={unreadCount()} onClick={scrollToBottom} />
        </div>
      </Show>
    </div>
  );
}