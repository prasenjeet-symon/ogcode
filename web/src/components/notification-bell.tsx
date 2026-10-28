import { createSignal, Show, For, onMount } from 'solid-js';
import { useNotifications } from '../context/notification';
import { getVersion, checkForUpdate } from '../api/client';
import type { VersionResponse } from '../api/client';

function formatTime(ts: number): string {
  const diffMin = Math.floor((Date.now() - ts) / 60000);
  if (diffMin < 1) return 'now';
  if (diffMin < 60) return `${diffMin}m`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h`;
  return `${Math.floor(diffHr / 24)}d`;
}

function statusIcon(type: string) {
  if (type === 'task.completed') {
    return <svg class="w-3 h-3 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5"><path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" /></svg>;
  }
  if (type === 'task.failed') {
    return <svg class="w-3 h-3 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>;
  }
  return <span class="w-1.5 h-1.5 rounded-full bg-amber-400 animate-pulse inline-block" />;
}

export default function NotificationBell() {
  const notifs = useNotifications();
  const [open, setOpen] = createSignal(false);
  const [versionInfo, setVersionInfo] = createSignal<VersionResponse | null>(null);
  const [showUpdatePopover, setShowUpdatePopover] = createSignal(false);

  onMount(() => {
    // Check for updates on mount
    getVersion().then(info => {
      if (info.updateAvailable) {
        setVersionInfo(info);
      }
    }).catch(() => { /* ignore */ });
  });

  const handleClick = (notif: any) => {
    notifs.markRead(notif.id);
    setOpen(false);
  };

  const handleUpdateClick = () => {
    setShowUpdatePopover(true);
  };

  const handleCheckUpdate = async () => {
    const info = await checkForUpdate();
    if (!info.updateAvailable) {
      setVersionInfo(null);
    } else {
      setVersionInfo(await getVersion());
    }
  };

  const handleDismissUpdate = () => {
    const dismissUntil = Date.now() + 7 * 24 * 60 * 60 * 1000; // 7 days
    localStorage.setItem('ogcode-update-dismissed-until', dismissUntil.toString());
    setVersionInfo(null);
  };

  const hasUpdate = () => versionInfo()?.updateAvailable ?? false;
  const updateCount = () => hasUpdate() ? 1 : 0;
  const totalUnread = () => notifs.unreadCount() + updateCount();

  return (
    <div class="relative flex items-center gap-1">
      {/* Update Badge */}
      <Show when={hasUpdate()}>
        <button
          type="button"
          onClick={() => setShowUpdatePopover((v) => !v)}
          class="w-7 h-7 flex items-center justify-center rounded-md text-amber-400 hover:text-amber-300 hover:bg-[color:var(--bg-hover)] transition relative"
          title="Update Available"
        >
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
          </svg>
          <span class="absolute -top-0.5 -right-0.5 min-w-[14px] h-[14px] flex items-center justify-center rounded-full bg-amber-500 text-[9px] font-bold text-black px-1 leading-none">
            1
          </span>
        </button>

        {/* Update Popover */}
        <Show when={showUpdatePopover()}>
          <div class="fixed inset-0 z-40" onClick={() => setShowUpdatePopover(false)} />
          <div class="absolute right-0 top-full mt-1 w-80 bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)] rounded-lg shadow-xl z-50 p-4">
            <div class="flex items-center justify-between mb-3">
              <div class="flex items-center gap-2">
                <span class="text-lg">🎉</span>
                <span class="font-semibold text-zinc-200">Update Available</span>
              </div>
              <button
                onClick={() => setShowUpdatePopover(false)}
                class="p-1 hover:bg-zinc-700 rounded transition-colors"
              >
                <svg class="w-4 h-4 text-zinc-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
                </svg>
              </button>
            </div>

            <div class="text-sm text-zinc-400 mb-3">
              v{versionInfo()?.version} → v{versionInfo()?.latestVersion?.replace(/^v/, '')}
            </div>

            <Show when={versionInfo()?.releaseNotes}>
              <div class="text-xs text-zinc-300 mb-3 bg-zinc-800/50 rounded p-2 max-h-32 overflow-y-auto">
                {versionInfo()?.releaseNotes}
              </div>
            </Show>

            <Show when={versionInfo()?.installCommand}>
              <div class="bg-zinc-800 rounded p-2 mb-3">
                <div class="flex items-center justify-between mb-1">
                  <span class="text-[10px] text-zinc-500 uppercase">Install Command</span>
                  <button
                    onClick={() => navigator.clipboard.writeText(versionInfo()?.installCommand || '')}
                    class="text-xs text-zinc-400 hover:text-zinc-300 flex items-center gap-1"
                  >
                    <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                    </svg>
                    Copy
                  </button>
                </div>
                <code class="text-xs text-green-400 font-mono">{versionInfo()?.installCommand}</code>
              </div>
            </Show>

            <div class="flex items-center justify-between">
              <button
                onClick={handleDismissUpdate}
                class="text-xs text-zinc-500 hover:text-zinc-400"
              >
                Dismiss (7 days)
              </button>
              <a
                href={versionInfo()?.releaseUrl}
                target="_blank"
                rel="noopener noreferrer"
                class="text-xs text-blue-400 hover:text-blue-300"
              >
                View Release →
              </a>
            </div>
          </div>
        </Show>
      </Show>

      <button
        type="button"
        onClick={() => { setOpen((o) => !o); if (!open()) notifs.markAllRead(); }}
        class="w-7 h-7 flex items-center justify-center rounded-md text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)] transition relative"
        title="Notifications"
      >
        <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
          <path stroke-linecap="round" stroke-linejoin="round" d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9" />
        </svg>
        <Show when={totalUnread() > 0}>
          <span class="absolute -top-0.5 -right-0.5 min-w-[14px] h-[14px] flex items-center justify-center rounded-full bg-red-600 text-[9px] font-bold text-white px-1 leading-none">
            {totalUnread() > 9 ? '9+' : totalUnread()}
          </span>
        </Show>
      </button>

      <Show when={open()}>
        {/* Backdrop */}
        <div class="fixed inset-0 z-40" onClick={() => setOpen(false)} />

        {/* Dropdown */}
        <div class="absolute right-0 top-full mt-1 w-72 bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)] rounded-lg shadow-xl z-50 max-h-80 overflow-hidden flex flex-col">
          <div class="flex items-center justify-between px-3 py-2 border-b border-[color:var(--border-subtle)]">
            <span class="text-[11px] font-semibold text-zinc-200">Notifications</span>
            <Show when={notifs.notifications().length > 0}>
              <button
                onClick={() => notifs.clear()}
                class="text-[10px] text-zinc-500 hover:text-zinc-300 transition"
              >
                Clear all
              </button>
            </Show>
          </div>
          <div class="overflow-y-auto flex-1">
            <Show
              when={notifs.notifications().length > 0}
              fallback={
                <div class="px-3 py-6 text-center text-[12px] text-zinc-600">
                  No notifications
                </div>
              }
            >
              <For each={notifs.notifications().slice(0, 30)}>
                {(n) => (
                  <div
                    onClick={() => handleClick(n)}
                    class={`px-3 py-2 border-b border-[color:var(--border-subtle)] last:border-b-0 cursor-pointer hover:bg-[color:var(--bg-hover)] transition flex items-start gap-2 ${!n.read ? 'bg-[color:var(--accent-soft)]/30' : ''}`}
                  >
                    <span class="mt-0.5 shrink-0">{statusIcon(n.type)}</span>
                    <div class="min-w-0 flex-1">
                      <p class="text-[12px] text-zinc-200 truncate">{n.taskTitle}</p>
                      <p class="text-[10px] text-zinc-500 mt-0.5">
                        {n.type === 'task.started' ? 'Started' : n.type === 'task.completed' ? 'Completed' : 'Failed'}
                        <span class="ml-1.5">{formatTime(n.timestamp)}</span>
                      </p>
                    </div>
                    <Show when={!n.read}>
                      <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] shrink-0 mt-1.5" />
                    </Show>
                  </div>
                )}
              </For>
            </Show>
          </div>
        </div>
      </Show>
    </div>
  );
}
