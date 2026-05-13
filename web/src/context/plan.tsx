import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal, createEffect, on } from 'solid-js';
import {
  type Plan,
  type Task,
  type MessageWithParts,
  listPlans,
  createPlan,
  getPlan,
  updatePlan,
  lockPlan as lockPlanAPI,
  sendPlanPrompt,
  getPlanMessages,
  abortPlan as abortPlanAPI,
  listTasks,
  createTasks,
  startTask,
  completeTask,
  failTask,
  retryTask,
  getTask,
  getModels,
  getSession,
  deletePlan as deletePlanAPI,
} from '../api/client';
import { useServer } from './server';

interface PlanContextValue {
  plans: () => Plan[];
  activePlan: () => Plan | null;
  memorySavedTokens: () => number;
  tasks: () => Task[];
  messages: () => MessageWithParts[];
  loading: () => boolean;
  models: () => any[];
  selectedModel: () => string;
  archivePath: () => string;
  dismissArchiveNotification: () => void;
  selectModel: (modelId: string) => void;
  selectPlan: (id: string) => Promise<void>;
  newPlan: (title?: string, model?: string) => Promise<Plan>;
  sendPrompt: (content: string) => Promise<void>;
  abort: () => Promise<void>;
  lockPlan: () => Promise<void>;
  refresh: () => void;
  createTasksFromBreakdown: (tasks: Array<{
    title: string;
    description?: string;
    effort?: string;
    complexity?: string;
    dependencies?: string[];
    orderIndex?: number;
  }>) => Promise<Task[]>;
  startTaskById: (id: string) => Promise<void>;
  completeTaskById: (id: string) => Promise<void>;
  failTaskById: (id: string) => Promise<void>;
  retryTaskById: (id: string) => Promise<void>;
  startAllTasks: () => Promise<void>;
  deletePlan: (id: string) => Promise<void>;
}

const PlanContext = createContext<PlanContextValue>();

