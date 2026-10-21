import { For, Show, createMemo, createSignal } from 'solid-js';
import { useNavigate } from '@solidjs/router';
import { usePlan } from '../context/plan';
import type { Task } from '../api/client';
import MarkdownContent from './markdown-content';
import StatusIcon from './status-icon';

const STATUS_LABEL: Record<string, string> = {
  pending: 'Pending',
  in_progress: 'In Progress',
  completed: 'Done',
  failed: 'Failed',
};

const EFFORT_COLORS: Record<string, string> = {
  S: 'text-emerald-400 bg-emerald-500/10 border-emerald-500/20',
  M: 'text-blue-400 bg-blue-500/10 border-blue-500/20',
  L: 'text-amber-400 bg-amber-500/10 border-amber-500/20',
  XL: 'text-red-400 bg-red-500/10 border-red-500/20',
};

function TaskRow(props: { task: Task; onSelect: (t: Task) => void }) {
  const plan = usePlan();
  const navigate = useNavigate();
  const t = () => props.task;

  const completedIds = () => new Set(plan.tasks().filter((x) => x.status === 'completed').map((x) => x.id));
  const canStart = () =>
    t().status === 'pending' &&
    t().dependencies.every((d) => completedIds().has(d));

  const handleStart = async (e: MouseEvent) => {
    e.stopPropagation();
    await plan.startTaskById(t().id);
  };

  return (
    <div
      onClick={() => props.onSelect(t())}
      class="group flex items-start gap-3 px-3 py-2.5 hover:bg-[color:var(--bg-hover)]/50 rounded-lg transition-colors cursor-pointer"
    >
      <div class="mt-[3px] shrink-0">
        <StatusIcon status={t().status} size={14} />
      </div>
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2 mb-0.5">
          <span class="text-[13px] font-medium text-zinc-200 truncate flex-1">{t().title}</span>
          <span class={`shrink-0 text-[9px] font-bold px-1 py-0.5 rounded border ${EFFORT_COLORS[t().effort] || EFFORT_COLORS.M}`}>
            {t().effort}
          </span>
        </div>
        <Show when={t().description}>
          <MarkdownContent text={t().description} class="prose-chat-preview line-clamp-1 mb-1 text-zinc-500" />
        </Show>
        <div class="flex items-center gap-2">
          <span class="text-[10px] text-zinc-600">{STATUS_LABEL[t().status]}</span>
          <Show when={t().dependencies.length > 0}>
            <span class="text-[10px] text-zinc-700">· {t().dependencies.length} dep{t().dependencies.length > 1 ? 's' : ''}</span>
          </Show>
          <div class="flex-1" />
          <Show when={t().status === 'in_progress'}>
            <button
              type="button"
              onClick={(e) => { e.stopPropagation(); navigate(`/task/${t().id}`); }}
              class="text-[10px] px-1.5 py-0.5 rounded border border-blue-500/30 bg-blue-500/10 text-blue-400 hover:bg-blue-500/20 transition-colors"
            >
              Open →
            </button>
          </Show>
          <Show when={canStart()}>
            <button
              type="button"
              onClick={handleStart}
              class="text-[10px] px-1.5 py-0.5 rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] text-zinc-300 hover:text-white hover:border-[color:var(--accent)] hover:bg-[color:var(--accent-soft)] transition-colors"
            >
              Start
            </button>
          </Show>
          <Show when={t().status === 'failed'}>
            <button
              type="button"
              onClick={(e) => { e.stopPropagation(); plan.retryTaskById(t().id); }}
              class="text-[10px] px-1.5 py-0.5 rounded border border-red-500/30 bg-red-500/10 text-red-400 hover:bg-red-500/20 transition-colors"
            >
              Retry
            </button>
          </Show>
          <Show when={t().status === 'completed' && t().prUrl}>
            <a
              href={t().prUrl!}
              target="_blank"
              rel="noopener noreferrer"
              onClick={(e) => e.stopPropagation()}
              class="text-[10px] text-blue-400 hover:text-blue-300 transition-colors"
            >
              PR #{t().prNumber}
            </a>
          </Show>
          <Show when={t().status === 'completed' && !t().prUrl && t().prError}>
            <span
              title={t().prError}
              class="text-[10px] text-amber-500 cursor-help"
            >
              No PR
            </span>
          </Show>
          <svg class="w-3 h-3 text-zinc-700 group-hover:text-zinc-500 transition-colors shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
          </svg>
        </div>
      </div>
    </div>
  );
}

