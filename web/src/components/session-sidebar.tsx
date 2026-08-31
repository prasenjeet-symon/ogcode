import { useNavigate, useLocation } from '@solidjs/router';
import { useSession } from '../context/session';
import { useServer } from '../context/server';
import { createSignal, createMemo, For, Show } from 'solid-js';
import { deleteSession } from '../api/client';
import Logo from './logo';

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

export default function SessionSidebar() {
  const session = useSession();
  const server = useServer();
  const navigate = useNavigate();
  const location = useLocation();
  const [query, setQuery] = createSignal('');
  const [collapsed, setCollapsed] = createSignal(false);
  const [editingId, setEditingId] = createSignal<string | null>(null);
  const [draftTitle, setDraftTitle] = createSignal('');

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

    const list = filtered();
    const buckets = GROUPS.map((g) => ({ label: g.label, items: [] as typeof list }));
    for (const s of list) {
      // Age in calendar days, so "yesterday at 11pm" never reads as "today".
      const days = Math.floor((startOfToday.getTime() - s.updatedAt) / dayMs) + 1;
      const idx = GROUPS.findIndex((g) => days <= g.maxAgeDays);
      buckets[idx === -1 ? GROUPS.length - 1 : idx].items.push(s);
    }
    return buckets.filter((b) => b.items.length > 0);
  });

  return (
    <Show
      when={!collapsed()}
      fallback={
        <div class="w-12 border-r border-[color:var(--border-subtle)] flex flex-col items-center py-2 gap-1 bg-[color:var(--bg-surface)]">
          <button
            onClick={() => setCollapsed(false)}
            title="Expand sidebar"
            class="w-8 h-8 rounded-lg text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition-all active:scale-[0.92]"
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 6h16M4 12h16M4 18h16" />
            </svg>
          </button>
          <button
            onClick={handleNew}
            title="New session"
            class="w-8 h-8 rounded-lg text-zinc-400 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition-all active:scale-[0.92]"
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
            </svg>
          </button>
          <div class="flex-1" />
          <button
            onClick={() => navigate('/notes')}
            title="Notes"
            class={`w-8 h-8 rounded-lg flex items-center justify-center transition-all active:scale-[0.92]
              ${location.pathname.startsWith('/notes')
                ? 'text-[color:var(--accent)] bg-[color:var(--accent-soft)]'
                : 'text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)]'
              }`}
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
            </svg>
          </button>
          <button
            onClick={() => navigate('/docindex')}
            title="Doc Index"
            class={`w-8 h-8 rounded-lg flex items-center justify-center transition-all active:scale-[0.92]
              ${location.pathname.startsWith('/docindex')
                ? 'text-[color:var(--accent)] bg-[color:var(--accent-soft)]'
                : 'text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)]'
              }`}
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
            </svg>
          </button>
          <button
            onClick={() => navigate('/settings/skills', { state: { from: location.pathname } })}
            title="Skills"
            class={`w-8 h-8 rounded-lg flex items-center justify-center transition-all active:scale-[0.92]
              ${location.pathname.startsWith('/settings/skills')
                ? 'text-[color:var(--accent)] bg-[color:var(--accent-soft)]'
                : 'text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)]'
              }`}
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09z" />
            </svg>
          </button>
          <button
            onClick={() => navigate('/settings', { state: { from: location.pathname } })}
            title="Settings"
            class="w-8 h-8 rounded-lg text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition-all active:scale-[0.92]"
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
              <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065zM15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
          </button>
        </div>
      }
    >
    <div class="w-[260px] shrink-0 border-r border-[color:var(--border-subtle)] flex flex-col" style={{ background: 'linear-gradient(var(--tint), var(--tint)) var(--bg-surface)' }}>
      {/* Header: brand + collapse + new */}
      <div class="h-11 shrink-0 px-3 flex items-center gap-2">
        <button
          onClick={() => navigate('/')}
          title="Home"
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
          <svg class="w-3 h-3 text-[color:var(--text-muted)] absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            type="text"
            value={query()}
            onInput={(e) => setQuery(e.currentTarget.value)}
            placeholder="Search sessions"
            aria-label="Search sessions"
            class="w-full h-7 pl-7 pr-2 bg-[color:var(--bg-base)] border border-transparent
                   rounded-md text-meta text-[color:var(--text-primary)] placeholder:text-[color:var(--text-muted)]
                   focus:outline-none focus:border-[color:var(--border-default)] focus:bg-[color:var(--bg-elevated)]
                   transition-colors"
          />
        </div>
      </div>

      {/* Session list */}
      <div class="flex-1 overflow-y-auto px-2 pt-0.5 pb-2">
        <For each={grouped()}>
          {(group) => (
            <div class="mb-1">
              <div class="px-2 pt-2 pb-1 text-micro font-medium uppercase tracking-[0.07em] text-[color:var(--text-muted)] select-none">
                {group.label}
              </div>
              <For each={group.items}>
                {(s) => {
                  const isActive = () => session.activeSession()?.id === s.id;
                  const isEditing = () => editingId() === s.id;
                  return (
                    <div
                      onClick={() => !isEditing() && handleSelect(s.id)}
                      onDblClick={(e) => { e.stopPropagation(); startRename(s); }}
                      title={s.title || 'Untitled'}
                      class={`group/row relative flex items-center gap-2 cursor-pointer rounded-md h-7 pl-2.5 pr-1.5 text-ui transition-colors
                        ${isActive()
                          ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)] font-medium'
                          : 'text-[color:var(--text-secondary)] hover:bg-[color:var(--bg-hover)] hover:text-[color:var(--text-primary)]'
                        }`}
                    >
                      {/* Active marker: a rail on the row's edge rather than a
                          dot inline with the title, so titles all start on the
                          same x whether or not the row is selected. */}
                      <Show when={isActive()}>
                        <span class="absolute left-0 top-1.5 bottom-1.5 w-[2px] rounded-full bg-[color:var(--accent)]" />
                      </Show>
                      <Show
                        when={!isEditing()}
                        fallback={
                          <input
                            type="text"
                            value={draftTitle()}
                            onInput={(e) => setDraftTitle(e.currentTarget.value)}
                            onBlur={() => commitRename(s.id)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') { e.preventDefault(); commitRename(s.id); }
                              else if (e.key === 'Escape') { e.preventDefault(); cancelRename(); }
                            }}
                            onClick={(e) => e.stopPropagation()}
                            onDblClick={(e) => e.stopPropagation()}
                            placeholder="Untitled"
                            autoFocus
                            class="flex-1 min-w-0 bg-[color:var(--bg-base)] border border-[color:var(--border-default)]
                                   rounded px-1.5 h-5.5 text-ui text-[color:var(--text-primary)]
                                   focus:outline-none focus:border-[color:var(--accent)]"
                          />
                        }
                      >
                        <span class="truncate flex-1 min-w-0">{s.title || 'Untitled'}</span>
                        {/* Timestamp and row actions occupy the same slot: the
                            time is ambient information, the actions are what
                            you want the instant you reach for the row. */}
                        <span class="text-micro tabular-nums shrink-0 text-[color:var(--text-muted)] group-hover/row:hidden">
                          {formatTime(s.updatedAt)}
                        </span>
                        <span class="hidden group-hover/row:flex items-center gap-0.5 shrink-0">
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
                }}
              </For>
            </div>
          )}
        </For>
        <Show when={filtered().length === 0}>
          <div class="px-3 py-10 text-center text-meta text-[color:var(--text-muted)]">
            {query() ? 'No matches' : 'No sessions yet'}
          </div>
        </Show>
      </div>

      {/* Notes nav item — above footer */}
      <div class="shrink-0 px-2 pb-1">
        <button
          type="button"
          onClick={() => navigate('/notes')}
          class={`w-full flex items-center gap-2 px-2.5 h-7 rounded-md text-ui transition-colors
            ${location.pathname.startsWith('/notes')
              ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]'
              : 'text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)]/50'
            }`}
        >
          <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
          </svg>
          <span>Notes</span>
        </button>
        <button
          type="button"
          onClick={() => navigate('/docindex')}
          class={`w-full flex items-center gap-2 px-2.5 h-7 rounded-md text-ui transition-colors
            ${location.pathname.startsWith('/docindex')
              ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]'
              : 'text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)]/50'
            }`}
        >
          <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
          </svg>
          <span>Project Index</span>
        </button>
        <button
          type="button"
          onClick={() => navigate('/settings/skills', { state: { from: location.pathname } })}
          class={`w-full flex items-center gap-2 px-2.5 h-7 rounded-md text-ui transition-colors
            ${location.pathname.startsWith('/settings/skills')
              ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]'
              : 'text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)]/50'
            }`}
        >
          <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09zM18.259 5.74L18 6.75l-.259-1.01a3.375 3.375 0 00-2.455-2.455L14.27 3 14.526 2.01a3.375 3.375 0 012.455-2.455L18 1.25l.259-1.01a3.375 3.375 0 012.455 2.455L21 5.25l1.01-.259a3.375 3.375 0 012.455 2.455z" />
          </svg>
          <span>Skills</span>
        </button>
      </div>

      {/* Footer */}
      <div class="border-t border-[color:var(--border-subtle)] h-10 px-2 flex items-center gap-1">
        <button
          type="button"
          onClick={() => navigate('/settings', { state: { from: location.pathname } })}
          title="Settings"
          class="w-7 h-7 rounded-md text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition-all active:scale-[0.92] shrink-0"
        >
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065zM15 12a3 3 0 11-6 0 3 3 0 016 0z" />
          </svg>
        </button>
        <span class="text-micro text-zinc-500 truncate flex-1 font-mono" title={server.directory() || 'unknown'}>
          {shortenPath(server.directory()) || '—'}
        </span>
        <span
          title={server.connected() ? 'Connected' : 'Disconnected'}
          class={`w-1.5 h-1.5 rounded-full shrink-0 mr-1 ${server.connected() ? 'bg-emerald-400' : 'bg-zinc-600'}`}
        />
      </div>
    </div>
    </Show>
  );
}
