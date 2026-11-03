import { onMount, onCleanup, Show, type JSX } from 'solid-js';

/**
 * The frame every modal in ogcode shares: the dimmed backdrop, the panel, and
 * the keyboard contract a modal owes — focus moves in when it opens, Tab stays
 * inside it, Escape closes it, and focus returns to whatever opened it.
 *
 * One panel for every dialog: a roomy header over a scrolling body, and a
 * footer bar that carries the hint on the left and the actions on the right.
 * The `.dlg-*` classes in styles/index.css hold the colour, radius and shadow,
 * drawn from the theme tokens so dark and light stay in step.
 *
 * `onClose` is optional. A dialog that must not be dismissed — ask_user, where
 * a close is indistinguishable from an answer that never arrived — leaves it
 * out: Escape and the backdrop then do nothing, and DialogHeader draws no ×.
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
  /** Omit to make the dialog non-dismissible. */
  onClose?: () => void;
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
        if (!props.onClose) return;
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
      class="fixed inset-0 z-50 bg-black/55 backdrop-blur-[3px] flex items-end sm:items-center justify-center sm:p-6 modal-backdrop"
      onMouseDown={(e) => { if (e.target === e.currentTarget) props.onClose?.(); }}
    >
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={props.labelledBy}
        aria-describedby={props.describedBy}
        tabindex="-1"
        onKeyDown={(e) => props.onKeyDown?.(e)}
        class={`dlg-panel sm:max-w-[680px] ${props.class ?? ''}`}
      >
        {props.children}
      </div>
    </div>
  );
}

/**
 * Title row: an optional glyph tile, the title and one line of description, and
 * the ×. Anything passed as children (a tab strip, say) sits under that row,
 * above the header's divider.
 */
export function DialogHeader(props: {
  id: string;
  descriptionId?: string;
  title: string;
  description?: JSX.Element;
  icon?: JSX.Element;
  tone?: 'accent' | 'warning';
  onClose?: () => void;
  /** A chip or badge that rides the right of the title, before the ×. */
  aside?: JSX.Element;
  children?: JSX.Element;
}) {
  return (
    <div class="dlg-header">
      <div class="flex items-start gap-3">
        <Show when={props.icon}>
          <div class="dlg-glyph" classList={{ 'dlg-glyph--warning': props.tone === 'warning' }}>
            {props.icon}
          </div>
        </Show>
        <div class="min-w-0 flex-1">
          <h2 id={props.id} class="dlg-title">
            {props.title}
          </h2>
          <Show when={props.description}>
            <p id={props.descriptionId} class="dlg-subtitle">
              {props.description}
            </p>
          </Show>
        </div>
        <Show when={props.aside}>
          <div class="shrink-0 pt-0.5">{props.aside}</div>
        </Show>
        <Show when={props.onClose}>
          {(close) => (
            <button
              type="button"
              onClick={close()}
              class="icon-btn -mr-1.5 -mt-0.5 shrink-0"
              title="Close (Esc)"
              aria-label="Close"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          )}
        </Show>
      </div>
      {props.children}
    </div>
  );
}

/** The scrolling middle of a dialog. `flush` drops the padding for a body whose
 *  own sections carry it. */
export function DialogBody(props: { flush?: boolean; class?: string; children: JSX.Element }) {
  return (
    <div class={`dlg-body ${props.flush ? 'dlg-body--flush' : ''} ${props.class ?? ''}`}>
      {props.children}
    </div>
  );
}

/** The bar under the body: a hint on the left, the actions on the right. */
export function DialogFooter(props: {
  hint?: JSX.Element;
  /** `danger` recolours the hint for an error rather than a neutral note. */
  tone?: 'muted' | 'danger';
  children: JSX.Element;
}) {
  return (
    <div class="dlg-footer">
      <Show when={props.hint} fallback={<span class="flex-1" />}>
        <p class="dlg-hint" classList={{ 'dlg-hint--danger': props.tone === 'danger' }}>
          <svg class="shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
            <path stroke-linecap="round" stroke-linejoin="round" d="M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z" />
          </svg>
          <span class="dlg-hint-text">{props.hint}</span>
        </p>
      </Show>
      <div class="dlg-actions">{props.children}</div>
    </div>
  );
}

/** A title-bar tab strip, one option per DialogTab. */
export function DialogTabs(props: { label: string; children: JSX.Element }) {
  return (
    <div class="dlg-tabs" role="tablist" aria-label={props.label}>
      {props.children}
    </div>
  );
}

export function DialogTab(props: {
  selected: boolean;
  onClick: () => void;
  count?: number | string;
  children: JSX.Element;
}) {
  return (
    <button type="button" role="tab" aria-selected={props.selected} class="dlg-tab" onClick={props.onClick}>
      {props.children}
      <Show when={props.count !== undefined && props.count !== '' && props.count !== 0}>
        <span class="dlg-tab-count">{props.count}</span>
      </Show>
    </button>
  );
}