export default function TaskBoard() {
  const plan = usePlan();
  const navigate = useNavigate();
  const [selectedTask, setSelectedTask] = createSignal<Task | null>(null);

  const breakdownStatus = () => plan.activePlan()?.breakdownStatus;
  const hasTasks = () => plan.tasks().length > 0;

  const ordered = createMemo(() => {
    const order = ['in_progress', 'pending', 'completed', 'failed'];
    return [...plan.tasks()].sort((a, b) => {
      const ai = order.indexOf(a.status);
      const bi = order.indexOf(b.status);
      if (ai !== bi) return ai - bi;
      return a.orderIndex - b.orderIndex;
    });
  });

  const counts = createMemo(() => ({
    pending: plan.tasks().filter((t) => t.status === 'pending').length,
    in_progress: plan.tasks().filter((t) => t.status === 'in_progress').length,
    completed: plan.tasks().filter((t) => t.status === 'completed').length,
    failed: plan.tasks().filter((t) => t.status === 'failed').length,
    total: plan.tasks().length,
  }));

  const completedIds = () => new Set(plan.tasks().filter((t) => t.status === 'completed').map((t) => t.id));
  const eligibleCount = () => plan.tasks().filter(
    (t) => t.status === 'pending' && t.dependencies.every((d) => completedIds().has(d))
  ).length;

  const planId = () => plan.activePlan()?.id;

  return (
    <div class="h-full flex flex-col relative overflow-hidden">
      {/* Breakdown status banners */}
      <Show when={breakdownStatus() === 'in_progress'}>
        <div class="px-4 py-2.5 flex items-center gap-2 text-[12px] text-amber-300 bg-amber-400/10 border-b border-amber-400/20 shrink-0">
          <svg class="w-3.5 h-3.5 animate-spin shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v4m0 8v4m8-8h-4M8 12H4m13.657-5.657l-2.829 2.829M9.172 14.828l-2.829 2.829m11.314 0l-2.829-2.829M9.172 9.172L6.343 6.343" />
          </svg>
          AI is breaking down your plan into tasks…
        </div>
      </Show>
      <Show when={breakdownStatus() === 'failed'}>
        <div class="px-4 py-2.5 flex items-center gap-2 text-[12px] text-red-300 bg-red-400/10 border-b border-red-400/20 shrink-0">
          <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
          Breakdown failed — try locking the plan again
        </div>
      </Show>

      {/* Header with actions */}
      <Show when={hasTasks()}>
        <div class="px-3 py-2.5 border-b border-[color:var(--border-subtle)] flex items-center gap-2 shrink-0">
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="text-[12px] font-semibold text-zinc-200">{counts().total} Tasks</span>
              <Show when={counts().in_progress > 0}>
                <span class="text-[10px] text-blue-400">{counts().in_progress} running</span>
              </Show>
              <Show when={counts().completed > 0}>
                <span class="text-[10px] text-emerald-400">{counts().completed} done</span>
              </Show>
              <Show when={counts().failed > 0}>
                <span class="text-[10px] text-red-400">{counts().failed} failed</span>
              </Show>
            </div>
          </div>

          {/* Open full board */}
          <Show when={planId()}>
            <button
              type="button"
              onClick={() => navigate(`/plan/${planId()}/tasks`)}
              class="shrink-0 h-7 px-2 flex items-center gap-1 text-[11px] text-zinc-400 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)] rounded-md border border-[color:var(--border-subtle)] transition-colors"
              title="Open full task board"
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M4 8V4m0 0h4M4 4l5 5m11-1V4m0 0h-4m4 0l-5 5M4 16v4m0 0h4m-4 0l5-5m11 5l-5-5m5 5v-4m0 4h-4" />
              </svg>
              Board
            </button>
          </Show>
        </div>

        {/* Start All CTA */}
        <Show when={eligibleCount() > 0}>
          <div class="px-3 py-2 border-b border-[color:var(--border-subtle)] shrink-0">
            <button
              type="button"
              onClick={() => plan.startAllTasks()}
              class="w-full h-8 rounded-lg bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)] text-[color:var(--on-primary)] text-[12px] font-semibold flex items-center justify-center gap-2 transition-colors shadow-sm"
            >
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
              </svg>
              Start {eligibleCount()} Task{eligibleCount() > 1 ? 's' : ''}
            </button>
          </div>
        </Show>
      </Show>

      {/* Empty states */}
      <Show when={!hasTasks() && breakdownStatus() !== 'in_progress' && breakdownStatus() !== 'failed'}>
        <div class="flex-1 flex flex-col items-center justify-center py-12 text-center px-4">
          <div class="w-12 h-12 rounded-xl bg-[color:var(--accent-soft)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-3">
            <svg class="w-5 h-5 text-[color:var(--accent-ink)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.6">
              <path stroke-linecap="round" stroke-linejoin="round" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
            </svg>
          </div>
          <p class="text-[13px] font-medium text-zinc-300 mb-1">No tasks yet</p>
          <p class="text-[11px] text-zinc-500">Lock the plan to generate tasks.</p>
        </div>
      </Show>

      {/* Task list */}
      <Show when={hasTasks()}>
        <div class="flex-1 overflow-y-auto py-1">
          <For each={ordered()}>
            {(task) => <TaskRow task={task} onSelect={setSelectedTask} />}
          </For>
        </div>
      </Show>

      {/* Mini detail panel — slides over the side panel */}
      <Show when={selectedTask()}>
        <div class="absolute inset-0 z-20 flex flex-col bg-[color:var(--bg-surface)] border-l border-[color:var(--border-subtle)]">
          {/* Header */}
          <div class="h-11 shrink-0 border-b border-[color:var(--border-subtle)] flex items-center px-3 gap-2">
            <button
              type="button"
              onClick={() => setSelectedTask(null)}
              class="w-6 h-6 rounded flex items-center justify-center text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)] transition shrink-0"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
              </svg>
            </button>
            <span class="text-[12px] font-semibold text-zinc-200 truncate flex-1">{selectedTask()!.title}</span>
          </div>
          {/* Body */}
          <div class="flex-1 overflow-y-auto p-3 space-y-4 text-[12px]">
            {/* Status badges */}
            <div class="flex flex-wrap gap-1.5">
              <span class={`px-2 py-0.5 rounded border text-[10px] font-semibold ${
                selectedTask()!.status === 'completed' ? 'text-emerald-400 bg-emerald-500/10 border-emerald-500/20' :
                selectedTask()!.status === 'in_progress' ? 'text-blue-400 bg-blue-500/10 border-blue-500/20' :
                selectedTask()!.status === 'failed' ? 'text-red-400 bg-red-500/10 border-red-500/20' :
                'text-zinc-400 bg-zinc-500/10 border-zinc-500/20'
              }`}>{selectedTask()!.status.replace('_', ' ')}</span>
              <span class={`px-2 py-0.5 rounded border text-[10px] font-bold ${EFFORT_COLORS[selectedTask()!.effort] || EFFORT_COLORS.M}`}>
                {selectedTask()!.effort}
              </span>
              <span class="text-[10px] text-zinc-500 capitalize self-center">{selectedTask()!.complexity}</span>
            </div>
            {/* Description */}
            <Show when={selectedTask()!.description}>
              <div>
                <p class="text-[10px] font-semibold text-zinc-600 uppercase tracking-wider mb-1">Description</p>
                <MarkdownContent text={selectedTask()!.description} class="text-[13px]" />
              </div>
            </Show>
            {/* Branch */}
            <Show when={selectedTask()!.branchName}>
              <div>
                <p class="text-[10px] font-semibold text-zinc-600 uppercase tracking-wider mb-1">Branch</p>
                <code class="text-[11px] font-mono text-zinc-400 bg-[color:var(--bg-elevated)] px-2 py-1 rounded border border-[color:var(--border-subtle)] block">{selectedTask()!.branchName}</code>
              </div>
            </Show>
            <Show when={selectedTask()!.prUrl}>
              <div>
                <p class="text-[10px] font-semibold text-zinc-600 uppercase tracking-wider mb-1">Pull Request</p>
                <a
                  href={selectedTask()!.prUrl!}
                  target="_blank"
                  rel="noopener noreferrer"
                  class="text-[12px] text-blue-400 hover:text-blue-300 transition-colors"
                >
                  PR #{selectedTask()!.prNumber} ↗
                </a>
              </div>
            </Show>
            <Show when={!selectedTask()!.prUrl && selectedTask()!.prError}>
              <div>
                <p class="text-[10px] font-semibold text-zinc-600 uppercase tracking-wider mb-1">Pull Request</p>
                <div class="flex items-start gap-1.5 px-2 py-1.5 rounded-lg bg-amber-500/10 border border-amber-500/20">
                  <svg class="w-3.5 h-3.5 text-amber-400 shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
                  </svg>
                  <span class="text-[11px] text-amber-300 break-words">{selectedTask()!.prError}</span>
                </div>
              </div>
            </Show>
            {/* Dependencies */}
            <Show when={selectedTask()!.dependencies.length > 0}>
              <div>
                <p class="text-[10px] font-semibold text-zinc-600 uppercase tracking-wider mb-1.5">
                  Dependencies ({selectedTask()!.dependencies.length})
                </p>
                <div class="space-y-1">
                  <For each={selectedTask()!.dependencies.map((id) => plan.tasks().find((x) => x.id === id)).filter(Boolean) as Task[]}>
                    {(dep) => (
                      <div class="flex items-center gap-2 px-2 py-1.5 rounded-lg border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)]">
                        <StatusIcon status={dep.status} size={12} />
                        <span class="text-zinc-300 flex-1 truncate text-[11px]">{dep.title}</span>
                        <span class="text-[9px] text-zinc-600 capitalize">{dep.status.replace('_', ' ')}</span>
                      </div>
                    )}
                  </For>
                </div>
              </div>
            </Show>
          </div>
          {/* Footer actions */}
          <div class="shrink-0 border-t border-[color:var(--border-subtle)] p-2.5 flex gap-2">
            <Show when={selectedTask()!.status === 'in_progress'}>
              <button
                type="button"
                onClick={() => navigate(`/task/${selectedTask()!.id}`)}
                class="flex-1 h-8 rounded-lg bg-blue-500/15 border border-blue-500/30 text-blue-400 text-[11px] font-medium flex items-center justify-center gap-1.5 transition hover:bg-blue-500/25"
              >
                Open Session
              </button>
            </Show>
            <Show when={selectedTask()!.status === 'pending' && selectedTask()!.dependencies.every((d) => plan.tasks().find((x) => x.id === d)?.status === 'completed')}>
              <button
                type="button"
                onClick={async () => { await plan.startTaskById(selectedTask()!.id); setSelectedTask(null); }}
                class="flex-1 h-8 rounded-lg bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)] text-[color:var(--on-primary)] text-[11px] font-semibold flex items-center justify-center gap-1.5 transition"
              >
                Start Task
              </button>
            </Show>
          </div>
        </div>
      </Show>
    </div>
  );
}
