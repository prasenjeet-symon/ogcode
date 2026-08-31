import { useNavigate, useLocation, type RouteSectionProps } from '@solidjs/router';
import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from 'solid-js';
import { useServer } from '../../context/server';
import { useSession } from '../../context/session';
import { SettingsShell, type ShellReport } from './ui';

// ---------------------------------------------------------------------------
// The settings application shell.
//
//   ┌─ app bar ──────────────────── search ── workspace ─┐
//   │ nav     │  desk                                     │
//   │ drawer  │    ┌ paper ─────────────────────────┐     │
//   │ (pills) │    └────────────────────────────────┘     │
//   └─────────┴───────────────────────────────────────────┘
//
// The app bar and the drawer are fixed furniture; only the page scrolls, so
// navigation and search stay put no matter how long a page runs.
//
// Tone follows the rest of the app rather than inventing its own: the chrome —
// bar and drawer — takes the lighter surface, exactly as SessionSidebar and
// PlanSidebar do, and the content sits on the darker base beneath it. Settings
// used to invert that, which made crossing between a session and its settings
// feel like crossing between two apps.
// ---------------------------------------------------------------------------

// Module-level store so the entry route survives SettingsShell remounts as the
// user moves between settings sub-pages. Null until someone actually navigates
// in from elsewhere — a hard load straight onto /settings re-evaluates this
// module, so null reliably means "no in-app page to go back to" and the back
// button falls through to the mode-aware home route instead of guessing.
let storedPreviousRoute: string | null = null;

interface Page {
  id: string;
  label: string;
  href: string;
  icon: string;
  match: (pathname: string) => boolean;
}

