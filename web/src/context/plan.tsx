import { createContext, useContext, type ParentComponent } from 'solid-js';
import { batch, createSignal, createEffect, on, onMount, onCleanup } from 'solid-js';
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
  getPlanMessagesPage,
  REFRESH_SIZE,
  type MessagesPage,
  abortPlan as abortPlanAPI,
  listTasks,
  createTasks,
  startTask,
  completeTask,
  failTask,
  retryTask,
  updateTask,
  getTask,
  getModels,
  getSession,
  deletePlan as deletePlanAPI,
  isNotFoundError,
} from '../api/client';
import { useServer } from './server';
import { useSession } from './session';
import { trackModelSelected } from '../lib/analytics';
import { joinsHeld, mergeTranscript } from '../lib/transcript';

interface PlanContextValue {
  plans: () => Plan[];
  activePlan: () => Plan | null;
  // True when the selected plan id doesn't exist on the server, so pages can
  // show a not-found screen instead of a live composer for a ghost plan.
  planMissing: () => boolean;
  lockError: () => string;
  tasks: () => Task[];
  messages: () => MessageWithParts[];
  loading: () => boolean;
  /** True while an older page of the plan transcript is being fetched. */
  loadingOlder: () => boolean;
  /** Whether the server may hold messages above the top of the transcript. */
  hasOlder: () => boolean;
  /**
   * Fetch and merge the page of messages immediately older than the top;
   * resolves whether a page was merged. `around`, when given, receives the
   * merge to run inside the same batch as its own writes (see SessionContext).
   */
  loadOlder: (around?: (merge: () => void) => void) => Promise<boolean>;
  models: () => any[];
  selectedModel: () => string;
  /** The provider chosen with the model; '' means resolve it from the model id. */
  selectedProvider: () => string;
  archivePath: () => string;
  dismissArchiveNotification: () => void;
  selectModel: (modelId: string, providerId?: string) => void;
  selectPlan: (id: string) => Promise<void>;
  newPlan: (title?: string, model?: string, provider?: string) => Promise<Plan>;
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
  setTaskModel: (id: string, model: string, provider?: string) => Promise<void>;
  startAllTasks: () => Promise<void>;
  deletePlan: (id: string) => Promise<void>;
}

const PlanContext = createContext<PlanContextValue>();

