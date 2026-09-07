import { createSignal, Show } from 'solid-js';
import { useLocation } from '@solidjs/router';
import { useServer } from '../context/server';
import { useDesktopNotifications } from '../context/desktop-notification';

// DesktopNotificationBanner offers the browser notification permission from a
// user gesture — requestPermission() must run inside a click, so the provider
// only checks state at boot and this banner is the opt-in surface. Shows when
// permission is still 'default' (undecided); once granted or denied it goes
// away, and a dismissal is remembered so we never nag across reloads.
export default function DesktopNotificationBanner() {
  const server = useServer();
  const desktop = useDesktopNotifications();
  const location = useLocation();
  let storedDismissed = false;
  try {
    storedDismissed = localStorage.getItem('ogcode-desktop-notif-banner-dismissed') === '1';
  } catch { /* private mode etc. — just don't pre-dismiss */ }
  const [dismissed, setDismissed] = createSignal(storedDismissed);

  const onDismiss = () => {
    setDismissed(true);
    try {
      localStorage.setItem('ogcode-desktop-notif-banner-dismissed', '1');
    } catch { /* private mode etc. — dismissal just won't persist */ }
  };

  // Visible only while the choice is open. Skipped on the onboarding wizard —
  // first-run users are mid-setup, and a permission ask there is noise.
  const visible = () =>
    !dismissed() &&
    desktop.permission() === 'default' &&
    location.pathname !== '/onboarding';

  // The git-sync banner occupies the same top-center spot while in plan mode;
  // drop below it then instead of painting over it.
  const top = () => (server.mode() === 'plan' ? 'top-16' : 'top-4');

  return (
    <Show when={visible()}>
      <div
        class={`fixed left-1/2 z-[150] -translate-x-1/2 flex items-center gap-2.5 pl-3 pr-2 py-2 rounded-[10px] border max-w-[92vw] transition-[top] duration-200 ${top()}`}
        style={{ background: 'var(--bg-overlay)', 'border-color': 'color-mix(in srgb, var(--accent) 30%, transparent)', 'box-shadow': 'var(--shadow-lg)' }}
      >
        <span class="w-5 h-5 rounded-md flex items-center justify-center shrink-0" style={{ background: 'var(--accent-soft)', color: 'var(--accent)' }}>
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M14.857 17.082a23.848 23.848 0 005.454-1.31A8.967 8.967 0 0118 9.75v-.7V9A6 6 0 006 9v.75a8.967 8.967 0 01-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 01-5.714 0m5.714 0a3 3 0 11-5.714 0" />
          </svg>
        </span>

        <span class="text-[12.5px] leading-snug" style={{ color: 'var(--text-secondary)' }}>
          Get desktop notifications when a session finishes or needs your approval — even when this tab is in the background.
        </span>

        <button
          type="button"
          onClick={() => desktop.requestPermission()}
          class="px-2.5 py-1 rounded-md text-[12px] font-medium shrink-0 transition-colors bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)] text-[color:var(--on-primary)]"
          title="Enable desktop notifications"
        >
          Enable
        </button>

        <button
          type="button"
          onClick={onDismiss}
          class="w-6 h-6 rounded-md flex items-center justify-center shrink-0 transition-colors hover:bg-[color:var(--bg-hover)]"
          style={{ color: 'var(--text-muted)' }}
          title="Dismiss"
        >
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      </div>
    </Show>
  );
}