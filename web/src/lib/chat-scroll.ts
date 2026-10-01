import { createSignal, onCleanup } from 'solid-js';
import { saveScroll, getScroll, SavedScroll } from './scroll-memory';

// Distance from the bottom (px) within which the transcript counts as "at the
// bottom" and should follow new content. Deliberately generous: one streamed
// chunk can add several lines at once, and a tight threshold detaches the view
// mid-answer.
const BOTTOM_THRESHOLD = 96;

// How long after a wheel / touch / key gesture a scroll event still counts as
// the user's own. Trackpad and touch momentum keep firing scroll events well
// after the fingers leave, so the window is refreshed while motion continues.
const USER_INTENT_MS = 700;

// Window after a "jump to latest" click during which follow-up pins animate
// too, so the smooth scroll is not cut short by the next streamed chunk.
const SMOOTH_WINDOW_MS = 450;

const NAV_KEYS = new Set([
  'PageUp', 'PageDown', 'Home', 'End', 'ArrowUp', 'ArrowDown', ' ', 'Spacebar',
]);

/**
 * Scroll behaviour for a streaming transcript.
 *
 * Two rules, and everything here exists to keep them true:
 *
 *  1. While the view is at the bottom it follows new content.
 *  2. Anywhere else it holds its place — content appearing or resizing above
 *     or below the viewport must not move what the reader is looking at.
 *
 * The subtle part is rule 1's *exit* condition. Only a real gesture may detach
 * the view from the bottom. Scroll events also come from the browser's own
 * scroll anchoring, from late-loading images and iframes, and from our own
 * scrollTop writes; treating those as "the user scrolled up" is what made a
 * long answer jitter — the view detached, the next chunk snapped it back down,
 * and the two fought for the length of the response.
 */
