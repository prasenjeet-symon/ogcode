import { createSignal, createEffect, Show } from 'solid-js';
import { useServer } from '../context/server';
import { getGitSync, type GitSyncStatus } from '../api/client';

// GitSyncBanner runs once when plan mode loads: it asks the server whether the
// working-directory branch is in sync with its upstream (the server does a
// best-effort fetch first) and shows a dismissible warning when the branch is
// behind / diverged / has no upstream — so the user can reconcile before tasks
// branch from a stale base. Renders nothing when in sync or outside plan mode.
export default function GitSyncBanner() {
  const server = useServer();
  const [status, setStatus] = createSignal<GitSyncStatus | null>(null);
  const [dismissed, setDismissed] = createSignal(false);
  let requested = false;

  createEffect(() => {
    if (server.mode() !== 'plan' || requested) return;
    requested = true;
    getGitSync()
      .then((s) => setStatus(s))
      .catch(() => {});
  });

  // Decide whether (and how loudly) to warn.
  const issue = () => {
    const s = status();
    if (!s || !s.isRepo) return null;
    if (s.behind > 0 && s.ahead > 0) {
      return { level: 'warn' as const, text: `Your branch “${s.branch}” has diverged from ${s.upstream} — ${s.ahead} ahead, ${s.behind} behind. Reconcile before planning so tasks branch from the right base.` };
    }
    if (s.behind > 0) {
      return { level: 'warn' as const, text: `“${s.branch}” is ${s.behind} commit${s.behind === 1 ? '' : 's'} behind ${s.upstream}. Pull before planning so tasks branch from the latest.` };
    }
    if (!s.hasUpstream && s.branch && s.branch !== 'HEAD') {
      return { level: 'info' as const, text: `Branch “${s.branch}” has no upstream — task PRs will target the repository's default branch.` };
    }
    return null;
  };

  const staleNote = () => {
    const s = status();
    return s && s.hasUpstream && !s.fetched ? ' (couldn’t reach the remote — based on your last fetch)' : '';
  };

  return (
    <Show when={!dismissed() && issue()}>
      {(iss) => {
        const warn = () => iss().level === 'warn';
        const color = () => (warn() ? 'var(--warning)' : 'var(--accent)');
        const soft = () => (warn() ? 'rgba(245, 158, 11, 0.12)' : 'var(--accent-soft)');
        return (
          <div
            class="fixed left-1/2 top-16 z-[150] -translate-x-1/2 flex items-center gap-2.5 pl-3 pr-2 py-2 rounded-[10px] border max-w-[92vw]"
            style={{ background: 'var(--bg-overlay)', 'border-color': `color-mix(in srgb, ${color()} 30%, transparent)`, 'box-shadow': 'var(--shadow-lg)' }}
          >
            <span class="w-5 h-5 rounded-md flex items-center justify-center shrink-0" style={{ background: soft(), color: color() }}>
              <Show
                when={warn()}
                fallback={
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                }
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m0 3.5h.01M10.34 3.94l-7.6 13.16A1.5 1.5 0 004.04 19.5h15.92a1.5 1.5 0 001.3-2.4L13.66 3.94a1.5 1.5 0 00-2.6 0z" />
                </svg>
              </Show>
            </span>

            <span class="text-[12.5px] leading-snug" style={{ color: 'var(--text-secondary)' }}>
              {iss().text}{staleNote()}
            </span>

            <button
              type="button"
              onClick={() => setDismissed(true)}
              class="w-6 h-6 rounded-md flex items-center justify-center shrink-0 transition-colors hover:bg-[color:var(--bg-hover)]"
              style={{ color: 'var(--text-muted)' }}
              title="Dismiss"
            >
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        );
      }}
    </Show>
  );
}
