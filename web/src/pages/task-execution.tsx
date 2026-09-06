import { useParams, useNavigate, useLocation } from '@solidjs/router';
import { useSession } from '../context/session';
import { usePlan } from '../context/plan';
import { useServer } from '../context/server';
import { createEffect, on, createSignal, Show, onCleanup } from 'solid-js';
import MessageList from '../components/message-list';
import PromptInput from '../components/prompt-input';
import Breadcrumb from '../components/breadcrumb';
import { DrawerToggle } from '../components/sidebar-shell';
import { getTask, isNotFoundError } from '../api/client';
import { NotFoundPanel } from './not-found';

function getModelLabel(model: string | undefined): string {
  if (!model) return '';
  const parts = model.split('/');
  const name = parts[parts.length - 1];
  return name.replace(/-\d{4}-\d{2}-\d{2}$/, '').replace(/-preview$/, '');
}

const STATUS_STYLES: Record<string, string> = {
  pending: 'bg-zinc-500/15 text-zinc-400 border-zinc-500/20',
  in_progress: 'bg-blue-500/15 text-blue-400 border-blue-500/20',
  completed: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/20',
  failed: 'bg-red-500/15 text-red-400 border-red-500/20',
};

const EFFORT_COLORS: Record<string, string> = {
  S: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/20',
  M: 'bg-blue-500/15 text-blue-400 border-blue-500/20',
  L: 'bg-amber-500/15 text-amber-400 border-amber-500/20',
  XL: 'bg-red-500/15 text-red-400 border-red-500/20',
};

export default function TaskExecution() {
  return <TaskExecutionContent />;
}

