import { createSignal, createMemo, createEffect, onMount, onCleanup, For, Show } from 'solid-js';
import { useNavigate } from '@solidjs/router';
import { useSession } from '../context/session';
import { searchSessions, type Session, type SessionSearchHit } from '../api/client';
import { fuzzyMatch } from '../lib/fuzzy';
import { loadPinned } from '../lib/pinned-sessions';
import './session-palette.css';

// The palette's open state is module-level: the shortcut is bound by the
// palette itself (so ⌘K works on routes with no sidebar) and by the sidebar
// (so the panel the user is looking at answers to it too). One signal keeps
// those two from owning separate palettes.
const [paletteOpen, setPaletteOpen] = createSignal(false);

/** Open the ⌘K session search palette. Idempotent. */
export function openSessionPalette() {
  setPaletteOpen(true);
}

export function closeSessionPalette() {
  setPaletteOpen(false);
}

// A row in the palette. Headings and the muted nudge are rows too, so the whole
// list is one array and ↑/↓ walks it — they just are not selectable, and the
// move skips them the way CommandMenu skips disabled items.
type Row =
  | { kind: 'heading'; label: string }
  | { kind: 'hint'; text: string }
  | { kind: 'session'; session: PaletteSession; hit?: SessionSearchHit | undefined }
  | { kind: 'new' };

// The two fields a row draws from a session, whatever it came out of: the
// local session list, or a server hit (whose session row may be older than
// the list the page holds).
interface PaletteSession {
  id: string;
  title: string;
  model?: string | undefined;
  updatedAt: number;
}

const rowSession = (s: Session): PaletteSession => ({
  id: s.id,
  title: s.title,
  model: s.model,
  updatedAt: s.updatedAt,
});

// Compact age for the row's right edge. Deliberately shorter than a sentence:
// it sits beside the title, not under it.
function relativeTime(ts: number): string {
  if (!ts) return '';
  const diffMin = Math.floor((Date.now() - ts) / 60000);
  if (diffMin < 1) return 'now';
  if (diffMin < 60) return `${diffMin}m`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 7) return `${diffDay}d`;
  return new Date(ts).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

// Server snippets wrap each matched term in U+0002 / U+0003. Split on them and
// render the middle segments as <mark>: the text is memory-file content, so it
// goes through JSX as text — never dangerouslySetInnerHTML.
const MATCH_OPEN = '\u0002';
const MATCH_CLOSE = '\u0003';

function Snippet(props: { text: string }) {
  const segments = createMemo(() => {
    const out: { text: string; mark: boolean }[] = [];
    let mark = false;
    let buf = '';
    for (const ch of props.text) {
      if (ch === MATCH_OPEN || ch === MATCH_CLOSE) {
        if (buf) out.push({ text: buf, mark });
        buf = '';
        if (ch === MATCH_OPEN) mark = true;
        else mark = false;
      } else {
        buf += ch;
      }
    }
    if (buf) out.push({ text: buf, mark });
    return out;
  });
  return (
    <For each={segments()}>
      {(seg) => (seg.mark ? <mark class="sp-mark">{seg.text}</mark> : <>{seg.text}</>)}
    </For>
  );
}

