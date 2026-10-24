import { For, Show, createEffect, createMemo, createSignal, onCleanup } from 'solid-js';
import { Portal } from 'solid-js/web';
import { useNavigate } from '@solidjs/router';
import { useServer } from '../context/server';
import { useSession } from '../context/session';
import { getBoardRoster, stopBoardTeammate, type BoardHandle } from '../api/client';
import {
  buildTree,
  decorateRows,
  isLive,
  matchesQuery,
  rosterSummary,
  subtreeCounts,
  visibleWithFinishedHidden,
  type DrawerRow,
  type TreeRow,
} from '../lib/board-tree';
import BoardRow from './board-drawer-row';

// The board drawer: the tree of agents working in this directory. One row per
// build session or teammate, nested by the spawned_by edge, so the shape of the
// work — who spawned whom, what each one is doing, and how many are still going
// — is visible without opening a session. A row opens its session; a running
// teammate can be stopped, behind a confirm so a stray click cannot kill work.
//
// There is no post log to show: agents coordinate by editing files, and a child
// reports back to its parent by finishing (its last message is the result).
//
// It refreshes on board.* events rather than eventTick: the latter also fires
// for every streamed token, which would make the drawer reload continuously
// while an agent is generating.

// The same focus ring the Project Index dialogs use: Tab stays inside the panel
// while it is open, so the composer behind it cannot take the keystrokes.
const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

// ApiError carries the raw response body behind an "API error NNN: " prefix;
// the drawer shows the server's line, not that wrapper.
function errorText(e: unknown): string {
  const raw = e instanceof Error ? e.message : String(e);
  const m = raw.match(/^API error \d+: ([\s\S]*)$/);
  const text = (m ? m[1] : raw).trim();
  return text || 'request failed';
}

function BoardIcon(props: { class?: string }) {
  return (
    <svg
      class={props.class ?? 'w-3.5 h-3.5 shrink-0'}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.8"
      aria-hidden="true"
    >
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v4M12 8H6v4M12 8h6v4M6 12v4M18 12v4M4 16h4M16 16h4" />
    </svg>
  );
}

// hideCollapsed drops everything below a collapsed row. Rows arrive depth-first
// and parents before children, so one depth cut is enough: clear it as soon as
// a row at the collapsed depth (or shallower) comes back.
function hideCollapsed(rows: TreeRow[], collapsed: Set<string>): TreeRow[] {
  const out: TreeRow[] = [];
  let cut = -1;
  for (const row of rows) {
    if (cut >= 0 && row.depth > cut) continue;
    cut = -1;
    out.push(row);
    if (collapsed.has(row.h.handle)) cut = row.depth;
  }
  return out;
}

