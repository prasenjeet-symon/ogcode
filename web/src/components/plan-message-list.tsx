import { Index, Show, createEffect, on, onMount, onCleanup, createSignal } from 'solid-js';
import { usePlan } from '../context/plan';
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

export default function PlanMessageList() {
  const plan = usePlan();
  let scrollRef: HTMLDivElement | undefined;
  let bottomAnchor: HTMLDivElement | undefined;
  let restored = false;
  const [isScrolledUp, setIsScrolledUp] = createSignal(false);
  const [unreadCount, setUnreadCount] = createSignal(0);
  // Messages already seen. Unread is (total - readMarker); without this the
  // badge showed the whole conversation's length the moment you scrolled up.
  const [readMarker, setReadMarker] = createSignal(0);
  const [stickToBottom, setStickToBottom] = createSignal(true);

  const scrollKey = () => {
    const id = plan.activePlan()?.id || '';
    return id ? `plan:${id}` : '';
  };

  const visibleMessages = () => {
    const activeId = plan.activePlan()?.sessionId;
    return plan.messages()
      .filter((msg: any) => msg.info.sessionId === activeId)
      .filter((msg: any) => !isToolResultMessage(msg) && !isEmptyInProgress(msg));
  };

  const checkNearBottom = () => {
    if (!scrollRef) return false;
    return scrollRef.scrollHeight - scrollRef.scrollTop - scrollRef.clientHeight < 80;
  };

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

  createEffect(on(
    () => plan.activePlan()?.id,
    () => {
      restored = false;
      setStickToBottom(true);
      setIsScrolledUp(false);
      setUnreadCount(0);
      setReadMarker(0);
    },
  ));

  createEffect(on(
    () => {
      const msgs = plan.messages();
      const last = msgs[msgs.length - 1];
      let tailMark = 0;
      if (last?.parts) {
        for (const p of last.parts) {
          if (p.updatedAt > tailMark) tailMark = p.updatedAt;
        }
      }
      const loadingKey = plan.loading() ? '1' : '0';
      return msgs.length + ':' + tailMark + ':' + loadingKey;
    },
    (_curr, prev) => {
      if (!scrollRef) return;
      if (prev === undefined && !restored) return;
      if (stickToBottom()) {
        requestAnimationFrame(() => {
          bottomAnchor?.scrollIntoView({ behavior: 'instant' });
        });
        setReadMarker(visibleMessages().length);
      } else {
        setUnreadCount(Math.max(0, visibleMessages().length - readMarker()));
      }
    },
  ));


  const scrollToBottom = () => {
    if (bottomAnchor) bottomAnchor.scrollIntoView({ behavior: 'smooth' });
    setStickToBottom(true);
    setIsScrolledUp(false);
    setUnreadCount(0);
    setReadMarker(visibleMessages().length);
  };

  return (
    <div class="flex-1 min-h-0 relative flex flex-col">
      <div ref={scrollRef} class="flex-1 overflow-y-auto">
        <div class="max-w-3xl mx-auto px-4 md:px-6 py-6 space-y-6">
          <Show when={visibleMessages().length === 0 && !plan.loading()}>
            <div class="flex flex-col items-center justify-center py-24 text-center">
              <div class="w-14 h-14 rounded-xl bg-[color:var(--accent-soft)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-4">
                <svg class="w-6 h-6 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.6">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                </svg>
              </div>
              <p class="text-[14px] font-medium text-zinc-300 mb-1">Start planning</p>
              <p class="text-[12px] text-zinc-500">Describe your project or requirement to begin.</p>
            </div>
          </Show>

          <Index each={visibleMessages()}>
            {(msg) => (
              <div class="anim-enter">
                <MessageItem msg={msg()} />
                <Show when={msg().info.role === 'assistant' && msg().info.error}>
                  <div class="flex gap-3">
                    <div class="w-7 h-7 shrink-0 rounded-lg bg-red-500/20 border border-red-500/30 flex items-center justify-center">
                      <svg class="w-3.5 h-3.5 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v4m0 4h.01M12 3a9 9 0 100 18A9 9 0 0012 3z" />
                      </svg>
                    </div>
                    <div class="flex-1 min-w-0 py-1">
                      <p class="text-[13px] text-red-400 font-medium">Agent error</p>
                      <p class="text-[12px] text-red-400/70 mt-0.5 break-all">{msg().info.error}</p>
                    </div>
                  </div>
                </Show>
              </div>
            )}
          </Index>

          <Show when={plan.loading()}>
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