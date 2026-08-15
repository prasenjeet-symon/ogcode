import { Show, createEffect, onCleanup, onMount } from 'solid-js';
import { useSession } from '../context/session';

// Per-tool presentation: a human question, the monospace glyph shown before the
// command/path, and whether the icon reads as "shell/caution" (warning) or a
// plain file edit (accent).
const TOOL_META: Record<string, { question: string; verb: string; glyph: string; warn: boolean }> = {
  bash: { question: 'Run this shell command?', verb: 'bash', glyph: '$', warn: true },
  write: { question: 'Write this file?', verb: 'write', glyph: '✎', warn: false },
  edit: { question: 'Edit this file?', verb: 'edit', glyph: '✎', warn: false },
};

function metaFor(tool: string) {
  return TOOL_META[tool] ?? { question: `Allow ${tool}?`, verb: tool, glyph: '›', warn: true };
}

// PermissionPrompt surfaces the agent's request to run a mutating tool
// (bash/write/edit) and blocks it until the user approves or rejects. Multiple
// requests queue; the oldest is shown first with a count of the rest.
//
// Keyboard: Enter approves once (the Allow button is auto-focused), "A" allows
// the tool for the rest of the session, and Escape rejects. Escape is captured
// so it rejects the tool rather than aborting the whole loop (the composer's
// global Escape handler).
export default function PermissionPrompt() {
  const session = useSession();
  const queue = () => session.pendingPermissions();
  const current = () => queue()[0];

  let allowBtn: HTMLButtonElement | undefined;

  // Focus the primary action whenever a new request surfaces, so Enter approves.
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
      // Reject this tool — and stop the composer's global Escape from also
      // aborting the whole loop.
      e.preventDefault();
      e.stopImmediatePropagation();
      session.respondPermission(req.permissionId, 'reject');
    } else if ((e.key === 'a' || e.key === 'A') && !typing) {
      e.preventDefault();
      e.stopImmediatePropagation();
      session.respondPermission(req.permissionId, 'always');
    }
  };
  // Capture phase so we run before the composer's bubble-phase Escape handler.
  onMount(() => document.addEventListener('keydown', handleKey, true));
  onCleanup(() => document.removeEventListener('keydown', handleKey, true));

  return (
    <Show when={current()}>
      {(req) => {
        const meta = () => metaFor(req().tool);
        return (
          <div class="shrink-0 px-4 md:px-6 pb-2">
            <div class="max-w-3xl mx-auto overflow-hidden rounded-2xl border border-[color:var(--border-default)] bg-[color:var(--bg-surface)] shadow-lg shadow-black/30">
              {/* Top accent bar — warm for shell, accent for file edits */}
              <div
                class="h-[2px] w-full"
                style={{
                  background: meta().warn
                    ? 'linear-gradient(90deg, var(--warning), transparent 70%)'
                    : 'linear-gradient(90deg, var(--accent), transparent 70%)',
                }}
              />
              <div class="p-3.5 md:p-4">
                {/* Header */}
                <div class="flex items-center gap-2.5">
                  <span
                    class="flex h-[26px] w-[26px] shrink-0 items-center justify-center rounded-lg"
                    style={{
                      background: meta().warn ? 'rgba(245,166,35,0.13)' : 'var(--accent-soft)',
                      color: meta().warn ? 'var(--warning)' : 'var(--accent)',
                    }}
                  >
                    <Show
                      when={req().tool === 'bash'}
                      fallback={
                        <svg class="w-[15px] h-[15px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                          <path stroke-linecap="round" stroke-linejoin="round" d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931z" />
                        </svg>
                      }
                    >
                      <svg class="w-[15px] h-[15px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M8 9l3 3-3 3m5 0h3M4 5h16a1 1 0 011 1v12a1 1 0 01-1 1H4a1 1 0 01-1-1V6a1 1 0 011-1z" />
                      </svg>
                    </Show>
                  </span>
                  <span class="text-[13px] font-semibold text-[color:var(--text-primary)]">{meta().question}</span>
                  <span class="text-[11.5px] text-[color:var(--text-tertiary)]">Permission required</span>
                  <Show when={queue().length > 1}>
                    <span class="ml-auto rounded-full border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] px-2 py-[2px] text-[10.5px] text-[color:var(--text-tertiary)]">
                      +{queue().length - 1} more
                    </span>
                  </Show>
                </div>

                {/* Command / path */}
                <Show when={req().pattern && req().pattern !== '*'}>
                  <div class="mt-2.5 flex max-h-44 items-start gap-2.5 overflow-auto rounded-[10px] border border-[color:var(--border-default)] bg-[color:var(--bg-base)] px-3 py-2.5">
                    <span class="shrink-0 select-none font-mono text-[12.5px] leading-[1.55] text-[color:var(--text-tertiary)]">{meta().glyph}</span>
                    <code class="whitespace-pre-wrap break-words font-mono text-[12.5px] leading-[1.55] text-[color:var(--text-primary)]">{req().pattern}</code>
                  </div>
                </Show>

                {/* Actions */}
                <div class="mt-3 flex flex-wrap items-center gap-2">
                  <button
                    ref={allowBtn}
                    type="button"
                    onClick={() => session.respondPermission(req().permissionId, 'once')}
                    class="inline-flex h-8 items-center gap-2 rounded-lg bg-[color:var(--accent)] px-3.5 text-[12.5px] font-medium text-[color:var(--on-primary)] transition-all var(--spring-sm) hover:bg-[color:var(--accent-hover)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[color:var(--accent-ring)] active:scale-[0.97]"
                  >
                    Allow once
                    <kbd class="rounded border border-white/30 px-1 font-mono text-[10px] leading-[15px] opacity-80">↵</kbd>
                  </button>
                  <button
                    type="button"
                    onClick={() => session.respondPermission(req().permissionId, 'always')}
                    class="inline-flex h-8 items-center gap-2 rounded-lg border border-[color:var(--border-default)] px-3.5 text-[12.5px] font-medium text-[color:var(--text-secondary)] transition-all var(--spring-sm) hover:bg-[color:var(--bg-hover)] hover:text-[color:var(--text-primary)] active:scale-[0.97]"
                  >
                    Always allow {meta().verb}
                    <kbd class="rounded border border-current px-1 font-mono text-[10px] leading-[15px] opacity-60">A</kbd>
                  </button>
                  <div class="flex-1" />
                  <button
                    type="button"
                    onClick={() => session.respondPermission(req().permissionId, 'reject')}
                    class="inline-flex h-8 items-center gap-2 rounded-lg border border-red-500/30 px-3.5 text-[12.5px] font-medium text-red-400 transition-all var(--spring-sm) hover:bg-red-500/10 hover:text-red-300 active:scale-[0.97]"
                  >
                    Reject
                    <kbd class="rounded border border-current px-1 font-mono text-[10px] leading-[15px] opacity-60">Esc</kbd>
                  </button>
                </div>
              </div>
            </div>
          </div>
        );
      }}
    </Show>
  );
}