function TaskExecutionContent() {
  const session = useSession();
  const plan = usePlan();
  const server = useServer();
  const params = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const [taskData, setTaskData] = createSignal<any>(null);
  const [loading, setLoading] = createSignal(true);
  const [notFound, setNotFound] = createSignal(false);

  // Merge incoming task data, preferring newer timestamps and preserving
  // non-empty values when the incoming data has empty/missing values.
  const mergeTaskData = (incoming: any) => {
    setTaskData((prev) => {
      if (!prev) return incoming;
      // Keep the version with the newer updatedAt
      if ((incoming.updatedAt || 0) < (prev.updatedAt || 0)) return prev;
      // Merge: incoming overwrites prev, but preserve non-empty values
      return { ...prev, ...incoming };
    });
  };

  // Sync taskData with plan context (reactive to SSE updates)
  createEffect(() => {
    const id = params.id;
    if (!id) return;
    const fromContext = plan.tasks().find((t) => t.id === id);
    if (fromContext) {
      mergeTaskData(fromContext);
      if (fromContext.sessionId) {
        session.selectSession(fromContext.sessionId);
      }
    }
  });

  // Also handle SSE task events directly (in case plan context isn't loaded)
  createEffect(() => {
    const id = params.id;
    if (!id) return;
    // Access eventTick to make this reactive
    server.eventTick();
    const last = server.lastEvent();
    if (!last) return;

    const taskEventTypes = ['task.started', 'task.updated', 'task.completed', 'task.failed'];
    if (!taskEventTypes.includes(last.type)) return;

    const updated = last.properties;
    if (updated?.id !== id) return;

    mergeTaskData(updated);
    if (updated.sessionId) {
      session.selectSession(updated.sessionId);
    }
  });

  // Initial load: find from plan context or fetch from API.
  // Deliberately not deferred — this has to run on mount, which is the common
  // case (deep link, refresh, or clicking through from the task board).
  // Deferring it meant the first fetch only happened on the 3s poll tick below,
  // so every task page opened with a needless multi-second spinner.
  createEffect(on(() => params.id, async (id) => {
    if (!id) return;
    setNotFound(false);
    const fromContext = plan.tasks().find((t) => t.id === id);
    if (fromContext) {
      mergeTaskData(fromContext);
      setLoading(false);
      if (fromContext.sessionId) {
        session.selectSession(fromContext.sessionId);
      }
    } else {
      try {
        const t = await getTask(id);
        mergeTaskData(t);
        setLoading(false);
        if (t?.sessionId) {
          session.selectSession(t.sessionId);
        }
      } catch (e) {
        console.error('fetch task failed:', e);
        setLoading(false);
        // Only a genuinely missing task counts as not-found; a transient network
        // error should keep the spinner up so the poll below can recover.
        if (isNotFoundError(e)) setNotFound(true);
      }
    }
  }));

  // Poll for task updates when: no sessionId yet, or task is in a non-terminal state
  createEffect(() => {
    const id = params.id;
    if (!id) return;

    // A task we know doesn't exist will never appear — polling it just 404s
    // every 3 seconds forever.
    if (notFound()) return;

    const task = taskData();
    const needsPoll = !task?.sessionId || (task.status !== 'completed' && task.status !== 'failed');

    if (!needsPoll) return;

    const interval = setInterval(async () => {
      try {
        const t = await getTask(id);
        mergeTaskData(t);
        if (t?.sessionId) {
          session.selectSession(t.sessionId);
        }
      } catch (e) {
        // ignore poll errors
      }
    }, 3000);

    onCleanup(() => clearInterval(interval));
  });

  const breadcrumbs = () => {
    const task = taskData();
    const active = plan.activePlan();
    const items: Array<{ label: string; href?: string }> = [
      { label: 'Plans', href: '/plan' },
    ];
    // Prefer the task's own planId over the plan context: this page never calls
    // selectPlan, so activePlan is null on a deep link or refresh and the link
    // back to the parent plan would otherwise vanish exactly when it's needed.
    const planId = task?.planId ?? active?.id;
    if (planId) {
      const title = active && active.id === planId ? active.title : undefined;
      items.push({ label: title || 'Plan', href: `/plan/${planId}` });
    }
    if (task) {
      items.push({ label: task.title || 'Task' });
    }
    return items;
  };

  return (
    <div class="flex h-dvh w-full">
      <div class="flex-1 flex flex-col min-w-0 bg-[color:var(--bg-base)]">
        {/* Header */}
        <header class="h-12 shrink-0 border-b border-[color:var(--border-subtle)] flex items-center px-2 sm:px-4 backdrop-blur-sm" style={{ background: 'linear-gradient(var(--tint), var(--tint)) rgba(17,17,20,0.8)', 'z-index': 100, [ 'padding-top']: 'env(safe-area-inset-top)' }}>
          <DrawerToggle drawer="plans" label="Open plans" />
          <div class="flex items-center gap-2 min-w-0 flex-1">
            <Breadcrumb items={breadcrumbs()} />

            <Show when={taskData()}>
              <span class={`text-[10px] font-medium px-1.5 py-0.5 rounded border shrink-0 ${STATUS_STYLES[taskData()?.status] || STATUS_STYLES.pending}`}>
                {taskData()?.status?.replace('_', ' ')}
              </span>
              <span class={`hidden sm:inline-flex text-[10px] font-medium px-1.5 py-0.5 rounded border ${EFFORT_COLORS[taskData()?.effort] || EFFORT_COLORS.M}`}>
                {taskData()?.effort}
              </span>
            </Show>

            <Show when={session.loading() || session.hasRunningTools()}>
              <span class="flex items-center gap-1 text-[11px] text-[color:var(--accent)] ml-1 shrink-0">
                <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] animate-pulse" />
                <span class="hidden sm:inline">{session.hasRunningTools() ? 'running tools' : 'generating'}</span>
              </span>
            </Show>
          </div>

          <div class="flex items-center gap-2 shrink-0">
            <Show when={session.activeSession()?.model}>
              <span class="hidden sm:inline-block text-[11px] text-zinc-400 bg-[color:var(--bg-elevated)] px-2 py-1 rounded-md border border-[color:var(--border-subtle)] font-medium max-w-[9rem] truncate">
                {getModelLabel(session.activeSession()?.model)}
              </span>
            </Show>
            <Show when={taskData()?.branchName}>
              <span
                class="hidden sm:flex items-center gap-1.5 text-[11px] text-zinc-500 px-2 py-1 rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] font-mono"
              >
                <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 3v12" />
                  <path stroke-linecap="round" stroke-linejoin="round" d="M18 9a3 3 0 100 6 3 3 0 000-6z" />
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 21a3 3 0 100-6 3 3 0 000 6z" />
                </svg>
                {taskData()?.branchName}
              </span>
            </Show>
            <button
              type="button"
              onClick={() => navigate('/settings', { state: { from: location.pathname } })}
              class="w-7 h-7 flex items-center justify-center rounded-md text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)] transition"
              title="Settings"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065zM15 12a3 3 0 11-6 0 3 3 0 016 0z" />
              </svg>
            </button>
          </div>
        </header>

        {/* Main content */}
        <Show
          when={!notFound()}
          fallback={
            <NotFoundPanel
              title="Task not found"
              message="This task no longer exists. It may have been deleted along with its plan."
              actionLabel="Back to plans"
              actionHref="/plan"
            />
          }
        >
        <Show
          when={taskData()?.sessionId}
          fallback={
            <div class="flex-1 flex items-center justify-center">
              <div class="flex flex-col items-center gap-3">
                <svg class="w-6 h-6 text-[color:var(--accent)] animate-spin" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v4m0 8v4m8-8h-4M8 12H4m13.657-5.657l-2.829 2.829M9.172 14.828l-2.829 2.829m11.314 0l-2.829-2.829M9.172 9.172L6.343 6.343" />
                </svg>
                <p class="text-[13px] text-zinc-400">
                  {taskData()?.status === 'pending' ? 'Waiting to start…' : 'Starting task…'}
                </p>
              </div>
            </div>
          }
        >
          <MessageList />
          <PromptInput />
        </Show>
        </Show>
      </div>
    </div>
  );
}