import { useParams, useNavigate } from '@solidjs/router';
import { createEffect, on, onMount, onCleanup, For, Show, createMemo, createSignal } from 'solid-js';
import { usePlan } from '../context/plan';
import Breadcrumb from '../components/breadcrumb';
import { NotFoundPanel } from './not-found';
import MarkdownContent from '../components/markdown-content';
import StatusIcon from '../components/status-icon';
import CommandMenu, { type CommandItem } from '../components/command-menu';
import ModelSelector from '../components/model-selector';
import { useSession } from '../context/session';
import type { Task } from '../api/client';

// ─── constants ────────────────────────────────────────────────────────────────

// Linear-aligned status palette — colors live in the --status-* design tokens.
const STATUS_META: Record<string, { label: string; color: string; soft: string }> = {
  pending:     { label: 'Pending',     color: 'var(--status-pending)',    soft: 'var(--status-pending-soft)' },
  in_progress: { label: 'In Progress', color: 'var(--status-progress)',   soft: 'var(--status-progress-soft)' },
  completed:   { label: 'Completed',   color: 'var(--status-completed)',  soft: 'var(--status-completed-soft)' },
  failed:      { label: 'Failed',      color: 'var(--status-failed)',     soft: 'var(--status-failed-soft)' },
};

const EFFORT_META: Record<string, { cls: string; label: string }> = {
  S:  { cls: 'text-emerald-400 bg-emerald-500/10 border-emerald-500/25', label: 'Small' },
  M:  { cls: 'text-blue-400 bg-blue-500/10 border-blue-500/25',          label: 'Medium' },
  L:  { cls: 'text-amber-400 bg-amber-500/10 border-amber-500/25',       label: 'Large' },
  XL: { cls: 'text-red-400 bg-red-500/10 border-red-500/25',             label: 'XL' },
};

const COMPLEXITY_CLS: Record<string, string> = {
  low:    'text-emerald-400',
  medium: 'text-amber-400',
  high:   'text-red-400',
};

const COLUMNS: Array<{ status: Task['status']; label: string }> = [
  { status: 'pending',     label: 'Pending' },
  { status: 'in_progress', label: 'In Progress' },
  { status: 'completed',   label: 'Completed' },
  { status: 'failed',      label: 'Failed' },
];

// StatusBadge — a small Linear-style status pill (soft bg, colored text, faint border).
function StatusBadge(props: { status: string; class?: string }) {
  const m = () => STATUS_META[props.status] || STATUS_META.pending;
  return (
    <span
      class={`inline-flex items-center gap-1.5 rounded-md border font-medium text-[11px] px-2 py-0.5 leading-none ${props.class || ''}`}
      style={{
        color: m().color,
        background: m().soft,
        'border-color': `color-mix(in srgb, ${m().color} 22%, transparent)`,
      }}
    >
      <StatusIcon status={props.status} size={11} />
      {m().label}
    </span>
  );
}

// ─── task detail drawer ───────────────────────────────────────────────────────

