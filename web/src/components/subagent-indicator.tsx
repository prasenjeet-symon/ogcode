import { createMemo, createSignal, For, onCleanup, Show } from 'solid-js';
import { useSession } from '../context/session';
import type { ToolPartData } from '../api/client';

// A single active sub-agent delegation. The main agent calls the `task` tool to
// spin up a read-only sub-agent; while that tool call is running/pending the
// sub-agent is alive.
interface ActiveTask {
  id: string;
  title: string;
  workload: string;
  startedAt: number;
}

// Above this many concurrent agents the dock shows a "+N" tail instead of
// growing tall enough to eat the transcript.
const MAX_VISIBLE = 5;

function isStaleLoop(msgs: any[]): boolean {
  for (let i = msgs.length - 1; i >= 0; i--) {
    if (msgs[i].info.role === 'assistant') {
      const finish = msgs[i].info.finish;
      if (finish === 'error' || finish === 'aborted') return true;
      break;
    }
  }
  return false;
}

function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return `${minutes}:${String(seconds).padStart(2, '0')}`;
}

/**
 * Floating corner dock for running sub-agents.
 *
 * Rendered from the session page inside a `relative` wrapper around the
 * composer, anchored `bottom-full` so the stack sits just above the input and
 * never squeezes the header title. Each pill is one `task` delegation: bot
 * head + title at rest, workload and elapsed time revealed on hover.
 *
 * It must stay outside the message scroller — an absolutely positioned child of
 * a scrolling column would scroll away with the transcript.
 */
export default function SubagentIndicator() {
  const session = useSession();

  // One shared ticker drives every elapsed label, rather than a timer per pill.
  const [now, setNow] = createSignal(Date.now());
  const ticker = setInterval(() => setNow(Date.now()), 1000);
  onCleanup(() => clearInterval(ticker));

  const activeTasks = createMemo<ActiveTask[]>(() => {
    const msgs = session.messages();
    const stale = isStaleLoop(msgs);
    const tasks: ActiveTask[] = [];
    for (const m of msgs) {
      if (!m.parts) continue;
      for (const part of m.parts) {
        if (part.type !== 'tool') continue;
        let data: ToolPartData | null = null;
        try {
          data = typeof part.data === 'string'
            ? JSON.parse(part.data)
            : part.data;
        } catch { continue; }
        if (!data || data.tool !== 'task') continue;
        const status = data.state?.status;
        if (status !== 'running' && status !== 'pending') continue;
        if (stale) continue;
        const input = data.state?.input ?? {};
        const title = (data.state?.title as string) || input?.description || 'Sub-agent';
        tasks.push({
          id: data.callId || `${m.info.id}:${tasks.length}`,
          title: String(title),
          workload: typeof input?.workload === 'string' ? input.workload : '',
          startedAt: data.state?.time?.start ?? part.updatedAt ?? Date.now(),
        });
      }
    }
    return tasks;
  });

  const visibleTasks = createMemo(() => activeTasks().slice(0, MAX_VISIBLE));
  const overflowCount = createMemo(() => Math.max(0, activeTasks().length - MAX_VISIBLE));

  return (
    <Show when={activeTasks().length > 0}>
      <div
        class="pointer-events-none absolute bottom-full right-3 z-[120] mb-1 flex flex-col items-end gap-1.5"
        aria-live="polite"
        aria-label={`${activeTasks().length} sub-agent${activeTasks().length === 1 ? '' : 's'} running`}
      >
        <For each={visibleTasks()}>
          {(task) => (
            <div
              class="group pointer-events-auto flex items-center gap-1.5 h-7 pl-2 pr-2.5 rounded-full border border-[color:var(--border-default)] bg-[color:var(--bg-overlay)]/95 backdrop-blur-md shadow-lg shadow-black/30 text-micro text-[color:var(--text-primary)] cursor-default select-none max-w-[230px] transition-[transform,border-color] duration-150 hover:-translate-y-px hover:border-[color:var(--border-strong)] animate-fade-in-up"
              title={`${task.title} — sub-agent running`}
            >
              {/* Bot head */}
              <svg class="w-3.5 h-3.5 shrink-0 text-[color:var(--accent-ink)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                {/* antenna */}
                <path stroke-linecap="round" d="M12 3v3" />
                <circle cx="12" cy="2.5" r="1" fill="currentColor" stroke="none" />
                {/* head */}
                <rect x="5" y="6" width="14" height="11" rx="3" />
                {/* eyes */}
                <circle cx="9.5" cy="11" r="1.4" fill="currentColor" stroke="none" />
                <circle cx="14.5" cy="11" r="1.4" fill="currentColor" stroke="none" />
                {/* mouth */}
                <path stroke-linecap="round" d="M9.5 14.5h5" />
              </svg>
              <span class="truncate min-w-0 max-w-[150px]">{task.title}</span>
              <span class="w-2.5 h-2.5 shrink-0 rounded-full border-[1.5px] border-[color:var(--accent)] border-t-transparent animate-spin" />
              {/* Expanded on hover: workload + elapsed. Collapses to zero width
                  at rest so the pill keeps its compact title-only footprint. */}
              <span class="shrink-0 max-w-0 opacity-0 overflow-hidden text-ellipsis whitespace-nowrap tabular-nums text-[10.5px] text-[color:var(--text-tertiary)] transition-all duration-150 group-hover:max-w-[130px] group-hover:ml-0.5 group-hover:opacity-100">
                {[task.workload, formatElapsed(now() - task.startedAt)].filter(Boolean).join(' · ')}
              </span>
            </div>
          )}
        </For>
        <Show when={overflowCount() > 0}>
          <div class="pointer-events-auto flex items-center h-7 px-2.5 rounded-full border border-[color:var(--border-default)] bg-[color:var(--bg-overlay)]/95 backdrop-blur-md shadow-lg shadow-black/30 text-micro text-[color:var(--text-tertiary)] cursor-default select-none animate-fade-in-up">
            +{overflowCount()} more
          </div>
        </Show>
      </div>
    </Show>
  );
}
