import { Show } from 'solid-js';
import { useSession } from '../context/session';

// Human labels for the gated tools.
const TOOL_LABEL: Record<string, string> = {
  bash: 'Run a shell command',
  write: 'Write a file',
  edit: 'Edit a file',
};

function labelFor(tool: string): string {
  return TOOL_LABEL[tool] ?? `Use ${tool}`;
}

// PermissionPrompt surfaces the agent's request to run a mutating tool
// (bash/write/edit) and blocks it until the user approves or rejects. Multiple
// requests queue; the oldest is shown first with a count of the rest.
export default function PermissionPrompt() {
  const session = useSession();
  const queue = () => session.pendingPermissions();
  const current = () => queue()[0];

  return (
    <Show when={current()}>
      {(req) => (
        <div class="shrink-0 border-t border-amber-400/25 bg-amber-400/[0.07] px-4 py-3">
          <div class="flex items-start gap-3">
            <svg
              class="w-4 h-4 mt-0.5 shrink-0 text-amber-400"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="1.8"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M12 9v2m0 4h.01M5.07 19h13.86c1.54 0 2.5-1.67 1.73-3L13.73 4a2 2 0 00-3.46 0L3.34 16c-.77 1.33.19 3 1.73 3z"
              />
            </svg>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="text-[12px] font-medium text-amber-200">Permission needed</span>
                <span class="text-[11px] text-amber-300/70">{labelFor(req().tool)}</span>
                <Show when={queue().length > 1}>
                  <span class="text-[10px] text-amber-300/60 ml-auto">
                    +{queue().length - 1} more queued
                  </span>
                </Show>
              </div>
              <Show when={req().pattern && req().pattern !== '*'}>
                <pre class="mt-1.5 max-h-28 overflow-auto rounded-md border border-amber-400/20 bg-[color:var(--bg-base)]/60 px-2.5 py-1.5 text-[11px] leading-relaxed text-zinc-300 font-mono whitespace-pre-wrap break-all">
                  {req().pattern}
                </pre>
              </Show>
              <div class="mt-2 flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  onClick={() => session.respondPermission(req().permissionId, 'once')}
                  class="px-2.5 py-1 rounded-md text-[11px] font-medium bg-[color:var(--accent)] text-black hover:brightness-110 transition-all active:scale-[0.96]"
                >
                  Allow once
                </button>
                <button
                  type="button"
                  onClick={() => session.respondPermission(req().permissionId, 'always')}
                  class="px-2.5 py-1 rounded-md text-[11px] font-medium border border-[color:var(--border-subtle)] text-zinc-200 hover:bg-[color:var(--bg-hover)] transition-all active:scale-[0.96]"
                >
                  Always allow {req().tool}
                </button>
                <button
                  type="button"
                  onClick={() => session.respondPermission(req().permissionId, 'reject')}
                  class="px-2.5 py-1 rounded-md text-[11px] font-medium border border-red-400/30 text-red-300 hover:bg-red-400/10 transition-all active:scale-[0.96]"
                >
                  Reject
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </Show>
  );
}
