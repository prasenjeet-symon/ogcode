import { createSignal, createMemo, createEffect, For, Show, type JSX } from 'solid-js';

export interface CommandItem {
  id: string;
  label: string;
  hint?: string; // right-aligned shortcut / meta text
  icon?: JSX.Element;
  danger?: boolean;
  disabled?: boolean;
  keywords?: string; // extra searchable text
  onSelect: () => void;
}

// fuzzyMatch returns true when every character of query appears in text in order
// (case-insensitive subsequence) — the same forgiving match Linear's menus use.
function fuzzyMatch(text: string, query: string): boolean {
  if (!query) return true;
  const t = text.toLowerCase();
  const q = query.toLowerCase();
  let i = 0;
  for (let j = 0; j < t.length && i < q.length; j++) {
    if (t[j] === q[i]) i++;
  }
  return i === q.length;
}

// CommandMenu is a reusable Linear-style command palette: a centered, searchable,
// keyboard-driven list. ↑/↓ move, ↵ runs the active item, Esc / click-outside close.
export default function CommandMenu(props: {
  open: boolean;
  onClose: () => void;
  items: CommandItem[];
  placeholder?: string;
}) {
  const [query, setQuery] = createSignal('');
  const [active, setActive] = createSignal(0);
  let inputRef: HTMLInputElement | undefined;

  const filtered = createMemo(() =>
    props.items.filter((it) => fuzzyMatch(`${it.label} ${it.keywords || ''}`, query())),
  );

  // Reset state and focus the input whenever the palette opens.
  createEffect(() => {
    if (props.open) {
      setQuery('');
      setActive(0);
      queueMicrotask(() => inputRef?.focus());
    }
  });

  // Keep the active index in range as the filtered list changes.
  createEffect(() => {
    const n = filtered().length;
    if (active() >= n) setActive(Math.max(0, n - 1));
  });

  const run = (it?: CommandItem) => {
    if (!it || it.disabled) return;
    props.onClose();
    it.onSelect();
  };

  const moveActive = (delta: number) => {
    const items = filtered();
    if (items.length === 0) return;
    let i = active();
    for (let step = 0; step < items.length; step++) {
      i = (i + delta + items.length) % items.length;
      if (!items[i].disabled) break;
    }
    setActive(i);
  };

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      moveActive(1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      moveActive(-1);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      run(filtered()[active()]);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      props.onClose();
    }
  };

  return (
    <Show when={props.open}>
      {/* Backdrop — Linear-style blurred overlay */}
      <div
        class="fixed inset-0 z-[200] bg-black/60 backdrop-blur-[3px] animate-fade-in"
        onClick={props.onClose}
        style={{ 'animation-duration': '0.15s' }}
      />

      {/* Palette — slides down from top with spring bounce */}
      <div
        class="fixed left-1/2 top-[18vh] z-[201] w-[min(560px,92vw)] -translate-x-1/2
               rounded-[14px] border overflow-hidden animate-pop-in"
        style={{
          background: 'var(--bg-overlay)',
          'border-color': 'var(--border-default)',
          'box-shadow': 'var(--shadow-lg), var(--shadow-glow)',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Search input */}
        <div class="flex items-center gap-2.5 px-4 h-12 border-b" style={{ 'border-color': 'var(--border-subtle)' }}>
          <svg class="w-4 h-4 shrink-0" style={{ color: 'var(--text-muted)' }} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 11a6 6 0 11-12 0 6 6 0 0112 0z" />
          </svg>
          <input
            ref={inputRef}
            value={query()}
            onInput={(e) => setQuery(e.currentTarget.value)}
            onKeyDown={onKeyDown}
            placeholder={props.placeholder || 'Type a command…'}
            autocomplete="off"
            spellcheck={false}
            class="flex-1 bg-transparent outline-none text-[14px] placeholder:text-[color:var(--text-muted)]"
            style={{ color: 'var(--text-primary)' }}
          />
          <kbd class="text-[10px] px-1.5 py-0.5 rounded border font-mono" style={{ color: 'var(--text-muted)', 'border-color': 'var(--border-default)' }}>esc</kbd>
        </div>

        {/* Results */}
        <div class="max-h-[46vh] overflow-y-auto py-1.5">
          <For each={filtered()}>
            {(it, i) => (
              <button
                type="button"
                disabled={it.disabled}
                onMouseEnter={() => setActive(i())}
                onClick={() => run(it)}
                class="w-full flex items-center gap-2.5 px-3 mx-1 h-9 rounded-lg text-left transition-all var(--spring-sm) disabled:opacity-40 disabled:cursor-not-allowed"
                style={{
                  width: 'calc(100% - 8px)',
                  background: active() === i() && !it.disabled ? 'var(--bg-hover)' : 'transparent',
                  color: it.danger ? 'var(--danger)' : active() === i() ? 'var(--text-primary)' : 'var(--text-secondary)',
                }}
              >
                <Show when={it.icon}>
                  <span class="w-4 h-4 shrink-0 flex items-center justify-center">{it.icon}</span>
                </Show>
                <span class="flex-1 truncate text-[13px]">{it.label}</span>
                <Show when={it.hint}>
                  <span class="text-[11px] font-mono shrink-0" style={{ color: 'var(--text-muted)' }}>{it.hint}</span>
                </Show>
              </button>
            )}
          </For>

          <Show when={filtered().length === 0}>
            <div class="px-4 py-6 text-center text-[12px]" style={{ color: 'var(--text-muted)' }}>
              No matching commands
            </div>
          </Show>
        </div>

        {/* Footer hint */}
        <div class="flex items-center gap-3 px-3 h-8 border-t text-[10px]" style={{ 'border-color': 'var(--border-subtle)', color: 'var(--text-muted)' }}>
          <span class="flex items-center gap-1"><kbd class="font-mono">↑↓</kbd> navigate</span>
          <span class="flex items-center gap-1"><kbd class="font-mono">↵</kbd> select</span>
          <span class="flex items-center gap-1"><kbd class="font-mono">esc</kbd> close</span>
        </div>
      </div>
    </Show>
  );
}
