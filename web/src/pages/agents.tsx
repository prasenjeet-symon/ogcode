import { For, Show, createEffect, createMemo, createSignal, onCleanup } from 'solid-js';
import { useNavigate } from '@solidjs/router';
import { useServer } from '../context/server';
import { useSession } from '../context/session';
import SessionSidebar from '../components/session-sidebar';
import { DrawerToggle } from '../components/sidebar-shell';
import AgentForceGraph from '../components/agent-force-graph';
import { getBoardRoster, stopBoardTeammate, type BoardHandle } from '../api/client';
import { buildTree, rosterSummary } from '../lib/board-tree';

// The Agents page: the tree of agents working in this directory as a full
// screen. One row per build session or teammate, nested by the spawned_by edge,
// so the shape of the work — who spawned whom, what each one is doing, and how
// many are still going — is visible without opening a session. A row opens its
// session; a running teammate can be stopped, behind a confirm so a stray click
// cannot kill work.
//
// There is no post log to show: agents coordinate by editing files, and a child
// reports back to its parent by finishing (its last message is the result).
//
// It refreshes on board.* events rather than eventTick: the latter also fires
// for every streamed token, which would make the page reload continuously while
// an agent is generating.

// ApiError carries the raw response body behind an "API error NNN: " prefix;
// the page shows the server's line, not that wrapper.
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

