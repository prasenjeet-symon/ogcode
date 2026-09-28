/**
 * Scrolls `container` just enough to bring `el` into view.
 *
 * Element.scrollIntoView scrolls every scrollable ancestor, the document
 * included. The app shell is a fixed-height column, but when anything nudges
 * the document a few pixels taller than the viewport, scrollIntoView takes the
 * chance and shifts the whole page — header and all — up under the browser
 * chrome. Scrolling the one container that should move avoids that.
 */
export function revealWithin(
  container: HTMLElement,
  el: HTMLElement,
  opts: { block?: 'nearest' | 'center' | 'start'; margin?: number; smooth?: boolean } = {},
): void {
  const { block = 'nearest', margin = 0, smooth = false } = opts;
  const c = container.getBoundingClientRect();
  const r = el.getBoundingClientRect();
  let delta = 0;
  if (block === 'start') {
    delta = r.top - c.top - margin;
  } else if (block === 'center') {
    delta = r.top - c.top - (c.height - r.height) / 2;
  } else if (r.top < c.top + margin) {
    delta = r.top - c.top - margin;
  } else if (r.bottom > c.bottom - margin) {
    delta = r.bottom - c.bottom + margin;
  }
  if (delta) container.scrollTo({ top: container.scrollTop + delta, behavior: smooth ? 'smooth' : 'auto' });
}

/** The nearest ancestor that actually scrolls vertically. */
export function scrollParent(el: HTMLElement): HTMLElement | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const oy = getComputedStyle(p).overflowY;
    if ((oy === 'auto' || oy === 'scroll') && p.scrollHeight > p.clientHeight) return p;
  }
  return null;
}
