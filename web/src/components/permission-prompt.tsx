import { Show, createEffect, onCleanup, onMount } from 'solid-js';
import { useSession } from '../context/session';

// Minimal, inline permission prompt — designed to sit at the very top of the
// composer card (see prompt-input.tsx), the way terminal coding agents surface
// approvals right by the input. Two compact rows: the command/path, then the
// actions. No card/shadow of its own; it shares the composer's surface with a
// divider below.
//
// Keyboard: Enter approves once (the Allow button is auto-focused), "A" allows
// the tool for the rest of the session, Esc rejects. Esc is captured so it
// rejects the tool rather than triggering the composer's global "abort loop".
export default function PermissionPrompt() {
  const session = useSession();
  const queue = () => session.pendingPermissions();
  const current = () => queue()[0];

  let allowBtn: HTMLButtonElement | undefined;

  createEffect(() => {
    const id = current()?.permissionId;
    if (id) queueMicrotask(() => allowBtn?.focus());
  });

  const handleKey = (e: KeyboardEvent) => {
    const req = current();
    if (!req) return;
    const target = e.target as HTMLElement | null;
    const typing = !!target && (target.tagName === 'TEXTAREA' || target.tagName === 'INPUT' || target.isContentEditable);
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopImmediatePropagation();
      session.respondPermission(req.permissionId, 'reject');
    } else if ((e.key === 'a' || e.key === 'A') && !typing) {
      e.preventDefault();
      e.stopImmediatePropagation();
      session.respondPermission(req.permissionId, 'always');
    }
  };
  onMount(() => document.addEventListener('keydown', handleKey, true));
  onCleanup(() => document.removeEventListener('keydown', handleKey, true));

  return (
    <Show when={current()}>
      {(req) => {
        const isBash = () => req().tool === 'bash';
        return (
          <div class="border-b border-[color:var(--border-subtle)] px-3.5 pt-2.5 pb-2">
            {/* Row 1 — what it wants to do */}
            <div class="flex items-center gap-2">
              <span
                class="shrink-0"
                style={{ color: isBash() ? 'var(--warning)' : 'var(--accent)' }}
                aria-hidden="true"
              >
                <Show
                  when={isBash()}
                  fallback={
                    <svg class="w-[14px] h-[14px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931z" />
                    </svg>
                  }
                >
                  <svg class="w-[14px] h-[14px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M8 9l3 3-3 3m5 0h3M4 5h16a1 1 0 011 1v12a1 1 0 01-1 1H4a1 1 0 01-1-1V6a1 1 0 011-1z" />
                  </svg>
                </Show>
              </span>
              <code
                class="min-w-0 flex-1 truncate font-mono text-[12px] text-[color:var(--text-secondary)]"
                title={req().pattern}
              >
                {req().pattern && req().pattern !== '*' ? req().pattern : `Use ${req().tool}`}
              </code>
              <Show when={queue().length > 1}>
                <span class="shrink-0 text-[10.5px] text-[color:var(--text-tertiary)]">+{queue().length - 1}</span>
              </Show>
            </div>

            {/* Row 2 — actions */}
            <div class="mt-2 flex items-center gap-1.5">
              <button
                ref={allowBtn}
                type="button"
                onClick={() => session.respondPermission(req().permissionId, 'once')}
                class="inline-flex h-7 items-center gap-1.5 rounded-md bg-[color:var(--accent)] px-2.5 text-[11.5px] font-medium text-[color:var(--on-primary)] transition-all var(--spring-sm) hover:bg-[color:var(--accent-hover)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[color:var(--accent-ring)] active:scale-[0.97]"
              >
                Allow
                <kbd class="rounded border border-white/30 px-1 font-mono text-[9.5px] leading-[14px] opacity-80">↵</kbd>
              </button>
              <button
                type="button"
                onClick={() => session.respondPermission(req().permissionId, 'always')}
                class="inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-[11.5px] font-medium text-[color:var(--text-secondary)] transition-all var(--spring-sm) hover:bg-[color:var(--bg-hover)] hover:text-[color:var(--text-primary)] active:scale-[0.97]"
                title={`Always allow ${req().tool} for this session`}
              >
                Always
                <kbd class="rounded border border-current px-1 font-mono text-[9.5px] leading-[14px] opacity-55">A</kbd>
              </button>
              <div class="flex-1" />
              <button
                type="button"
                onClick={() => session.respondPermission(req().permissionId, 'reject')}
                class="inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-[11.5px] font-medium text-[color:var(--text-tertiary)] transition-all var(--spring-sm) hover:bg-red-500/10 hover:text-red-300 active:scale-[0.97]"
              >
                Reject
                <kbd class="rounded border border-current px-1 font-mono text-[9.5px] leading-[14px] opacity-55">esc</kbd>
              </button>
            </div>
          </div>
        );
      }}
    </Show>
  );
}