export default function AgentsPage() {
  const session = useSession();
  const server = useServer();
  const navigate = useNavigate();

  const [handles, setHandles] = createSignal<BoardHandle[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [loadError, setLoadError] = createSignal('');
  const [armedStop, setArmedStop] = createSignal<string | null>(null);
  const [stopping, setStopping] = createSignal<Set<string>>(new Set());
  const [notice, setNotice] = createSignal<{ text: string; bad: boolean } | null>(null);
  // One clock for every live row: each row's elapsed time is derived from this
  // rather than each row running its own interval.
  const [now, setNow] = createSignal(Date.now());

  const mine = () => session.activeSession()?.id;

  const agents = createMemo(() => handles().filter((h) => h.handle !== 'user'));

  const summary = createMemo(() => rosterSummary(agents()));

  // The graph's rows: the whole tree, unfiltered — every agent is always shown.
  const tree = createMemo(() => buildTree(agents()));

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

  refresh();

  // board.* events are the only ones that change this page. A short debounce
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

  // Poll as a backstop: a teammate's row can move with no event of its own (a
  // loop that exits unexpectedly, a reconcile at startup), and a stale status
  // would leave a dead agent looking live.
  createEffect(() => {
    const id = setInterval(() => refresh(), 15000);
    onCleanup(() => clearInterval(id));
  });

  // The elapsed-time labels only tick while the page is on screen.
  createEffect(() => {
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    onCleanup(() => clearInterval(id));
  });

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
    navigate(`/session/${h.sessionId}`);
  }

  // Arrow keys walk the tree once a row holds focus, so the whole tree stays
  // reachable from the keyboard.
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
    }
  }

  const emptyText = () => {
    if (agents().length === 0) return 'No agent has joined this project yet. Start a build session and it appears here.';
    if (summary().finished > 0) return 'Nothing is working right now — every agent has finished.';
    return 'No agent to show.';
  };

  return (
    <div class="flex h-dvh w-full">
      <SessionSidebar />
      <main class="page-enter flex-1 flex flex-col min-w-0 min-h-0 bg-[color:var(--bg-base)]">
        {/* Page header — the same h-11 band as the session page, so the chrome
            reads as one app across screens. */}
        <header
          class="h-11 shrink-0 border-b border-[color:var(--border-subtle)] flex items-center px-2 sm:px-3.5 gap-2 backdrop-blur-md"
          style={{ background: 'linear-gradient(var(--tint), var(--tint)) color-mix(in srgb, var(--bg-surface) 82%, transparent)', 'z-index': 100, ['padding-top']: 'env(safe-area-inset-top)' }}
        >
          <DrawerToggle drawer="sessions" label="Open navigation" />
          <div class="flex items-center gap-2 min-w-0 flex-1">
            <span class="w-5 h-5 rounded-[6px] flex items-center justify-center shrink-0 bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)] shadow-[inset_0_0_0_1px_var(--accent-ring)]">
              <BoardIcon class="w-3 h-3" />
            </span>
            <h1 class="text-ui font-semibold text-[color:var(--text-primary)] truncate">Agents</h1>
            <span class="text-meta text-[color:var(--text-tertiary)] tabular-nums truncate hidden sm:inline">
              {summary().total} in this project · who spawned whom
            </span>
          </div>
          <button
            type="button"
            onClick={() => refresh()}
            title="Refresh"
            aria-label="Refresh the agent tree"
            class="icon-btn shrink-0"
          >
            <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5" />
            </svg>
          </button>
        </header>

        <Show when={notice()}>
          <div
            role="status"
            class="shrink-0 flex items-start gap-2 px-4 py-2 border-b text-micro leading-[1.4]"
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

        <div class="flex-1 min-h-0 overflow-y-auto overscroll-contain">
          <div class="h-full flex flex-col">

          <Show when={loadError()}>
            <div
              role="alert"
              class="shrink-0 px-4 sm:px-6 py-1.5 flex items-start gap-2 text-micro text-red-300 bg-red-500/10"
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

          {/* The graph, full bleed: no card, no toolbar, no max-height — wide as
              the page and tall as the viewport allows. Every agent is always
              shown; status rides colour alone and the hover tooltip is the only
              text surface on the page. */}
          <div class="relative flex-1 min-h-[320px] flex flex-col">
            <Show
              when={loaded() || loadError()}
              fallback={
                <div class="my-auto space-y-1 px-2 pt-1" aria-busy="true" aria-label="Loading the agent tree">
                  <For each={[92, 78, 84]}>
                    {(w) => (
                      <div class="h-9 rounded-lg bg-[color:var(--bg-elevated)] animate-pulse" style={{ width: `${w}%` }} />
                    )}
                  </For>
                </div>
              }
            >
              <Show
                when={tree().length > 0}
                fallback={
                  <div class="my-auto px-4 py-12 text-center">
                    <span class="mx-auto mb-2.5 w-10 h-10 rounded-lg flex items-center justify-center bg-[color:var(--bg-elevated)] text-[color:var(--text-muted)]">
                      <BoardIcon class="w-5 h-5" />
                    </span>
                    <p class="text-meta text-[color:var(--text-tertiary)] leading-[1.5] max-w-[48ch] mx-auto">
                      {emptyText()}
                    </p>
                  </div>
                }
              >
                <div
                  role="tree"
                  aria-label="Agent graph"
                  onKeyDown={onTreeKey}
                  class="flex-1"
                >
                  <AgentForceGraph
                    rows={tree()}
                    isMe={mine()}
                    now={now()}
                    stopArmed={armedStop()}
                    stopping={stopping()}
                    onOpen={openSession}
                    onStop={stop}
                  />
                  {/* Keyboard/screen-reader surface: the canvas cannot take focus,
                      so the tree walk (and a screen reader) gets one openable
                      button per agent. */}
                  <ul class="sr-only" role="presentation">
                    <For each={tree()}>
                      {(row) => (
                        <li>
                          <button
                            data-board-handle={row.h.handle}
                            onClick={() => openSession(row.h)}
                          >
                            {row.h.handle} — {row.h.status}
                          </button>
                        </li>
                      )}
                    </For>
                  </ul>
                </div>
              </Show>
            </Show>
          </div>

          <p class="shrink-0 text-micro leading-[1.5] text-[color:var(--text-muted)] px-4 sm:px-6 py-2.5">
            Agents share this directory and edit it directly. A teammate reports back to the agent that spawned
            it when it finishes: its last message is the result. A background task (task-*) is a read-only
            investigation that reports the same way — stop it if you no longer want its answer.
          </p>
        </div>
      </div>
    </main>
    </div>
  );
}