function TaskDrawer(props: { task: Task | null; onClose: () => void }) {
  const plan = usePlan();
  const session = useSession();
  const navigate = useNavigate();
  const [starting, setStarting] = createSignal(false);
  const [completing, setCompleting] = createSignal(false);
  const [failing, setFailing] = createSignal(false);
  const [retrying, setRetrying] = createSignal(false);

  const t = () => props.task;

  // Model: a task may override the plan's model; empty = inherit the plan default.
  const planModelId = () => plan.activePlan()?.model || '';
  const effectiveModelId = () => t()?.model || planModelId();
  const modelLabel = (id: string) => session.models().find((m) => m.id === id)?.name || id || 'Default';
  const modelEditable = () => t()?.status === 'pending' || t()?.status === 'failed';

  const depTasks = () => {
    if (!t()) return [];
    return t()!.dependencies.map((id) => plan.tasks().find((x) => x.id === id)).filter(Boolean) as Task[];
  };

  const completedIds = () => new Set(plan.tasks().filter((x) => x.status === 'completed').map((x) => x.id));
  const canStart = () => plan.activePlan()?.status === 'locked' && t()?.status === 'pending' && (t()?.dependencies ?? []).every((d) => completedIds().has(d));
  const blockedBy = () => depTasks().filter((d) => d.status !== 'completed');

  const handleStart = async () => {
    if (!t()) return;
    setStarting(true);
    try {
      await plan.startTaskById(t()!.id);
    } finally {
      setStarting(false);
    }
  };

  const handleComplete = async () => {
    if (!t()) return;
    setCompleting(true);
    try {
      await plan.completeTaskById(t()!.id);
    } finally {
      setCompleting(false);
    }
  };

  const handleFail = async () => {
    if (!t()) return;
    setFailing(true);
    try {
      await plan.failTaskById(t()!.id);
    } finally {
      setFailing(false);
    }
  };

  const handleRetry = async () => {
    if (!t()) return;
    setRetrying(true);
    try {
      await plan.retryTaskById(t()!.id);
    } finally {
      setRetrying(false);
    }
  };

  return (
    <>
      {/* Backdrop */}
      <Show when={!!t()}>
        <div
          class="fixed inset-0 z-40 bg-black/40 backdrop-blur-[1px]"
          onClick={props.onClose}
        />
      </Show>

      {/* Drawer */}
      <div
        class={`fixed top-0 right-0 h-full z-50 w-[40vw] min-w-[420px] max-w-[95vw] flex flex-col
                border-l border-[color:var(--border-subtle)] shadow-2xl
                transition-transform duration-250 ease-out
                ${t() ? 'translate-x-0' : 'translate-x-full'}`}
        style={{ background: 'var(--bg-surface)' }}
      >
        <Show when={!!t()}>
          {/* Drawer header */}
          <div class="h-12 shrink-0 border-b border-[color:var(--border-subtle)] flex items-center px-4 gap-3">
            <div class="flex items-center gap-2 flex-1 min-w-0">
              <StatusIcon status={t()!.status} size={15} />
              <span class="text-[13px] font-semibold text-zinc-100 truncate">{t()!.title}</span>
            </div>
            <button
              type="button"
              onClick={props.onClose}
              class="w-7 h-7 rounded-md text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition shrink-0"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>

          {/* Drawer body */}
          <div class="flex-1 overflow-y-auto p-4 space-y-5">

            {/* Status + badges */}
            <div class="flex items-center gap-2 flex-wrap">
              <StatusBadge status={t()!.status} />
              <span class={`text-[11px] font-bold px-2 py-1 rounded-lg border ${EFFORT_META[t()!.effort]?.cls || EFFORT_META.M.cls}`}>
                {EFFORT_META[t()!.effort]?.label || t()!.effort} effort
              </span>
              <span class={`text-[11px] font-medium capitalize ${COMPLEXITY_CLS[t()!.complexity] || 'text-zinc-400'}`}>
                {t()!.complexity} complexity
              </span>
            </div>

            {/* Model */}
            <div>
              <p class="text-[11px] font-semibold text-zinc-500 uppercase tracking-wider mb-1.5">Model</p>
              <Show
                when={modelEditable()}
                fallback={
                  <span class="text-[12px] text-zinc-300">
                    {modelLabel(effectiveModelId())}
                    <Show when={!t()!.model}><span class="text-zinc-600"> · plan default</span></Show>
                  </span>
                }
              >
                <div class="flex items-center gap-2 flex-wrap">
                  <div class="rounded-lg border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)]">
                    <ModelSelector
                      selectedModel={() => effectiveModelId()}
                      onSelect={(id) => plan.setTaskModel(t()!.id, id)}
                      placement="top"
                    />
                  </div>
                  <Show
                    when={t()!.model}
                    fallback={<span class="text-[11px] text-zinc-600">inherits plan default</span>}
                  >
                    <button
                      type="button"
                      onClick={() => plan.setTaskModel(t()!.id, '')}
                      class="text-[11px] text-zinc-500 hover:text-zinc-300 transition-colors"
                    >
                      Reset to plan default
                    </button>
                  </Show>
                </div>
              </Show>
            </div>

            {/* Description */}
            <Show when={t()!.description}>
              <div>
                <p class="text-[11px] font-semibold text-zinc-500 uppercase tracking-wider mb-1.5">Description</p>
                <MarkdownContent text={t()!.description} />
              </div>
            </Show>

            {/* Branch */}
            <Show when={t()!.branchName}>
              <div>
                <p class="text-[11px] font-semibold text-zinc-500 uppercase tracking-wider mb-1.5">Branch</p>
                <code class="text-[12px] text-zinc-300 bg-[color:var(--bg-elevated)] px-2.5 py-1.5 rounded-lg border border-[color:var(--border-subtle)] font-mono block w-fit">
                  {t()!.branchName}
                </code>
              </div>
            </Show>

            {/* PR link */}
            <Show when={t()!.prUrl}>
              <div>
                <p class="text-[11px] font-semibold text-zinc-500 uppercase tracking-wider mb-1.5">Pull Request</p>
                <a
                  href={t()!.prUrl!}
                  target="_blank"
                  rel="noopener noreferrer"
                  class="inline-flex items-center gap-1.5 text-[12px] text-[color:var(--accent)] hover:opacity-80 transition-opacity"
                >
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                  </svg>
                  PR #{t()!.prNumber}
                </a>
              </div>
            </Show>

            {/* Dependencies */}
            <Show when={depTasks().length > 0}>
              <div>
                <p class="text-[11px] font-semibold text-zinc-500 uppercase tracking-wider mb-2">
                  Dependencies ({depTasks().length})
                </p>
                <div class="space-y-1.5">
                  <For each={depTasks()}>
                    {(dep) => (
                      <div
                        class="flex items-center gap-2.5 px-3 py-2 rounded-lg border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] cursor-pointer hover:border-[color:var(--border-default)] transition-colors"
                      >
                        <StatusIcon status={dep.status} size={13} />
                        <span class="text-[12px] text-zinc-300 flex-1 truncate">{dep.title}</span>
                        <StatusBadge status={dep.status} />
                      </div>
                    )}
                  </For>
                </div>
              </div>
            </Show>

            {/* Blocked notice */}
            <Show when={t()!.status === 'pending' && blockedBy().length > 0}>
              <div class="flex items-start gap-2.5 px-3 py-2.5 rounded-lg bg-amber-500/8 border border-amber-500/20">
                <svg class="w-4 h-4 text-amber-400 shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M12 3a9 9 0 100 18A9 9 0 0012 3z" />
                </svg>
                <p class="text-[12px] text-amber-300">
                  Blocked by {blockedBy().length} incomplete {blockedBy().length === 1 ? 'dependency' : 'dependencies'}
                </p>
              </div>
            </Show>
          </div>

          {/* Drawer footer actions */}
          <div class="shrink-0 border-t border-[color:var(--border-subtle)] p-3 space-y-2">
            {/* Primary action row */}
            <div class="flex gap-2">
              <Show when={t()!.status === 'in_progress'}>
                <button
                  type="button"
                  onClick={() => navigate(`/task/${t()!.id}`)}
                  class="flex-1 h-9 rounded-lg bg-blue-500/15 hover:bg-blue-500/25 border border-blue-500/30 text-blue-400 text-[13px] font-medium flex items-center justify-center gap-2 transition"
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
                  </svg>
                  Open Agent Session
                </button>
              </Show>
              <Show when={canStart()}>
                <button
                  type="button"
                  onClick={handleStart}
                  disabled={starting()}
                  class="flex-1 h-9 rounded-lg bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)] text-[color:var(--on-primary)] text-[13px] font-semibold flex items-center justify-center gap-2 transition shadow-sm disabled:opacity-60"
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
                  </svg>
                  {starting() ? 'Starting…' : 'Start Task'}
                </button>
              </Show>
              <Show when={t()!.status === 'failed'}>
                <button
                  type="button"
                  onClick={handleRetry}
                  disabled={retrying()}
                  class="flex-1 h-9 rounded-lg bg-red-500/15 hover:bg-red-500/25 border border-red-500/30 text-red-400 text-[13px] font-semibold flex items-center justify-center gap-2 transition disabled:opacity-60"
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                  </svg>
                  {retrying() ? 'Retrying…' : 'Retry Task'}
                </button>
              </Show>
              <Show when={t()!.status === 'completed' || (t()!.status === 'pending' && !canStart() && blockedBy().length === 0)}>
                <div class="flex-1 h-9 rounded-lg border border-[color:var(--border-subtle)] flex items-center justify-center">
                  <span class="text-[12px] text-zinc-600">No actions available</span>
                </div>
              </Show>
            </div>

            {/* Manual override row for in-progress tasks */}
            <Show when={t()!.status === 'in_progress'}>
              <div class="flex gap-2">
                <button
                  type="button"
                  onClick={handleComplete}
                  disabled={completing()}
                  class="flex-1 h-8 rounded-lg bg-emerald-500/12 hover:bg-emerald-500/20 border border-emerald-500/25 text-emerald-400 text-[12px] font-medium flex items-center justify-center gap-1.5 transition disabled:opacity-50"
                >
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                  {completing() ? 'Completing…' : 'Mark Complete'}
                </button>
                <button
                  type="button"
                  onClick={handleFail}
                  disabled={failing()}
                  class="flex-1 h-8 rounded-lg bg-red-500/12 hover:bg-red-500/20 border border-red-500/25 text-red-400 text-[12px] font-medium flex items-center justify-center gap-1.5 transition disabled:opacity-50"
                >
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                  {failing() ? 'Failing…' : 'Mark Failed'}
                </button>
              </div>
            </Show>
          </div>
        </Show>
      </div>
    </>
  );
}

