import { createSignal, type Accessor } from 'solid-js';

// The left panels are drag-resizable from their right edge. Width lives in
// localStorage (there is no server setting for it), clamped to a range that
// keeps the icon rail usable and the panel from swallowing the chat column.
const STORAGE_KEY = 'ogcode-sidebar-width';
const MIN_WIDTH = 208;
const MAX_WIDTH = 420;
const DEFAULT_WIDTH = 260;

export interface SidebarWidthOptions {
  /** Panel width is remembered per panel, so each one gets its own key. */
  key?: string;
  min?: number;
  max?: number;
  defaultWidth?: number;
}

function clamp(n: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, n));
}

function loadWidth(key: string, min: number, max: number, fallback: number): number {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return fallback;
    const n = Number.parseInt(raw, 10);
    return Number.isFinite(n) ? clamp(n, min, max) : fallback;
  } catch {
    return fallback;
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

export function createSidebarWidth(options: SidebarWidthOptions = {}): SidebarWidth {
  const key = options.key ?? STORAGE_KEY;
  const min = options.min ?? MIN_WIDTH;
  const max = options.max ?? MAX_WIDTH;
  const fallback = clamp(options.defaultWidth ?? DEFAULT_WIDTH, min, max);
  const [width, setWidth] = createSignal(loadWidth(key, min, max, fallback));

  const persist = () => {
    try {
      localStorage.setItem(key, String(width()));
    } catch {
      // private mode / quota — resizing still works for this session
    }
  };

  const start = (e: PointerEvent) => {
    e.preventDefault();
    const x0 = e.clientX;
    const w0 = width();
    const onMove = (ev: PointerEvent) => setWidth(clamp(w0 + (ev.clientX - x0), min, max));
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
      case 'Home': next = min; break;
      case 'End': next = max; break;
      default: return;
    }
    e.preventDefault();
    setWidth(clamp(next, min, max));
    persist();
  };

  return { width, start, nudge, min, max };
}
