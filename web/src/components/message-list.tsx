import { Show, For, batch, createEffect, createMemo, on, createSignal, onMount, onCleanup } from 'solid-js';
import { useSession } from '../context/session';
import MessageItem from './message-item';
import { createChatScroll } from '../lib/chat-scroll';
import { SavedScroll } from '../lib/scroll-memory';
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

// How far outside the viewport rows are kept mounted. Generous enough that a
// fast flick never reaches an unrendered band before the window catches up,
// small enough that the mounted set stays in the tens.
const OVERSCAN_PX = 1200;

// Start fetching the page above once the reader is this close to the top, so
// it has landed before they get there. A page is tens of thousands of pixels
// of history, so asking early costs nothing; asking late is a wall at the top
// for as long as the page takes to arrive — through a tunnel, with a
// multi-megabyte page, that is most of a second.
const LOAD_OLDER_PX = 6000;

// The height assumed for a row before any row has been measured. Once rows
// have been measured, an unmeasured row is assumed to be the average of every
// row of its kind measured so far.
const DEFAULT_ROW_H = 96;

interface Row {
  key: string;
  msg: any;
  user: boolean;
}

interface Rows {
  list: Row[];
  byKey: Map<string, Row>;
  index: Map<string, number>;
}

// The row the reader is looking at, and the distance from the top of the
// scrollport to its top edge (negative when the row starts above it).
interface Anchor {
  key: string;
  offset: number;
}

const sameKeys = (a: string[], b: string[]) => a.length === b.length && a.every((k, i) => k === b[i]);
const sameAnchor = (a: Anchor | null, b: Anchor | null) =>
  a === b || (!!a && !!b && a.key === b.key && Math.abs(a.offset - b.offset) < 0.5);

/**
 * The transcript.
 *
 * A long conversation is thousands of DOM nodes — a 300-message session is
 * ~9k nodes and ~1s of layout on every switch. Only the rows near the viewport
 * are mounted here; the rest are represented by the two pad divs that keep the
 * scrollbar honest. `lib/chat-scroll.ts` still sees a flow of child rows, so
 * restore / follow / jump-to-latest work as before.
 *
 * The pads are estimates until their rows have been seen, so the layout above
 * the reader keeps changing: a row mounts at its real height in place of an
 * estimate, a measurement moves the estimate behind the pads, a page of
 * history lands, a diagram finishes rendering. None of that may move what the
 * reader is looking at. The row they are on is held instead (see holdReader):
 * after any change that is not their own scroll it is put back at the same
 * offset from the top of the scrollport, read off the DOM rather than the
 * height model, before the frame is painted.
 *
 * Rows are keyed by message id, never by position: a page of history landing
 * above, or a message updating as it streams, keeps every mounted row's DOM —
 * and so the row being held.
 *
 * Each row's rhythm gap (see .chat-flow in the stylesheet) is applied inline
 * rather than by CSS, because the row above it may be unmounted and the sibling
 * selectors cannot see it.
 */
