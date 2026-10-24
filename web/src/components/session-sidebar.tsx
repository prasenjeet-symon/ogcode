import { useNavigate, useLocation } from '@solidjs/router';
import { useSession } from '../context/session';
import { useServer } from '../context/server';
import { createSignal, createMemo, createEffect, For, Show, onCleanup, onMount, type JSXElement } from 'solid-js';
import { deleteSession, getBoardRoster, type BoardHandle } from '../api/client';
import type { Session } from '../api/client';
import { isLive, statusMeta } from '../lib/board-tree';
import { createSidebarWidth } from '../lib/sidebar-width';
import Logo from './logo';
import SidebarShell from './sidebar-shell';

function formatTime(ts: number): string {
  const d = new Date(ts);
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return 'now';
  if (diffMin < 60) return `${diffMin}m`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 7) return `${diffDay}d`;
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

function shortenPath(path: string): string {
  if (!path) return '';
  const home = path.match(/^\/(Users|home)\/[^/]+/);
  const collapsed = home ? path.replace(home[0], '~') : path;
  const segments = collapsed.split('/').filter(Boolean);
  if (segments.length <= 2) return collapsed;
  return `${collapsed.startsWith('~') ? '~' : ''}/…/${segments.slice(-2).join('/')}`;
}

// Pinned sessions are a local preference (there is no server field for them).
// Stored as a plain list of ids; they keep the newest-first order they arrive in.
const PIN_STORAGE_KEY = 'ogcode-pinned-sessions';

function loadPinned(): Set<string> {
  try {
    const raw = localStorage.getItem(PIN_STORAGE_KEY);
    if (!raw) return new Set();
    const arr = JSON.parse(raw);
    return Array.isArray(arr) ? new Set(arr.filter((x) => typeof x === 'string')) : new Set();
  } catch {
    return new Set();
  }
}

// The sidebar itself. Exported wrapped in SidebarShell so every page that
// renders <SessionSidebar /> gets the drawer behaviour below lg for free.
export default function SessionSidebar() {
  return (
    <SidebarShell drawer="sessions">
      <SessionSidebarInner />
    </SidebarShell>
  );
}

