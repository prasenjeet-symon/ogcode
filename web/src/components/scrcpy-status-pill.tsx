import { createSignal, onCleanup, onMount } from 'solid-js';
import { useNavigate } from '@solidjs/router';
import { getScrcpyStatus, type ScrcpyStatus } from '../api/client';

// Same cadence the device panel polls at; the cost is one 1-byte GET.
const POLL_MS = 10_000;

/**
 * ScrcpyStatusPill is the session-header glance at the separately-run ws-scrcpy
 * device UI: a dot (up / down / checking) and a quiet "device" label. Clicking
 * opens the device screen. When ws-scrcpy is down the panel itself carries the
 * start hint — the pill only has to make the state visible from a session.
 */
export default function ScrcpyStatusPill() {
  const navigate = useNavigate();
  const [status, setStatus] = createSignal<ScrcpyStatus | null>(null);

  let timer: ReturnType<typeof setInterval> | undefined;
  const refresh = () => {
    getScrcpyStatus()
      .then(setStatus)
      .catch(() => setStatus({ up: false, target: '' }));
  };

  onMount(() => {
    refresh();
    timer = setInterval(refresh, POLL_MS);
  });
  onCleanup(() => {
    if (timer) clearInterval(timer);
  });

  const dot = () => {
    const st = status();
    if (!st) return 'bg-zinc-600 animate-pulse';
    return st.up ? 'bg-emerald-400' : 'bg-zinc-600';
  };
  const hint = () => {
    const st = status();
    if (!st) return 'Checking the ws-scrcpy device UI…';
    return st.up
      ? 'ws-scrcpy is up — open the device screen'
      : 'ws-scrcpy is down — open the device screen for the start hint';
  };

  return (
    <button
      type="button"
      onClick={() => navigate('/device')}
      title={hint()}
      aria-label="Device screen status"
      class="flex items-center gap-1.5 h-7 px-2 rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] hover:border-[color:var(--border-default)] transition-colors select-none shrink-0 cursor-pointer"
    >
      <span class={`w-1.5 h-1.5 rounded-full shrink-0 ${dot()}`} />
      <span class="text-micro font-medium text-zinc-300">device</span>
    </button>
  );
}