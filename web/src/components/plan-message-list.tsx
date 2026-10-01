import { For, Show, createEffect, createMemo, on, createSignal, onCleanup } from 'solid-js';
import { usePlan } from '../context/plan';
import MessageItem from './message-item';
import { createChatScroll } from '../lib/chat-scroll';
import type { SavedScroll } from '../lib/scroll-memory';
import JumpToLatest from './jump-to-latest';
import LoadingOlder from './loading-older';
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

// Start fetching the page above once the reader is this close to the top, so
// it has usually landed before they get there.
const LOAD_OLDER_PX = 1500;

export default function PlanMessageList() {
  const plan = usePlan();
  const [unreadCount, setUnreadCount] = createSignal(0);
  // The newest message the reader has seen. Unread is what is newer than it,
  // so history paged in above never counts as news.
  const [lastSeenId, setLastSeenId] = createSignal('');

  const visibleMessages = createMemo(() => {
    const activeId = plan.activePlan()?.sessionId;
    return plan.messages()
      .filter((msg: any) => msg.info.sessionId === activeId)
      .filter((msg: any) => !isToolResultMessage(msg) && !isEmptyInProgress(msg));
  });

  // Rows are keyed by message id rather than by position, so a page of history
  // landing above keeps every row's element — and with it the element
  // chat-scroll holds the reading position on, which is all it takes to keep
  // the reader's place. Keyed by position, the same element would show a
  // different message after the prepend, and the view held on the wrong one.
  const byId = createMemo(() => {
    const m = new Map<string, any>();
    for (const msg of visibleMessages()) m.set(msg.info.id, msg);
    return m;
  });
  const keys = createMemo(
    () => visibleMessages().map((msg: any) => msg.info.id as string),
    undefined,
    { equals: (a, b) => a.length === b.length && a.every((k, i) => k === b[i]) },
  );

  const newestId = () => {
    const k = keys();
    return k.length ? k[k.length - 1] : '';
  };
  const markSeen = () => {
    setUnreadCount(0);
    setLastSeenId(newestId());
  };
  const countUnread = () => {
    const seen = lastSeenId();
    const k = keys();
    let n = 0;
    for (let i = k.length - 1; i >= 0 && k[i] > seen; i--) n++;
    return n;
  };

  // The rows render inside this element; the scroll restore below needs it
  // to look the anchor row up by key.
  let contentEl: HTMLElement | undefined;

  // Follows new content only while the view is at the bottom, and holds the
  // reading position anywhere else. See lib/chat-scroll.ts.
  const scroll = createChatScroll({
    key: () => {
      const id = plan.activePlan()?.id || '';
      return id ? `plan:${id}` : '';
    },
    onAtBottom: markSeen,
    // Put the view back on the anchor row the reader left it on. The anchor
    // can sit in a page this mount does not hold yet, so older pages are
    // fetched until it is present, then the view is placed on it.
    onRestore: (saved: SavedScroll): boolean => {
      if (!saved.anchorId || !scrollEl) return false;
      const place = (el: HTMLElement) => {
        if (!scrollEl) return;
        scrollEl.scrollTop = Math.max(0, el.offsetTop - saved.anchorOffset);
      };
      const find = () =>
        contentEl?.querySelector(`[data-vkey="${CSS.escape(saved.anchorId)}"]`) as HTMLElement | null;
      const el = find();
      if (el) {
        place(el);
        return true;
      }
      if (!plan.hasOlder()) return false;
      void (async () => {
        const planId = plan.activePlan()?.id;
        if (!planId) return;
        for (let guard = 0; guard < 20; guard++) {
          if (plan.loadingOlder()) break;
          const added = await plan.loadOlder();
          if (plan.activePlan()?.id !== planId) return;
          const found = find();
          if (found) {
            place(found);
            break;
          }
          if (!added) break;
        }
      })();
      return true;
    },
  });

  createEffect(on(
    () => visibleMessages().length,
    (count) => {
      if (count === 0) return;
      // Everything present on arrival counts as seen.
      if (!lastSeenId()) setLastSeenId(newestId());
      scroll.restore();
    },
  ));

  createEffect(on(
    () => plan.activePlan()?.id,
    () => {
      scroll.reset();
      setUnreadCount(0);
      setLastSeenId('');
      // A cached transcript is already in place when the plan flips, and its
      // length can equal the outgoing plan's — the length effect above would
      // then never re-run. Restore here as well; the restored flag makes
      // whichever effect gets there first the only one.
      if (visibleMessages().length > 0) scroll.restore();
    },
  ));

  // Fetch the previous page while the reader is near the top. Nothing else is
  // needed to keep their place: the rows land above, the rows on screen keep
  // their elements, and chat-scroll's anchor puts the one being read back
  // where it was before the frame is painted.
  let scrollEl: HTMLElement | undefined;
  let loadingOlderHere = false;
  const maybeLoadOlder = () => {
    if (!scrollEl || loadingOlderHere) return;
    if (scrollEl.scrollTop >= LOAD_OLDER_PX) return;
    if (!plan.hasOlder() || plan.loadingOlder()) return;
    loadingOlderHere = true;
    plan.loadOlder().finally(() => {
      loadingOlderHere = false;
    });
  };
  // At the very top a further wheel-up fires no scroll event; it should still
  // fetch what is above.
  const onWheel = (e: WheelEvent) => {
    if (e.deltaY < 0) maybeLoadOlder();
  };
  onCleanup(() => {
    scrollEl?.removeEventListener('scroll', maybeLoadOlder);
    scrollEl?.removeEventListener('wheel', onWheel);
  });

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
      if (prev === undefined && !scroll.hasRestored()) return;
      if (scroll.stickToBottom()) {
        scroll.follow();
        markSeen();
      } else {
        // Scrolled up — count only what is newer than the last message seen.
        setUnreadCount(countUnread());
      }
    },
  ));

  const scrollToBottom = () => {
    scroll.jumpToBottom();
    markSeen();
  };

  return (
    <div class="flex-1 min-h-0 relative flex flex-col">
      <div
        ref={(el) => {
          scrollEl = el;
          scroll.attachScroll(el);
          el.addEventListener('scroll', maybeLoadOlder, { passive: true });
          el.addEventListener('wheel', onWheel, { passive: true });
        }}
        class="chat-scroll flex-1 overflow-y-auto"
      >
        <div
          ref={(el) => {
            contentEl = el;
            scroll.attachContent(el);
          }}
          class="max-w-3xl mx-auto px-4 md:px-6 py-6 space-y-6"
        >
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

          <For each={keys()}>
            {(key) => {
              // The row outlives its message by at most one update — the key
              // leaving the list is what unmounts it — so it keeps showing the
              // last version it had rather than read a missing one.
              let last: any;
              const msg = () => {
                const m = byId().get(key);
                if (m) last = m;
                return last;
              };
              return (
                <div class="anim-enter" data-vkey={key}>
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
              );
            }}
          </For>

          <Show when={plan.loading()}>
            <div class="flex gap-3 animate-fade-in">
              <div class="w-7 h-7 shrink-0 rounded-lg bg-[color:var(--accent)] flex items-center justify-center shadow-sm">
                <Logo class="w-3.5 h-3.5 text-[color:var(--on-primary)]" small />
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
        </div>

      </div>

      <Show when={plan.loadingOlder()}>
        <LoadingOlder />
      </Show>

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