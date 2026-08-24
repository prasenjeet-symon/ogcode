import { useNavigate, useLocation, type RouteSectionProps } from '@solidjs/router';
import { For, Show } from 'solid-js';
import { useServer } from '../../context/server';

// Module-level store so the entry route survives SettingsShell remounts as the
// user moves between settings sub-pages. Null until someone actually navigates
// in from elsewhere — a hard load straight onto /settings re-evaluates this
// module, so null reliably means "no in-app page to go back to" and the back
// button falls through to the mode-aware home route instead of guessing.
let storedPreviousRoute: string | null = null;

interface NavItem {
  id: string;
  label: string;
  href: string;
  match: (pathname: string) => boolean;
  icon: string;
}

const NAV: NavItem[] = [
  {
    id: 'general',
    label: 'General',
    href: '/settings',
    match: (p) => p === '/settings' || p === '/settings/',
    icon: 'M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065zM15 12a3 3 0 11-6 0 3 3 0 016 0z',
  },
  {
    id: 'models',
    label: 'Models',
    href: '/settings/models',
    match: (p) => p.startsWith('/settings/models'),
    icon: 'M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.847.813a4.5 4.5 0 00-3.09 3.091z',
  },
  {
    id: 'about',
    label: 'About',
    href: '/settings/about',
    match: (p) => p.startsWith('/settings/about'),
    icon: 'M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z',
  },
];

export default function SettingsLayout(props: RouteSectionProps) {
  return <SettingsShell>{props.children}</SettingsShell>;
}

function SettingsShell(props: { children?: any }) {
  const navigate = useNavigate();
  const location = useLocation();
  const server = useServer();

  // Read the route we came from (passed as router state by each navigate('/settings') call site).
  const from = (location.state as Record<string, unknown> | null)?.from;
  if (typeof from === 'string' && !from.startsWith('/settings')) {
    storedPreviousRoute = from;
  }

  // Replace rather than push: leaving settings should pop the user back out,
  // not stack another entry so the browser's own Back button re-enters settings.
  const goBack = () =>
    navigate(storedPreviousRoute ?? (server.mode() === 'plan' ? '/plan' : '/'), { replace: true });

  return (
    <div class="flex h-screen w-full">
      <aside class="w-[260px] shrink-0 border-r border-[color:var(--border-subtle)] flex flex-col" style={{ background: 'linear-gradient(var(--tint), var(--tint)) var(--bg-surface)' }}>
        {/* Header: back + title */}
        <div class="h-12 shrink-0 px-3 flex items-center gap-2">
          <button
            type="button"
            onClick={goBack}
            title="Back"
            class="w-7 h-7 rounded-md text-zinc-400 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition shrink-0"
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
            </svg>
          </button>
          <span class="text-[13px] font-semibold text-zinc-100">Settings</span>
        </div>

        {/* Nav */}
        <nav class="flex-1 px-2 pt-1 space-y-0.5 overflow-y-auto">
          <For each={NAV}>
            {(item) => {
              const active = () => item.match(location.pathname);
              return (
                <button
                  type="button"
                  onClick={() => navigate(item.href)}
                  class={`relative w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-md text-left transition
                    ${active()
                      ? 'bg-[color:var(--bg-hover)] text-zinc-50'
                      : 'text-zinc-400 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)]/50'
                    }`}
                >
                  <Show when={active()}>
                    <span class="absolute left-0 top-1.5 bottom-1.5 w-[2px] rounded-r bg-[color:var(--accent)]" />
                  </Show>
                  <svg class="w-4 h-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                    <path stroke-linecap="round" stroke-linejoin="round" d={item.icon} />
                  </svg>
                  <span class="text-[13px] font-medium">{item.label}</span>
                </button>
              );
            }}
          </For>
        </nav>

        {/* Footer */}
        <div class="border-t border-[color:var(--border-subtle)] h-10 px-3 flex items-center gap-2">
          <span class={`w-1.5 h-1.5 rounded-full shrink-0 ${server.connected() ? 'bg-emerald-400' : 'bg-zinc-600'}`} />
          <span class="text-[11px] text-zinc-500 truncate">
            {server.connected() ? 'Connected' : 'Offline'}
          </span>
        </div>
      </aside>

      <main class="page-enter flex-1 min-w-0 overflow-y-auto bg-[color:var(--bg-base)]">
        {props.children}
      </main>
    </div>
  );
}