const PAGES: Page[] = [
  {
    id: 'general',
    label: 'General',
    href: '/settings',
    match: (p) => p === '/settings' || p === '/settings/',
    icon: 'M10.5 6h9.75M10.5 6a1.5 1.5 0 11-3 0m3 0a1.5 1.5 0 10-3 0M3.75 6H7.5m3 12h9.75m-9.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-3.75 0H7.5m9-6h3.75m-3.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-9.75 0h9.75',
  },
  {
    id: 'models',
    label: 'Models',
    href: '/settings/models',
    match: (p) => p.startsWith('/settings/models'),
    icon: 'M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 002.25-2.25V6.75a2.25 2.25 0 00-2.25-2.25H6.75A2.25 2.25 0 004.5 6.75v10.5a2.25 2.25 0 002.25 2.25zm.75-12h9v9h-9v-9z',
  },
  {
    id: 'skills',
    label: 'Skills',
    href: '/settings/skills',
    match: (p) => p.startsWith('/settings/skills'),
    icon: 'M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09zM18.259 8.715L18 9.75l-.259-1.035a3.375 3.375 0 00-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 002.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 002.456 2.456L21.75 6l-1.035.259a3.375 3.375 0 00-2.456 2.456z',
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
  const navigate = useNavigate();
  const location = useLocation();
  const server = useServer();
  const session = useSession();

  const [query, setQuery] = createSignal('');
  const [report, setReport] = createSignal<ShellReport>({ noun: 'settings' });
  const [shown, setShown] = createSignal(0);
  const [total, setTotal] = createSignal(0);
  // True once the large title has scrolled under the bar, at which point the
  // bar shows the page name instead. One threshold with a dead zone rather
  // than a continuous fade, so a slow scroll cannot leave both half-visible.
  const [collapsed, setCollapsed] = createSignal(false);

  let deskEl: HTMLDivElement | undefined;
  let searchEl: HTMLInputElement | undefined;

  // Read the route we came from (passed as router state by each navigate('/settings') call site).
  const from = (location.state as Record<string, unknown> | null)?.from;
  if (typeof from === 'string' && !from.startsWith('/settings')) {
    storedPreviousRoute = from;
  }

  // Replace rather than push: leaving settings should pop the user back out,
  // not stack another entry so the browser's own Back button re-enters settings.
  const goBack = () =>
    navigate(storedPreviousRoute ?? (server.mode() === 'plan' ? '/plan' : '/'), { replace: true });

  const currentPage = createMemo(() => PAGES.find((p) => p.match(location.pathname)) ?? PAGES[0]);

  // The value shown beside a nav row, where the page has a number worth
  // knowing before you open it. Only Models does: how many of its catalogue
  // actually reach the picker. Anything else would need its own fetch to
  // answer, and a drawer is not worth a round trip.
  const pageValue = (id: string): string => {
    if (id !== 'models') return '';
    const all = session.models();
    if (all.length === 0) return '';
    return `${all.filter((m) => m.enabled).length} on`;
  };

  // Counts are read off the rendered page rather than declared by it, so the
  // result line can never disagree with the rows actually on screen. Rows are
  // filtered by toggling `hidden`, which is an attribute change — without
  // attributeFilter the observer would never fire and every count would freeze.
  const rescan = () => {
    if (!deskEl) return;
    setTotal(deskEl.querySelectorAll('[data-setting]').length);
    setShown(deskEl.querySelectorAll('[data-setting]:not([hidden])').length);
  };

  onMount(() => {
    if (!deskEl) return;
    let frame = 0;
    const observer = new MutationObserver(() => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(rescan);
    });
    observer.observe(deskEl, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ['hidden'],
    });
    rescan();
    onCleanup(() => {
      cancelAnimationFrame(frame);
      observer.disconnect();
    });
  });

  // Moving between pages resets the reading position and the filter — a query
  // typed against the model catalogue means nothing on the About page.
  createEffect(() => {
    location.pathname;
    setQuery('');
    setCollapsed(false);
    if (deskEl) deskEl.scrollTop = 0;
  });

  return (
    <SettingsShell.Provider value={{ query, setQuery, report: setReport }}>
      <div class="h-screen w-full flex flex-col bg-[color:var(--bg-base)]">
        {/* Nav bar. The page title is centred and the back control names its
            destination, so the bar reads the same whichever page is open. */}
        <header
          class="h-12 shrink-0 grid grid-cols-[1fr_auto_1fr] items-center gap-2 px-3
                 bg-[color:var(--bg-surface)]/90 backdrop-blur-xl
                 border-b border-[color:var(--border-subtle)] z-10"
        >
          <div class="flex items-center gap-2 min-w-0">
            <button
              type="button"
              onClick={goBack}
              title="Close settings"
              class="flex items-center gap-0.5 h-8 pl-1 pr-2 rounded-lg shrink-0 transition-colors
                     text-[color:var(--accent)] hover:bg-[color:var(--bg-hover)]"
            >
              <svg class="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                <path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
              </svg>
              <span class="text-ui">Done</span>
            </button>
          </div>

          {/* The compact title fades in as the large one scrolls away. */}
          <span
            class="text-ui font-semibold text-[color:var(--text-primary)] text-center truncate transition-opacity duration-150"
            style={{ opacity: collapsed() ? 1 : 0 }}
          >
            {currentPage().label}
          </span>

          <div class="flex items-center justify-end gap-2 min-w-0">
            <div class="relative w-[15rem] max-w-[40vw]">
              <svg
                class="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-[color:var(--text-muted)]"
                fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"
              >
                <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
              <input
                ref={searchEl}
                type="text"
                value={query()}
                onInput={(e) => setQuery(e.currentTarget.value)}
                onKeyDown={(e) => { if (e.key === 'Escape' && query()) { e.stopPropagation(); setQuery(''); } }}
                placeholder={`Search ${report().noun}`}
                aria-label={`Search ${report().noun}`}
                spellcheck={false}
                class="w-full h-8 pl-8 pr-8 rounded-[10px] bg-[color:var(--bg-elevated)]
                       border border-transparent text-meta text-[color:var(--text-primary)]
                       placeholder:text-[color:var(--text-muted)] focus:outline-none
                       focus:border-[color:var(--accent)] transition-colors"
              />
              <Show when={query()}>
                <button
                  type="button"
                  onClick={() => { setQuery(''); searchEl?.focus(); }}
                  aria-label="Clear search"
                  class="absolute right-2 top-1/2 -translate-y-1/2 w-4 h-4 rounded-full flex items-center justify-center
                         bg-[color:var(--text-muted)] text-[color:var(--bg-base)] hover:bg-[color:var(--text-tertiary)] transition-colors"
                >
                  <svg class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </Show>
            </div>
          </div>
        </header>

        <div class="flex-1 min-h-0 flex">
          {/* Navigation drawer.
              Deliberately quiet. Four destinations do not need coloured tiles,
              chevrons and a caption to be legible — those read as decoration
              at this size, and decoration is what makes a sidebar look like a
              toy. What is left is a glyph, a word, and one number that is
              worth knowing before you open the page.

              The width is 260px to the pixel because that is what
              SessionSidebar and PlanSidebar are: leaving settings sits behind
              this column, and a column that changes width on the way out reads
              as the whole app shifting. */}
          <nav
            class="w-[260px] shrink-0 flex flex-col py-2 border-r border-[color:var(--border-subtle)]"
            style={{ background: 'linear-gradient(var(--tint), var(--tint)) var(--bg-surface)' }}
            aria-label="Settings pages"
          >
            {/* A hair of space between rows, so an active or hovered row reads as
                its own shape rather than merging with its neighbour. */}
            <div class="flex-1 px-2 space-y-px overflow-y-auto">
              <For each={PAGES}>
                {(page) => {
                  const active = () => page.match(location.pathname);
                  return (
                    <button
                      type="button"
                      onClick={() => navigate(page.href)}
                      aria-current={active() ? 'page' : undefined}
                      class={`w-full flex items-center gap-2.5 h-8 px-2 rounded-md text-left
                              transition-colors duration-150
                        ${active()
                          ? 'bg-[color:var(--bg-elevated)]/70 text-[color:var(--text-primary)] font-medium'
                          : 'text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)]/50'
                        }`}
                    >
                      {/* The accent marks the page you are on and nothing else,
                          so the eye lands on it without a filled pill shouting. */}
                      <svg
                        class={`w-4 h-4 shrink-0 transition-colors duration-150
                          ${active() ? 'text-[color:var(--accent)]' : 'text-[color:var(--text-muted)]'}`}
                        fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.7"
                      >
                        <path stroke-linecap="round" stroke-linejoin="round" d={page.icon} />
                      </svg>
                      <span class="text-ui truncate flex-1">{page.label}</span>
                      <Show when={pageValue(page.id)}>
                        {(v) => (
                          <span class="text-micro tabular-nums text-[color:var(--text-muted)] shrink-0">{v()}</span>
                        )}
                      </Show>
                    </button>
                  );
                }}
              </For>
            </div>

            {/* Which project these settings belong to. At the foot, where a
                status line belongs, rather than taking the top of the column
                from the navigation. */}
            <div
              class="mt-2 pt-2 mx-2 border-t border-[color:var(--border-subtle)]"
              title={server.directory() || 'No workspace open'}
            >
              <div class="flex items-center gap-1.5 px-2">
                <span
                  class="w-1.5 h-1.5 rounded-full shrink-0"
                  style={{ background: server.connected() ? 'var(--success)' : 'var(--text-muted)' }}
                />
                <span class="text-micro font-medium text-[color:var(--text-secondary)] truncate">
                  {projectName(server.directory())}
                </span>
              </div>
              <div class="px-2 mt-px text-micro text-[color:var(--text-muted)] truncate">
                {server.connected() ? 'Connected' : 'Offline'}
                <Show when={server.branch()}>{` · ${server.branch()}`}</Show>
              </div>
            </div>
          </nav>

          {/* Desk */}
          <div
            ref={deskEl}
            onScroll={() => {
              if (!deskEl) return;
              const y = deskEl.scrollTop;
              // Hysteresis: collapse past the title, restore well before it, so
              // scrolling across the boundary cannot flicker the two titles.
              if (y > 34) setCollapsed(true);
              else if (y < 18) setCollapsed(false);
            }}
            class="page-enter relative flex-1 min-w-0 overflow-y-auto"
          >
            <div class="max-w-[52rem] mx-auto px-6 py-4 pb-28">
              <h1 class="px-4 pb-2 text-[1.75rem] font-bold tracking-[-0.02em] text-[color:var(--text-primary)]">
                {currentPage().label}
              </h1>
              <Show when={query().trim()}>
                <div class="flex items-center gap-2 px-4 pb-2 text-meta text-[color:var(--text-tertiary)]">
                  <span class="tabular-nums">
                    {shown()} of {total()} {report().noun}
                  </span>
                  <button
                    type="button"
                    onClick={() => { setQuery(''); searchEl?.focus(); }}
                    class="text-[color:var(--accent)] font-medium hover:underline underline-offset-2"
                  >
                    Clear
                  </button>
                </div>
              </Show>
              {props.children}
            </div>
          </div>
        </div>
      </div>
    </SettingsShell.Provider>
  );
}

/** The project's own name, not the whole path — the full path is one hover
 *  away and a truncated absolute path tells you nothing. */
function projectName(dir: string): string {
  return dir.split('/').filter(Boolean).pop() || dir || 'No workspace';
}
