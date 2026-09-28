import { Show, createSignal, createEffect, onCleanup, on, type JSX } from 'solid-js';
import { Portal } from 'solid-js/web';

/**
 * An anchored panel — a menu, a document outline, a details card. It opens
 * under its anchor, or above it when there is more room there, and it is held
 * inside the viewport by capping its size rather than by measuring it first, so
 * it lands in place on the first frame instead of jumping there on the second.
 *
 * It closes on Escape, on a press outside both panel and anchor, and on window
 * resize. Escape is taken in the capture phase and stopped there, so a popover
 * opened inside a dialog closes itself without closing the dialog too.
 *
 * Arrow keys move focus between `[data-pop-item]` elements, which is all a menu
 * needs; richer content just leaves them out.
 */
export interface PopoverProps {
  open: boolean;
  anchor: HTMLElement | undefined;
  onClose: () => void;
  /** Which edge of the anchor the panel lines up with. Default `end`. */
  align?: 'start' | 'end';
  /** Accessible name for the panel. */
  label: string;
  role?: 'dialog' | 'menu';
  class?: string;
  style?: JSX.CSSProperties;
  children: JSX.Element;
}

const GAP = 6;
const MARGIN = 8;

export default function Popover(props: PopoverProps) {
  let panel: HTMLDivElement | undefined;
  const [place, setPlace] = createSignal<{ style: JSX.CSSProperties; side: 'top' | 'bottom' }>({ style: {}, side: 'bottom' });

  const measure = () => {
    const a = props.anchor;
    if (!a) return;
    const r = a.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const below = vh - r.bottom - GAP - MARGIN;
    const above = r.top - GAP - MARGIN;
    const up = below < 240 && above > below;
    const style: JSX.CSSProperties = {};
    if (up) {
      style.bottom = `${vh - r.top + GAP}px`;
      style['max-height'] = `${Math.max(120, above)}px`;
    } else {
      style.top = `${r.bottom + GAP}px`;
      style['max-height'] = `${Math.max(120, below)}px`;
    }
    if ((props.align ?? 'end') === 'start') {
      const left = Math.max(MARGIN, r.left);
      style.left = `${left}px`;
      style['max-width'] = `${vw - left - MARGIN}px`;
    } else {
      const right = Math.max(MARGIN, vw - r.right);
      style.right = `${right}px`;
      style['max-width'] = `${vw - right - MARGIN}px`;
    }
    setPlace({ style, side: up ? 'top' : 'bottom' });
  };

  const items = () => (panel ? [...panel.querySelectorAll<HTMLElement>('[data-pop-item]:not(:disabled)')] : []);

  createEffect(on(() => props.open, (open) => {
    if (!open) return;
    measure();

    const onPointer = (e: PointerEvent) => {
      const t = e.target as Node;
      if (panel?.contains(t) || props.anchor?.contains(t)) return;
      props.onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopImmediatePropagation();
        props.onClose();
        props.anchor?.focus({ preventScroll: true });
        return;
      }
      if (!panel?.contains(document.activeElement)) return;
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        const list = items();
        if (!list.length) return;
        e.preventDefault();
        const i = list.indexOf(document.activeElement as HTMLElement);
        const next = e.key === 'ArrowDown'
          ? list[(i + 1) % list.length]
          : list[(i - 1 + list.length) % list.length];
        next.focus();
      }
    };
    // Scrolling anything but the panel itself moves the anchor out from under
    // it; following along is cheaper than closing and costs nothing to read.
    const onScroll = (e: Event) => {
      if (panel && e.target instanceof Node && panel.contains(e.target)) return;
      measure();
    };
    const onResize = () => props.onClose();

    document.addEventListener('pointerdown', onPointer, true);
    window.addEventListener('keydown', onKey, true);
    window.addEventListener('scroll', onScroll, true);
    window.addEventListener('resize', onResize);

    // Focus lands on the first item for a menu, on the panel otherwise, so
    // the keyboard is already inside when the panel appears.
    queueMicrotask(() => {
      const first = props.role === 'menu' ? items()[0] : panel?.querySelector<HTMLElement>('[data-autofocus]');
      (first ?? panel)?.focus({ preventScroll: true });
    });

    onCleanup(() => {
      document.removeEventListener('pointerdown', onPointer, true);
      window.removeEventListener('keydown', onKey, true);
      window.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('resize', onResize);
    });
  }));

  return (
    <Show when={props.open}>
      <Portal>
        <div
          ref={panel}
          role={props.role ?? 'dialog'}
          aria-label={props.label}
          tabindex="-1"
          data-side={place().side}
          class={`pop focus:outline-none ${props.class ?? ''}`}
          style={{ ...place().style, ...props.style }}
        >
          {props.children}
        </div>
      </Portal>
    </Show>
  );
}
