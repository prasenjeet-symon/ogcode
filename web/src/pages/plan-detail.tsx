import { useParams, useNavigate, useLocation } from '@solidjs/router';
import { usePlan } from '../context/plan';
import { useServer } from '../context/server';
import { downloadPlanExport } from '../api/client';
import { createEffect, on, Show, createSignal } from 'solid-js';
import PlanSidebar from '../components/plan-sidebar';
import PlanMessageList from '../components/plan-message-list';
import PlanPromptInput from '../components/plan-prompt-input';
import TaskBoard from '../components/task-board';
import Breadcrumb from '../components/breadcrumb';
import NotificationBell from '../components/notification-bell';

function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}

function getModelLabel(model: string | undefined): string {
  if (!model) return '';
  const parts = model.split('/');
  const name = parts[parts.length - 1];
  return name.replace(/-\d{4}-\d{2}-\d{2}$/, '').replace(/-preview$/, '');
}

export default function PlanDetail() {
  return <PlanDetailContent />;
}

function PlanDetailContent() {
  const plan = usePlan();
  const server = useServer();
  const params = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const [activeTab, setActiveTab] = createSignal<'conversation' | 'tasks'>('conversation');

  createEffect(on(() => params.id, (id) => {
    if (id) {
      plan.selectPlan(id);
    }
  }));

  // Auto-switch to tasks tab when plan is locked
  createEffect(() => {
    const p = plan.activePlan();
    if (p?.status === 'locked') {
      setActiveTab('tasks');
    }
  });

  const breadcrumbs = () => {
    const p = plan.activePlan();
    return [
      { label: 'Plans', href: '/plan' },
      { label: p?.title || 'Untitled plan' },
    ];
  };

  return (
    <div class="flex h-screen w-full">
      <PlanSidebar />

      <div class="flex-1 flex flex-col min-w-0 bg-[color:var(--bg-base)]">
        {/* Header */}
        <header class="h-12 shrink-0 border-b border-[color:var(--border-subtle)] flex items-center px-4 backdrop-blur-sm" style={{ background: 'linear-gradient(var(--tint), var(--tint)) rgba(17,17,20,0.8)', 'z-index': 100 }}>
          <div class="flex items-center gap-2 min-w-0 flex-1">
            <Breadcrumb items={breadcrumbs()} />
            <Show when={plan.activePlan()?.status === 'locked'}>
              <span class="flex items-center gap-1 text-[11px] text-amber-400 font-medium">
                <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                </svg>
                Locked
              </span>
            </Show>
            <Show when={plan.loading()}>
              <span class="flex items-center gap-1 text-[11px] text-[color:var(--accent)] ml-1">
                <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] animate-pulse" />
                generating
              </span>
            </Show>
          </div>

          <div class="flex items-center gap-2 shrink-0">
            <Show when={plan.activePlan()?.model}>
              <span class="text-[11px] text-zinc-400 bg-[color:var(--bg-elevated)] px-2 py-1 rounded-md border border-[color:var(--border-subtle)] font-medium">
                {getModelLabel(plan.activePlan()?.model)}
              </span>
            </Show>

            <Show when={server.memoryEnabled()}>
              <span
                title={(() => {
                  const t = plan.memorySavedTokens();
                  if (t > 0) return `Memory is saving ~${formatTokens(t)} tokens vs. sending full history`;
                  if (t < 0) return `Memory is adding ~${formatTokens(-t)} tokens of overhead — savings kick in as history grows`;
                  return 'Agentic memory active';
                })()}
                class={`flex items-center gap-1.5 text-[11px] px-2 py-1 rounded-md border font-medium
                  ${plan.memorySavedTokens() < 0
                    ? 'text-amber-400 bg-amber-400/10 border-amber-400/20'
                    : 'text-emerald-400 bg-emerald-400/10 border-emerald-400/20'
                  }`}
              >
                <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.847.813a4.5 4.5 0 00-3.09 3.091z" />
                </svg>
                Memory
                <Show when={plan.memorySavedTokens() > 0}>
                  <span class="text-emerald-500/70">·</span>
                  <span class="text-emerald-300">~{formatTokens(plan.memorySavedTokens())} saved</span>
                </Show>
                <Show when={plan.memorySavedTokens() < 0}>
                  <span class="text-amber-500/70">·</span>
                  <span class="text-amber-300">~{formatTokens(-plan.memorySavedTokens())} overhead</span>
                </Show>
              </span>
            </Show>

            {/* Tab toggle for narrow screens */}
            <div class="flex rounded-lg border border-[color:var(--border-default)] overflow-hidden lg:hidden">
              <button
                onClick={() => setActiveTab('conversation')}
                class={`px-2.5 py-1 text-[11px] font-medium transition ${activeTab() === 'conversation' ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]' : 'text-zinc-400 hover:text-zinc-200'}`}
              >
                Chat
              </button>
              <button
                onClick={() => setActiveTab('tasks')}
                class={`px-2.5 py-1 text-[11px] font-medium transition ${activeTab() === 'tasks' ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]' : 'text-zinc-400 hover:text-zinc-200'}`}
              >
                Tasks
              </button>
            </div>

            <button
              type="button"
              onClick={() => {
                const p = plan.activePlan();
                if (p) downloadPlanExport(p.id).catch(console.error);
              }}
              class="w-7 h-7 flex items-center justify-center rounded-md text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)] transition"
              title="Export as Markdown"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
              </svg>
            </button>
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
            <NotificationBell />
          </div>
        </header>

        {/* All-tasks-complete notification banner */}
        <Show when={plan.archivePath()}>
          <div class="shrink-0 px-4 py-2 bg-emerald-500/10 border-b border-emerald-500/20 flex items-center gap-2">
            <svg class="w-4 h-4 text-emerald-400 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
            <span class="text-[12px] text-emerald-300 flex-1 min-w-0">
              <strong>All tasks completed.</strong>
              {' '}Plan archived to{' '}
              <code class="font-mono text-[11px] bg-emerald-900/30 px-1 rounded break-all">{plan.archivePath()}</code>
            </span>
            <button
              type="button"
              onClick={() => plan.dismissArchiveNotification()}
              class="shrink-0 w-5 h-5 flex items-center justify-center rounded text-emerald-500 hover:text-emerald-300 hover:bg-emerald-500/10 transition"
              title="Dismiss"
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </Show>

        {/* Breakdown warning / failure reason banner */}
        <Show when={plan.activePlan()?.breakdownWarnings}>
          {(msg) => {
            const isFailed = () => plan.activePlan()?.breakdownStatus === 'failed';
            return (
              <div class={`shrink-0 px-4 py-2 border-b flex items-center gap-2 ${isFailed() ? 'bg-red-500/10 border-red-500/20' : 'bg-amber-500/10 border-amber-500/20'}`}>
                <svg class={`w-4 h-4 shrink-0 ${isFailed() ? 'text-red-400' : 'text-amber-400'}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
                </svg>
                <span class={`text-[12px] ${isFailed() ? 'text-red-300' : 'text-amber-300'}`}>
                  <strong>{isFailed() ? 'Breakdown failed:' : 'Breakdown warning:'}</strong> {msg()}
                </span>
              </div>
            );
          }}
        </Show>

        {/* Content: two-panel on lg, tabbed on mobile */}
        <div class="flex-1 flex min-h-0">
          {/* Conversation panel */}
          <div class={`flex-1 flex flex-col min-w-0 ${activeTab() !== 'conversation' ? 'hidden lg:flex' : 'flex'}`}>
            <PlanMessageList />
            <PlanPromptInput />
          </div>

          {/* Task board panel */}
          <div class={`w-full lg:w-[45%] lg:min-w-[380px] lg:max-w-[560px] lg:border-l lg:border-[color:var(--border-subtle)] ${activeTab() !== 'tasks' ? 'hidden lg:flex' : 'flex'} flex-col`}>
            <TaskBoard />
          </div>
        </div>
      </div>
    </div>
  );
}