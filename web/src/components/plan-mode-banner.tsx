import { createSignal, Show } from 'solid-js';
import { useServer } from '../context/server';

// PlanModeBanner warns that Plan Mode is still under rapid development. It
// shows on every surface while the server is running in plan mode, a dismissal
// is remembered so it does not nag across reloads, and the server's plan-mode
// feature flag gates it — turning the flag off in PostHog hides the warning
// without withholding plan mode itself.
export default function PlanModeBanner() {
  const server = useServer();
  let storedDismissed = false;
  try {
    storedDismissed = localStorage.getItem('ogcode-plan-mode-banner-dismissed') === '1';
  } catch { /* private mode etc. — just don't pre-dismiss */ }
  const [dismissed, setDismissed] = createSignal(storedDismissed);

  const onDismiss = () => {
    setDismissed(true);
    try {
      localStorage.setItem('ogcode-plan-mode-banner-dismissed', '1');
    } catch { /* private mode etc. — dismissal just won't persist */ }
  };

  return (
    <Show when={server.mode() === 'plan' && server.planModeEnabled() && !dismissed()}>
      <div
        class="fixed left-1/2 top-4 z-[151] -translate-x-1/2 flex items-center gap-2.5 pl-3 pr-2 py-2 rounded-[10px] border max-w-[92vw]"
        style={{ background: 'var(--bg-overlay)', 'border-color': 'color-mix(in srgb, var(--warning) 30%, transparent)', 'box-shadow': 'var(--shadow-lg)' }}
      >
        <span class="w-5 h-5 rounded-md flex items-center justify-center shrink-0" style={{ background: 'color-mix(in srgb, var(--warning) 12%, transparent)', color: 'var(--warning)' }}>
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m0 3.5h.01M10.34 3.94l-7.6 13.16A1.5 1.5 0 004.04 19.5h15.92a1.5 1.5 0 001.3-2.4L13.66 3.94a1.5 1.5 0 00-2.6 0z" />
          </svg>
        </span>

        <span class="text-[12.5px] leading-snug" style={{ color: 'var(--text-secondary)' }}>
          Plan mode is under rapid development and not yet stable — you may run into bugs. Report them, or use Build mode for now.
        </span>

        <a
          href="https://github.com/prasenjeet-symon/ogcode/issues"
          target="_blank"
          rel="noopener noreferrer"
          class="px-2.5 py-1 rounded-md text-[12px] font-medium shrink-0 transition-colors bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)] text-[color:var(--on-primary)]"
          title="Report an issue on GitHub"
        >
          Report an issue
        </a>

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