// ─── kanban card ──────────────────────────────────────────────────────────────

function KanbanCard(props: { task: Task; focused: boolean; onSelect: (t: Task) => void }) {
  const plan = usePlan();
  const sess = useSession();
  const [starting, setStarting] = createSignal(false);

  const t = () => props.task;
  const completedIds = () => new Set(plan.tasks().filter((x) => x.status === 'completed').map((x) => x.id));
  const canStart = () => plan.activePlan()?.status === 'locked' && t().status === 'pending' && t().dependencies.every((d) => completedIds().has(d));
  const isBlocked = () => t().status === 'pending' && !canStart();
  const depCount = () => t().dependencies.length;

  // Effective model for this task: its override, else the plan's model, else default.
  const modelOverridden = () => !!t().model;
  const modelId = () => t().model || plan.activePlan()?.model || '';
  const modelName = () => {
    const id = modelId();
    if (!id) return 'Default';
    return sess.models().find((m) => m.id === id)?.name || id;
  };

  const handleStart = async (e: MouseEvent) => {
    e.stopPropagation();
    setStarting(true);
    try { await plan.startTaskById(t().id); } finally { setStarting(false); }
  };

  return (
    <div
      id={`task-card-${t().id}`}
      onClick={() => props.onSelect(t())}
      class={`group rounded-[10px] border p-2.5 cursor-pointer transition-colors duration-100
              ${props.focused
                ? 'border-[color:var(--accent)] bg-[color:var(--bg-elevated)]'
                : 'border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] hover:border-[color:var(--border-default)] hover:bg-[color:var(--bg-elevated)]'}
              ${isBlocked() ? 'opacity-70' : ''}`}
      style={{ 'box-shadow': props.focused ? '0 0 0 1px var(--accent)' : undefined }}
    >
      {/* Title */}
      <p class="text-[13px] font-medium leading-snug line-clamp-2 mb-2" style={{ color: 'var(--text-primary)' }}>
        {t().title}
      </p>

      {/* Meta row */}
      <div class="flex items-center gap-1.5">
        <span class={`text-[9px] font-bold px-1.5 py-0.5 rounded border ${EFFORT_META[t().effort]?.cls || EFFORT_META.M.cls}`}>
          {t().effort}
        </span>
        <span class={`text-[10px] capitalize ${COMPLEXITY_CLS[t().complexity] || 'text-zinc-500'}`}>
          {t().complexity}
        </span>

        <Show when={depCount() > 0}>
          <span
            class="inline-flex items-center gap-0.5 text-[10px] tabular-nums"
            style={{ color: isBlocked() ? 'var(--status-progress)' : 'var(--text-muted)' }}
            title={`${depCount()} ${depCount() === 1 ? 'dependency' : 'dependencies'}`}
          >
            <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M13.828 10.172a4 4 0 010 5.657l-3 3a4 4 0 01-5.657-5.657l1.5-1.5M10.172 13.828a4 4 0 010-5.657l3-3a4 4 0 015.657 5.657l-1.5 1.5" />
            </svg>
            {depCount()}
          </span>
        </Show>

        <div class="flex-1" />

        <Show when={t().prUrl}>
          <a
            href={t().prUrl!}
            target="_blank"
            rel="noopener noreferrer"
            onClick={(e) => e.stopPropagation()}
            class="text-[10px] text-[color:var(--accent)] hover:opacity-80 transition-opacity"
          >
            PR #{t().prNumber}
          </a>
        </Show>

        <Show when={canStart()}>
          <button
            type="button"
            onClick={handleStart}
            disabled={starting()}
            class={`text-[10px] px-2 py-0.5 rounded-md border border-[color:var(--accent)]/50
                    bg-[color:var(--accent-soft)] text-[color:var(--accent)] hover:bg-[color:var(--accent)]/25
                    transition disabled:opacity-50 font-medium
                    ${props.focused ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'}`}
          >
            {starting() ? '…' : 'Start'}
          </button>
        </Show>

        <Show when={isBlocked()}>
          <span class="text-[10px] font-medium" style={{ color: 'var(--status-progress)' }}>Blocked</span>
        </Show>
      </div>

      {/* Model — effective model for this task (override or plan default) */}
      <div
        class="flex items-center gap-1 mt-2 min-w-0 text-[10px]"
        style={{ color: modelOverridden() ? 'var(--text-secondary)' : 'var(--text-muted)' }}
        title={`Model: ${modelName()}${modelOverridden() ? ' (per-task override)' : ' (plan default)'}`}
      >
        <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
          <path stroke-linecap="round" stroke-linejoin="round" d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" />
        </svg>
        <span class="truncate">{modelName()}</span>
        <Show when={modelOverridden()}>
          <span
            class="shrink-0 text-[8.5px] uppercase tracking-wide px-1 py-px rounded font-medium"
            style={{ color: 'var(--accent)', background: 'var(--accent-soft)' }}
          >
            custom
          </span>
        </Show>
      </div>
    </div>
  );
}