function SessionSidebarInner() {
  const session = useSession();
  const server = useServer();
  const navigate = useNavigate();
  const location = useLocation();
  const notesOn = () => server.notesEnabled();
  const deviceOn = () => server.devicePanelEnabled();
  const [query, setQuery] = createSignal('');
  const [collapsed, setCollapsed] = createSignal(false);
  const [editingId, setEditingId] = createSignal<string | null>(null);
  const [draftTitle, setDraftTitle] = createSignal('');
  const [pinned, setPinned] = createSignal<Set<string>>(loadPinned());
  const [pinnedOpen, setPinnedOpen] = createSignal(true);
  const sidebar = createSidebarWidth();
  let searchInput: HTMLInputElement | undefined;

  const togglePin = (e: MouseEvent, id: string) => {
    e.stopPropagation();
    setPinned((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      try {
        localStorage.setItem(PIN_STORAGE_KEY, JSON.stringify([...next]));
      } catch {
        // private mode / quota — the pin still holds for this session
      }
      return next;
    });
  };

  // ⌘K / Ctrl-K focuses the session search. Nothing else binds it. The shell
  // renders two copies of this panel (desktop inline + mobile drawer), so the
  // guard acts only on the visible one — the hidden copy has no offsetParent.
  const onSearchShortcut = (e: KeyboardEvent) => {
    if (!((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k')) return;
    const el = searchInput;
    if (!el || !el.offsetParent) return;
    e.preventDefault();
    el.focus();
    el.select();
  };
  if (typeof window !== 'undefined') {
    window.addEventListener('keydown', onSearchShortcut);
    onCleanup(() => window.removeEventListener('keydown', onSearchShortcut));
  }

  const startRename = (s: { id: string; title: string }) => {
    setEditingId(s.id);
    setDraftTitle(s.title || '');
  };

  const commitRename = async (id: string) => {
    const title = draftTitle().trim();
    setEditingId(null);
    if (title === (session.sessions().find((s) => s.id === id)?.title || '')) return;
    await session.renameSession(id, title);
  };

  const cancelRename = () => {
    setEditingId(null);
    setDraftTitle('');
  };

  // Focus the rename input by hand: browsers honour the autofocus attribute
  // only once per page, so later renames opened with focus still on <body>.
  // Deferred a microtask so the input is in the DOM. An untouched title is
  // selected for a quick overwrite; an edited one keeps the caret at the end,
  // since the rows are rebuilt on every session-list change (any
  // session.updated) and re-selecting would let the next keystroke wipe it.
  const focusRenameInput = (el: HTMLInputElement, title: string) =>
    queueMicrotask(() => {
      el.focus();
      if (el.value === title) el.select();
    });

  // Chrome also fires blur when it removes the focused input — on Escape,
  // after Enter, when the rows are rebuilt — and fires it while the input is
  // still in the DOM. Checking a microtask later, once the removal is done,
  // lets only a real blur (a click away) commit.
  const commitOnBlur = (el: HTMLInputElement, id: string) =>
    queueMicrotask(() => {
      if (el.isConnected) commitRename(id);
    });

  const handleNew = async () => {
    const s = await session.newSession();
    navigate(`/session/${s.id}`);
  };

  const handleSelect = (id: string) => {
    session.selectSession(id);
    navigate(`/session/${id}`);
  };

  const handleDelete = async (e: MouseEvent, id: string) => {
    e.stopPropagation();
    if (!confirm('Delete this session? This cannot be undone.')) return;
    // Capture before the awaits — reading activeSession afterwards is fragile,
    // since anything that clears it in the meantime would silently skip the
    // redirect and leave the user on /session/<deleted-id>.
    const wasActive = session.activeSession()?.id === id;
    try {
      await deleteSession(id);
      await session.refresh();
      if (wasActive) {
        navigate('/', { replace: true });
      }
    } catch (err) {
      console.error('delete session failed:', err);
    }
  };

  const filtered = createMemo(() => {
    const q = query().trim().toLowerCase();
    const list = session.sessions();
    if (!q) return list;
    return list.filter((s) =>
      (s.title || '').toLowerCase().includes(q) ||
      (s.model || '').toLowerCase().includes(q)
    );
  });

  // Sessions arrive newest-first, so bucketing them by age keeps that order
  // while giving the eye somewhere to land. A flat list of 40 near-identical
  // rows reads as a wall; "Today / Yesterday / …" turns it into a timeline you
  // can skim for the conversation you half-remember having last week.
  const GROUPS: { label: string; maxAgeDays: number }[] = [
    { label: 'Today', maxAgeDays: 0 },
    { label: 'Yesterday', maxAgeDays: 1 },
    { label: 'Previous 7 days', maxAgeDays: 7 },
    { label: 'Previous 30 days', maxAgeDays: 30 },
    { label: 'Older', maxAgeDays: Infinity },
  ];

  const grouped = createMemo(() => {
    const startOfToday = new Date();
    startOfToday.setHours(0, 0, 0, 0);
    const dayMs = 86_400_000;

    const list = filtered().filter((s) => !pinned().has(s.id));
    const buckets = GROUPS.map((g) => ({ label: g.label, items: [] as typeof list }));
    for (const s of list) {
      // Age in calendar days, so "yesterday at 11pm" never reads as "today".
      const days = Math.floor((startOfToday.getTime() - s.updatedAt) / dayMs) + 1;
      const idx = GROUPS.findIndex((g) => days <= g.maxAgeDays);
      buckets[idx === -1 ? GROUPS.length - 1 : idx].items.push(s);
    }
    return buckets.filter((b) => b.items.length > 0);
  });

  const pinnedSessions = createMemo(() => filtered().filter((s) => pinned().has(s.id)));

  // Cluster chips (design of the same name in the sidebar-grouping mockups):
  // the session list joins the agent board — the roster the board drawer
  // renders — without any API change. Every agent on the roster climbs its
  // spawned_by edge to the root row the build session joined the board as, and
  // the chip on that session's row counts the descendants; clicking the chip
  // folds them in beneath the row. Sessions with no agents render exactly as
  // before. The session's own root row is not a chip member: the chip counts
  // only who the session spawned, not the session itself.
  const [roster, setRoster] = createSignal<BoardHandle[]>([]);
  const [expanded, setExpanded] = createSignal<Set<string>>(new Set());

  const teamsById = createMemo(() => {
    const byHandle = new Map<string, BoardHandle>();
    for (const h of roster()) if (h.handle !== 'user') byHandle.set(h.handle, h);
    const teams = new Map<string, { rootHandle: string; members: BoardHandle[] }>();
    for (const h of byHandle.values()) {
      // Climb cycle-safely: two rows naming each other — nothing writes that,
      // but a hand-edited database could hold it — must not hang the memo.
      const seen = new Set<string>([h.handle]);
      let root: BoardHandle | undefined = h;
      while (root && root.spawnedBy && byHandle.has(root.spawnedBy) && !seen.has(root.spawnedBy)) {
        seen.add(root.spawnedBy);
        root = byHandle.get(root.spawnedBy);
      }
      if (!root || root === h || !root.sessionId) continue;
      const team = teams.get(root.sessionId) ?? { rootHandle: root.handle, members: [] as BoardHandle[] };
      team.members.push(h);
      teams.set(root.sessionId, team);
    }
    return teams;
  });

  const toggleTeam = (e: MouseEvent, id: string) => {
    e.stopPropagation();
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  // The chip has to be live: the same refresh pattern as the board drawer —
  // board.* SSE events folded into one debounced read 150 ms later, a resync
  // on reconnect (events published while the stream was down never arrived),
  // and a 15 s poll as backstop for moves that publish no event of their own.
  const refreshRoster = async () => {
    try {
      const r = await getBoardRoster();
      setRoster(r.agents ?? []);
    } catch (err) {
      // The board may be unavailable (503 when it is nil); the last roster
      // keeps rendering — a chip is a summary, not ground truth.
      console.error('board roster failed:', err);
    }
  };

  let sseTimer: ReturnType<typeof setTimeout> | null = null;
  createEffect(() => {
    const last = server.lastEvent();
    if (!last || !last.type.startsWith('board.')) return;
    if (sseTimer) clearTimeout(sseTimer);
    sseTimer = setTimeout(() => {
      sseTimer = null;
      refreshRoster();
    }, 150);
  });
  createEffect(() => {
    server.reconnectTick();
    refreshRoster();
  });
  onMount(() => {
    const id = setInterval(refreshRoster, 15_000);
    onCleanup(() => clearInterval(id));
  });

  // One row renderer shared by the Pinned zone and the date groups.
  const sessionRow = (s: Session) => {
    const isActive = () => session.activeSession()?.id === s.id;
    const isEditing = () => editingId() === s.id;
    const isPinned = () => pinned().has(s.id);
    // All reads via the memos are inside JSX, so a session that gains — or
    // loses — agents on the roster re-renders.
    const members = createMemo(() => teamsById().get(s.id)?.members ?? []);
    const liveCount = () => members().filter((m) => isLive(m)).length;
    const finCount = () => members().length - liveCount();
    return (
      <div
        class={`group/row relative flex items-center gap-2 rounded-md h-7 pl-2.5 pr-1.5 text-ui transition-colors
          ${isActive()
            ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)] font-medium'
            : 'text-[color:var(--text-secondary)] hover:bg-[color:var(--bg-hover)] hover:text-[color:var(--text-primary)]'
          }`}
      >
        {/* Active marker: a rail on the row's edge rather than a dot inline
            with the title, so titles all start on the same x whether or not
            the row is selected. */}
        <Show when={isActive()}>
          <span aria-hidden="true" class="absolute left-0 top-1.5 bottom-1.5 w-[2px] rounded-full bg-[color:var(--accent)]" />
        </Show>
        <Show
          when={!isEditing()}
          fallback={
            <input
              ref={(el) => focusRenameInput(el, s.title || '')}
              type="text"
              value={draftTitle()}
              onInput={(e) => setDraftTitle(e.currentTarget.value)}
              onBlur={(e) => commitOnBlur(e.currentTarget, s.id)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') { e.preventDefault(); commitRename(s.id); }
                else if (e.key === 'Escape') { e.preventDefault(); cancelRename(); }
              }}
              onClick={(e) => e.stopPropagation()}
              onDblClick={(e) => e.stopPropagation()}
              placeholder="Untitled"
              class="flex-1 min-w-0 bg-[color:var(--bg-base)] border border-[color:var(--border-default)]
                     rounded px-1.5 h-5.5 text-ui text-[color:var(--text-primary)]
                     focus:outline-none focus:border-[color:var(--accent)]"
            />
          }
        >
          {/* The row itself is not interactive: a button inside it takes the
              click and the focus, so the row is reachable by keyboard (and
              announced) without turning the whole div into a control. */}
          <button
            type="button"
            onClick={() => handleSelect(s.id)}
            onDblClick={() => startRename(s)}
            title={s.title || 'Untitled'}
            aria-current={isActive() ? 'true' : undefined}
            class="flex items-center gap-2 flex-1 min-w-0 h-full text-left cursor-pointer"
          >
            <Show when={s.status === 'running' || s.status === 'failed'}>
              <span
                role="img"
                aria-label={s.status === 'failed' ? 'Task failed' : 'Task running'}
                title={s.status === 'failed' ? 'Task failed' : 'Task running'}
                class={`w-1.5 h-1.5 rounded-full shrink-0 ${s.status === 'failed' ? 'bg-[color:var(--danger)]' : 'bg-amber-400 animate-pulse'}`}
              />
            </Show>
            <span class="truncate flex-1 min-w-0">{s.title || 'Untitled'}</span>
          </button>
          {/* The agent-cluster chip (design D): live agents on the row's end —
              orange, finished — grey. Clicking unfolds the team in flow. */}
          <Show when={members().length > 0}>
            <button
              type="button"
              onClick={(e) => toggleTeam(e, s.id)}
              title={`${members().length} agent${members().length === 1 ? '' : 's'} · ${liveCount()} live${expanded().has(s.id) ? ' — click to hide' : ' — click to show'}`}
              aria-label={expanded().has(s.id) ? `Hide ${members().length} agents` : `Show ${members().length} agents`}
              aria-expanded={expanded().has(s.id) ? 'true' : 'false'}
              class="flex items-center -space-x-1 shrink-0 cursor-pointer"
            >
              <Show when={liveCount() > 0}>
                <i class="flex items-center justify-center w-3.5 h-3.5 rounded-full border-[1.5px] border-[color:var(--bg-surface)] bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)] text-[8px] font-bold not-italic font-mono" title="Live teammates">
                  {liveCount()}
                </i>
              </Show>
              <Show when={finCount() > 0}>
                <i class="flex items-center justify-center w-3.5 h-3.5 rounded-full border-[1.5px] border-[color:var(--bg-surface)] bg-[color:var(--bg-elevated)] text-[color:var(--text-label)] text-[8px] font-bold not-italic font-mono" title="Finished teammates">
                  {finCount()}
                </i>
              </Show>
            </button>
          </Show>
          {/* Timestamp and actions share the row end. The time stays put on
              hover — the actions appear beside it rather than replacing it,
              so the moment you were reading is not the moment it vanishes. */}
          <span class={`text-micro tabular-nums shrink-0 ${isActive() ? 'text-[color:var(--accent-ink)]' : 'text-[color:var(--text-label)]'}`}>
            {formatTime(s.updatedAt)}
          </span>
          <span class={`group-hover/row:flex items-center gap-0.5 shrink-0 ${isPinned() ? 'flex' : 'hidden'}`}>
            <button
              onClick={(e) => togglePin(e, s.id)}
              title={isPinned() ? 'Unpin' : 'Pin'}
              aria-label={isPinned() ? 'Unpin session' : 'Pin session'}
              class={`w-5 h-5 rounded flex items-center justify-center transition-colors
                     ${isPinned()
                       ? 'text-[color:var(--accent-ink)] hover:bg-[color:var(--bg-elevated)]'
                       : 'text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)]'
                     }`}
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M9 4.5h6l-.9 5.4 2.4 2.4H7.5l2.4-2.4L9 4.5zm3 7.8V19.5" />
              </svg>
            </button>
            <button
              onClick={(e) => { e.stopPropagation(); startRename(s); }}
              title="Rename"
              aria-label="Rename session"
              class="w-5 h-5 rounded flex items-center justify-center transition-colors
                     text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)]"
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931zm0 0L19.5 7.125" />
              </svg>
            </button>
            <button
              onClick={(e) => handleDelete(e, s.id)}
              title="Delete"
              aria-label="Delete session"
              class="w-5 h-5 rounded flex items-center justify-center transition-colors
                     text-[color:var(--text-tertiary)] hover:text-red-400 hover:bg-red-500/10"
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6M1 7h22M10 7V4a1 1 0 011-1h2a1 1 0 011 1v3" />
              </svg>
            </button>
          </span>
        </Show>
      </div>
    );
  };

  // The unfolded team (design D): an agent row per member beneath a clicked
  // session row. Nested teammates sit on their own rail level, read-only task
  // children carry a T badge, and a click opens the member's session. Sibling
  // order is the roster's — the order the agents registered in.
  const teamRows = (s: Session) => {
    const byParent = createMemo(() => {
      const team = teamsById().get(s.id);
      if (!team) return null;
      const map = new Map<string, BoardHandle[]>();
      for (const m of team.members) {
        const key = m.spawnedBy || '';
        const list = map.get(key) ?? [];
        list.push(m);
        map.set(key, list);
      }
      return { root: team.rootHandle, map };
    });
    const walk = (depth: number, m: BoardHandle): JSXElement => {
      const pad = 10 + depth * 14;
      const railX = pad - 7;
      const meta = statusMeta(m.status);
      return (
        <div>
          <div
            style={{ 'padding-left': `${pad}px` }}
            onClick={() => m.sessionId && handleSelect(m.sessionId)}
            title={m.sessionId ? `Open session — ${m.title || m.handle} (${meta.label})` : `${m.handle} (${meta.label})`}
            class="group/row relative flex items-center gap-2 h-6 rounded-md pr-1.5 text-meta cursor-pointer
                   text-[color:var(--text-secondary)] hover:bg-[color:var(--bg-hover)] hover:text-[color:var(--text-primary)]"
          >
            {/* Indent rail + elbow, drawn from the depth, one pair per level
                — the guide lines the board drawer's rows draw. */}
            <span aria-hidden="true" class="absolute top-0 bottom-0 w-px bg-[color:var(--border-default)]" style={{ left: `${railX}px` }} />
            <span aria-hidden="true" class="absolute top-1/2 -translate-y-1/2 h-px w-2.5 bg-[color:var(--border-default)]" style={{ left: `${railX}px` }} />
            <span role="img" aria-label={meta.label} class={`w-1.5 h-1.5 rounded-full shrink-0 ${meta.dot}`} />
            <Show when={m.kind === 'task'}>
              <span class="shrink-0 text-[8px] font-bold leading-none tracking-[0.05em] not-italic px-1 py-[2px] rounded bg-[color:var(--bg-elevated)] text-[color:var(--text-tertiary)] border border-[color:var(--border-subtle)]">T</span>
            </Show>
            <span class="truncate flex-1 min-w-0">{m.title || m.handle}</span>
            <span class="text-micro tabular-nums shrink-0 text-[color:var(--text-label)] opacity-0 group-hover/row:opacity-100">open</span>
          </div>
          <For each={byParent()?.map.get(m.handle) ?? []}>{(next) => walk(depth + 1, next)}</For>
        </div>
      );
    };
    return <For each={byParent()?.map.get(byParent()?.root ?? '') ?? []}>{(m) => walk(1, m)}</For>;
  };

  // A session row, plus its unfolded team when the chip was clicked. One
  // wrapper the Pinned zone and the date groups both render.
  const sessionItem = (s: Session) => (
    <div>
      {sessionRow(s)}
      <Show when={expanded().has(s.id) && (teamsById().get(s.id)?.members.length ?? 0) > 0}>
        <div class="pb-0.5">{teamRows(s)}</div>
      </Show>
    </div>
  );

  return (
    <Show
      when={!collapsed()}
      fallback={
        <div class="w-12 border-r border-[color:var(--border-subtle)] flex flex-col items-center py-2 gap-1 bg-[color:var(--bg-surface)]">
          <button
            onClick={() => setCollapsed(false)}
            title="Expand sidebar"
            aria-label="Expand sidebar"
            class="w-8 h-8 rounded-lg text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition-all active:scale-[0.92]"
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 6h16M4 12h16M4 18h16" />
            </svg>
          </button>
          <button
            onClick={handleNew}
            title="New session"
            aria-label="New session"
            class="w-8 h-8 rounded-lg text-zinc-400 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition-all active:scale-[0.92]"
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
            </svg>
          </button>
          <div class="flex-1" />
          <Show when={notesOn()}>
            <button
              onClick={() => navigate('/notes')}
              title="Notes"
              aria-label="Notes"
              class={`w-8 h-8 rounded-lg flex items-center justify-center transition-all active:scale-[0.92]
                ${location.pathname.startsWith('/notes')
                  ? 'text-[color:var(--accent-ink)] bg-[color:var(--accent-soft)]'
                  : 'text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)]'
                }`}
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
              </svg>
            </button>
          </Show>
          <button
            onClick={() => navigate('/docindex')}
            title="Project Index"
            aria-label="Project Index"
            class={`w-8 h-8 rounded-lg flex items-center justify-center transition-all active:scale-[0.92]
              ${location.pathname.startsWith('/docindex')
                ? 'text-[color:var(--accent-ink)] bg-[color:var(--accent-soft)]'
                : 'text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)]'
              }`}
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
            </svg>
          </button>
          <button
            onClick={() => navigate('/settings', { state: { from: location.pathname } })}
            title="Settings"
            aria-label="Settings"
            class="w-8 h-8 rounded-lg text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition-all active:scale-[0.92]"
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065zM15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
          </button>
        </div>
      }
    >
    <div class="relative shrink-0 border-r border-[color:var(--border-subtle)] flex flex-col" style={{ width: `${sidebar.width()}px`, background: 'linear-gradient(var(--tint), var(--tint)) var(--bg-surface)' }}>
      {/* Header: brand + collapse + new */}
      <div class="h-12 shrink-0 px-3 flex items-center gap-2">
        <button
          onClick={() => navigate('/')}
          title="Home"
          aria-label="Home"
          class="flex items-center gap-2 flex-1 min-w-0 group"
        >
          <span class="w-6 h-6 rounded-md bg-[color:var(--accent)] flex items-center justify-center shadow-sm shadow-[color:var(--accent)]/15 ring-1 ring-white/10 shrink-0 group-hover:shadow-[color:var(--accent)]/25 transition-shadow">
            <Logo class="w-3 h-3 text-[color:var(--on-primary)]" small />
          </span>
          <span class="text-ui font-semibold text-[color:var(--text-primary)] truncate transition-colors">ogcode</span>
        </button>
        <button
          onClick={handleNew}
          title="New session"
          aria-label="New session"
          class="icon-btn transition-colors"
        >
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 5v14M5 12h14" />
          </svg>
        </button>
        <button
          onClick={() => setCollapsed(true)}
          title="Collapse sidebar"
          aria-label="Collapse sidebar"
          class="icon-btn transition-colors"
        >
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M11 19l-7-7 7-7m8 14l-7-7 7-7" />
          </svg>
        </button>
      </div>

      {/* Search */}
      <div class="px-3 pb-2">
        <div class="relative">
          <svg class="w-3 h-3 text-[color:var(--text-label)] absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            ref={(el) => { searchInput = el; }}
            type="text"
            value={query()}
            onInput={(e) => setQuery(e.currentTarget.value)}
            placeholder="Search sessions"
            aria-label="Search sessions"
            class="w-full h-7 pl-7 pr-10 bg-[color:var(--bg-base)] border border-transparent
                   rounded-md text-meta text-[color:var(--text-primary)] placeholder:text-[color:var(--text-label)]
                   focus:outline-none focus:border-[color:var(--accent)] focus:bg-[color:var(--bg-elevated)]
                   transition-colors"
          />
          <Show when={!query()}>
            <kbd class="absolute right-1.5 top-1/2 -translate-y-1/2 pointer-events-none text-[10px] font-mono leading-none text-[color:var(--text-label)] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] rounded px-1 py-px">
              ⌘K
            </kbd>
          </Show>
        </div>
      </div>

      {/* Session list */}
      <div class="flex-1 overflow-y-auto px-2 pt-0.5 pb-2">
        <Show when={pinnedSessions().length > 0}>
          <div class="mb-1">
            <button
              type="button"
              onClick={() => setPinnedOpen((v) => !v)}
              class="w-full flex items-center gap-1 px-2 pt-2 pb-1 text-micro font-medium uppercase tracking-[0.07em] text-[color:var(--text-label)] select-none hover:text-[color:var(--text-secondary)] transition-colors"
            >
              <svg class={`w-2.5 h-2.5 transition-transform ${pinnedOpen() ? '' : '-rotate-90'}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
              </svg>
              <span>Pinned</span>
            </button>
            <Show when={pinnedOpen()}>
              <For each={pinnedSessions()}>{(s) => sessionItem(s)}</For>
            </Show>
          </div>
        </Show>
        <For each={grouped()}>
          {(group) => (
            <div class="mb-1">
              <div class="px-2 pt-2 pb-1 text-micro font-medium uppercase tracking-[0.07em] text-[color:var(--text-label)] select-none">
                {group.label}
              </div>
              <For each={group.items}>{(s) => sessionItem(s)}</For>
            </div>
          )}
        </For>
        <Show when={filtered().length === 0}>
          <div class="px-3 py-10 text-center text-meta text-[color:var(--text-label)]">
            {query() ? 'No matches' : 'No sessions yet'}
          </div>
        </Show>
      </div>

      {/* Workspace nav — above footer */}
      <div class="shrink-0 px-2 pb-1">
        <div class="px-2 pt-2 pb-1 text-micro font-medium uppercase tracking-[0.07em] text-[color:var(--text-label)] select-none">
          Workspace
        </div>
        <Show when={notesOn()}>
          <button
            type="button"
            onClick={() => navigate('/notes')}
            class={`w-full flex items-center gap-2 px-2.5 h-7 rounded-md text-ui transition-colors
              ${location.pathname.startsWith('/notes')
                ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)]'
                : 'text-[color:var(--text-label)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)]/50'
              }`}
          >
            <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
            </svg>
            <span>Notes</span>
          </button>
        </Show>
        <button
          type="button"
          onClick={() => navigate('/docindex')}
          class={`w-full flex items-center gap-2 px-2.5 h-7 rounded-md text-ui transition-colors
            ${location.pathname.startsWith('/docindex')
              ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)]'
              : 'text-[color:var(--text-label)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)]/50'
            }`}
        >
          <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
          </svg>
          <span>Project Index</span>
        </button>
        <Show when={deviceOn()}>
          <button
            type="button"
            onClick={() => navigate('/device')}
            class={`w-full flex items-center gap-2 px-2.5 h-7 rounded-md text-ui transition-colors
              ${location.pathname.startsWith('/device')
                ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)]'
                : 'text-[color:var(--text-label)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)]/50'
              }`}
          >
            <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M10.5 1.5H8.25A2.25 2.25 0 006 3.75v16.5a2.25 2.25 0 002.25 2.25h7.5A2.25 2.25 0 0018 20.25V3.75a2.25 2.25 0 00-2.25-2.25H13.5m-3 0V3h3V1.5m-3 0h3m-3 18.75h3" />
            </svg>
            <span>Device</span>
          </button>
        </Show>
        <button
          type="button"
          onClick={() => navigate('/preview')}
          class={`w-full flex items-center gap-2 px-2.5 h-7 rounded-md text-ui transition-colors
            ${location.pathname.startsWith('/preview')
              ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)]'
              : 'text-[color:var(--text-label)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)]/50'
            }`}
        >
          <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M5.25 5.653c0-.856.917-1.398 1.667-.986l11.54 6.348a1.125 1.125 0 010 1.971l-11.54 6.347a1.125 1.125 0 01-1.667-.985V5.653z" />
          </svg>
          <span>Preview</span>
        </button>
      </div>

      {/* Footer */}
      <div class="border-t border-[color:var(--border-subtle)] h-10 px-2 flex items-center gap-1">
        <button
          type="button"
          onClick={() => navigate('/settings', { state: { from: location.pathname } })}
          title="Settings"
          aria-label="Settings"
          class="w-7 h-7 rounded-md text-[color:var(--text-label)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition-all active:scale-[0.92] shrink-0"
        >
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065zM15 12a3 3 0 11-6 0 3 3 0 016 0z" />
          </svg>
        </button>
        <span class="text-micro text-[color:var(--text-label)] truncate flex-1 font-mono" title={server.directory() || 'unknown'}>
          {shortenPath(server.directory()) || '—'}
        </span>
        <span
          role="img"
          title={server.connected() ? 'Connected' : 'Disconnected'}
          aria-label={server.connected() ? 'Connected' : 'Disconnected'}
          class={`w-1.5 h-1.5 rounded-full shrink-0 mr-1 ${server.connected() ? 'bg-emerald-400' : 'bg-zinc-600'}`}
        />
      </div>
      {/* Drag grip: resizes the panel from its right edge. It is a focusable
          separator, so the panel is also resizable from the keyboard. */}
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize sidebar"
        aria-valuenow={sidebar.width()}
        aria-valuemin={sidebar.min}
        aria-valuemax={sidebar.max}
        tabindex="0"
        onPointerDown={sidebar.start}
        onKeyDown={sidebar.nudge}
        title="Drag to resize"
        class="absolute top-0 -right-[3px] bottom-0 w-[6px] cursor-col-resize z-10 hidden lg:block hover:bg-[color:var(--accent)]/25 focus-visible:bg-[color:var(--accent)]/40 focus-visible:outline-none transition-colors"
      />
    </div>
    </Show>
  );
}