export default function BoardDrawer() {
  const session = useSession();
  const server = useServer();
  const navigate = useNavigate();

  const [open, setOpen] = createSignal(false);
  const [handles, setHandles] = createSignal<BoardHandle[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [loadError, setLoadError] = createSignal('');
  const [query, setQuery] = createSignal('');
  const [showFinished, setShowFinished] = createSignal(false);
  const [collapsed, setCollapsed] = createSignal<Set<string>>(new Set());
  const [armedStop, setArmedStop] = createSignal<string | null>(null);
  const [stopping, setStopping] = createSignal<Set<string>>(new Set());
  const [notice, setNotice] = createSignal<{ text: string; bad: boolean } | null>(null);
  // One clock for every live row: each row's elapsed time is derived from this
  // rather than each row running its own interval.
  const [now, setNow] = createSignal(Date.now());

  let panel: HTMLElement | undefined;
  let searchInput: HTMLInputElement | undefined;
  let trigger: HTMLButtonElement | undefined;

  const mine = () => session.activeSession()?.id;

  const agents = createMemo(() => handles().filter((h) => h.handle !== 'user'));

  // Every agent but the session in view: what the badge counts.
  const liveOthers = () => agents().filter((h) => h.sessionId !== mine() && isLive(h)).length;
  const summary = createMemo(() => rosterSummary(agents()));
  // rosterSummary's `live` is running + idle; the header shows the disjoint
  // split so the three counts add up to the roster total.
  const running = () => summary().live - summary().idle;

  const filtered = createMemo(() => {
    const q = query().trim();
    return q ? agents().filter((h) => matchesQuery(h, q)) : agents();
  });
  const tree = createMemo(() => buildTree(filtered()));
  const visible = createMemo(() =>
    showFinished() ? tree() : visibleWithFinishedHidden(filtered(), tree()),
  );
  const counts = createMemo(() => subtreeCounts(filtered()));
  // Child-ness is read off the uncollapsed rows, so a collapsed parent keeps
  // its chevron (and can be expanded again) even though its children are gone.
  const hasChildren = createMemo(() => {
    const map = new Map<string, boolean>();
    for (const row of decorateRows(visible())) map.set(row.h.handle, row.hasChildren);
    return map;
  });
  const rows = createMemo(() => decorateRows(hideCollapsed(visible(), collapsed())));

  async function loadOnce() {
    try {
      const roster = await getBoardRoster();
      setHandles(roster.agents ?? []);
      setLoadError('');
      setLoaded(true);
    } catch (e) {
      setLoadError(errorText(e));
    }
  }

  // Serialize refreshes: an in-flight read would otherwise swallow the event
  // that arrived while it ran.
  let chain: Promise<void> = Promise.resolve();
  function refresh(): Promise<void> {
    chain = chain.then(loadOnce, loadOnce);
    return chain;
  }

  // The badge has to be right before the drawer is ever opened, so the roster
  // is read on mount too.
  refresh();

  // board.* events are the only ones that change this panel. A short debounce
  // folds the burst a spawn produces into one read.
  let sseTimer: ReturnType<typeof setTimeout> | null = null;
  createEffect(() => {
    const last = server.lastEvent();
    if (!last || !last.type.startsWith('board.')) return;
    if (sseTimer) clearTimeout(sseTimer);
    sseTimer = setTimeout(() => {
      sseTimer = null;
      refresh();
    }, 150);
  });

  let noticeTimer: ReturnType<typeof setTimeout> | null = null;
  let armTimer: ReturnType<typeof setTimeout> | null = null;
  onCleanup(() => {
    if (sseTimer) clearTimeout(sseTimer);
    if (noticeTimer) clearTimeout(noticeTimer);
    if (armTimer) clearTimeout(armTimer);
  });

  function flash(text: string, bad: boolean) {
    if (noticeTimer) clearTimeout(noticeTimer);
    setNotice({ text, bad });
    noticeTimer = setTimeout(() => {
      noticeTimer = null;
      setNotice(null);
    }, 4500);
  }

  // A reconnect means events published while the stream was down never
  // arrived; resync rather than trust the stale tree.
  createEffect(() => {
    server.reconnectTick();
    if (loaded()) refresh();
  });

  // While open, poll as a backstop: a teammate's row can move with no event of
  // its own (a loop that exits unexpectedly, a reconcile at startup), and a
  // stale status would leave a dead agent looking live.
  createEffect(() => {
    if (!open()) return;
    refresh();
    const id = setInterval(() => refresh(), 15000);
    onCleanup(() => clearInterval(id));
  });

  // The elapsed-time labels only tick while the drawer is on screen.
  createEffect(() => {
    if (!open()) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    onCleanup(() => clearInterval(id));
  });

  // The keyboard + focus contract of a modal, adapted to the side panel: focus
  // moves to the filter when it opens and returns to the badge when it closes,
  // Tab stays inside, the page behind cannot scroll, and Escape closes — the
  // composer reads Escape as "stop the agent", which must not fire through an
  // open drawer. Escape clears a filter first, then closes.
  createEffect(() => {
    if (!open()) return;
    const opener = document.activeElement as HTMLElement | null;
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    queueMicrotask(() => searchInput?.focus({ preventScroll: true }));

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        if (query()) {
          setQuery('');
          return;
        }
        setOpen(false);
        return;
      }
      if (e.key !== 'Tab') return;
      const items = [...(panel?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [])].filter(
        (el) => el.offsetParent !== null,
      );
      if (!items.length) return;
      const first = items[0];
      const last = items[items.length - 1];
      const active = document.activeElement;
      if (e.shiftKey && (active === first || !panel?.contains(active))) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && (active === last || !panel?.contains(active))) {
        e.preventDefault();
        first.focus();
      }
    };
    window.addEventListener('keydown', onKey, true);
    onCleanup(() => {
      window.removeEventListener('keydown', onKey, true);
      document.body.style.overflow = prevOverflow;
      const target = opener && opener !== document.body && document.contains(opener) ? opener : trigger;
      target?.focus({ preventScroll: true });
    });
  });

  function toggle() {
    setArmedStop(null);
    setOpen(!open());
  }

  function toggleCollapse(handle: string) {
    const next = new Set(collapsed());
    if (next.has(handle)) next.delete(handle);
    else next.add(handle);
    setCollapsed(next);
  }

  function armStop(handle: string) {
    if (armTimer) clearTimeout(armTimer);
    setArmedStop(handle);
    armTimer = setTimeout(() => {
      armTimer = null;
      setArmedStop(null);
    }, 4000);
  }

  async function stop(handle: string) {
    // First press arms, second press stops: a running teammate cannot be
    // handed back, so the destructive action is never one click away.
    if (armedStop() !== handle) {
      armStop(handle);
      return;
    }
    if (armTimer) clearTimeout(armTimer);
    armTimer = null;
    setArmedStop(null);
    setStopping((prev) => new Set(prev).add(handle));
    try {
      const res = await stopBoardTeammate(handle);
      flash(res.Notice || `Stopped ${handle}`, false);
      await refresh();
    } catch (e) {
      flash(errorText(e), true);
    } finally {
      setStopping((prev) => {
        const next = new Set(prev);
        next.delete(handle);
        return next;
      });
    }
  }

  function openSession(h: BoardHandle) {
    if (!h.sessionId) return;
    setOpen(false);
    navigate(`/session/${h.sessionId}`);
  }

  // Arrow keys walk the tree once a row holds focus; the filter field hands
  // off with ArrowDown so the whole panel is reachable from the keyboard.
  function onTreeKey(e: KeyboardEvent) {
    const host = e.currentTarget as HTMLElement;
    const items = [...host.querySelectorAll<HTMLElement>('[data-board-handle]')];
    if (!items.length) return;
    const at = items.indexOf(document.activeElement as HTMLElement);
    const focusRow = (i: number) => {
      const el = items[Math.max(0, Math.min(items.length - 1, i))];
      el?.focus({ preventScroll: false });
    };
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        focusRow(at < 0 ? 0 : at + 1);
        break;
      case 'ArrowUp':
        e.preventDefault();
        focusRow(at < 0 ? items.length - 1 : at - 1);
        break;
      case 'Home':
        e.preventDefault();
        focusRow(0);
        break;
      case 'End':
        e.preventDefault();
        focusRow(items.length - 1);
        break;
      case 'ArrowRight': {
        const handle = items[at]?.dataset.boardHandle;
        if (handle && hasChildren().get(handle) && collapsed().has(handle)) {
          e.preventDefault();
          toggleCollapse(handle);
        }
        break;
      }
      case 'ArrowLeft': {
        const handle = items[at]?.dataset.boardHandle;
        if (handle && hasChildren().get(handle) && !collapsed().has(handle)) {
          e.preventDefault();
          toggleCollapse(handle);
        }
        break;
      }
    }
  }

  const emptyText = () => {
    if (query().trim()) return `No agent matches “${query().trim()}”.`;
    if (agents().length === 0) return 'No agent has joined this project yet. Start a build session and it appears here.';
    if (summary().finished > 0) return 'Nothing is working right now — every agent has finished.';
    return 'No agent to show.';
  };

  return (
    <>
      <button
        ref={trigger}
        type="button"
        onClick={toggle}
        aria-haspopup="dialog"
        aria-expanded={open()}
        aria-controls="board-drawer"
        aria-label={liveOthers() > 0 ? `Agents, ${liveOthers()} working` : 'Agents'}
        title="The agent tree — who is working and who spawned whom"
        class="h-7 px-2 rounded-md border flex items-center gap-1.5 transition-colors shrink-0"
        classList={{
          'border-[color:var(--accent-ring)] bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)]': open(),
          'border-[color:var(--border-subtle)] text-[color:var(--text-tertiary)] hover:text-[color:var(--text-secondary)] hover:border-[color:var(--border-default)]': !open(),
        }}
      >
        <BoardIcon />
        <Show when={liveOthers() > 0}>
          <span class="text-micro font-medium tabular-nums">{liveOthers()}</span>
        </Show>
      </button>

      <Show when={open()}>
        <Portal>
          <div
            class="fixed inset-0 z-[210] bg-black/45 backdrop-blur-[2px] animate-fade-in"
            onMouseDown={(e) => {
              if (e.target === e.currentTarget) setOpen(false);
            }}
          />
          <aside
            ref={panel}
            id="board-drawer"
            role="dialog"
            aria-modal="true"
            aria-labelledby="board-drawer-title"
            class="fixed inset-y-0 right-0 z-[211] w-[min(94vw,440px)] flex flex-col bg-[color:var(--bg-surface)] border-l border-[color:var(--border-subtle)] shadow-2xl animate-fade-in-right"
            style={{ 'padding-top': 'env(safe-area-inset-top)', 'padding-bottom': 'env(safe-area-inset-bottom)' }}
          >
            <header class="shrink-0 px-4 pt-3.5 pb-3 border-b border-[color:var(--border-subtle)]">
              <div class="flex items-start gap-2.5">
                <span class="w-8 h-8 rounded-[9px] flex items-center justify-center shrink-0 bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)] shadow-[inset_0_0_0_1px_var(--accent-ring)]">
                  <BoardIcon class="w-4 h-4" />
                </span>
                <div class="min-w-0 flex-1 pt-px">
                  <h2 id="board-drawer-title" class="text-ui font-semibold text-[color:var(--text-primary)] leading-tight">
                    Agents
                  </h2>
                  <p class="text-micro text-[color:var(--text-tertiary)] mt-0.5 tabular-nums">
                    {summary().total} agent{summary().total === 1 ? '' : 's'} in this project · shared directory
                  </p>
                </div>
                <div class="flex items-center gap-0.5 -mr-1.5 -mt-0.5">
                  <button
                    type="button"
                    onClick={() => refresh()}
                    title="Refresh"
                    aria-label="Refresh the agent tree"
                    class="icon-btn"
                  >
                    <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                      <path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5" />
                    </svg>
                  </button>
                  <button
                    type="button"
                    onClick={() => setOpen(false)}
                    title="Close (Esc)"
                    aria-label="Close the agent tree"
                    class="icon-btn"
                  >
                    <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                      <path d="M6 6l12 12M18 6L6 18" />
                    </svg>
                  </button>
                </div>
              </div>

              <div class="flex items-center gap-1 mt-2.5" aria-label="Agent counts">
                {/* Disjoint counts: running + idle = live, and the three sum to the total.
                    Counting "live" AND "idle" side by side made the row add up to more
                    agents than the roster holds. */}
                <span class="inline-flex items-center gap-1.5 h-5 px-1.5 rounded-md bg-[color:var(--bg-elevated)] text-micro text-[color:var(--text-secondary)] tabular-nums">
                  <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)]" classList={{ 'animate-pulse': running() > 0 }} />
                  {running()} running
                </span>
                <span class="inline-flex items-center gap-1.5 h-5 px-1.5 rounded-md bg-[color:var(--bg-elevated)] text-micro text-[color:var(--text-secondary)] tabular-nums">
                  <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--text-tertiary)]" />
                  {summary().idle} idle
                </span>
                <span class="inline-flex items-center gap-1.5 h-5 px-1.5 rounded-md bg-[color:var(--bg-elevated)] text-micro text-[color:var(--text-secondary)] tabular-nums">
                  <span class="w-1.5 h-1.5 rounded-full bg-emerald-400" />
                  {summary().finished} finished
                </span>
              </div>
            </header>

            <div class="shrink-0 px-3 py-2 border-b border-[color:var(--border-subtle)] flex items-center gap-1.5">
              <div class="relative flex-1 min-w-0">
                <svg
                  class="absolute left-2 top-1/2 -translate-y-1/2 w-3 h-3 text-[color:var(--text-muted)] pointer-events-none"
                  viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"
                >
                  <circle cx="11" cy="11" r="7" />
                  <path d="m20 20-3.5-3.5" />
                </svg>
                <input
                  ref={searchInput}
                  type="text"
                  value={query()}
                  onInput={(e) => setQuery(e.currentTarget.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'ArrowDown') {
                      const first = panel?.querySelector<HTMLElement>('[data-board-handle]');
                      if (first) {
                        e.preventDefault();
                        first.focus();
                      }
                    }
                  }}
                  placeholder="Filter agents"
                  aria-label="Filter agents by name, title or model"
                  spellcheck={false}
                  autocomplete="off"
                  class="w-full h-7 pl-6 pr-6 rounded-md bg-[color:var(--bg-base)] border border-[color:var(--border-subtle)] text-meta text-[color:var(--text-primary)] placeholder:text-[color:var(--text-muted)] outline-none transition-colors focus:border-[color:var(--accent-ring)]"
                />
                <Show when={query()}>
                  <button
                    type="button"
                    onClick={() => {
                      setQuery('');
                      searchInput?.focus();
                    }}
                    title="Clear filter"
                    aria-label="Clear the filter"
                    class="absolute right-1 top-1/2 -translate-y-1/2 w-5 h-5 flex items-center justify-center rounded text-[color:var(--text-muted)] hover:text-[color:var(--text-secondary)] transition-colors"
                  >
                    <svg class="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                      <path d="M6 6l12 12M18 6L6 18" />
                    </svg>
                  </button>
                </Show>
              </div>
              <button
                type="button"
                onClick={() => setShowFinished(!showFinished())}
                aria-pressed={showFinished()}
                title={showFinished() ? 'Hide finished agents' : 'Show finished agents'}
                class="h-7 px-2 rounded-md border flex items-center gap-1.5 text-micro tabular-nums transition-colors shrink-0 bg-[color:var(--bg-base)] outline-none focus-visible:ring-1 focus-visible:ring-[color:var(--accent-ring)]"
                classList={{
                  // Checked keeps the pill neutral — the checkbox alone carries
                  // the accent. A full accent wash turned the control into a
                  // red slab that read as "13 failures" (red is this palette's
                  // failed tone; the finished chip above is green), and the
                  // cramped tick was indistinguishable from a warning badge.
                  'border-[color:var(--border-default)] text-[color:var(--text-primary)] hover:border-[color:var(--border-strong)]': showFinished(),
                  'border-[color:var(--border-subtle)] text-[color:var(--text-tertiary)] hover:text-[color:var(--text-secondary)] hover:border-[color:var(--border-default)]': !showFinished(),
                }}
              >
                <span
                  class="w-3 h-3 rounded-[3px] border flex items-center justify-center shrink-0"
                  classList={{
                    'border-[color:var(--accent)] bg-[color:var(--accent)] text-[color:var(--on-primary)]': showFinished(),
                    'border-[color:var(--border-default)]': !showFinished(),
                  }}
                  aria-hidden="true"
                >
                  <Show when={showFinished()}>
                    <svg class="w-2 h-2" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                      <path d="M20 6 9 17l-5-5" />
                    </svg>
                  </Show>
                </span>
                Finished {summary().finished}
              </button>
            </div>

            <Show when={loadError()}>
              <div
                role="alert"
                class="shrink-0 flex items-start gap-2 px-3 py-2 text-micro text-red-300 bg-red-500/10 border-b border-red-500/20"
              >
                <svg class="w-3.5 h-3.5 shrink-0 mt-px" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                  <path d="M12 9v4M12 17h.01" />
                  <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" stroke-linejoin="round" />
                </svg>
                <span class="flex-1 min-w-0 break-words leading-[1.4]">{loadError()}</span>
                <button
                  type="button"
                  onClick={() => refresh()}
                  class="shrink-0 text-micro font-medium text-red-200 hover:text-red-100 underline underline-offset-2 transition-colors"
                >
                  Retry
                </button>
              </div>
            </Show>

            <div class="flex-1 min-h-0 overflow-y-auto overscroll-contain px-2 py-2">
              <Show
                when={loaded() || loadError()}
                fallback={
                  <div class="space-y-1 px-1 pt-1" aria-busy="true" aria-label="Loading the agent tree">
                    <For each={[92, 78, 84]}>
                      {(w) => (
                        <div class="h-9 rounded-lg bg-[color:var(--bg-elevated)] animate-pulse" style={{ width: `${w}%` }} />
                      )}
                    </For>
                  </div>
                }
              >
                <Show
                  when={rows().length > 0}
                  fallback={
                    <div class="px-2 py-8 text-center">
                      <span class="mx-auto mb-2.5 w-8 h-8 rounded-lg flex items-center justify-center bg-[color:var(--bg-elevated)] text-[color:var(--text-muted)]">
                        <BoardIcon class="w-4 h-4" />
                      </span>
                      <p class="text-meta text-[color:var(--text-tertiary)] leading-[1.5] max-w-[36ch] mx-auto">
                        {emptyText()}
                      </p>
                      <Show when={!showFinished() && summary().finished > 0}>
                        <button
                          type="button"
                          onClick={() => setShowFinished(true)}
                          class="mt-2 text-micro text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] underline underline-offset-2 transition-colors"
                        >
                          Show {summary().finished} finished
                        </button>
                      </Show>
                    </div>
                  }
                >
                  <div role="tree" aria-label="Agent tree" onKeyDown={onTreeKey}>
                    <For each={rows()}>
                      {(row) => (
                        <BoardRow
                          row={{ ...row, hasChildren: hasChildren().get(row.h.handle) ?? false }}
                          counts={counts().get(row.h.handle) ?? { total: 0, live: 0 }}
                          isMe={!!row.h.sessionId && row.h.sessionId === mine()}
                          collapsed={collapsed().has(row.h.handle)}
                          stopArmed={armedStop() === row.h.handle}
                          stopping={stopping().has(row.h.handle)}
                          now={now()}
                          onOpen={(r: DrawerRow) => openSession(r.h)}
                          onStop={stop}
                          onToggleCollapse={toggleCollapse}
                        />
                      )}
                    </For>
                  </div>
                </Show>
              </Show>
            </div>

            <Show when={notice()}>
              <div
                role="status"
                class="shrink-0 flex items-start gap-2 px-3 py-2 border-t text-micro leading-[1.4]"
                classList={{
                  'border-emerald-500/20 bg-emerald-500/10 text-emerald-300': !notice()!.bad,
                  'border-red-500/20 bg-red-500/10 text-red-300': notice()!.bad,
                }}
              >
                <span class="flex-1 min-w-0 break-words">{notice()!.text}</span>
                <button
                  type="button"
                  onClick={() => setNotice(null)}
                  aria-label="Dismiss"
                  class="shrink-0 text-current opacity-70 hover:opacity-100 transition-opacity"
                >
                  <svg class="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                    <path d="M6 6l12 12M18 6L6 18" />
                  </svg>
                </button>
              </div>
            </Show>

            <details class="group shrink-0 border-t border-[color:var(--border-subtle)] px-3 py-2">
              <summary class="flex items-center gap-1 text-micro text-[color:var(--text-muted)] cursor-pointer select-none list-none hover:text-[color:var(--text-secondary)] transition-colors [&::-webkit-details-marker]:hidden">
                <svg class="w-3 h-3 transition-transform group-open:rotate-90" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="m9 6 6 6-6 6" />
                </svg>
                How the tree works
              </summary>
              <p class="mt-1.5 text-micro leading-[1.5] text-[color:var(--text-muted)]">
                Agents share this directory and edit it directly. A teammate reports back to the agent that spawned
                it when it finishes: its last message is the result. A background task (task-*) is a read-only
                investigation that reports the same way — stop it if you no longer want its answer.
              </p>
            </details>
          </aside>
        </Portal>
      </Show>
    </>
  );
}
