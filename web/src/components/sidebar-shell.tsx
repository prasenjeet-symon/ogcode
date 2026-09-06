import { Show, createEffect, onCleanup, type JSX } from 'solid-js';
import { useLocation } from '@solidjs/router';
import { drawerIsOpen, openDrawer, closeDrawer } from '../lib/mobile-drawer';

interface SidebarShellProps {
  // Which drawer this sidebar acts as on small screens ('sessions' or 'plans').
  drawer: 'sessions' | 'plans';
  children: JSX.Element;
}

// Responsive sidebar wrapper.
//
// On lg+ screens the children render inline exactly as before — the shell is
// invisible to the layout. Below lg the same children become an overlay
// drawer that slides in from the left when opened via openDrawer(id) (the
// hamburger buttons in each page header) and closes on: scrim tap, Escape,
// or any navigation.
//
// The children are NOT unmounted when the drawer is closed — they stay in the
// DOM behind the translateX(-102%) transform, which keeps sidebar state
// (search text, rename-in-progress, collapsed rail) alive across open/close
// cycles and keeps the closed state free of layout cost.
export default function SidebarShell(props: SidebarShellProps) {
  const location = useLocation();
  const isOpen = () => drawerIsOpen(props.drawer);

  // Any navigation closes the drawer — selecting a session/plan on mobile
  // should land you in the conversation, not staring at the list. The
  // comparison is against the pathname seen on the previous run of this
  // effect, so opening the drawer (which re-runs the effect without moving
  // the route) never registers as a navigation.
  let lastPath = location.pathname;
  createEffect(() => {
    const p = location.pathname;
    if (isOpen() && p !== lastPath) closeDrawer();
    lastPath = p;
  });

  // While open, Escape closes and the page behind must not scroll (iOS
  // rubber-bands the whole document otherwise). The cleanup pair runs when
  // the drawer closes or the shell unmounts.
  createEffect(() => {
    if (!isOpen()) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeDrawer();
    };
    document.addEventListener('keydown', onKey);
    document.body.classList.add('drawer-open');
    onCleanup(() => {
      document.removeEventListener('keydown', onKey);
      document.body.classList.remove('drawer-open');
    });
  });

  return (
    <>
      {/* Inline on desktop — zero cost, unchanged layout. */}
      <div class="hidden lg:flex shrink-0">{props.children}</div>

      {/* Drawer below lg */}
      <div
        class={`sidebar-drawer lg:hidden ${isOpen() ? 'is-open' : ''}`}
        aria-hidden={!isOpen()}
      >
        <Show when={isOpen()}>
          <div
            class="sidebar-drawer-scrim is-active"
            onClick={closeDrawer}
            aria-label="Close navigation"
          />
        </Show>
        <div
          class="sidebar-drawer-viewport"
          onClick={(e) => {
            // Taps on the sidebar's own empty padding (not its controls) also
            // close it, matching how mobile apps treat a drawer surface.
            if (e.target === e.currentTarget) closeDrawer();
          }}
        >
          {props.children}
        </div>
      </div>
    </>
  );
}

// Hamburger — the control in each page header that opens the drawer. Hidden
// at lg+ where the inline sidebar is always visible.
export function DrawerToggle(props: { drawer: 'sessions' | 'plans'; label?: string }) {
  return (
    <button
      type="button"
      onClick={() => openDrawer(props.drawer)}
      class="lg:hidden flex items-center justify-center h-8 w-8 -ml-1 rounded-md text-zinc-400 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] transition-colors"
      aria-label={props.label ?? 'Open navigation'}
      title={props.label ?? 'Open navigation'}
    >
      <svg class="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
        <path stroke-linecap="round" stroke-linejoin="round" d="M4 6h16M4 12h16M4 18h16" />
      </svg>
    </button>
  );
}
