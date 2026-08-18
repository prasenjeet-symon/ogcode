import { Index, Show, createEffect, on, onMount, onCleanup, createSignal } from 'solid-js';
import { useSession } from '../context/session';
import MessageItem from './message-item';
import { saveScroll, getScroll } from '../lib/scroll-memory';
import JumpToLatest from './jump-to-latest';

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
  let scrollRef: HTMLDivElement | undefined;
  let bottomAnchor: HTMLDivElement | undefined;
  let restored = false;
  const [isScrolledUp, setIsScrolledUp] = createSignal(false);
  const [unreadCount, setUnreadCount] = createSignal(0);
  // Messages already seen. Unread is (total - readMarker); without this the
  // badge showed the whole conversation's length the moment you scrolled up.
  const [readMarker, setReadMarker] = createSignal(0);

  // Whether the user is "stuck to the bottom" — true when they're at the
  // bottom and should auto-scroll with new content. Stays true during
  // streaming as long as the user doesn't scroll up.
  const [stickToBottom, setStickToBottom] = createSignal(true);

  const scrollKey = () => {
    const id = session.activeSession()?.id || '';
    return id ? `chat:${id}` : '';
  };

  const visibleMessages = () => {
    const activeId = session.activeSession()?.id;
    return session.messages()
      .filter((msg: any) => msg.info.sessionId === activeId)
      .filter((msg: any) => !isToolResultMessage(msg) && !isEmptyInProgress(msg));
  };

  // Detect whether the user is near the bottom (within 80px threshold).
  const checkNearBottom = () => {
    if (!scrollRef) return false;
    return scrollRef.scrollHeight - scrollRef.scrollTop - scrollRef.clientHeight < 80;
  };

  // Track scroll position and update stickiness
  onMount(() => {
    if (!scrollRef) return;
    const handler = () => {
      if (!scrollRef) return;
      const key = scrollKey();
      if (key) saveScroll(key, scrollRef.scrollTop);

      const nearBottom = checkNearBottom();
      setIsScrolledUp(!nearBottom);
      setStickToBottom(nearBottom);
      if (nearBottom) {
        setUnreadCount(0);
        setReadMarker(visibleMessages().length);
      }
    };
    scrollRef.addEventListener('scroll', handler, { passive: true });
    onCleanup(() => scrollRef?.removeEventListener('scroll', handler));
  });

  // Restore scroll once messages first appear after mount/navigation.
  createEffect(on(
    () => visibleMessages().length,
    (count) => {
      if (restored || !scrollRef || count === 0) return;
      const key = scrollKey();
      const saved = key ? getScroll(key) : 0;
      requestAnimationFrame(() => {
        if (!scrollRef) return;
        if (saved > 0) {
          scrollRef.scrollTop = saved;
        } else {
          bottomAnchor?.scrollIntoView({ behavior: 'instant' });
        }
        restored = true;
      });
    },
  ));

  // When the session changes, reset state and stick to bottom.
  createEffect(on(
    () => session.activeSession()?.id,
    () => {
      restored = false;
      setStickToBottom(true);
      setIsScrolledUp(false);
      setUnreadCount(0);
      setReadMarker(0);
    },
  ));

  // Auto-scroll when new content arrives during streaming.
  // Only scrolls if stickToBottom is true (user is at the bottom).
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
      if (!scrollRef) return;
      if (prev === undefined && !restored) return;

      if (stickToBottom()) {
        // Use scrollIntoView on the bottom anchor — much more reliable than
        // setting scrollTop because the DOM has already updated by the time
        // this effect runs (SolidJS synchronous rendering).
        requestAnimationFrame(() => {
          bottomAnchor?.scrollIntoView({ behavior: 'instant' });
        });
        setReadMarker(visibleMessages().length);
      } else {
        // User scrolled up — count only messages that arrived since they left.
        setUnreadCount(Math.max(0, visibleMessages().length - readMarker()));
      }
    },
  ));


  const scrollToBottom = () => {
    if (bottomAnchor) {
      bottomAnchor.scrollIntoView({ behavior: 'smooth' });
    }
    setStickToBottom(true);
    setIsScrolledUp(false);
    setUnreadCount(0);
    setReadMarker(visibleMessages().length);
  };

  return (
    <div class="flex-1 min-h-0 relative flex flex-col">
      <div ref={scrollRef} class="flex-1 overflow-y-auto">
        <div class="max-w-3xl mx-auto px-4 md:px-6 py-6 space-y-6">
          <Show when={visibleMessages().length === 0 && !session.loading()}>
            <div class="flex flex-col items-center justify-center py-24 text-center">
              <div class="w-14 h-14 rounded-2xl bg-[color:var(--accent-soft)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-4">
                <svg class="w-6 h-6 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.6">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
                </svg>
              </div>
              <p class="text-[14px] font-medium text-zinc-300 mb-1">Ready when you are</p>
              <p class="text-[12px] text-zinc-500">Describe a task, ask a question, or paste an error.</p>
            </div>
          </Show>

          <Index each={visibleMessages()}>
            {(msg) => <MessageItem msg={msg()} />}
          </Index>

          {/* Thinking indicator */}
          <Show when={session.loading() || session.hasRunningTools()}>
            <div class="flex gap-3 animate-fade-in">
              <div class="w-7 h-7 shrink-0 rounded-lg bg-[color:var(--accent)] flex items-center justify-center shadow-sm">
                <svg class="w-3.5 h-3.5 text-[color:var(--on-primary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
                </svg>
              </div>
              <div class="flex items-center py-1.5">
                <div class="thinking-dots">
                  <span></span>
                  <span></span>
                  <span></span>
                </div>
              </div>
            </div>
          </Show>

          {/* Bottom anchor for auto-scroll */}
          <div ref={bottomAnchor} />
        </div>

      </div>

      {/* Anchored to the message column and just above the composer, so it
          centres on the conversation and never overlaps a grown input. */}
      <Show when={isScrolledUp()}>
        <div class="pointer-events-none absolute inset-x-0 bottom-3 flex justify-center">
          <JumpToLatest count={unreadCount()} onClick={scrollToBottom} />
        </div>
      </Show>
    </div>
  );
}