// SessionPalette is the ⌘K quick-open: VS Code's one list, but each row is a
// conversation. Local title/model matches render immediately from the session
// list the page already holds; what the sessions were *about* comes from the
// server a beat later, in its own section.
export default function SessionPalette() {
  const session = useSession();
  const navigate = useNavigate();
  const [query, setQuery] = createSignal('');
  const [active, setActive] = createSignal(-1);
  const [hits, setHits] = createSignal<SessionSearchHit[]>([]);
  const [hitsQuery, setHitsQuery] = createSignal('');
  const [searching, setSearching] = createSignal(false);
  const [pinned, setPinned] = createSignal<Set<string>>(new Set());
  let inputRef: HTMLInputElement | undefined;
  // What had focus when the palette opened, so Esc can put the user back
  // where they were instead of on <body>.
  let restoreFocus: HTMLElement | null = null;

  // Reset on open, restore focus on close. Writing signals inside the effect is
  // safe here: it reads only paletteOpen().
  createEffect(() => {
    if (paletteOpen()) {
      restoreFocus = (document.activeElement as HTMLElement | null) ?? null;
      setQuery('');
      setActive(-1);
      setHits([]);
      setHitsQuery('');
      setSearching(false);
      // Re-read pins: they are a local preference the sidebar can change while
      // the palette is closed.
      setPinned(loadPinned());
      queueMicrotask(() => inputRef?.focus());
    } else {
      const el = restoreFocus;
      restoreFocus = null;
      if (el && el.isConnected) queueMicrotask(() => el.focus());
    }
  });

  // Debounced server search. 120 ms folds a burst of keystrokes into one
  // request; the previous request is aborted on the next keystroke, so a slow
  // answer can never overwrite a newer one. Under 2 characters the server
  // answers 400 — so nothing is sent at all.
  createEffect(() => {
    const q = query().trim();
    if (!paletteOpen() || q.length < 2) {
      setHits([]);
      setHitsQuery('');
      setSearching(false);
      return;
    }
    setSearching(true);
    const ctrl = new AbortController();
    const timer = setTimeout(async () => {
      try {
        const res = await searchSessions(q, ctrl.signal);
        if (ctrl.signal.aborted) return;
        setHits(res.results ?? []);
        setHitsQuery(q);
      } catch {
        // Aborted, offline, or a 400 — the local title rows are still on
        // screen, so a failed search only means no extra section.
        if (!ctrl.signal.aborted) {
          setHits([]);
          setHitsQuery(q);
        }
      } finally {
        if (!ctrl.signal.aborted) setSearching(false);
      }
    }, 120);
    onCleanup(() => {
      clearTimeout(timer);
      ctrl.abort();
    });
  });

  const rows = createMemo<Row[]>(() => {
    const q = query().trim();
    // Newest first regardless of how the list arrived, so "Continue" really is
    // the five most recently touched conversations.
    const all = [...session.sessions()].sort((a, b) => b.updatedAt - a.updatedAt);

    if (!q) {
      const pinnedIds = pinned();
      const byId = new Map(all.map((s) => [s.id, s]));
      const pinnedRows = [...pinnedIds]
        .map((id) => byId.get(id))
        .filter((s): s is Session => !!s);
      const recent = all.filter((s) => !pinnedIds.has(s.id)).slice(0, 5);
      const out: Row[] = [];
      if (pinnedRows.length || recent.length) out.push({ kind: 'heading', label: 'Continue' });
      for (const s of pinnedRows) out.push({ kind: 'session', session: rowSession(s) });
      for (const s of recent) out.push({ kind: 'session', session: rowSession(s) });
      out.push({ kind: 'hint', text: 'Type to search what each session was about.' });
      // Last on purpose: continuing is the default, starting a new conversation
      // is a choice.
      out.push({ kind: 'new' });
      return out;
    }

    // A server hit whose session also fuzzy-matched the title keeps its place
    // in "Sessions" and brings its snippet along — the row says both why it is
    // there and what it said.
    const hitById = new Map(hits().map((h) => [h.sessionId, h]));
    const local = all.filter((s) => fuzzyMatch(`${s.title || ''} ${s.model || ''}`, q));
    const localIds = new Set(local.map((s) => s.id));
    const found = hits().filter((h) => !localIds.has(h.sessionId));

    const out: Row[] = [];
    if (local.length) {
      out.push({ kind: 'heading', label: 'Sessions' });
      for (const s of local) {
        out.push({ kind: 'session', session: rowSession(s), hit: hitById.get(s.id) });
      }
    }
    if (found.length) {
      out.push({ kind: 'heading', label: 'Found in memory' });
      for (const h of found) {
        out.push({
          kind: 'session',
          session: { id: h.sessionId, title: h.title, model: h.model, updatedAt: h.updatedAt },
          hit: h,
        });
      }
    }
    return out;
  });

  // The indices ↑/↓ may land on, in render order.
  const selectable = createMemo(() =>
    rows().flatMap((row, i) => (row.kind === 'session' || row.kind === 'new' ? [i] : [])),
  );

  // Keep the highlight on a row that can actually be run: after a query change
  // (active is -1, "nothing picked yet") and whenever the list shrinks.
  createEffect(() => {
    const idx = selectable();
    if (!idx.includes(active())) setActive(idx.length ? idx[0] : -1);
  });

  const moveActive = (delta: number) => {
    const idx = selectable();
    if (idx.length === 0) return;
    const pos = idx.indexOf(active());
    const next = pos < 0 ? (delta > 0 ? 0 : idx.length - 1) : (pos + delta + idx.length) % idx.length;
    setActive(idx[next]);
  };

  const openSession = (id: string) => {
    closeSessionPalette();
    navigate(`/session/${id}`);
  };

  // ⌘⇧N — ⌘N belongs to the browser, so the palette offers the chord a page can
  // still claim. It is also just the last row, which is the reliable path.
  const createNew = async () => {
    closeSessionPalette();
    try {
      const s = await session.newSession();
      navigate(`/session/${s.id}`);
    } catch (e) {
      console.error('new session failed:', e);
    }
  };

  const runRow = (row?: Row) => {
    if (!row) return;
    if (row.kind === 'session') openSession(row.session.id);
    else if (row.kind === 'new') void createNew();
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
      runRow(rows()[active()]);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      closeSessionPalette();
    }
  };

  // Global shortcuts. ⌘K opens from anywhere (the sidebar binds it too, for the
  // panel the user is looking at); ⌘⇧N closes and starts a new session.
  onMount(() => {
    const onShortcut = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey)) return;
      const key = e.key.toLowerCase();
      if (key === 'k' && !e.shiftKey) {
        e.preventDefault();
        openSessionPalette();
        return;
      }
      if (key === 'n' && e.shiftKey && paletteOpen()) {
        e.preventDefault();
        void createNew();
      }
    };
    window.addEventListener('keydown', onShortcut);
    onCleanup(() => window.removeEventListener('keydown', onShortcut));
  });

  const sourceLabel = (hit?: SessionSearchHit) => {
    if (!hit) return '';
    return hit.source === 'memory' ? 'memory' : hit.source === 'turn' ? 'turn' : 'title';
  };

  return (
    <Show when={paletteOpen()}>
      {/* Backdrop — the same blurred overlay the command menu uses */}
      <div
        class="fixed inset-0 z-[200] bg-black/60 backdrop-blur-[3px] animate-fade-in"
        onClick={closeSessionPalette}
        style={{ 'animation-duration': '0.15s' }}
      />

      {/* Palette — slides down from the top with the same spring bounce */}
      <div
        class="fixed left-1/2 top-[18vh] z-[201] w-[min(620px,92vw)] -translate-x-1/2
               rounded-[9px] border overflow-hidden animate-pop-in"
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
            onInput={(e) => {
              setQuery(e.currentTarget.value);
              // A new query starts a new list: the highlight goes back to the
              // first result, like any quick-open.
              setActive(-1);
            }}
            onKeyDown={onKeyDown}
            placeholder="Search sessions and what they were about…"
            aria-label="Search sessions"
            autocomplete="off"
            spellcheck={false}
            class="flex-1 bg-transparent outline-none text-[14px] placeholder:text-[color:var(--text-muted)]"
            style={{ color: 'var(--text-primary)' }}
          />
          <kbd class="text-[10px] px-1.5 py-0.5 rounded border font-mono" style={{ color: 'var(--text-muted)', 'border-color': 'var(--border-default)' }}>
            esc
          </kbd>
        </div>

        {/* Results */}
        <div class="max-h-[52vh] overflow-y-auto py-1.5" role="listbox" aria-label="Sessions">
          <For each={rows()}>
            {(row, i) => {
              if (row.kind === 'heading') {
                return (
                  <div
                    class="px-3 pt-2 pb-1 text-[10px] font-medium uppercase tracking-[0.07em] select-none"
                    style={{ color: 'var(--text-label)' }}
                  >
                    {row.label}
                  </div>
                );
              }

              if (row.kind === 'hint') {
                return (
                  <div class="px-4 py-1.5 text-[11px]" style={{ color: 'var(--text-muted)' }}>
                    {row.text}
                  </div>
                );
              }

              if (row.kind === 'new') {
                return (
                  <button
                    type="button"
                    role="option"
                    aria-selected={active() === i()}
                    onMouseEnter={() => setActive(i())}
                    onClick={() => runRow(row)}
                    class="w-full flex items-center gap-2.5 px-3 mx-1 h-9 rounded-lg text-left transition-all"
                    style={{
                      width: 'calc(100% - 8px)',
                      background: active() === i() ? 'var(--bg-hover)' : 'transparent',
                      color: active() === i() ? 'var(--text-primary)' : 'var(--text-secondary)',
                    }}
                  >
                    <svg class="w-4 h-4 shrink-0" style={{ color: 'var(--text-muted)' }} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M12 5v14M5 12h14" />
                    </svg>
                    <span class="flex-1 truncate text-[13px]">New session</span>
                    <span class="text-[11px] font-mono shrink-0" style={{ color: 'var(--text-muted)' }}>⌘⇧N</span>
                  </button>
                );
              }

              const s = row.session;
              const hit = row.hit;
              const labels = (hit?.labels ?? []).slice(0, 2);
              return (
                <button
                  type="button"
                  role="option"
                  aria-selected={active() === i()}
                  onMouseEnter={() => setActive(i())}
                  onClick={() => runRow(row)}
                  class="w-full flex flex-col gap-0.5 px-3 py-1.5 mx-1 rounded-lg text-left transition-all"
                  style={{
                    width: 'calc(100% - 8px)',
                    background: active() === i() ? 'var(--bg-hover)' : 'transparent',
                    color: active() === i() ? 'var(--text-primary)' : 'var(--text-secondary)',
                  }}
                >
                  {/* Line one: what it was called, when it was, why it is here */}
                  <span class="flex items-baseline gap-2 min-w-0">
                    <span class="flex-1 truncate text-[13px]">{s.title || 'Untitled session'}</span>
                    <Show when={(hit?.matchedTurns ?? 0) > 1}>
                      <span class="text-[10px] shrink-0" style={{ color: 'var(--text-label)' }}>
                        {hit?.matchedTurns} turns
                      </span>
                    </Show>
                    <Show when={sourceLabel(hit)}>
                      <span
                        class="text-[9px] uppercase tracking-[0.06em] font-medium shrink-0 px-1 py-px rounded border"
                        style={{ color: 'var(--text-label)', 'border-color': 'var(--border-subtle)' }}
                      >
                        {sourceLabel(hit)}
                      </span>
                    </Show>
                    <span class="text-[11px] font-mono shrink-0" style={{ color: 'var(--text-label)' }}>
                      {relativeTime(s.updatedAt)}
                    </span>
                  </span>

                  {/* Line two: the matched excerpt, else its topic labels, else
                      the model — never a bare title with dead space under it. */}
                  <Show when={hit?.snippet} fallback={
                    <span class="flex items-center gap-1 min-w-0">
                      <Show
                        when={labels.length}
                        fallback={
                          <span class="text-[11px] truncate" style={{ color: 'var(--text-label)' }}>
                            {s.model || ''}
                          </span>
                        }
                      >
                        <For each={labels}>
                          {(label) => (
                            <span
                              class="text-[10px] px-1 rounded border shrink-0 truncate max-w-[12rem]"
                              style={{ color: 'var(--text-label)', 'border-color': 'var(--border-subtle)' }}
                            >
                              {label}
                            </span>
                          )}
                        </For>
                      </Show>
                    </span>
                  }>
                    <span class="sp-snippet text-[11px]" style={{ color: 'var(--text-muted)' }}>
                      <Snippet text={hit!.snippet!} />
                    </span>
                  </Show>
                </button>
              );
            }}
          </For>

          <Show when={query().trim() && rows().length === 0}>
            <div class="px-4 py-6 text-center text-[12px]" style={{ color: 'var(--text-muted)' }}>
              No matching sessions
            </div>
          </Show>
        </div>

        {/* Footer hint */}
        <div
          class="flex items-center gap-3 px-3 h-8 border-t text-[10px]"
          style={{ 'border-color': 'var(--border-subtle)', color: 'var(--text-muted)' }}
        >
          <span class="flex items-center gap-1"><kbd class="font-mono">↑↓</kbd> navigate</span>
          <span class="flex items-center gap-1"><kbd class="font-mono">↵</kbd> open</span>
          <span class="flex items-center gap-1"><kbd class="font-mono">esc</kbd> close</span>
          <Show when={searching()}>
            <span class="flex-1 text-right">searching…</span>
          </Show>
        </div>
      </div>
    </Show>
  );
}
