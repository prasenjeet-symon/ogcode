import { createMemo, For, Show } from 'solid-js';
import { useSession } from '../context/session';
import type { ToolPartData } from '../api/client';

// A single active sub-agent delegation. The main agent calls the `task` tool to
// spin up a read-only sub-agent; while that tool call is running/pending the
// sub-agent is alive. We surface each one as an animated bot-head pill in the
// session header.
interface ActiveTask {
  title: string;
}

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

export default function SubagentIndicator() {
  const session = useSession();

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
        tasks.push({ title: String(title) });
      }
    }
    return tasks;
  });

  return (
    <Show when={activeTasks().length > 0}>
      <div class="flex items-center gap-1.5 animate-fade-in">
        <For each={activeTasks()}>
          {(task) => (
            <div
              class="group relative flex items-center gap-1.5 h-7 px-2 rounded-md border border-[color:var(--accent-ring)] bg-[color:var(--accent-soft)] cursor-default select-none animate-pulse-ring overflow-visible"
              title={`${task.title} — sub-agent running`}
            >
              {/* Bot head */}
              <svg class="w-3.5 h-3.5 text-[color:var(--accent)] shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
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
              <span class="text-[11px] font-medium text-[color:var(--accent)] max-w-[140px] truncate">
                {task.title}
              </span>

              {/* Hover tooltip */}
              <div
                class="absolute top-full left-1/2 -translate-x-1/2 mt-1.5 px-2.5 py-1.5 rounded-md border border-[color:var(--border-default)] bg-[color:var(--bg-overlay)] shadow-xl
                       opacity-0 pointer-events-none group-hover:opacity-100 group-hover:pointer-events-auto transition whitespace-nowrap"
                style={{ 'z-index': 9999 }}
              >
                <span class="text-[11px] text-zinc-300">{task.title}</span>
                <span class="text-[10px] text-[color:var(--accent)] ml-1.5">sub-agent running</span>
              </div>
            </div>
          )}
        </For>
      </div>
    </Show>
  );
}