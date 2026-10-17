import { createSignal, type Accessor } from 'solid-js';

// The left panels are drag-resizable from their right edge. Width lives in
// localStorage (there is no server setting for it), clamped to a range that
// keeps the icon rail usable and the panel from swallowing the chat column.
const STORAGE_KEY = 'ogcode-sidebar-width';
const MIN_WIDTH = 208;
const MAX_WIDTH = 420;
const DEFAULT_WIDTH = 260;

function clamp(n: number): number {
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, n));
}

function loadWidth(): number {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULT_WIDTH;
    const n = Number.parseInt(raw, 10);
    return Number.isFinite(n) ? clamp(n) : DEFAULT_WIDTH;
  } catch {
    return DEFAULT_WIDTH;
  }
}

export interface SidebarWidth {
  width: Accessor<number>;
  start: (e: PointerEvent) => void;
  /** Keyboard resizing — the grip is a focusable separator, so arrows move it. */
  nudge: (e: KeyboardEvent) => void;
  min: number;
  max: number;
}

export function createSidebarWidth(): SidebarWidth {
  const [width, setWidth] = createSignal(loadWidth());

  const persist = () => {
    try {
      localStorage.setItem(STORAGE_KEY, String(width()));
    } catch {
      // private mode / quota — resizing still works for this session
    }
  };

  const start = (e: PointerEvent) => {
    e.preventDefault();
    const x0 = e.clientX;
    const w0 = width();
    const onMove = (ev: PointerEvent) => setWidth(clamp(w0 + (ev.clientX - x0)));
    const onUp = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      persist();
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
  };

  // Arrows step by 8 (Shift 24); Home/End jump to the limits. The element is
  // declared a separator with aria-valuednow/min/max, so these are the
  // conventional separator keys.
  const nudge = (e: KeyboardEvent) => {
    const step = e.shiftKey ? 24 : 8;
    let next: number;
    switch (e.key) {
      case 'ArrowLeft': next = width() - step; break;
      case 'ArrowRight': next = width() + step; break;
      case 'Home': next = MIN_WIDTH; break;
      case 'End': next = MAX_WIDTH; break;
      default: return;
    }
    e.preventDefault();
    setWidth(clamp(next));
    persist();
  };

  return { width, start, nudge, min: MIN_WIDTH, max: MAX_WIDTH };
}
