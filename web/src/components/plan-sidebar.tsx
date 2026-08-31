import { useNavigate, useLocation } from '@solidjs/router';
import { usePlan } from '../context/plan';
import { useServer } from '../context/server';
import type { Plan } from '../api/client';
import { createSignal, createMemo, For, Show } from 'solid-js';

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

export default function PlanSidebar() {
  const plan = usePlan();
  const server = useServer();
  const navigate = useNavigate();
  const location = useLocation();
  const [query, setQuery] = createSignal('');
  const [collapsed, setCollapsed] = createSignal(false);
  const [archiveFilter, setArchiveFilter] = createSignal<'active' | 'archived'>('active');

  const handleNew = async () => {
    const p = await plan.newPlan();
    navigate(`/plan/${p.id}`);
  };

  const handleSelect = (id: string) => {
    plan.selectPlan(id);
    navigate(`/plan/${id}`);
  };

  const handleDelete = async (e: MouseEvent, id: string) => {
    e.stopPropagation();
    if (!confirm('Delete this plan?')) return;
    // Capture this before the delete — deletePlan clears activePlan when the
    // deleted plan was the active one, so checking afterwards always misses and
    // leaves the user stranded on /plan/<deleted-id>.
    const wasActive = plan.activePlan()?.id === id;
    await plan.deletePlan(id);
    if (wasActive) {
      navigate('/plan', { replace: true });
    }
  };

  const isArchived = (p: Plan) => p.status === 'locked' && p.allTasksCompleted === true;
  const isActive = (p: Plan) => p.status === 'open' || (p.status === 'locked' && !p.allTasksCompleted);

  const filtered = createMemo(() => {
    const q = query().trim().toLowerCase();
    const mode = archiveFilter();
    let list = plan.plans();
    if (mode === 'active') {
      list = list.filter(isActive);
    } else {
      list = list.filter(isArchived);
    }
    if (!q) return list;
    return list.filter((p) =>
      (p.title || '').toLowerCase().includes(q)
    );
  });

  const archivedCount = createMemo(() => plan.plans().filter(isArchived).length);

  return (
    <Show
      when={!collapsed()}
      fallback={
        <div class="w-12 border-r border-[color:var(--border-subtle)] flex flex-col items-center py-2 gap-1 bg-[color:var(--bg-surface)]">
          <button
            onClick={() => setCollapsed(false)}
            title="Expand sidebar"
            class="w-8 h-8 rounded-lg text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition"
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 6h16M4 12h16M4 18h16" />
            </svg>
          </button>
          <button
            onClick={handleNew}
            title="New plan"
            class="w-8 h-8 rounded-lg text-zinc-400 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition"
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
            </svg>
          </button>
          <div class="flex-1" />
          <button
            onClick={() => navigate('/notes')}
            title="Notes"
            class={`w-8 h-8 rounded-lg flex items-center justify-center transition
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
            title="Project Index"
            class={`w-8 h-8 rounded-lg flex items-center justify-center transition
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
            class={`w-8 h-8 rounded-lg flex items-center justify-center transition
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
            class="w-8 h-8 rounded-lg text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition"
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
      <div class="h-12 shrink-0 px-3 flex items-center gap-2">
        <button
          onClick={() => navigate('/plan')}
          title="Plans"
          class="flex items-center gap-2 flex-1 min-w-0 group"
        >
          <span class="w-6 h-6 rounded-md bg-[color:var(--accent)] flex items-center justify-center shadow-sm ring-1 ring-white/10 shrink-0">
            <svg class="w-3 h-3 text-[color:var(--on-primary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.6">
              <path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
            </svg>
          </span>
          <span class="text-[13px] font-semibold text-zinc-100 group-hover:text-white truncate">Plans</span>
        </button>
        <button
          onClick={handleNew}
          title="New plan"
          class="w-7 h-7 rounded-md text-zinc-400 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition"
        >
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 5v14M5 12h14" />
          </svg>
        </button>
        <button
          onClick={() => setCollapsed(true)}
          title="Collapse sidebar"
          class="w-7 h-7 rounded-md text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition"
        >
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M11 19l-7-7 7-7m8 14l-7-7 7-7" />
          </svg>
        </button>
      </div>

      {/* Search */}
      <div class="px-3 pb-2">
        <div class="relative">
          <svg class="w-3.5 h-3.5 text-zinc-500 absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            type="text"
            value={query()}
            onInput={(e) => setQuery(e.currentTarget.value)}
            placeholder="Search plans"
            class="w-full h-8 pl-8 pr-2 bg-[color:var(--bg-base)] border border-transparent
                   rounded-md text-[12px] text-zinc-200 placeholder-zinc-600
                   focus:outline-none focus:border-[color:var(--border-default)] focus:bg-[color:var(--bg-elevated)]
                   transition"
          />
        </div>
      </div>

      {/* Archive filter */}
      <div class="px-3 pb-2">
        <div class="flex rounded-lg border border-[color:var(--border-default)] overflow-hidden">
          <button
            onClick={() => setArchiveFilter('active')}
            class={`flex-1 py-1 text-[11px] font-medium transition ${archiveFilter() === 'active' ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]' : 'text-zinc-500 hover:text-zinc-300'}`}
          >
            Active
          </button>
          <button
            onClick={() => setArchiveFilter('archived')}
            class={`flex-1 py-1 text-[11px] font-medium transition ${archiveFilter() === 'archived' ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]' : 'text-zinc-500 hover:text-zinc-300'}`}
          >
            Archive
            <Show when={archivedCount() > 0}>
              <span class="ml-1 text-[10px] opacity-60">({archivedCount()})</span>
            </Show>
          </button>
        </div>
      </div>

      {/* Plan list */}
      <div class="flex-1 overflow-y-auto px-2 pt-1 pb-2">
        <For each={filtered()}>
          {(p) => {
            const isActive = () => plan.activePlan()?.id === p.id;
            const statusBadge = () => {
              if (p.status === 'locked') {
                if (p.breakdownStatus === 'in_progress') return { icon: 'pulse', color: 'text-amber-400' };
                if (p.breakdownStatus === 'completed') return { icon: 'check', color: 'text-emerald-400' };
                if (p.breakdownStatus === 'failed') return { icon: 'x', color: 'text-red-400' };
                return { icon: 'lock', color: 'text-amber-400' };
              }
              return { icon: 'dot', color: 'text-zinc-500' };
            };
            return (
              <div
                onClick={() => handleSelect(p.id)}
                class={`group relative cursor-pointer rounded-md px-2.5 py-1.5 text-[13px] transition
                  ${isActive()
                    ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]'
                    : 'text-zinc-400 hover:bg-[color:var(--bg-hover)]/50 hover:text-zinc-200'
                  }`}
              >
                <div class="flex items-center gap-2">
                  <Show when={isActive()}>
                    <span class="w-1 h-1 rounded-full bg-[color:var(--accent)] shrink-0" />
                  </Show>
                  <span class="truncate flex-1 min-w-0">{p.title || 'Untitled plan'}</span>
                  <span class={`shrink-0 ${statusBadge().color}`}>
                    <Show when={statusBadge().icon === 'lock'}>
                      <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                      </svg>
                    </Show>
                    <Show when={statusBadge().icon === 'pulse'}>
                      <span class="w-1.5 h-1.5 rounded-full bg-amber-400 animate-pulse inline-block" />
                    </Show>
                    <Show when={statusBadge().icon === 'check'}>
                      <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                      </svg>
                    </Show>
                    <Show when={statusBadge().icon === 'x'}>
                      <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </Show>
                  </span>
                  <span class={`text-[10.5px] tabular-nums shrink-0 transition ${isActive() ? 'text-zinc-500' : 'text-zinc-600'} group-hover:opacity-0`}>
                    {formatTime(p.updatedAt)}
                  </span>
                  <button
                    onClick={(e) => handleDelete(e, p.id)}
                    title="Delete"
                    class="absolute right-1.5 w-6 h-6 rounded
                           opacity-0 group-hover:opacity-100
                           text-zinc-500 hover:text-red-400 hover:bg-red-500/10
                           flex items-center justify-center transition"
                  >
                    <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6M1 7h22M10 7V4a1 1 0 011-1h2a1 1 0 011 1v3" />
                    </svg>
                  </button>
                </div>
              </div>
            );
          }}
        </For>
        <Show when={filtered().length === 0}>
          <div class="px-3 py-10 text-center text-[12px] text-zinc-600">
            {query() ? 'No matches' : archiveFilter() === 'archived' ? 'No archived plans' : 'No active plans'}
          </div>
        </Show>
      </div>

      {/* Notes nav item — above footer */}
      <div class="shrink-0 px-2 pb-1">
        <button
          type="button"
          onClick={() => navigate('/notes')}
          class={`w-full flex items-center gap-2 px-2.5 py-1.5 rounded-md text-[13px] transition
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
          class={`w-full flex items-center gap-2 px-2.5 py-1.5 rounded-md text-[13px] transition
            ${location.pathname.startsWith('/docindex')
              ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]'
              : 'text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)]/50'
            }`}
        >
          <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
          </svg>
          <span>Doc Index</span>
        </button>
        <button
          type="button"
          onClick={() => navigate('/settings/skills', { state: { from: location.pathname } })}
          class={`w-full flex items-center gap-2 px-2.5 py-1.5 rounded-md text-[13px] transition
            ${location.pathname.startsWith('/settings/skills')
              ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]'
              : 'text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)]/50'
            }`}
        >
          <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09z" />
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
          class="w-7 h-7 rounded-md text-zinc-500 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition shrink-0"
        >
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065zM15 12a3 3 0 11-6 0 3 3 0 016 0z" />
          </svg>
        </button>
        <span class="text-[11px] text-zinc-500 truncate flex-1 font-mono" title={server.directory() || 'unknown'}>
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