export const PlanProvider: ParentComponent = (props) => {
  const server = useServer();
  const [plans, setPlans] = createSignal<Plan[]>([]);
  const [activePlan, setActivePlan] = createSignal<Plan | null>(null);
  const [planMissing, setPlanMissing] = createSignal(false);
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

  // Every server page lands through the merge (polls, SSE, select, abort), so a
  // poll's newest window never drops the older pages loaded above it, and a
  // page written for another plan's session is discarded. Optimistic inserts
  // and clears still write raw so they are not diffed.
  const applyServerMessages = (incoming: MessageWithParts[]) => {
    setMessagesRaw((prev) => mergeTranscript(prev, incoming, activePlan()?.sessionId));
  };

  // Whether older messages exist above the plan transcript, as the server
  // reported for the oldest page held. Reset per plan.
  const [hasOlder, setHasOlder] = createSignal(false);
  const [loadingOlder, setLoadingOlder] = createSignal(false);

  // The newest message held that the server wrote. An optimistic prompt sits
  // at the end of the transcript under a temp- id until the server's copy
  // replaces it, and says nothing about what the server has.
  const newestServerId = (): string => {
    const held = messagesRaw();
    for (let i = held.length - 1; i >= 0; i--) {
      if (!held[i].info.id.startsWith('temp-')) return held[i].info.id;
    }
    return '';
  };

  // Apply the newest page of the active plan's transcript. Whether anything
  // older exists is the page's answer — unless older pages are already held.
  // A page that does not join up with what is held (see joinsHeld) replaces
  // it instead, as opening the plan afresh would.
  const applyNewestPage = (page: MessagesPage) => {
    if (!joinsHeld(newestServerId(), page)) {
      // Merged into nothing, a page written for another plan's session comes
      // back empty; it is dropped like any other.
      const fresh = mergeTranscript([], page.messages, activePlan()?.sessionId);
      if (fresh.length === 0) return;
      setMessagesRaw(fresh);
      setHasOlder(page.hasOlder);
      return;
    }
    const oldestHeld = messagesRaw()[0]?.info.id;
    const holdsOlder = !!oldestHeld && page.messages.length > 0 && oldestHeld < page.messages[0].info.id;
    applyServerMessages(page.messages);
    if (!holdsOlder) setHasOlder(page.hasOlder);
  };

  // The newest messages of a plan's transcript, for a refresh: a short window
  // (REFRESH_SIZE), or the full newest page when that window does not reach
  // back to the newest message held, or nothing is held yet (see the session
  // context's fetchNewest). Resolves null once the user has moved to another
  // plan; the caller must then leave the one on screen alone.
  async function fetchNewest(planId: string): Promise<MessagesPage | null> {
    const refreshing = newestServerId() !== '';
    const page = await getPlanMessagesPage(planId, undefined, refreshing ? REFRESH_SIZE : undefined);
    if (activePlan()?.id !== planId) return null;
    if (!refreshing) return page;
    const newest = newestServerId();
    if (newest && joinsHeld(newest, page)) return page;
    const full = await getPlanMessagesPage(planId);
    return activePlan()?.id === planId ? full : null;
  }

  async function loadOlder(around?: (merge: () => void) => void): Promise<boolean> {
    const plan = activePlan();
    if (!plan || loadingOlder() || !hasOlder()) return false;
    const oldest = messagesRaw()[0];
    if (!oldest) return false;
    const planId = plan.id;
    setLoadingOlder(true);
    try {
      const page = await getPlanMessagesPage(planId, oldest.info.id);
      if (activePlan()?.id !== planId) return false;
      // Dropped if the top of the transcript moved meanwhile — see the session
      // context's loadOlder.
      if (messagesRaw()[0]?.info.id !== oldest.info.id) return false;
      const merge = () => {
        applyServerMessages(page.messages);
        setHasOlder(page.hasOlder);
      };
      batch(() => (around ? around(merge) : merge()));
      return page.messages.length > 0;
    } catch (e) {
      console.error('load older plan messages failed:', e);
      return false;
    } finally {
      setLoadingOlder(false);
    }
  }

  // Cache the active plan's transcript window so switching back does not
  // refetch before the user's scroll position can be restored — same shape as
  // the session cache. Keyed by plan id, LRU-capped at 8.
  const transcriptCache = new Map<string, { messages: MessageWithParts[]; hasOlder: boolean }>();
  createEffect(() => {
    const id = activePlan()?.id;
    if (!id) return;
    const msgs = messagesRaw();
    if (msgs.length === 0) return;
    transcriptCache.delete(id);
    transcriptCache.set(id, { messages: msgs, hasOlder: hasOlder() });
    if (transcriptCache.size > 8) {
      const oldest = transcriptCache.keys().next().value;
      if (oldest !== undefined && oldest !== id) transcriptCache.delete(oldest);
    }
  });

  const [loadingPlanId, setLoadingPlanId] = createSignal<string>('');
  const loading = () => loadingPlanId() === activePlan()?.id && loadingPlanId() !== '';
  const [lockError, setLockError] = createSignal('');

  // Archive notification: set by the plan.archived SSE event, cleared on plan switch or dismiss.
  const [archivePath, setArchivePath] = createSignal<string>('');
  const dismissArchiveNotification = () => setArchivePath('');

  const [models, setModels] = createSignal<any[]>([]);
  // Persisted model selection for the home/plan-list page — survives app restarts.
  const STORAGE_KEY = 'ogcode-selected-model';
  const [pendingModel, setPendingModel] = createSignal<string>(
    typeof localStorage !== 'undefined' ? localStorage.getItem(STORAGE_KEY) || '' : ''
  );
  // The provider chosen alongside pendingModel: a model id can be served by more
  // than one provider, so the id alone does not pin an endpoint.
  const PROVIDER_STORAGE_KEY = 'ogcode-selected-provider';
  const [pendingProvider, setPendingProvider] = createSignal<string>(
    typeof localStorage !== 'undefined' ? localStorage.getItem(PROVIDER_STORAGE_KEY) || '' : ''
  );

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

  // The provider paired with the selected model, in the same precedence order:
  // an explicit pending pick, then the plan's stored provider, then the
  // catalog's own providerId for the resolved model. '' lets the server resolve
  // by model id.
  const selectedProvider = (): string => {
    if (pendingProvider()) return pendingProvider();
    const plan = activePlan();
    if (plan?.provider) return plan.provider;
    const id = selectedModel();
    return models().find((m: any) => m.id === id)?.providerId || '';
  };

  async function selectModel(modelId: string, providerId?: string) {
    setPendingModel(modelId);
    if (providerId) setPendingProvider(providerId);
    trackModelSelected({ model: modelId, provider: providerId || '', context: 'plan' });
    // Persist so the selection survives app restarts.
    try {
      localStorage.setItem(STORAGE_KEY, modelId);
      if (providerId) localStorage.setItem(PROVIDER_STORAGE_KEY, providerId);
    } catch (_e) { /* ignore */ }
    const plan = activePlan();
    if (!plan) return;
    try {
      const updated = await updatePlan(plan.id, providerId ? { model: modelId, provider: providerId } : { model: modelId });
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
        const page = await fetchNewest(planId);
        if (page) applyNewestPage(page);
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
    fastPollInterval = setInterval(async () => {
      try {
        const plan = activePlan();
        if (!plan || plan.id !== planId) {
          stopFastPoll();
          return;
        }
        if (Date.now() - lastSSEUpdate < 2000) return;

        const page = await fetchNewest(planId);
        // Null when the user moved to another plan meanwhile: its loading
        // state is not this poll's to change.
        if (!page) return;
        applyNewestPage(page);

        if (!isAgentLoopActive(page.messages)) {
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

    setPlanMissing(false);

    let plan = plans().find((p) => p.id === id);
    if (!plan) {
      plan = activePlan()?.id === id ? activePlan()! : undefined;
    }
    // Restore the destination plan's cached transcript BEFORE the active plan
    // flips, so the list's layout and the scroll restore run against the right
    // transcript in the same frame. setMessages is raw here (no merge), so the
    // restore is direct. Falls back to a clear for an uncached plan.
    const cached = transcriptCache.get(id);
    if (cached) {
      setMessages(cached.messages);
      setHasOlder(cached.hasOlder);
    } else {
      setMessages([]);
      setHasOlder(false);
    }
    setLoadingOlder(false);

    if (plan) setActivePlan(plan);

    setPendingModel('');
    setArchivePath('');
    stopPolling();
    stopTitlePoll();

    try {
      const p = await getPlan(id);
      setActivePlan(p);
      const page = await getPlanMessagesPage(id);
      // A switch to another plan while this was in flight must not take this
      // plan's answer about older messages.
      if (activePlan()?.id === id) applyNewestPage(page);

      // Load tasks for the plan
      const t = await listTasks(id);
      setTasks(t || []);

      startBgPoll(id);
      if (p.status === 'locked') {
        startTaskPoll(id);
      }
      if (isAgentLoopActive(page.messages)) {
        setLoadingPlanId(id);
        startPolling(id);
      }
    } catch (e) {
      console.error('load plan failed:', e);
      // A 404 means the id is dead (deleted, or a typo'd URL). Flag it so the
      // page renders a not-found screen rather than an inviting empty composer
      // whose prompts would silently fail against a plan that doesn't exist.
      if (isNotFoundError(e)) {
        if (activePlan()?.id === id) setActivePlan(null);
        setPlanMissing(true);
      }
    }
  }

  async function newPlan(title?: string, model?: string, provider?: string): Promise<Plan> {
    const plan = await createPlan(server.directory(), title, model || selectedModel(), provider || selectedProvider());
    setPlans((prev) => prev.find((p) => p.id === plan.id) ? prev : [plan, ...prev]);
    setActivePlan(plan);
    setMessages([]);
    setHasOlder(false);
    setLoadingOlder(false);
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
      const page = await fetchNewest(plan.id);
      if (page) applyNewestPage(page);
    } catch (e) {
      console.error('refresh after abort failed:', e);
    }
  }

  async function lockPlan() {
    const plan = activePlan();
    if (!plan) return;
    setLockError('');
    setLoadingPlanId(plan.id);
    try {
      const updated = await lockPlanAPI(plan.id);
      setActivePlan(updated);
      stopFastPoll();
      setLoadingPlanId('');
    } catch (e: any) {
      setLoadingPlanId('');
      const msg = e?.message?.replace(/^API error \d+:\s*/, '').trim() ?? 'Failed to lock plan';
      setLockError(msg);
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

  // setTaskModel sets a per-task model override ('' clears it back to the plan
  // default), together with the provider that serves it — a model id alone can
  // match more than one provider. Updates optimistically, then reconciles with
  // the server response.
  async function setTaskModel(id: string, model: string, provider?: string) {
    setTasks((prev) => prev.map((t) => (t.id === id ? { ...t, model, ...(provider !== undefined ? { provider } : {}) } : t)));
    try {
      const updated = await updateTask(id, provider !== undefined ? { model, provider } : { model });
      setTasks((prev) => prev.map((t) => (t.id === id ? updated : t)));
    } catch (e) {
      console.error('set task model failed:', e);
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
        fetchNewest(plan.id).then((page) => {
          if (!page) return;
          applyNewestPage(page);
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
    if (last.type !== 'message.updated' && last.type !== 'message.part.updated' && last.type !== 'message.deleted') return;
    const evtSessionId = last.properties?.sessionId || last.properties?.id;
    if (evtSessionId && evtSessionId !== plan.sessionId) return;

    const targetPlanId = plan.id;
    sseRefreshDebounce = setTimeout(async () => {
      if (activePlan()?.id !== targetPlanId) return;
      try {
        const page = await fetchNewest(targetPlanId);
        if (!page) return;
        applyNewestPage(page);
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

  // On SSE reconnect, refresh active plan (messages + plan metadata + tasks).
  // Only on a reconnect, not the first connection, which selectPlan's own
  // fetches already cover — see the session context's reconnect effect.
  createEffect(on(server.reconnectTick, () => {
    const plan = activePlan();
    if (!plan) return;
    Promise.all([
      fetchNewest(plan.id),
      getPlan(plan.id),
      listTasks(plan.id),
    ]).then(([page, updatedPlan, updatedTasks]) => {
      if (activePlan()?.id !== plan.id) return;
      if (page) applyNewestPage(page);
      setActivePlan((prev) => prev ? { ...prev, ...updatedPlan } : prev);
      setPlans((prev) => prev.map((p) => (p.id === updatedPlan.id ? { ...p, ...updatedPlan } : p)));
      setTasks(updatedTasks || []);
      lastSSEUpdate = Date.now();
    }).catch(() => {});
  }, { defer: true }));

  // ── Model switch hotkey (Alt+1–4) ──
  // The session context owns the slot assignments, so we reuse its modelSlots
  // and popup signal. On plan screens the hotkey switches the *plan's* model
  // (not the session's) and still shows the glass confirmation popover.
  const session = useSession();
  const handlePlanHotkey = (e: KeyboardEvent) => {
    if (!e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
    let slot = -1;
    if (e.code === 'Digit1') slot = 0;
    else if (e.code === 'Digit2') slot = 1;
    else if (e.code === 'Digit3') slot = 2;
    else if (e.code === 'Digit4') slot = 3;
    if (slot < 0) return;
    const modelId = session.modelSlots()[slot];
    if (!modelId) return;
    // Only switch if the model is currently enabled in this context.
    const target = models().find((m: any) => m.id === modelId);
    if (!target?.enabled) return;
    e.preventDefault();
    e.stopPropagation();
    selectModel(modelId, target.providerId);
    session.showModelSwitchPopup(modelId, slot + 1);
  };
  onMount(() => document.addEventListener('keydown', handlePlanHotkey));
  onCleanup(() => document.removeEventListener('keydown', handlePlanHotkey));

  const value: PlanContextValue = {
    plans,
    activePlan,
    planMissing,
    lockError,
    tasks,
    messages,
    loading,
    loadingOlder,
    hasOlder,
    loadOlder,
    models,
    selectedModel,
    selectedProvider,
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
        await sendPlanPrompt(plan.id, content, selectedModel(), window.innerWidth, window.innerHeight, selectedProvider());
        const page = await fetchNewest(plan.id);
        if (page) applyNewestPage(page);
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
    setTaskModel,
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