// ─── page ─────────────────────────────────────────────────────────────────────

export default function PlanTasksPage() {
  const plan = usePlan();
  const params = useParams();
  const navigate = useNavigate();
  const [startingAll, setStartingAll] = createSignal(false);
  const [startError, setStartError] = createSignal('');
  const [selectedTask, setSelectedTask] = createSignal<Task | null>(null);
  const [focusedId, setFocusedId] = createSignal<string | null>(null);
  const [menuOpen, setMenuOpen] = createSignal(false);

  createEffect(on(() => params.id, (id) => {
    if (id) plan.selectPlan(id);
  }));

  // Keep drawer in sync with live task data
  createEffect(() => {
    const sel = selectedTask();
    if (!sel) return;
    const fresh = plan.tasks().find((t) => t.id === sel.id);
    if (fresh) setSelectedTask(fresh);
  });

  // Scroll the keyboard-focused card into view when it changes.
  createEffect(() => {
    const id = focusedId();
    if (!id) return;
    queueMicrotask(() => document.getElementById(`task-card-${id}`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' }));
  });

  const breadcrumbs = () => {
    const p = plan.activePlan();
    return [
      { label: 'Plans', href: '/plan' },
      // Link via params.id, not activePlan: the plan loads asynchronously (and
      // not at all if the fetch fails), so `p?.id` interpolates to the string
      // "undefined" and the crumb points at /plan/undefined.
      { label: p?.title || 'Plan', href: `/plan/${params.id}` },
      { label: 'Tasks' },
    ];
  };

  const byStatus = (status: Task['status']) =>
    plan.tasks().filter((t) => t.status === status).sort((a, b) => a.orderIndex - b.orderIndex);

  const completedIds = () => new Set(plan.tasks().filter((t) => t.status === 'completed').map((t) => t.id));
  const eligibleCount = () =>
    plan.tasks().filter((t) => t.status === 'pending' && t.dependencies.every((d) => completedIds().has(d))).length;
  const planLocked = () => plan.activePlan()?.status === 'locked';
  const canStartTask = (t: Task) =>
    planLocked() && t.status === 'pending' && t.dependencies.every((d) => completedIds().has(d));

  const counts = createMemo(() => ({
    total:       plan.tasks().length,
    done:        plan.tasks().filter((t) => t.status === 'completed').length,
    running:     plan.tasks().filter((t) => t.status === 'in_progress').length,
    failed:      plan.tasks().filter((t) => t.status === 'failed').length,
    pending:     plan.tasks().filter((t) => t.status === 'pending').length,
  }));

  const progress = () => counts().total > 0 ? Math.round((counts().done / counts().total) * 100) : 0;

  const handleStartAll = async () => {
    setStartingAll(true);
    setStartError('');
    try {
      await plan.startAllTasks();
    } catch (e: any) {
      setStartError(e?.message || String(e));
    } finally {
      setStartingAll(false);
    }
  };

  // ── keyboard navigation across the board ──
  // 2D model: columns in COLUMNS order, tasks within each column by orderIndex.
  const grid = () => COLUMNS.map((c) => byStatus(c.status));

  const locate = (id: string | null): [number, number] | null => {
    if (!id) return null;
    const g = grid();
    for (let c = 0; c < g.length; c++) {
      const r = g[c].findIndex((t) => t.id === id);
      if (r >= 0) return [c, r];
    }
    return null;
  };

  const firstFocusable = (): string | null => {
    const g = grid();
    for (let c = 0; c < g.length; c++) if (g[c].length) return g[c][0].id;
    return null;
  };

  const moveFocus = (dCol: number, dRow: number) => {
    const g = grid();
    const pos = locate(focusedId());
    if (!pos) { setFocusedId(firstFocusable()); return; }
    let [c, r] = pos;
    if (dRow !== 0) {
      r = Math.max(0, Math.min(g[c].length - 1, r + dRow));
    }
    if (dCol !== 0) {
      let nc = c;
      for (let step = 0; step < g.length; step++) {
        nc = nc + dCol;
        if (nc < 0 || nc >= g.length) return; // off the edge
        if (g[nc].length) break;
      }
      if (nc < 0 || nc >= g.length || !g[nc].length) return;
      c = nc;
      r = Math.min(r, g[c].length - 1);
    }
    const target = g[c]?.[r];
    if (target) setFocusedId(target.id);
  };

  const focusedTask = () => plan.tasks().find((t) => t.id === focusedId()) || null;
  // Command palette acts on the open drawer task, else the keyboard-focused task.
  const menuTask = () => selectedTask() || focusedTask();

  const commands = (): CommandItem[] => {
    const items: CommandItem[] = [];
    const t = menuTask();
    if (t) {
      items.push({ id: 'open', label: `Open “${t.title}”`, hint: '↵', keywords: 'detail view', onSelect: () => setSelectedTask(t) });
      if (canStartTask(t)) items.push({ id: 'start', label: 'Start task', hint: 'S', keywords: 'run begin execute', onSelect: () => void plan.startTaskById(t.id) });
      if (t.status === 'in_progress') {
        items.push({ id: 'session', label: 'Open agent session', keywords: 'logs terminal', onSelect: () => navigate(`/task/${t.id}`) });
        items.push({ id: 'complete', label: 'Mark complete', keywords: 'done finish', onSelect: () => void plan.completeTaskById(t.id) });
        items.push({ id: 'fail', label: 'Mark failed', danger: true, keywords: 'cancel stop', onSelect: () => void plan.failTaskById(t.id) });
      }
      if (t.status === 'failed') items.push({ id: 'retry', label: 'Retry task', hint: 'R', keywords: 'rerun again', onSelect: () => void plan.retryTaskById(t.id) });
      if (t.prUrl) items.push({ id: 'pr', label: `Open pull request #${t.prNumber}`, keywords: 'github review', onSelect: () => window.open(t.prUrl!, '_blank', 'noopener') });
    }
    if (planLocked() && eligibleCount() > 0) {
      items.push({ id: 'start-all', label: `Start all eligible tasks (${eligibleCount()})`, keywords: 'run everything batch', onSelect: () => void handleStartAll() });
    }
    return items;
  };

  const onKeyDown = (e: KeyboardEvent) => {
    // Command palette: Cmd/Ctrl+K
    if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
      e.preventDefault();
      setMenuOpen(true);
      return;
    }
    // Ignore while typing or when the palette/drawer owns the keys.
    if (menuOpen()) return;
    const el = e.target as HTMLElement | null;
    if (el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)) return;
    if (e.metaKey || e.ctrlKey || e.altKey) return;

    switch (e.key) {
      case 'ArrowDown': case 'j': e.preventDefault(); moveFocus(0, 1); break;
      case 'ArrowUp':   case 'k': e.preventDefault(); moveFocus(0, -1); break;
      case 'ArrowRight':case 'l': e.preventDefault(); moveFocus(1, 0); break;
      case 'ArrowLeft': case 'h': e.preventDefault(); moveFocus(-1, 0); break;
      case 'Enter': {
        const t = focusedTask();
        if (t) { e.preventDefault(); setSelectedTask(t); }
        break;
      }
      case 'Escape':
        if (selectedTask()) { e.preventDefault(); setSelectedTask(null); }
        else if (focusedId()) { e.preventDefault(); setFocusedId(null); }
        break;
      case 's': case 'S': {
        const t = focusedTask();
        if (t && canStartTask(t)) { e.preventDefault(); void plan.startTaskById(t.id); }
        break;
      }
      case 'r': case 'R': {
        const t = focusedTask();
        if (t && t.status === 'failed') { e.preventDefault(); void plan.retryTaskById(t.id); }
        break;
      }
    }
  };

  onMount(() => window.addEventListener('keydown', onKeyDown));
  onCleanup(() => window.removeEventListener('keydown', onKeyDown));

  if (plan.planMissing()) {
    return (
      <div class="flex h-screen w-full">
        <NotFoundPanel
          title="Plan not found"
          message="This plan no longer exists, so it has no tasks to show."
          actionLabel="Back to plans"
          actionHref="/plan"
        />
      </div>
    );
  }

  return (
    <div class="flex flex-col h-screen w-full min-w-0 bg-[color:var(--bg-base)]">

      {/* ── header ── */}
      <header
        class="h-13 shrink-0 border-b border-[color:var(--border-subtle)] flex items-center px-4 gap-3"
        style={{ background: 'linear-gradient(var(--tint),var(--tint)) rgba(17,17,20,0.9)', 'z-index': 100 }}
      >
        <Breadcrumb items={breadcrumbs()} />
        <div class="flex-1" />

        {/* Command palette trigger */}
        <button
          type="button"
          onClick={() => setMenuOpen(true)}
          class="hidden sm:flex items-center gap-2 h-8 pl-2.5 pr-1.5 rounded-lg border text-[12px] transition-colors"
          style={{ 'border-color': 'var(--border-subtle)', color: 'var(--text-tertiary)', background: 'var(--bg-elevated)' }}
          title="Command menu"
        >
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 11a6 6 0 11-12 0 6 6 0 0112 0z" />
          </svg>
          <kbd class="text-[10px] font-mono px-1.5 py-0.5 rounded border" style={{ 'border-color': 'var(--border-default)' }}>⌘K</kbd>
        </button>

        {/* Progress pill */}
        <Show when={counts().total > 0}>
          <div class="hidden sm:flex items-center gap-2.5 px-3 py-1.5 rounded-lg bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)]">
            <div class="w-20 h-1.5 rounded-full bg-zinc-800 overflow-hidden">
              <div
                class="h-full rounded-full transition-all duration-500"
                style={{ width: `${progress()}%`, background: 'var(--status-completed)' }}
              />
            </div>
            <span class="text-[11px] text-zinc-300 font-medium tabular-nums">
              {counts().done}/{counts().total}
            </span>
            <Show when={counts().running > 0}>
              <span class="text-[10px] font-medium flex items-center gap-1" style={{ color: 'var(--status-progress)' }}>
                <StatusIcon status="in_progress" size={11} />
                {counts().running}
              </span>
            </Show>
          </div>
        </Show>

        {/* Start All */}
        <Show when={planLocked() && eligibleCount() > 0}>
          <button
            type="button"
            onClick={handleStartAll}
            disabled={startingAll()}
            class="h-8 px-3.5 rounded-lg bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)]
                   text-[color:var(--on-primary)] text-[12px] font-semibold flex items-center gap-1.5
                   transition shadow-sm disabled:opacity-60"
          >
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 3l14 9-14 9V3z" />
            </svg>
            {startingAll() ? 'Starting…' : `Start ${eligibleCount()}`}
          </button>
        </Show>
      </header>

      {/* ── error banner ── */}
      <Show when={startError()}>
        <div class="px-4 py-2.5 flex items-start gap-2 text-[12px] text-red-300 bg-red-500/10 border-b border-red-500/20 shrink-0">
          <svg class="w-4 h-4 text-red-400 shrink-0 mt-px" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <div>
            <p class="font-semibold mb-0.5">Failed to start some tasks:</p>
            <p class="whitespace-pre-wrap text-red-300/80">{startError()}</p>
          </div>
          <button
            type="button"
            onClick={() => setStartError('')}
            class="ml-auto w-5 h-5 rounded text-red-400 hover:text-red-300 flex items-center justify-center shrink-0"
          >
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
      </Show>

      {/* ── breakdown banner ── */}
      <Show when={plan.activePlan()?.breakdownStatus === 'in_progress'}>
        <div class="px-4 py-2.5 flex items-center gap-2 text-[12px] text-amber-300 bg-amber-400/8 border-b border-amber-400/20 shrink-0">
          <svg class="w-3.5 h-3.5 animate-spin shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v4m0 8v4m8-8h-4M8 12H4m13.657-5.657l-2.829 2.829M9.172 14.828l-2.829 2.829m11.314 0l-2.829-2.829M9.172 9.172L6.343 6.343" />
          </svg>
          AI is analysing the plan and generating tasks…
        </div>
      </Show>

      {/* ── kanban ── */}
      <div class="flex-1 overflow-x-auto overflow-y-hidden">
        <div class="flex h-full gap-5 px-5 py-4 w-full">
          <For each={COLUMNS}>
            {(col) => {
              const colTasks = () => byStatus(col.status);
              return (
                <div class="flex flex-col gap-2 flex-1 min-w-[280px]">
                  {/* Column header — icon, name, plain muted count (Linear style) */}
                  <div class="flex items-center gap-2 h-7 px-0.5 shrink-0">
                    <StatusIcon status={col.status} size={14} />
                    <span class="text-[13px] font-medium" style={{ color: 'var(--text-secondary)' }}>{col.label}</span>
                    <span class="text-[12px] tabular-nums" style={{ color: 'var(--text-muted)' }}>{colTasks().length}</span>
                  </div>

                  {/* Open card list — no surrounding box (Linear style) */}
                  <div class="flex-1 overflow-y-auto flex flex-col gap-2 pb-4 pr-1 -mr-1">
                    <For each={colTasks()}>
                      {(task) => (
                        <KanbanCard
                          task={task}
                          focused={focusedId() === task.id}
                          onSelect={(t) => { setFocusedId(t.id); setSelectedTask(t); }}
                        />
                      )}
                    </For>
                    <Show when={colTasks().length === 0}>
                      <div class="px-2 py-8 text-center text-[11px]" style={{ color: 'var(--text-muted)' }}>No tasks</div>
                    </Show>
                  </div>
                </div>
              );
            }}
          </For>
        </div>
      </div>

      {/* ── task detail drawer ── */}
      <TaskDrawer
        task={selectedTask()}
        onClose={() => setSelectedTask(null)}
      />

      {/* ── command palette ── */}
      <CommandMenu
        open={menuOpen()}
        onClose={() => setMenuOpen(false)}
        items={commands()}
        placeholder={menuTask() ? `Actions for “${menuTask()!.title}”…` : 'Type a command…'}
      />
    </div>
  );
}