export function createChatScroll(options: {
  /** Per-conversation key for the saved scroll position. */
  key: () => string;
  /** Called whenever the view settles at the bottom (clears unread state). */
  onAtBottom?: () => void;
  /**
   * Told the scrollTop this module just wrote, outside the ResizeObserver
   * callback, so a virtualized list can render the rows at the new position
   * before the frame is painted instead of a frame later. Writes made inside
   * the observer are not reported: rendering rows there would resize the
   * content mid-observation and trip the browser's ResizeObserver loop error.
   */
  onWrite?: (top: number) => void;
  /**
   * Offered the saved record first; return true when the list positioned the
   * view itself. A virtualized list can mount the rows the anchor lives among
   * (fetching older pages if the anchor predates what it holds), which a
   * pixel write cannot. May start async work and still return true.
   */
  onRestore?: (saved: SavedScroll) => boolean;
  /**
   * True when the list holds the reading position itself (message-list does,
   * off the DOM, in the same frame as every change). The ResizeObserver then
   * only follows the bottom — a second scrollTop writer after the list's own
   * correction is what made upward scroll jitter: both landed, moving the view
   * twice per change.
   */
  anchoredByList?: boolean;
}) {
  const [stickToBottom, setStickToBottom] = createSignal(true);
  const [isScrolledUp, setIsScrolledUp] = createSignal(false);

  let scrollEl: HTMLElement | undefined;
  let contentEl: HTMLElement | undefined;
  let observer: ResizeObserver | undefined;

  let userIntentAt = -Infinity;
  let smoothUntil = 0;
  // The scrollTop we last wrote ourselves. A scroll event landing exactly on
  // it is our own echo, not the reader moving the view.
  let ownTop = -1;
  // The row the reading position is pinned to while scrolled up, plus its
  // distance from the top of the scrollport when it was captured.
  let anchorEl: HTMLElement | null = null;
  let anchorOffset = 0;
  let restored = false;
  // True while the ResizeObserver callback runs (see onWrite).
  let inObserver = false;

  const now = () => (typeof performance !== 'undefined' ? performance.now() : 0);

  const distanceFromBottom = () =>
    scrollEl ? scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight : 0;

  const nearBottom = () => distanceFromBottom() <= BOTTOM_THRESHOLD;

  const write = (top: number, smooth = false) => {
    if (!scrollEl) return;
    if (smooth) scrollEl.scrollTo({ top, behavior: 'smooth' });
    else scrollEl.scrollTop = top;
    // Read back so the value is the clamped one the element actually took.
    ownTop = scrollEl.scrollTop;
    if (!inObserver) options.onWrite?.(ownTop);
  };

  const pinToBottom = () => {
    if (!scrollEl) return;
    write(scrollEl.scrollHeight, now() < smoothUntil);
  };

  // Pick the topmost row still on screen. Children are laid out in document
  // order so offsetTop is monotonic — binary search keeps this O(log n) even
  // in a transcript hundreds of messages long, which matters because it runs
  // on every scroll event.
  const captureAnchor = () => {
    if (!scrollEl || !contentEl) return;
    const kids = contentEl.children as HTMLCollectionOf<HTMLElement>;
    const target = scrollEl.scrollTop;
    let lo = 0;
    let hi = kids.length - 1;
    let pick = -1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      const el = kids[mid];
      if (el.offsetTop + el.offsetHeight > target) {
        pick = mid;
        hi = mid - 1;
      } else {
        lo = mid + 1;
      }
    }
    // A virtualized list's spacer stands in for rows that are not mounted; it
    // never moves when they change, so it cannot hold a reading position.
    // Anchor to the first real row after it instead.
    while (pick >= 0 && pick < kids.length && kids[pick].classList.contains('chat-pad')) pick++;
    anchorEl = pick >= 0 && pick < kids.length ? kids[pick] : null;
    // offsetTop is measured against the offsetParent, but only the *change* in
    // (offsetTop - scrollTop) is ever used, so the shared baseline cancels out.
    anchorOffset = anchorEl ? anchorEl.offsetTop - target : 0;
  };

  // Re-apply the captured anchor after a layout change. Called from the
  // ResizeObserver, which runs after layout and before paint, so the
  // correction is never painted.
  const holdAnchor = () => {
    if (!scrollEl || !anchorEl || !anchorEl.isConnected) return;
    const delta = (anchorEl.offsetTop - scrollEl.scrollTop) - anchorOffset;
    if (Math.abs(delta) < 1) return;
    write(scrollEl.scrollTop + delta);
  };

  const onScroll = () => {
    if (!scrollEl) return;
    const key = options.key();
    const atBottom = nearBottom();

    const isOwn = Math.abs(scrollEl.scrollTop - ownTop) < 1;
    ownTop = -1;

    if (atBottom) {
      // Re-sticking is always allowed: whatever put the view back at the
      // bottom, following from there is what the reader expects.
      anchorEl = null;
      if (!stickToBottom()) setStickToBottom(true);
      if (isScrolledUp()) setIsScrolledUp(false);
      options.onAtBottom?.();
    } else {
      if (!isOwn && now() - userIntentAt < USER_INTENT_MS) {
        userIntentAt = now(); // keep the window alive through momentum
        if (stickToBottom()) setStickToBottom(false);
        if (!isScrolledUp()) setIsScrolledUp(true);
      }
      if (!stickToBottom()) captureAnchor();
    }

    // Saved after captureAnchor, so the record describes the view as of THIS
    // event. The anchor row id and its distance from the top of the viewport
    // restore the same reading position whatever the rows around it later
    // measure — a pixel offset only does that in a list whose every row is
    // already mounted at its final height.
    if (key) {
      if (atBottom) {
        // "The end" rather than a position: a reader who left at the end
        // expects to land there, whatever streamed in since.
        saveScroll(key, { anchorId: '', anchorOffset: 0, atBottom: true, top: 0 });
      } else if (anchorEl && anchorEl.isConnected && anchorEl.dataset.vkey) {
        saveScroll(key, {
          anchorId: anchorEl.dataset.vkey,
          // Signed: the anchor is the row cut by the top edge, so it usually
          // starts above the viewport — and how far above is the reading
          // position. Clamping it put the row's top edge back instead.
          anchorOffset: anchorEl.offsetTop - scrollEl.scrollTop,
          atBottom: false,
          top: scrollEl.scrollTop,
        });
      } else {
        saveScroll(key, { anchorId: '', anchorOffset: 0, atBottom: false, top: scrollEl.scrollTop });
      }
    }
  };

  const noteIntent = () => { userIntentAt = now(); };
  const onKeyDown = (e: KeyboardEvent) => { if (NAV_KEYS.has(e.key)) noteIntent(); };

  // The two ref callbacks fire in whatever order the renderer emits them, and
  // a ResizeObserver delivers its first observation immediately — so wiring is
  // deferred until both halves are known, or that first observation lands with
  // nothing to scroll and the transcript opens at the top.
  const observeWhenReady = () => {
    if (!scrollEl || !contentEl || observer) return;
    observer = new ResizeObserver(() => {
      if (!scrollEl) return;
      // Runs after layout and before paint, so the correction is never painted.
      inObserver = true;
      try {
        if (stickToBottom()) pinToBottom();
        else if (!options.anchoredByList) holdAnchor();
      } finally {
        inObserver = false;
      }
    });
    observer.observe(contentEl);
  };

  /** ref callback for the scrollport. */
  const attachScroll = (el: HTMLElement) => {
    scrollEl = el;
    observeWhenReady();
    el.addEventListener('scroll', onScroll, { passive: true });
    el.addEventListener('wheel', noteIntent, { passive: true });
    el.addEventListener('touchstart', noteIntent, { passive: true });
    el.addEventListener('touchmove', noteIntent, { passive: true });
    el.addEventListener('pointerdown', noteIntent, { passive: true });
    window.addEventListener('keydown', onKeyDown);
  };

  /** ref callback for the element that wraps the message rows. */
  const attachContent = (el: HTMLElement) => {
    contentEl = el;
    observeWhenReady();
  };

  onCleanup(() => {
    observer?.disconnect();
    window.removeEventListener('keydown', onKeyDown);
    if (!scrollEl) return;
    scrollEl.removeEventListener('scroll', onScroll);
    scrollEl.removeEventListener('wheel', noteIntent);
    scrollEl.removeEventListener('touchstart', noteIntent);
    scrollEl.removeEventListener('touchmove', noteIntent);
    scrollEl.removeEventListener('pointerdown', noteIntent);
  });

  /**
   * Restore the position saved for this conversation. Runs once per
   * conversation, on the first render that has messages. Deferred a frame so
   * the rows have a layout to restore into; anything that resolves its height
   * later (images, iframes, diagrams) is absorbed by the anchor instead.
   */
  const restore = () => {
    if (restored || !scrollEl) return;
    restored = true;
    const key = options.key();
    const saved = key ? getScroll(key) : undefined;
    requestAnimationFrame(() => {
      if (!scrollEl) return;
      if (saved && !saved.atBottom && options.onRestore && options.onRestore(saved)) {
        // The list put the view where the reader left it (or is doing so
        // async, fetching the pages in between); anything but following new
        // content is wrong from here.
        setStickToBottom(false);
        setIsScrolledUp(true);
        return;
      }
      if (saved && !saved.atBottom && saved.top > 0) {
        write(saved.top);
        const atBottom = nearBottom();
        setStickToBottom(atBottom);
        setIsScrolledUp(!atBottom);
        if (!atBottom) captureAnchor();
      } else {
        setStickToBottom(true);
        setIsScrolledUp(false);
        pinToBottom();
      }
    });
  };

  /** Called when the conversation changes. */
  const reset = () => {
    restored = false;
    anchorEl = null;
    userIntentAt = -Infinity;
    ownTop = -1;
    smoothUntil = 0;
    setStickToBottom(true);
    setIsScrolledUp(false);
  };

  /**
   * Follow new content if the view is at the bottom. The ResizeObserver
   * already covers every height change; this is the same write driven from the
   * message signal, for content that changes without changing height.
   */
  const follow = () => { if (stickToBottom()) pinToBottom(); };

  const jumpToBottom = () => {
    smoothUntil = now() + SMOOTH_WINDOW_MS;
    anchorEl = null;
    setStickToBottom(true);
    setIsScrolledUp(false);
    pinToBottom();
  };

  return {
    stickToBottom,
    isScrolledUp,
    hasRestored: () => restored,
    attachScroll,
    attachContent,
    restore,
    reset,
    follow,
    jumpToBottom,
  };
}
