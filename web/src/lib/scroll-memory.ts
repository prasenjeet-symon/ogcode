// Scroll memory for chat-style transcripts. The stored value is an anchor
// record, not a pixel offset: a virtualized list rebuilds its content height
// from row estimates, so the same pixel lands on a different message. The
// anchor row id + the offset of its top edge from the viewport top restore the
// same reading position whatever the rows around it end up measuring.
export interface SavedScroll {
  /** Row key (data-vkey) of the topmost visible row, '' when unknown. */
  anchorId: string;
  /** Distance from the viewport top to the anchor row's top edge; negative
   *  when the row starts above the viewport. */
  anchorOffset: number;
  /** True when the view was pinned to the bottom when saved. */
  atBottom: boolean;
  /** Pixel scrollTop at save time — a fallback for lists without anchors. */
  top: number;
}

const saved = new Map<string, SavedScroll>();

export function saveScroll(key: string, value: SavedScroll): void {
  if (!key) return;
  saved.set(key, value);
}

export function getScroll(key: string): SavedScroll | undefined {
  if (!key) return undefined;
  return saved.get(key);
}

export function clearScroll(key: string): void {
  saved.delete(key);
}