export const PlanProvider: ParentComponent = (props) => {
  const server = useServer();
  const [plans, setPlans] = createSignal<Plan[]>([]);
  const [activePlan, setActivePlan] = createSignal<Plan | null>(null);
  const [tasks, setTasks] = createSignal<Task[]>([]);
  const [messagesRaw, setMessagesRaw] = createSignal<MessageWithParts[]>([]);
  const messages = messagesRaw;

  const setMessages = (next: MessageWithParts[] | ((prev: MessageWithParts[]) => MessageWithParts[])) => {
    if (typeof next === 'function') {
      setMessagesRaw(next as (prev: MessageWithParts[]) => MessageWithParts[]);
    } else {
      setMessagesRaw(next);
    }
  };

  const [loadingPlanId, setLoadingPlanId] = createSignal<string>('');
  const loading = () => loadingPlanId() === activePlan()?.id && loadingPlanId() !== '';
  const [memorySavedTokens, setMemorySavedTokens] = createSignal(0);

  // Archive notification: set by the plan.archived SSE event, cleared on plan switch or dismiss.
  const [archivePath, setArchivePath] = createSignal<string>('');
  const dismissArchiveNotification = () => setArchivePath('');

  const [models, setModels] = createSignal<any[]>([]);
  const [pendingModel, setPendingModel] = createSignal<string>('');

  const selectedModel = (): string => {
    if (pendingModel()) return pendingModel();
    const plan = activePlan();
    if (plan?.model) return plan.model;
    const enabled = models().filter((m: any) => m.enabled);
    const defaults = enabled.filter((m: any) => m.default);
    if (defaults.length > 0) return defaults[0].id;
    if (enabled.length > 0) return enabled[0].id;
    return '';
  };

  async function selectModel(modelId: string) {
    setPendingModel(modelId);
    const plan = activePlan();
    if (!plan) return;
    try {
      const updated = await updatePlan(plan.id, { model: modelId });
      setActivePlan(updated);
    } catch (e) {
      console.error('update plan model failed:', e);
    }
  }

  // Polling
  let fastPollInterval: ReturnType<typeof setInterval> | null = null;
  let bgPollInterval: ReturnType<typeof setInterval> | null = null;
  let taskPollInterval: ReturnType<typeof setInterval> | null = null;
  let titlePollInterval: ReturnType<typeof setInterval> | null = null;
  let lastSSEUpdate = 0;

  function stopFastPoll() {
    if (fastPollInterval) {
      clearInterval(fastPollInterval);
      fastPollInterval = null;
    }
  }

  function stopBgPoll() {
    if (bgPollInterval) {
      clearInterval(bgPollInterval);
      bgPollInterval = null;
    }
  }

  function stopTaskPoll() {
    if (taskPollInterval) {
      clearInterval(taskPollInterval);
      taskPollInterval = null;
    }
  }

  function stopTitlePoll() {
    if (titlePollInterval) {
      clearInterval(titlePollInterval);
      titlePollInterval = null;
    }
  }

  function stopPolling() {
    stopFastPoll();
    stopBgPoll();
    stopTaskPoll();
    stopTitlePoll();
  }

  // Poll plan metadata until a title appears (autoNamePlan runs async on the
  // backend and may finish before or after loop.done — SSE alone is not enough).
  function startTitlePoll(planId: string) {
    stopTitlePoll();
    const deadline = Date.now() + 60_000; // give up after 60 s
    titlePollInterval = setInterval(async () => {
      const p = activePlan();
      if (!p || p.id !== planId || p.title) {
        stopTitlePoll();
        return;
      }
      if (Date.now() > deadline) {
        stopTitlePoll();
        return;
      }
      try {
        const fresh = await getPlan(planId);
        if (!fresh?.title) return;
        if (activePlan()?.id !== planId) return;
        setActivePlan((prev) => prev ? { ...prev, title: fresh.title } : prev);
        setPlans((prev) => prev.map((q) =>
          q.id === planId ? { ...q, title: fresh.title } : q
        ));
        stopTitlePoll();
      } catch (_e) { /* ignore */ }
    }, 3_000);
  }

  function startBgPoll(planId: string) {
    stopBgPoll();
    bgPollInterval = setInterval(async () => {
      const plan = activePlan();
      if (!plan || plan.id !== planId) {
        stopBgPoll();
        return;
      }
      try {
        const msgs = await getPlanMessages(planId);
        if (activePlan()?.id !== planId) return;
        setMessages(msgs);
      } catch (_e) {
        // background — ignore
      }
    }, 15_000);
  }

  function isAgentLoopActive(msgs: MessageWithParts[]): boolean {
    if (msgs.length > 0) {
      const last = msgs[msgs.length - 1];
      if (last.info.role === 'user') {
        const hasText = (last.parts || []).some((p) => p.type === 'text');
        if (hasText) return true;
      }
    }
    for (let i = msgs.length - 1; i >= 0; i--) {
      if (msgs[i].info.role === 'assistant') {
        if (!msgs[i].info.finish && !msgs[i].info.error) return true;
        if (msgs[i].info.finish === 'tool_calls') return true;
        return false;
      }
    }
    return false;
  }

  function startPolling(planId: string) {
    stopFastPoll();
    const startedAt = Date.now();
    const MAX_WAIT_MS = 2 * 60 * 1000;
    fastPollInterval = setInterval(async () => {
      try {
        const plan = activePlan();
        if (!plan || plan.id !== planId) {
          stopFastPoll();
          return;
        }
        if (Date.now() - lastSSEUpdate < 2000) return;

        const msgs = await getPlanMessages(planId);
        setMessages(msgs);

        if (!isAgentLoopActive(msgs)) {
          setLoadingPlanId('');
          stopFastPoll();
        } else if (Date.now() - startedAt > MAX_WAIT_MS) {
          console.warn('poll timeout, clearing loading state');
          setLoadingPlanId('');
          stopFastPoll();
        } else {
          if (loadingPlanId() !== planId) {
            setLoadingPlanId(planId);
          }
        }
      } catch (e) {
        console.error('poll plan messages failed:', e);
      }
    }, 3000);
  }

  function startTaskPoll(planId: string) {
    stopTaskPoll();
    taskPollInterval = setInterval(async () => {
      const plan = activePlan();
      if (!plan || plan.id !== planId) {
        stopTaskPoll();
        return;
      }
      if (plan.status !== 'locked') return;
      try {
        const t = await listTasks(planId);
        if (activePlan()?.id !== planId) return;
        setTasks(t || []);
      } catch (_e) {
        // background — ignore
      }
    }, 10_000);
  }

  async function selectPlan(id: string) {
    if (sseRefreshDebounce) {
      clearTimeout(sseRefreshDebounce);
      sseRefreshDebounce = null;
    }

    let plan = plans().find((p) => p.id === id);
    if (!plan) {
      plan = activePlan()?.id === id ? activePlan()! : undefined;
    }
    if (plan) setActivePlan(plan);

    setPendingModel('');
    setArchivePath('');
    stopPolling();
    stopTitlePoll();
    setMessages([]);

    try {
      const p = await getPlan(id);
      setActivePlan(p);
      const msgs = await getPlanMessages(id);
      setMessages(msgs);

      // Restore persisted token savings for this plan's session.
      if (p.sessionId) {
        getSession(p.sessionId)
          .then((sess) => setMemorySavedTokens(sess.memoryTokensSaved ?? 0))
          .catch(() => setMemorySavedTokens(0));
      } else {
        setMemorySavedTokens(0);
      }

      // Load tasks for the plan
      const t = await listTasks(id);
      setTasks(t || []);

      startBgPoll(id);
      if (p.status === 'locked') {
        startTaskPoll(id);
      }
      if (isAgentLoopActive(msgs)) {
        setLoadingPlanId(id);
        startPolling(id);
      }
    } catch (e) {
      console.error('load plan failed:', e);
    }
  }

  async function newPlan(title?: string, model?: string): Promise<Plan> {
    const plan = await createPlan(server.directory(), title, model || selectedModel());
    setPlans((prev) => prev.find((p) => p.id === plan.id) ? prev : [plan, ...prev]);
    setActivePlan(plan);
    setMessages([]);
    setTasks([]);
    return plan;
  }

  async function refresh() {
    const dir = server.directory();
    if (!dir) return;
    try {
      const list = await listPlans(dir);
      setPlans(list);
    } catch (e) {
      console.error('refresh plans failed:', e);
    }
  }

  async function abort() {
    const plan = activePlan();
    if (!plan) return;

    stopFastPoll();
    setLoadingPlanId('');

    try {
      await abortPlanAPI(plan.id);
    } catch (e) {
      console.error('abort plan failed:', e);
    }

    try {
      const msgs = await getPlanMessages(plan.id);
      setMessages(msgs);
    } catch (e) {
      console.error('refresh after abort failed:', e);
    }
  }

  async function lockPlan() {
    const plan = activePlan();
    if (!plan) return;
    setLoadingPlanId(plan.id);
    try {
      const updated = await lockPlanAPI(plan.id);
      setActivePlan(updated);
      stopFastPoll();
      setLoadingPlanId('');
    } catch (e) {
      setLoadingPlanId('');
      console.error('lock plan failed:', e);
    }
  }

  async function createTasksFromBreakdown(taskList: Array<{
    title: string;
    description?: string;
    effort?: string;
    complexity?: string;
    dependencies?: string[];
    orderIndex?: number;
  }>) {
    const plan = activePlan();
    if (!plan) return [];
    try {
      const created = await createTasks(plan.id, taskList);
      setTasks((prev) => {
        const existing = new Map(prev.map((t) => [t.id, t]));
        for (const t of created) {
          existing.set(t.id, t);
        }
        return [...existing.values()];
      });
      return created;
    } catch (e) {
      console.error('create tasks failed:', e);
      return [];
    }
  }

  async function startTaskById(id: string) {
    try {
      const updated = await startTask(id);
      setTasks((prev) => prev.map((t) => (t.id === id ? updated : t)));
    } catch (e) {
      console.error('start task failed:', e);
    }
  }

  async function completeTaskById(id: string) {
    try {
      const updated = await completeTask(id);
      setTasks((prev) => prev.map((t) => (t.id === id ? updated : t)));
    } catch (e) {
      console.error('complete task failed:', e);
    }
  }

  async function failTaskById(id: string) {
    try {
      const updated = await failTask(id);
      setTasks((prev) => prev.map((t) => (t.id === id ? updated : t)));
    } catch (e) {
      console.error('fail task failed:', e);
    }
  }

  async function retryTaskById(id: string) {
    try {
      const updated = await retryTask(id);
      setTasks((prev) => prev.map((t) => (t.id === id ? updated : t)));
    } catch (e) {
      console.error('retry task failed:', e);
    }
  }

  async function startAllTasks() {
    const completedIds = new Set(
      tasks().filter((t) => t.status === 'completed').map((t) => t.id)
    );
    const eligible = tasks().filter(
      (t) => t.status === 'pending' && t.dependencies.every((d) => completedIds.has(d))
    );
    if (eligible.length === 0) return;
    const errors: string[] = [];
    for (const t of eligible) {
      try {
        const updated = await startTask(t.id);
        setTasks((prev) => prev.map((x) => (x.id === t.id ? updated : x)));
      } catch (e: any) {
        const msg = e?.message || String(e);
        // "already started" is not an error — auto-start may have picked it up first
        if (msg.includes('already started')) {
          const fresh = await getTask(t.id).catch(() => null);
          if (fresh) {
            setTasks((prev) => prev.map((x) => (x.id === t.id ? { ...x, ...fresh } : x)));
          }
          continue;
        }
        console.error('start task failed:', t.id, e);
        errors.push(`"${t.title}": ${msg}`);
      }
    }
    if (errors.length > 0) {
      throw new Error(errors.join('\n'));
    }
  }

  async function deletePlan(id: string) {
    try {
      await deletePlanAPI(id);
      setPlans((prev) => prev.filter((p) => p.id !== id));
      if (activePlan()?.id === id) {
        setActivePlan(null);
        setMessages([]);
        setTasks([]);
      }
    } catch (e) {
      console.error('delete plan failed:', e);
    }
  }

  // Load models on mount
  getModels()
    .then((list) => setModels(list || []))
    .catch((e) => console.error('load models failed:', e));

  // Load plans on mount
  createEffect(on(server.directory, (dir) => {
    if (dir) refresh();
  }));

  // Track memory token savings for the active plan session
  createEffect(on(server.eventTick, () => {
    const last = server.lastEvent();
    if (!last || last.type !== 'memory.savings') return;
    const evtSessionId = (last.properties as any)?.sessionId;
    const plan = activePlan();
    if (!evtSessionId || !plan || evtSessionId !== plan.sessionId) return;
    const saved = Number((last.properties as any)?.savedTokens ?? 0);
    setMemorySavedTokens((prev) => prev + saved);
  }));

  // SSE-driven updates for plan conversations
  let sseRefreshDebounce: ReturnType<typeof setTimeout> | null = null;
  createEffect(on([server.eventTick, activePlan], ([_tick, plan]) => {
    if (sseRefreshDebounce) {
      clearTimeout(sseRefreshDebounce);
      sseRefreshDebounce = null;
    }
    if (!plan) return;
    const last = server.lastEvent();
    if (!last) return;

    // Handle loop.done for the plan's session
    if (last.type === 'loop.done') {
      const evtSessionId = last.properties?.sessionId;
      if (evtSessionId && evtSessionId === plan.sessionId) {
        getPlanMessages(plan.id).then((msgs) => {
          if (activePlan()?.id !== plan.id) return;
          setMessages(msgs);
          lastSSEUpdate = Date.now();
          setLoadingPlanId('');
          stopFastPoll();
        }).catch(() => {
          setLoadingPlanId('');
          stopFastPoll();
        });
      }
      return;
    }

    // Handle plan events
    if (last.type === 'plan.updated' || last.type === 'plan.locked') {
      const updated = last.properties as Plan | undefined;
      if (updated?.id === plan.id) {
        setActivePlan((prev) => {
          if (!prev) return prev;
          if (prev.status === updated.status && prev.breakdownStatus === updated.breakdownStatus &&
              prev.title === updated.title && prev.model === updated.model) {
            return prev;
          }
          return { ...prev, ...updated };
        });
      }
      // Note: setPlans for plan.updated is handled by the standalone effect below
      // so it runs even when activePlan is null (e.g. user navigated back to list).
      if (updated?.status === 'locked') {
        startTaskPoll(plan.id);
      }
      return;
    }

    if (last.type === 'plan.deleted') {
      const deletedId = last.properties?.id;
      if (deletedId) {
        setPlans((prev) => prev.filter((p) => p.id !== deletedId));
        if (activePlan()?.id === deletedId) {
          setActivePlan(null);
          setMessages([]);
          setTasks([]);
        }
      }
      return;
    }

    // Handle breakdown events
    if (last.type === 'plan.breakdown.started') {
      const evtPlanId = last.properties?.planId;
      if (evtPlanId === plan.id) {
        setActivePlan((prev) => {
          if (!prev || prev.breakdownStatus === 'in_progress') return prev;
          return { ...prev, breakdownStatus: 'in_progress' };
        });
        setPlans((prev) => prev.map((p) => (p.id === evtPlanId ? { ...p, breakdownStatus: 'in_progress' as const } : p)));
      }
      return;
    }

    if (last.type === 'plan.breakdown.completed') {
      const evtPlanId = last.properties?.planId;
      const warnings: string = last.properties?.warnings || '';
      if (evtPlanId === plan.id) {
        setActivePlan((prev) => {
          if (!prev || prev.breakdownStatus === 'completed') return prev;
          return { ...prev, breakdownStatus: 'completed', breakdownWarnings: warnings };
        });
        setPlans((prev) => prev.map((p) => (p.id === evtPlanId ? { ...p, breakdownStatus: 'completed' as const, breakdownWarnings: warnings } : p)));
        // Reload tasks for this plan
        listTasks(plan.id).then((t) => {
          if (activePlan()?.id === plan.id) setTasks(t || []);
        }).catch(() => {});
        startTaskPoll(plan.id);
      }
      return;
    }

    if (last.type === 'plan.breakdown.failed') {
      const evtPlanId = last.properties?.planId;
      const reason: string = last.properties?.reason || '';
      if (evtPlanId === plan.id) {
        setActivePlan((prev) => {
          if (!prev || prev.breakdownStatus === 'failed') return prev;
          return { ...prev, breakdownStatus: 'failed', breakdownWarnings: reason };
        });
        setPlans((prev) => prev.map((p) => (p.id === evtPlanId ? { ...p, breakdownStatus: 'failed' as const, breakdownWarnings: reason } : p)));
      }
      return;
    }

    // Handle plan archived event
    if (last.type === 'plan.archived') {
      const evtPlanId = last.properties?.planId;
      const path: string = last.properties?.path || '';
      if (evtPlanId === plan.id) {
        setActivePlan((prev) => prev ? { ...prev, allTasksCompleted: true } : prev);
        setPlans((prev) => prev.map((p) => (p.id === evtPlanId ? { ...p, allTasksCompleted: true } : p)));
        if (path) setArchivePath(path);
      }
      return;
    }

    // Handle task events
    if (last.type === 'task.updated' || last.type === 'task.started' || last.type === 'task.completed' || last.type === 'task.failed') {
      const updated = last.properties as Task | undefined;
      if (updated?.planId === plan.id) {
        setTasks((prev) => {
          const exists = prev.find((t) => t.id === updated.id);
          if (exists) {
            // Only merge if incoming data is newer or same age.
            // Prevents stale task.started from overwriting fresher task.updated data.
            if ((exists.updatedAt || 0) > (updated.updatedAt || 0)) return prev;
            return prev.map((t) => (t.id === updated.id ? { ...t, ...updated } : t));
          }
          return [...prev, updated as Task];
        });
      }
      return;
    }

    // Handle message events for the plan's session
    if (last.type !== 'message.updated' && last.type !== 'message.part.updated') return;
    const evtSessionId = last.properties?.sessionId || last.properties?.id;
    if (evtSessionId && evtSessionId !== plan.sessionId) return;

    const targetPlanId = plan.id;
    sseRefreshDebounce = setTimeout(async () => {
      if (activePlan()?.id !== targetPlanId) return;
      try {
        const msgs = await getPlanMessages(targetPlanId);
        if (activePlan()?.id !== targetPlanId) return;
        setMessages(msgs);
        lastSSEUpdate = Date.now();
      } catch (e) {
        console.error('SSE-triggered plan refresh failed:', e);
      }
    }, 150);
  }));

  // Refresh plan metadata (title, etc.) after loop.done — kept in a separate
  // effect that only tracks eventTick so that calling setActivePlan inside
  // cannot re-trigger this effect and create an infinite loop.
  createEffect(on(server.eventTick, () => {
    const last = server.lastEvent();
    if (!last || last.type !== 'loop.done') return;
    const plan = activePlan();
    if (!plan) return;
    const evtSessionId = last.properties?.sessionId;
    if (!evtSessionId || evtSessionId !== plan.sessionId) return;
    getPlan(plan.id).then((updatedPlan) => {
      if (activePlan()?.id !== plan.id) return;
      setActivePlan((prev) => {
        if (!prev) return prev;
        return { ...prev, ...updatedPlan, title: updatedPlan.title || prev.title };
      });
      setPlans((prev) => prev.map((p) => {
        if (p.id !== updatedPlan.id) return p;
        return { ...p, ...updatedPlan, title: updatedPlan.title || p.title };
      }));
    }).catch(() => {});
  }));

  // Reactively flip allTasksCompleted on the active plan the moment the live
  // tasks signal shows every task is done — no poll cycle needed.
  createEffect(on(tasks, (currentTasks) => {
    const p = activePlan();
    if (!p || p.status !== 'locked' || p.allTasksCompleted) return;
    if (currentTasks.length === 0) return;
    if (currentTasks.every((t) => t.status === 'completed')) {
      setActivePlan((prev) => prev ? { ...prev, allTasksCompleted: true } : prev);
      setPlans((prev) => prev.map((q) => (q.id === p.id ? { ...q, allTasksCompleted: true } : q)));
    }
  }));

  // Handle plan.created events
  createEffect(on(server.eventTick, () => {
    const last = server.lastEvent();
    if (!last || last.type !== 'plan.created') return;
    const created = last.properties as Plan | undefined;
    if (!created?.id) return;
    setPlans((prev) => {
      if (prev.find((p) => p.id === created.id)) return prev;
      return [created, ...prev];
    });
  }));

  // Update the plans list on plan.updated regardless of whether there is an
  // active plan — autoNamePlan finishes asynchronously and the user may have
  // navigated back to the list page by then, which would make activePlan null
  // and cause the title update to be dropped by the main SSE effect.
  createEffect(on(server.eventTick, () => {
    const last = server.lastEvent();
    if (!last || (last.type !== 'plan.updated' && last.type !== 'plan.locked')) return;
    const updated = last.properties as Plan | undefined;
    if (!updated?.id) return;
    setPlans((prev) => prev.map((p) => {
      if (p.id !== updated.id) return p;
      // Never regress a non-empty title to empty (guards against the loop.done
      // race where a DB fetch can arrive before autoNamePlan has saved).
      return { ...p, ...updated, title: updated.title || p.title };
    }));
  }));

  // On SSE reconnect, refresh active plan (messages + plan metadata + tasks)
  createEffect(on(server.connected, (isConnected) => {
    if (!isConnected) return;
    const plan = activePlan();
    if (!plan) return;
    Promise.all([
      getPlanMessages(plan.id),
      getPlan(plan.id),
      listTasks(plan.id),
    ]).then(([msgs, updatedPlan, updatedTasks]) => {
      if (activePlan()?.id !== plan.id) return;
      setMessages(msgs);
      setActivePlan((prev) => prev ? { ...prev, ...updatedPlan } : prev);
      setPlans((prev) => prev.map((p) => (p.id === updatedPlan.id ? { ...p, ...updatedPlan } : p)));
      setTasks(updatedTasks || []);
      lastSSEUpdate = Date.now();
    }).catch(() => {});
  }));

  const value: PlanContextValue = {
    plans,
    activePlan,
    memorySavedTokens,
    tasks,
    messages,
    loading,
    models,
    selectedModel,
    archivePath,
    dismissArchiveNotification,
    selectModel,
    selectPlan,
    newPlan,
    sendPrompt: async (content: string) => {
      const plan = activePlan();
      if (!plan) return;
      setLoadingPlanId(plan.id);

      // Optimistic: add user message immediately
      const tempUserMsg: MessageWithParts = {
        info: {
          id: 'temp-' + Date.now(),
          sessionId: plan.sessionId,
          role: 'user',
          agent: 'plan',
          createdAt: Date.now(),
        },
        parts: [{
          id: 'temp-part-' + Date.now(),
          messageId: 'temp-' + Date.now(),
          sessionId: plan.sessionId,
          type: 'text',
          data: { text: content },
          createdAt: Date.now(),
          updatedAt: Date.now(),
        }],
      };
      setMessages((prev) => [...prev, tempUserMsg]);

      try {
        await sendPlanPrompt(plan.id, content, selectedModel());
        const msgs = await getPlanMessages(plan.id);
        setMessages(msgs);
        startBgPoll(plan.id);
        startPolling(plan.id);
        // autoNamePlan runs async on the backend — poll until a title appears.
        if (!plan.title) startTitlePoll(plan.id);
      } catch (e) {
        console.error('send plan prompt failed:', e);
        setLoadingPlanId('');
      }
    },
    abort,
    lockPlan,
    refresh,
    createTasksFromBreakdown,
    startTaskById,
    completeTaskById,
    failTaskById,
    retryTaskById,
    startAllTasks,
    deletePlan,
  };

  return (
    <PlanContext.Provider value={value}>
      {props.children}
    </PlanContext.Provider>
  );
};

export function usePlan() {
  const ctx = useContext(PlanContext);
  if (!ctx) throw new Error('usePlan must be used within PlanProvider');
  return ctx;
}