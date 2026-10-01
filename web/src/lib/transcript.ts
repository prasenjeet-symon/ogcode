import type { MessageWithParts, MessagesPage } from '../api/client';

// Merging transcript pages from the server into what a view already holds.
// Shared by the session and plan contexts, which page the same way.

// Reference-preserving comparisons for mergeTranscript, so a poll tick that
// changed nothing keeps the existing objects and the list rebuilds nothing.
export function shallowEqualPart(a: any, b: any): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  if (a.type !== b.type) return false;
  if (a.updatedAt !== b.updatedAt) return false;
  // When timestamps match, the server hasn't modified the part — skip deep
  // data comparison to avoid JSON.stringify stack overflow on large tool output.
  return true;
}

export function shallowEqualMessage(a: MessageWithParts, b: MessageWithParts): boolean {
  if (a === b) return true;
  if (a.info.id !== b.info.id) return false;
  if (a.info.finish !== b.info.finish) return false;
  if (a.info.error !== b.info.error) return false;
  const ap = a.parts || [];
  const bp = b.parts || [];
  if (ap.length !== bp.length) return false;
  for (let i = 0; i < ap.length; i++) {
    if (!shallowEqualPart(ap[i], bp[i])) return false;
  }
  return true;
}

// Every message enters a store with parts as a list. The server sends [] for a
// message without parts now, but an older one sent null, and the renderer
// reads parts directly — one null on a history page used to crash it.
const normalize = (m: MessageWithParts): MessageWithParts => ({ info: m.info, parts: m.parts || [] });

const byId = (a: MessageWithParts, b: MessageWithParts) =>
  a.info.id < b.info.id ? -1 : a.info.id > b.info.id ? 1 : 0;

/**
 * Whether a page of a transcript's newest messages joins up with what is held:
 * it reaches back to the newest message held (`newestHeldId`, '' when nothing
 * is), or it is the whole transcript. A page that starts after that message,
 * with more above it, may have left out messages that landed in between — a
 * burst while the event stream was down, a long run in a session left open
 * elsewhere — and merging it would leave a hole that paging can never fill,
 * since history is only ever fetched above the oldest message held.
 */
export function joinsHeld(newestHeldId: string, page: MessagesPage): boolean {
  if (!newestHeldId || page.messages.length === 0 || !page.hasOlder) return true;
  return page.messages[0].info.id <= newestHeldId;
}

/**
 * Merge a page from the server into the transcript held for `sessionId`.
 *
 * A page written for another session is DISCARDED: several callers fetch by a
 * session id captured before an await, so a slow response can land after the
 * user has moved on, and applying it would paint the old conversation into the
 * new one's view. Returning prev drops it.
 *
 * Otherwise merge, never replace, because the server answers a sliding window:
 * a page entirely older than what is held (a history fetch) is unioned in
 * above it, and a newest-N window replaces only the held entries inside its
 * range — honouring a deletion there — while keeping the older pages loaded
 * earlier. Entries that did not change keep their object identity, so the list
 * reuses their rows.
 */
export function mergeTranscript(
  prev: MessageWithParts[],
  incoming: MessageWithParts[],
  sessionId: string | undefined,
): MessageWithParts[] {
  if (sessionId) {
    for (const msg of incoming) {
      if (msg.info.sessionId !== sessionId) return prev;
    }
  }

  // Nothing to merge against — take the page as the new state. This is the
  // path after a switch clears the list, which is why the cross-session check
  // has to come first rather than after it.
  if (!prev || prev.length === 0) return incoming.map(normalize);

  // An empty page is never a reason to clear the list: a fetch that raced a
  // switch, or a poll that found nothing, must leave what is on screen alone.
  // The switch paths clear explicitly.
  if (incoming.length === 0) return prev;

  const incomingMin = incoming[0].info.id;
  const incomingMax = incoming[incoming.length - 1].info.id;
  if (incomingMax < prev[0].info.id) {
    return [...incoming.map(normalize), ...prev].sort(byId);
  }

  const kept = prev.filter((m) => m.info.id < incomingMin);
  const prevById = new Map(prev.map((m) => [m.info.id, m]));
  const merged = incoming.map((m) => {
    const normalized = normalize(m);
    const existing = prevById.get(m.info.id);
    if (!existing) return normalized;
    if (shallowEqualMessage(existing, normalized)) return existing;
    // Preserve part references for parts that didn't change.
    const newParts = normalized.parts.map((p) => {
      const prevPart = (existing.parts || []).find((pp) => pp.id === p.id);
      if (prevPart && shallowEqualPart(prevPart, p)) return prevPart;
      return p;
    });
    return { info: m.info, parts: newParts };
  });
  return [...kept, ...merged].sort(byId);
}
