import { onMount, onCleanup, Show, type JSX } from 'solid-js';

/**
 * The frame both Project Index dialogs share: the dimmed backdrop, the panel,
 * and the keyboard contract a modal owes — focus moves in when it opens, Tab
 * stays inside it, Escape closes it, and focus returns to whatever opened it.
 *
 * The backdrop closes on mousedown rather than click. A click is reported on
 * the common ancestor of press and release, so selecting text inside the panel
 * and letting go over the backdrop used to count as a click on the backdrop and
 * throw the dialog away mid-selection.
 *
 * On phones the panel docks to the bottom edge as a sheet, where it sits under
 * the thumb instead of floating in the middle of a tall screen.
 */

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

export default function DialogShell(props: {
  labelledBy: string;
  describedBy?: string;
  onClose: () => void;
  /** Width classes for the panel, e.g. `sm:max-w-[520px]`. */
  class?: string;
  /** Element to focus on open; defaults to the panel itself. */
  initialFocus?: () => HTMLElement | null | undefined;
  /** Where focus goes on close when the opener is gone (say, a menu item). */
  returnFocus?: () => HTMLElement | null | undefined;
  onKeyDown?: (e: KeyboardEvent) => void;
  children: JSX.Element;
}) {
  let panel!: HTMLDivElement;
  const opener = document.activeElement as HTMLElement | null;

  const trapTab = (e: KeyboardEvent) => {
    const items = [...panel.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.offsetParent !== null);
    if (!items.length) {
      e.preventDefault();
      panel.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (e.shiftKey && (active === first || active === panel || !panel.contains(active))) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && (active === last || !panel.contains(active))) {
      e.preventDefault();
      first.focus();
    }
  };

  onMount(() => {
    (props.initialFocus?.() ?? panel).focus({ preventScroll: true });
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        props.onClose();
      } else if (e.key === 'Tab') {
        trapTab(e);
      }
    };
    window.addEventListener('keydown', onKey);
    onCleanup(() => {
      window.removeEventListener('keydown', onKey);
      const target = opener && opener !== document.body && document.contains(opener) ? opener : props.returnFocus?.();
      target?.focus({ preventScroll: true });
    });
  });

  return (
    <div
      class="fixed inset-0 z-50 bg-black/60 backdrop-blur-[2px] flex items-end sm:items-center justify-center sm:p-4 modal-backdrop"
      onMouseDown={(e) => { if (e.target === e.currentTarget) props.onClose(); }}
    >
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={props.labelledBy}
        aria-describedby={props.describedBy}
        tabindex="-1"
        onKeyDown={(e) => props.onKeyDown?.(e)}
        class={`dlg-panel w-full bg-[color:var(--bg-surface)] border border-[color:var(--border-default)] rounded-t-2xl sm:rounded-2xl shadow-[0_24px_64px_rgba(0,0,0,0.6)] flex flex-col overflow-hidden max-h-[88dvh] sm:max-h-[84vh] focus:outline-none ${props.class ?? ''}`}
      >
        {props.children}
      </div>
    </div>
  );
}

/** Title row: tinted glyph, title and one line of description, close button. */
export function DialogHeader(props: {
  id: string;
  descriptionId?: string;
  title: string;
  description?: JSX.Element;
  icon: JSX.Element;
  tone?: 'accent' | 'warning';
  onClose: () => void;
  children?: JSX.Element;
}) {
  return (
    <div class="shrink-0 px-5 pt-4 pb-3.5 border-b border-[color:var(--border-subtle)]">
      <div class="flex items-start gap-3">
        <div
          class="w-8 h-8 rounded-[9px] flex items-center justify-center shrink-0"
          classList={{
            'bg-[color:var(--accent-soft)] text-[color:var(--accent)] shadow-[inset_0_0_0_1px_var(--accent-ring)]': (props.tone ?? 'accent') === 'accent',
            'bg-[color:var(--warning)]/[0.12] text-[color:var(--warning)] shadow-[inset_0_0_0_1px_rgba(245,166,35,0.28)]': props.tone === 'warning',
          }}
        >
          {props.icon}
        </div>
        <div class="min-w-0 flex-1 pt-px">
          <h2 id={props.id} class="text-sm font-semibold text-[color:var(--text-primary)] leading-tight tracking-[-0.01em]">
            {props.title}
          </h2>
          <Show when={props.description}>
            <p id={props.descriptionId} class="text-meta text-[color:var(--text-tertiary)] mt-1 leading-relaxed">
              {props.description}
            </p>
          </Show>
        </div>
        <button
          type="button"
          onClick={props.onClose}
          class="icon-btn -mr-1.5 -mt-0.5 shrink-0"
          title="Close (Esc)"
          aria-label="Close"
        >
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      </div>
      {props.children}
    </div>
  );
}