export default function MessageList() {
  const session = useSession();
  const [unreadCount, setUnreadCount] = createSignal(0);
  // The newest message the reader has seen. Unread is what is newer than it,
  // so history paged in above never counts as news.
  const [lastSeenId, setLastSeenId] = createSignal('');

  const visibleMessages = createMemo(() => {
    const activeId = session.activeSession()?.id;
    return session.messages()
      .filter((msg: any) => msg.info.sessionId === activeId)
      .filter((msg: any) => !isToolResultMessage(msg) && !isEmptyInProgress(msg));
  });

  const rows = createMemo<Rows>(() => {
    const msgs = visibleMessages();
    const list = new Array<Row>(msgs.length);
    const byKey = new Map<string, Row>();
    const index = new Map<string, number>();
    for (let i = 0; i < msgs.length; i++) {
      const msg = msgs[i];
      const row = { key: msg.info.id, msg, user: msg.info.role === 'user' };
      list[i] = row;
      byKey.set(row.key, row);
      index.set(row.key, i);
    }
    return { list, byKey, index };
  });

  const newestId = () => {
    const { list } = rows();
    return list.length ? list[list.length - 1].key : '';
  };

  const markSeen = () => {
    setUnreadCount(0);
    setLastSeenId(newestId());
  };
  const countUnread = () => {
    const seen = lastSeenId();
    const { list } = rows();
    let n = 0;
    for (let i = list.length - 1; i >= 0 && list[i].key > seen; i--) n++;
    return n;
  };

  const [scrollTop, setScrollTop] = createSignal(0);
  const [viewportHeight, setViewportHeight] = createSignal(0);

  // The row the reader is on (see holdReader). Re-read from the DOM on every
  // scroll the reader makes; null while the view follows the bottom, where
  // chat-scroll pins it instead.
  const [reader, setReader] = createSignal<Anchor | null>(null, { equals: sameAnchor });

  // The scroll position as of the last frame: whatever the reader saw last,
  // after this list's own corrections. A scroll event's distance from it is
  // the reader's own scroll.
  let lastTop = 0;

  // Follows new content only while the view is at the bottom, and holds the
  // reading position anywhere else. See lib/chat-scroll.ts.
  const scroll = createChatScroll({
    key: () => {
      const id = session.activeSession()?.id || '';
      return id ? `chat:${id}` : '';
    },
    onAtBottom: markSeen,
    // A restore or a jump moves the view to a position of its own choosing:
    // mount the rows there in the same frame rather than paint the spacer for
    // one, and let the next scroll event read the row that is on screen.
    onWrite: (top) => {
      lastTop = top;
      batch(() => {
        setReader(null);
        setScrollTop(top);
      });
    },
    // This list holds the reader's row itself (holdReader), off the DOM, in
    // the same frame as every change; a second writer after it would move the
    // view twice per change.
    anchoredByList: true,
    // Put the view back on the anchor row the reader left it on. The anchor
    // can sit in history this mount does not hold yet, so older pages are
    // fetched until it is present, then the window is placed on it.
    onRestore: (saved) => restoreToAnchor(saved),
  });

  // Measured heights, keyed by message id. Non-reactive on purpose; the
  // version signal below is what schedules a re-layout.
  const heights = new Map<string, number>();
  const [heightsVersion, setHeightsVersion] = createSignal(0);

  // Heights from recently visited sessions, so a switch back starts with the
  // layout already accurate instead of estimating and jumping. The map holds
  // copies: heights above is swapped in place, so nothing aliases it.
  const heightsBySession = new Map<string, {
    heights: Map<string, number>;
    userSum: number;
    userN: number;
    otherSum: number;
    otherN: number;
  }>();

  // Running totals over every row measured so far, by kind, for the rows never
  // shown. The estimate must not follow the mounted window: one that did moved
  // the window, which changed the estimate, which moved the window back — two
  // layouts that alternated every frame and remounted rows forever at rest.
  // Totals over all measured rows only ever converge.
  let userSum = 0;
  let userN = 0;
  let otherSum = 0;
  let otherN = 0;
  const estimate = (user: boolean) => {
    if (user && userN) return userSum / userN;
    if (!user && otherN) return otherSum / otherN;
    const n = userN + otherN;
    return n ? (userSum + otherSum) / n : DEFAULT_ROW_H;
  };
  // Record a row's measured height; reports whether the layout changed.
  const record = (key: string, user: boolean, h: number) => {
    const prev = heights.get(key);
    if (prev !== undefined && Math.abs(prev - h) < 0.5) return false;
    if (user) {
      userSum += h - (prev ?? 0);
      if (prev === undefined) userN++;
    } else {
      otherSum += h - (prev ?? 0);
      if (prev === undefined) otherN++;
    }
    heights.set(key, h);
    return true;
  };

  // rem is viewport-scaled (see :root in the stylesheet), so the gaps are
  // resolved against it, and re-resolved when the viewport resizes.
  const rootRem = () => {
    const v = parseFloat(getComputedStyle(document.documentElement).fontSize);
    return Number.isFinite(v) && v > 0 ? v : 16;
  };
  const [rem, setRem] = createSignal(rootRem());

  // Cumulative layout: where each row's margin-box begins, the gap that
  // precedes it, and the total content height.
  const layout = createMemo(() => {
    heightsVersion();
    const r = rem();
    const { list } = rows();
    const tight = 0.625 * r;
    const afterUser = 1.125 * r;
    const beforeUser = 1.875 * r;

    const gaps = new Array<number>(list.length);
    const tops = new Array<number>(list.length);
    let acc = 0;
    for (let i = 0; i < list.length; i++) {
      let gap = 0;
      if (i > 0) {
        if (list[i].user) gap = beforeUser;
        else if (list[i - 1].user) gap = afterUser;
        else gap = tight;
      }
      gaps[i] = gap;
      tops[i] = acc;
      const h = heights.get(list[i].key);
      acc += gap + (h === undefined ? estimate(list[i].user) : h);
    }
    return { gaps, tops, total: acc };
  });

  // The mounted window, remembered as its first and last message ids so that a
  // page landing above (which shifts every index) still means the same rows.
  let held: { first: string; last: string } | null = null;

  // Where the viewport starts, in layout coordinates. While a row is held the
  // viewport is wherever that row puts it — holdReader is about to scroll it
  // there — so a change to the estimates above (a page landing, a measurement
  // moving the average) moves the window with the row instead of leaving it at
  // a pixel offset that now means different rows and unmounting the one held.
  const viewTop = () => {
    const a = reader();
    if (a && !scroll.stickToBottom()) {
      const i = rows().index.get(a.key);
      if (i !== undefined) {
        const lay = layout();
        return lay.tops[i] + lay.gaps[i] - a.offset;
      }
    }
    return scrollTop();
  };

  // The mounted window: rows within overscan of the viewport. It is kept as is
  // while it still covers the viewport with half the overscan to spare, so the
  // small shifts that follow a measurement mount and unmount nothing; it is
  // recomputed with the full overscan otherwise.
  const windowRange = createMemo(
    () => {
      const { list, index } = rows();
      const n = list.length;
      if (n === 0) {
        held = null;
        return { start: 0, end: 0 };
      }
      const { tops, total } = layout();
      const vh = viewportHeight();
      // Clamped so a position past the end (the "open at the bottom" default
      // below) mounts the last screenful rather than a single row.
      const top = Math.min(viewTop(), Math.max(0, total - vh));
      const bottom = top + vh;
      const bottomOf = (i: number) => (i + 1 < n ? tops[i + 1] : total);

      if (held) {
        const s = index.get(held.first);
        const e = index.get(held.last);
        if (s !== undefined && e !== undefined && s <= e) {
          const margin = OVERSCAN_PX / 2;
          const coversTop = s === 0 || tops[s] <= top - margin;
          const coversBottom = e === n - 1 || bottomOf(e) >= bottom + margin;
          if (coversTop && coversBottom) return { start: s, end: e + 1 };
        }
      }

      // Binary search keeps this cheap — it runs on every scroll frame.
      const above = top - OVERSCAN_PX;
      let lo = 0;
      let hi = n - 1;
      let start = 0;
      while (lo <= hi) {
        const mid = (lo + hi) >> 1;
        if (tops[mid] <= above) {
          start = mid;
          lo = mid + 1;
        } else {
          hi = mid - 1;
        }
      }

      const below = bottom + OVERSCAN_PX;
      lo = start;
      hi = n - 1;
      let end = n;
      while (lo <= hi) {
        const mid = (lo + hi) >> 1;
        if (tops[mid] > below) {
          end = mid;
          hi = mid - 1;
        } else {
          lo = mid + 1;
        }
      }
      end = Math.max(end, start + 1);
      held = { first: list[start].key, last: list[end - 1].key };
      return { start, end };
    },
    undefined,
    { equals: (a, b) => a.start === b.start && a.end === b.end },
  );

  const mountedKeys = createMemo(
    () => {
      const { start, end } = windowRange();
      const { list } = rows();
      const out = new Array<string>(end - start);
      for (let i = start; i < end; i++) out[i - start] = list[i].key;
      return out;
    },
    undefined,
    { equals: sameKeys },
  );

  const topPad = () => {
    const { start } = windowRange();
    return start > 0 ? layout().tops[start] : 0;
  };
  const bottomPad = () => {
    const { end } = windowRange();
    const lay = layout();
    return end < rows().list.length ? lay.total - lay.tops[end] : 0;
  };

  let scrollEl: HTMLElement | undefined;
  let contentEl: HTMLElement | undefined;

  // The mounted row elements by message id.
  const rowEls = new Map<string, HTMLElement>();

  // The row to hold, as the DOM has it right now: the first mounted row whose
  // top edge is on screen. Not the row cut by the top edge — that one is
  // mostly out of sight, and is usually the row that just scrolled in and is
  // still growing (a diagram rendering, a long prompt collapsing behind "Show
  // more"); holding its top would push everything the reader is looking at
  // down by the growth. Only when one row fills the whole screen is it held.
  const readAnchor = (): Anchor | null => {
    if (!scrollEl) return null;
    const top = scrollEl.getBoundingClientRect().top;
    const bottom = top + scrollEl.clientHeight;
    let cut: Anchor | null = null;
    for (const key of mountedKeys()) {
      const el = rowEls.get(key);
      if (!el) continue;
      const r = el.getBoundingClientRect();
      if (r.top >= bottom) break;
      if (r.top >= top) return { key, offset: r.top - top };
      if (r.bottom > top && !cut) cut = { key, offset: r.top - top };
    }
    return cut;
  };

  // Put the reader's row back where they last saw it. Read off the DOM, so it
  // is exact whatever the height model believes, and applied as a relative
  // scrollTop write, which composes with a scroll the compositor has applied
  // but not yet reported. Writes nothing reactive, so it is safe inside a
  // ResizeObserver callback.
  const holdReader = () => {
    if (!scrollEl) return;
    const a = reader();
    const el = a && !scroll.stickToBottom() ? rowEls.get(a.key) : undefined;
    if (a && el) {
      const delta = el.getBoundingClientRect().top - scrollEl.getBoundingClientRect().top - a.offset;
      if (Math.abs(delta) >= 0.5) scrollEl.scrollTop += delta;
    }
    lastTop = scrollEl.scrollTop;
  };

  // Bring the held row up to date before anything reads where the reader is.
  // Since the last frame it has moved by their own scroll and by nothing else:
  // a layout change that has not been painted yet — a diagram that finished
  // rendering, an image that decoded — is taken back out here, where reading
  // positions off the DOM afresh would take it in as where the reader is.
  const catchUpReader = () => {
    const a = reader();
    if (!a || !scrollEl) return;
    setReader({ key: a.key, offset: a.offset - (scrollEl.scrollTop - lastTop) });
    holdReader();
  };

  // New heights reach the layout — and so the pads, and the estimate behind
  // them — on the next frame, never inside the observer: re-rendering the pads
  // there would resize what is being observed mid-callback. The reader's row
  // is held across it in the same frame.
  let commitRaf = 0;
  const scheduleCommit = () => {
    if (commitRaf) return;
    commitRaf = requestAnimationFrame(() => {
      commitRaf = 0;
      setHeightsVersion((v) => v + 1);
      holdReader();
      if (scrollEl) setScrollTop(scrollEl.scrollTop);
    });
  };

  // Every mounted row is observed, so its height is known after the layout
  // that gives it one and before that layout is painted: the first observation
  // of a row just mounted carries its real height in place of the estimate, a
  // later one an image, a diagram or a disclosure that changed it.
  const rowObserver = new ResizeObserver((entries) => {
    const { byKey } = rows();
    let changed = false;
    for (const entry of entries) {
      const el = entry.target as HTMLElement;
      const key = el.dataset.vkey;
      if (!key || rowEls.get(key) !== el) continue;
      const row = byKey.get(key);
      if (!row) continue;
      const h = entry.borderBoxSize?.[0]?.blockSize ?? el.getBoundingClientRect().height;
      if (record(key, row.user, h)) changed = true;
    }
    holdReader();
    if (changed) scheduleCommit();
  });

  // Put the view back on the anchor row the reader left it on. Returns false
  // when this list cannot (no anchor, or history holds nothing older), so
  // chat-scroll falls back to its own restore logic. Otherwise that row is
  // held at its saved offset: the window mounts around it as soon as it is in
  // the transcript — older pages are fetched until it is — and holdReader puts
  // it in place once mounted. This outlives the rAF restore() calls it in,
  // which is fine: the hook already answered true and the fallback path is not
  // taken.
  const restoreToAnchor = (saved: SavedScroll): boolean => {
    if (!saved.anchorId || !scrollEl) return false;
    const sess = session.activeSession();
    if (!sess) return false;
    if (!rows().byKey.has(saved.anchorId) && !session.hasOlder()) return false;
    const sessionId = sess.id;
    lastTop = scrollEl.scrollTop;
    setReader({ key: saved.anchorId, offset: saved.anchorOffset });
    const place = () => {
      if (session.activeSession()?.id !== sessionId || !scrollEl) return;
      holdReader();
      setScrollTop(scrollEl.scrollTop);
    };
    const ensure = (): Promise<void> => {
      if (rows().byKey.has(saved.anchorId)) return Promise.resolve();
      if (!session.hasOlder() || session.loadingOlder()) return Promise.resolve();
      return session.loadOlder().then(ensure);
    };
    void ensure().then(place);
    return true;
  };

  // Rows mounted because the reader scrolled to them are history; only a
  // message that arrives while the session is open plays the entrance
  // animation. Undefined until the transcript first renders, so the opening
  // rows rise in as they always have.
  let freshAfter: string | undefined;

  // Restore scroll once messages first appear after mount/navigation.
  createEffect(on(
    () => visibleMessages().length,
    (count) => {
      if (count === 0) return;
      // Everything present on arrival counts as seen.
      if (!lastSeenId()) setLastSeenId(newestId());
      if (!scroll.hasRestored()) {
        scroll.restore();
        // After restore's own frame, so the rows it scrolls to still count as
        // the opening render.
        const id = session.activeSession()?.id;
        requestAnimationFrame(() => {
          if (session.activeSession()?.id === id) freshAfter = newestId();
        });
      }
    },
  ));

  // When the session changes, reset state and stick to bottom. Measurements
  // belong to the session they were taken in: the outgoing one's are stashed
  // for a later return, the incoming one's are restored when we have them.
  createEffect(on(
    () => session.activeSession()?.id,
    (id, prevId) => {
      if (prevId) {
        heightsBySession.delete(prevId);
        heightsBySession.set(prevId, {
          heights: new Map(heights),
          userSum,
          userN,
          otherSum,
          otherN,
        });
        if (heightsBySession.size > 8) {
          const oldest = heightsBySession.keys().next().value;
          if (oldest !== undefined) heightsBySession.delete(oldest);
        }
      }
      scroll.reset();
      setUnreadCount(0);
      setLastSeenId('');
      freshAfter = undefined;
      held = null;
      const cached = id ? heightsBySession.get(id) : undefined;
      if (cached) {
        heights.clear();
        for (const [k, v] of cached.heights) heights.set(k, v);
        userSum = cached.userSum;
        userN = cached.userN;
        otherSum = cached.otherSum;
        otherN = cached.otherN;
      } else {
        heights.clear();
        userSum = userN = otherSum = otherN = 0;
      }
      setHeightsVersion((v) => v + 1);
      // A conversation opens at the bottom unless a saved position says
      // otherwise, so start the window there (windowRange clamps this) and
      // mount the rows the reader will actually see first.
      setReader(null);
      setScrollTop(Number.MAX_SAFE_INTEGER);
      // A cached transcript is already in place when the session flips, and
      // its length can equal the outgoing session's — the length effect
      // above would then never re-run. Restore here as well; the restored
      // flag makes whichever effect gets there first the only one.
      if (visibleMessages().length > 0) {
        scroll.restore();
        // After restore's own frame, so the rows it scrolls to still count
        // as the opening render.
        requestAnimationFrame(() => {
          if (session.activeSession()?.id === id) freshAfter = newestId();
        });
      }
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
        markSeen();
      } else {
        // Scrolled up — count only what is newer than the last message seen.
        setUnreadCount(countUnread());
      }
    },
  ));

  // Fetch the previous page while the reader is near the top. The page lands
  // above the reader's row: the window is recomputed around that row as the
  // merge renders, and the row is put back where it was before the frame is
  // painted, so nothing is drawn at the old position.
  let loadingOlderHere = false;
  const maybeLoadOlder = () => {
    if (!scrollEl || loadingOlderHere) return;
    if (scrollEl.scrollTop >= LOAD_OLDER_PX) return;
    if (!session.hasOlder() || session.loadingOlder()) return;
    loadingOlderHere = true;
    session
      .loadOlder((merge) => {
        // The view may have moved since the last scroll event; holding the
        // row where that event left it would pull the view back.
        catchUpReader();
        merge();
      })
      .then(() => {
        // The batch has rendered the taller content. Still before the next
        // paint.
        holdReader();
        if (scrollEl) setScrollTop(scrollEl.scrollTop);
      })
      .finally(() => {
        loadingOlderHere = false;
      });
  };

  const onScrollEvt = () => {
    if (!scrollEl) return;
    // chat-scroll's listener has already run, so stickToBottom is current.
    if (scroll.stickToBottom()) {
      batch(() => {
        setReader(null);
        setScrollTop(scrollEl!.scrollTop);
      });
    } else {
      catchUpReader();
      // The window follows the scroll synchronously, in this frame. Rows that
      // mount above the held row take their real height in place of an
      // estimate, and rows that leave become pad; either moves the row, and it
      // goes back before the frame is painted.
      setScrollTop(scrollEl.scrollTop);
      holdReader();
      // Hold whichever row is on screen now. With none held before (the
      // first scroll away from the bottom, or a drag past the window) that is
      // the first row the reader will see here.
      setReader(readAnchor());
    }
    lastTop = scrollEl.scrollTop;
    maybeLoadOlder();
  };

  // At the very top a further wheel-up fires no scroll event; it should still
  // fetch what is above.
  const onWheel = (e: WheelEvent) => {
    if (e.deltaY < 0) maybeLoadOlder();
  };

  // The scrollport sits above everything this changes, so re-rendering here
  // stays clear of the ResizeObserver loop limit; the rows that reflow are
  // re-measured by rowObserver later in the same frame.
  const scrollPortObserver = new ResizeObserver(() => {
    if (scrollEl) setViewportHeight(scrollEl.clientHeight);
    const r = rootRem();
    if (r !== rem()) setRem(r);
    holdReader();
  });

  onMount(() => {
    if (scrollEl) {
      setViewportHeight(scrollEl.clientHeight);
      scrollPortObserver.observe(scrollEl);
    }
  });

  onCleanup(() => {
    scrollEl?.removeEventListener('scroll', onScrollEvt);
    scrollEl?.removeEventListener('wheel', onWheel);
    scrollPortObserver.disconnect();
    rowObserver.disconnect();
    if (commitRaf) cancelAnimationFrame(commitRaf);
  });

  const scrollToBottom = () => {
    scroll.jumpToBottom();
    markSeen();
  };

  return (
    <div class="flex-1 min-h-0 relative flex flex-col">
      <div
        ref={(el) => {
          scrollEl = el;
          // After chat-scroll's own listener, so onScrollEvt sees whether this
          // scroll left the bottom (or reached it) before deciding to hold
          // the reader's row.
          scroll.attachScroll(el);
          el.addEventListener('scroll', onScrollEvt, { passive: true });
          el.addEventListener('wheel', onWheel, { passive: true });
        }}
        class="chat-scroll flex-1 overflow-y-auto"
      >
        {/* Spacing is rhythmic rather than uniform (see .chat-flow): a wide gap
            opens before each new user prompt, a medium one under it, and the
            agent's own run of tool calls and replies stays tightly packed —
            so the transcript reads as turns, not as an evenly spaced list.
            Rows carry that gap inline; the pads stand in for the rows that are
            not mounted, so the scrollbar and the reading position stay true. */}
        <div
          ref={(el) => {
            scroll.attachContent(el);
            contentEl = el;
          }}
          class="chat-col chat-flow px-4 md:px-8 pt-6 pb-4"
        >
          <Show when={rows().list.length === 0 && !session.loading()}>
            <div class="flex flex-col items-center justify-center py-24 text-center animate-fade-in-up">
              <div class="w-11 h-11 rounded-xl bg-[color:var(--accent-soft)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-3.5">
                <Logo class="w-6 h-6 text-[color:var(--accent)]" />
              </div>
              <p class="text-ui font-medium text-[color:var(--text-primary)] mb-1">Ready when you are</p>
              <p class="text-meta text-[color:var(--text-tertiary)]">Describe a task, ask a question, or paste an error.</p>
            </div>
          </Show>

          {/* Pad above the mounted rows. Kept in the DOM even at zero height so
              the row order — and the binary search for the anchor — never
              shifts under chat-scroll. */}
          <div class="chat-pad" style={{ height: `${topPad()}px` }} />

          <For each={mountedKeys()}>
            {(key) => {
              const fresh = freshAfter === undefined || key > freshAfter;
              // The row outlives its message by at most one update — the key
              // leaving the window is what unmounts it — so it keeps showing
              // the last version it had rather than read a missing one.
              let last: any;
              const msg = () => {
                const m = rows().byKey.get(key)?.msg;
                if (m) last = m;
                return last;
              };
              const gap = () => {
                const i = rows().index.get(key);
                return i === undefined ? 0 : layout().gaps[i];
              };
              let rowEl: HTMLElement | undefined;
              onCleanup(() => {
                if (!rowEl) return;
                rowObserver.unobserve(rowEl);
                if (rowEls.get(key) === rowEl) rowEls.delete(key);
              });
              return (
                <div
                  ref={(el) => {
                    rowEl = el;
                    rowEls.set(key, el);
                    rowObserver.observe(el);
                  }}
                  data-vrow
                  data-vkey={key}
                  data-fresh={fresh ? '' : undefined}
                  style={{ 'margin-top': `${gap()}px` }}
                >
                  <MessageItem msg={msg()} />
                </div>
              );
            }}
          </For>

          <div class="chat-pad" style={{ height: `${bottomPad()}px` }} />

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
              it where it was already waiting for the answer.
              reason==='aborted' is a user stop that landed between messages —
              after the tool results were written, before the next model call —
              leaving no aborted marker in the transcript to explain it. It is
              not a failure, so it gets the same neutral amber "Generation
              cancelled" notice the in-message marker uses (message-item.tsx),
              not this red banner. */}
          <Show when={session.loopError()}>
            {(failure) => (
              <Show
                when={failure().reason === 'aborted'}
                fallback={
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
                }
              >
                <div class="flex items-center gap-2 rounded-md border border-amber-700/40 bg-amber-950/30 px-3 py-1.5 text-meta text-amber-300 animate-fade-in">
                  <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" />
                  </svg>
                  <span>Generation cancelled</span>
                </div>
              </Show>
            )}
          </Show>

        </div>

      </div>

      <Show when={session.loadingOlder()}>
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
