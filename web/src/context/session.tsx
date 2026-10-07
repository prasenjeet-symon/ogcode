import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal, createMemo, createEffect, on, onMount, onCleanup, batch } from 'solid-js';
import {
  type Session,
  type MessageWithParts,
  type ModelInfo,
  type ImagePartData,
  type ToolPartData,
  listSessions,
  createSession,
  getSession,
  getMessagesPage,
  REFRESH_SIZE,
  type MessagesPage,
  sendPrompt,
  sendGuidance,
  replyPermission,
  listPendingPermissions,
  type PermissionResponse,
  replyQuestion,
  listPendingQuestions,
  type PendingQuestionAPI,
  type QuestionAnswerAPI,
  getModels,
  refreshModels as apiRefreshModels,
  updateSession,
  abortSession,
  resumeSession,
  setModelPreference,
  deleteModelPreference,
  isNotFoundError,
} from '../api/client';
import { useServer } from './server';
import { capture } from '../lib/posthog';
import {
  trackSessionStarted,
  trackMessageSent,
  trackModelSelected,
  trackPermissionPromptAnswered,
  trackCompactTriggered,
  trackErrorShown,
} from '../lib/analytics';
import { joinsHeld, mergeTranscript } from '../lib/transcript';

export interface PendingPermission {
  permissionId: string;
  tool: string;
  pattern: string;
  input: string;
}

/** One ask_user batch awaiting an answer, in the shape the dialog renders. */
export type PendingQuestion = PendingQuestionAPI;

/** Why the agent loop stopped, when nothing in the transcript says so. */
export type LoopFailure = {
  /** The loop's exit reason: "error", "panic", or any non-clean finish —
   *  including "aborted", a user stop that landed between messages (after the
   *  tool results were written, before the next model call) and so left no
   *  aborted marker in the transcript. Rendered as a neutral "Generation
   *  cancelled" notice rather than an error. */
  reason: string;
  /** The server-side error text, when there was one. For "aborted" this is
   *  Go's bare "context canceled" — carried for debugging, never rendered. */
  message: string;
};

/** Where mid-loop guidance stands: waiting for the loop's next iteration,
 *  just picked up by it, or nothing in flight. */
export type GuidanceStatus = 'idle' | 'queued' | 'delivered';

interface SessionContextValue {
  sessions: () => Session[];
  activeSession: () => Session | null;
  // True when the selected session id doesn't exist on the server, so pages can
  // show a not-found screen instead of an endlessly "Loading..." live composer.
  sessionMissing: () => boolean;
  messages: () => MessageWithParts[];
  loading: () => boolean;
  /** True while a fetch for the page of older messages is in flight. */
  loadingOlder: () => boolean;
  /** Whether older messages exist beyond the top of the transcript. */
  hasOlder: () => boolean;
  /**
   * Fetch and merge the page of messages older than the current top; resolves
   * whether a page was merged. `around`, when given, receives the merge to run
   * inside the same batch as its own writes — the transcript view uses it to
   * move its scroll position in the very update that grows the content above
   * it, so the reader's place is never drawn anywhere else.
   */
  loadOlder: (around?: (merge: () => void) => void) => Promise<boolean>;
  hasRunningTools: () => boolean;
  /** The tool calls still in flight — running, or pending while the model is
   *  still writing their arguments — in transcript order. Empty when none are,
   *  or when the turn they belong to has ended without finishing them. */
  liveTools: () => ToolPartData[];
  compacted: () => boolean;
  /** Where mid-loop guidance stands for the active session: waiting for the
   *  loop's next iteration, just picked up by it, or nothing in flight. */
  guidanceStatus: () => GuidanceStatus;
  /** Set when the agent loop ended in a way nothing in the transcript explains. */
  loopError: () => LoopFailure | null;
  dismissLoopError: () => void;
  pendingPermissions: () => PendingPermission[];
  respondPermission: (permissionId: string, response: PermissionResponse) => Promise<void>;
  /** ask_user batches the agent loop is blocked on, for the active session. */
  pendingQuestions: () => PendingQuestion[];
  respondQuestion: (questionId: string, answers: QuestionAnswerAPI[]) => Promise<void>;
  models: () => ModelInfo[];
  selectedModel: () => string;
  /** The provider chosen with the model; '' means resolve it from the model id. */
  selectedProvider: () => string;
  selectModel: (modelId: string, providerId?: string) => void;
  permissionMode: () => 'auto' | 'ask' | 'yolo';
  setPermissionMode: (mode: 'auto' | 'ask' | 'yolo') => Promise<void>;
  selectSession: (id: string) => Promise<void>;
  renameSession: (id: string, title: string) => Promise<void>;
  newSession: (model?: string, provider?: string) => Promise<Session>;
  prompt: (content: string, images?: ImagePartData[]) => Promise<void>;
  /** True when this optimistic message never reached the server. */
  sendFailed: (messageId: string) => boolean;
  guidance: (content: string, cancelTool?: boolean) => Promise<boolean>;
  abort: () => Promise<void>;
  /** Restart the loop on a session whose last turn was interrupted. */
  resume: () => Promise<{ resumed: boolean; message?: string }>;
  refreshModels: () => Promise<void>;
  /** Force a live re-fetch of provider catalogues (after a credential change). */
  reloadModels: () => Promise<void>;
  toggleModel: (model: ModelInfo, enabled: boolean) => Promise<void>;
  addCustomModel: (id: string, providerId: string, displayName: string, collection?: string) => Promise<void>;
  removeCustomModel: (id: string, providerId?: string) => Promise<void>;
  refresh: () => void;
  modelSlots: () => (string | null)[];
  setModelSlot: (slot: number, modelId: string | null) => void;
  modelSwitchPopup: () => { modelId: string; slot: number } | null;
  showModelSwitchPopup: (modelId: string, slot: number) => void;
}

const SessionContext = createContext<SessionContextValue>();

export const SessionProvider: ParentComponent = (props) => {
  const server = useServer();
  const [sessions, setSessions] = createSignal<Session[]>([]);
  const [activeSession, setActiveSession] = createSignal<Session | null>(null);
  const [sessionMissing, setSessionMissing] = createSignal(false);
  const [messagesRaw, setMessagesRaw] = createSignal<MessageWithParts[]>([]);

  // Messages the user has sent but the server has not yet echoed back, held
  // apart from the server's list rather than inside it.
  //
  // They cannot live in messagesRaw: every merge below is driven by what the
  // server returned, so anything the server does not know about is dropped. A
  // bubble sent while the model is mid-response is exactly that — the id is
  // client-side only — and the SSE refresh that fires on the model's next token
  // would delete it from under the user. Keeping them separate makes them
  // structurally immune to that instead of relying on the merge to spare them.
  const [optimistic, setOptimistic] = createSignal<MessageWithParts[]>([]);

  // What the UI renders: the server's messages, then any optimistic bubble the
  // server has not confirmed yet. Ids are checked in case a fetch does return
  // one of them, so a confirmed message is never shown twice.
  const messages = createMemo<MessageWithParts[]>(() => {
    const pending = optimistic();
    const confirmed = messagesRaw();
    if (pending.length === 0) return confirmed;
    const sessionId = activeSession()?.id;
    const known = new Set(confirmed.map((m) => m.info.id));
    return [
      ...confirmed,
      // Scoped to the session on screen as well as to what the server has not
      // confirmed. The switch paths clear this list, but filtering here is what
      // makes a stray bubble impossible rather than merely unlikely — it can
      // never surface in a conversation it was not typed into.
      ...pending.filter((m) => !known.has(m.info.id) && (!sessionId || m.info.sessionId === sessionId)),
    ];
  });

  // Merge a server page into the transcript on screen — see mergeTranscript.
  // A page for a session other than the one on screen is dropped.
  const mergeMessages = (prev: MessageWithParts[], incoming: MessageWithParts[]): MessageWithParts[] =>
    mergeTranscript(prev, incoming, activeSession()?.id);

  const setMessages = (next: MessageWithParts[] | ((prev: MessageWithParts[]) => MessageWithParts[])) => {
    if (typeof next === 'function') {
      // Use functional updater so mergeMessages reads the latest state
      setMessagesRaw((prev) => mergeMessages(prev, (next as (prev: MessageWithParts[]) => MessageWithParts[])(prev)));
    } else {
      setMessagesRaw((prev) => mergeMessages(prev, next));
    }
  };

  // Whether older messages exist above the top of the transcript, as the server
  // reported for the oldest page held. Reset per session on switch.
  const [hasOlder, setHasOlder] = createSignal(false);
  const [loadingOlder, setLoadingOlder] = createSignal(false);

  // Apply the newest page of the active session's transcript. Whether anything
  // older exists is the page's answer — unless older pages are already held,
  // in which case their answer still stands. A page that does not join up with
  // what is held (see joinsHeld) replaces it instead, as opening the session
  // afresh would: the pages above go, and are paged back in on demand.
  const applyNewestPage = (page: MessagesPage) => {
    const held = messagesRaw();
    if (!joinsHeld(held[held.length - 1]?.info.id ?? '', page)) {
      // Merged into nothing, a page written for another session comes back
      // empty; it is dropped like any other.
      const fresh = mergeTranscript([], page.messages, activeSession()?.id);
      if (fresh.length === 0) return;
      setMessagesRaw(fresh);
      setHasOlder(page.hasOlder);
      return;
    }
    const oldestHeld = held[0]?.info.id;
    const holdsOlder = !!oldestHeld && page.messages.length > 0 && oldestHeld < page.messages[0].info.id;
    setMessages(page.messages);
    if (!holdsOlder) setHasOlder(page.hasOlder);
  };

  // The newest messages of a session's transcript, for a refresh: a poll, a
  // live update, the fetch after a prompt. A short window is all that takes
  // (REFRESH_SIZE). When it does not reach back to the newest message held,
  // more landed in between than it covers, and the full newest page is fetched
  // instead. With nothing held this is a first load, not a refresh, and takes
  // the full page too: a window would leave the transcript a few messages long
  // and send the view paging back for the rest it is about to receive.
  // Resolves null once the user has moved to another session; the caller must
  // then leave the one on screen alone.
  async function fetchNewest(sessionId: string): Promise<MessagesPage | null> {
    const refreshing = messagesRaw().length > 0;
    const page = await getMessagesPage(sessionId, undefined, refreshing ? REFRESH_SIZE : undefined);
    if (activeSession()?.id !== sessionId) return null;
    if (!refreshing) return page;
    const held = messagesRaw();
    if (held.length > 0 && joinsHeld(held[held.length - 1].info.id, page)) return page;
    const full = await getMessagesPage(sessionId);
    return activeSession()?.id === sessionId ? full : null;
  }

  async function loadOlder(around?: (merge: () => void) => void): Promise<boolean> {
    const sess = activeSession();
    if (!sess || loadingOlder() || !hasOlder()) return false;
    const oldest = messagesRaw()[0];
    if (!oldest) return false;
    const sessionId = sess.id;
    setLoadingOlder(true);
    try {
      const page = await getMessagesPage(sessionId, oldest.info.id);
      if (activeSession()?.id !== sessionId) return false;
      // The page is the history just above what was the oldest message held.
      // If the top of the transcript moved meanwhile (a refresh replaced it,
      // or a first page landed under an early one), the page no longer joins
      // on there — merged, it could drop newer messages or leave a hole — so
      // it is dropped, and the next approach to the top asks again.
      if (messagesRaw()[0]?.info.id !== oldest.info.id) return false;
      const merge = () => {
        setMessagesRaw((prev) => mergeMessages(prev, page.messages));
        setHasOlder(page.hasOlder);
      };
      batch(() => (around ? around(merge) : merge()));
      return page.messages.length > 0;
    } catch (e) {
      console.error('load older messages failed:', e);
      return false;
    } finally {
      setLoadingOlder(false);
    }
  }
  // Cache-write effect: the transcript cache must be written whenever the
  // rendered transcript changes. The whole conversation is a page-backed
  // window, so what is cached is the window itself — messages plus whether
  // more exist above it. Restored synchronously on switch-back so the list
  // does not need to refetch before the user's scroll position can be
  // restored. LRU-capped: the oldest entry (first key) is evicted at 8.
  const transcriptCache = new Map<string, { messages: MessageWithParts[]; hasOlder: boolean }>();
  createEffect(() => {
    const id = activeSession()?.id;
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

  // Track which session is currently loading (not a global flag)
  const [loadingSessionId, setLoadingSessionId] = createSignal<string>('');
  // Optimistic messages whose POST never landed. Without this an failed send
  // leaves a bubble on screen that looks delivered, and the only trace is a
  // console error — the delivery ticks read this to show it as failed instead.
  const [failedSends, setFailedSends] = createSignal<Set<string>>(new Set());
  const sendFailed = (messageId: string) => failedSends().has(messageId);
  // Compute loading state: true only if active session is the one loading
  const loading = () => loadingSessionId() === activeSession()?.id && loadingSessionId() !== '';

  // Check if any tools are currently running or pending.
  // If the last assistant message has finished (stop/error/aborted), stale tool
  // statuses shouldn't block the UI — the loop is done and won't update them.
  //
  // A memo, not a plain function: half a dozen views read it, and every one of
  // them re-ran the whole scan on each change to the transcript — every poll,
  // every page of history loaded — over every tool part held.
  const liveTools = createMemo(() => messagesLiveTools(messagesRaw()));
  const hasRunningTools = createMemo(() => liveTools().length > 0);

  // The loop ended abnormally and no message carries the reason. Sticky, not
  // transient: a failure the user never saw is the bug this exists to fix, so
  // it clears only when they start another turn or dismiss it.
  const [loopError, setLoopError] = createSignal<LoopFailure | null>(null);

  // Transient flag: true for 5 s after the server auto-compacts the context window
  const [compacted, setCompacted] = createSignal(false);
  let compactedTimer: ReturnType<typeof setTimeout> | null = null;

  // Where mid-loop guidance stands for the active session. "queued" from the
  // moment the server accepts it until the loop drains it (loop.guidance:
  // delivered); then "delivered" for a moment, so the composer can say it was
  // applied; then idle — and idle at once when the loop exits.
  const [guidanceStatus, setGuidanceStatus] = createSignal<GuidanceStatus>('idle');
  // loop.guidance "delivered" events seen per session this page-load. guidance()
  // compares the count across its own request: the server wakes the loop while
  // handling that request, so the loop can drain the guidance and say so before
  // the response gets back here. A delivery that landed in flight means nothing
  // is waiting any more, and the resolved request must not raise "queued" again
  // — it would stay up, over guidance already applied, until the turn ended.
  const guidanceDeliveries = new Map<string, number>();
  // How many times guidance has been sent per session this page-load, so the
  // analytics event can tell a single course-correction from repeated steering.
  // In-memory only and never persisted — it is a counter for one metric.
  const guidanceCount = new Map<string, number>();
  let guidanceTimer: ReturnType<typeof setTimeout> | null = null;
  // Per-session permission queues, keyed by session id. A permission prompt is
  // owned by the session whose agent loop raised it — not by whichever session
  // happens to be on screen — so the loop stays blocked on the reply even while
  // the user has switched away. The active session's queue is what the UI shows.
  const [permQueues, setPermQueues] = createSignal<Record<string, PendingPermission[]>>({});
  const pendingPermissions = () => {
    const sess = activeSession();
    return sess ? permQueues()[sess.id] || [] : [];
  };
  const setPermQueue = (sessionId: string, updater: (prev: PendingPermission[]) => PendingPermission[]) => {
    setPermQueues((all) => {
      const prev = all[sessionId] || [];
      const next = updater(prev);
      if (next.length === 0 && prev.length === 0) return all;
      if (next.length === 0) {
        const { [sessionId]: _drop, ...rest } = all;
        return rest;
      }
      return { ...all, [sessionId]: next };
    });
  };

  // Per-session ask_user queues, keyed by session id and owned the same way the
  // permission queues are: the batch belongs to the session whose agent loop
  // raised it, and that loop stays blocked until it is answered. ask_user takes
  // the whole batch in one round trip, so at most one batch is pending per
  // session at a time — but the queue shape is kept so the restore and SSE paths
  // match the permission ones.
  const [questionQueues, setQuestionQueues] = createSignal<Record<string, PendingQuestion[]>>({});
  const pendingQuestions = () => {
    const sess = activeSession();
    return sess ? questionQueues()[sess.id] || [] : [];
  };
  const setQuestionQueue = (sessionId: string, updater: (prev: PendingQuestion[]) => PendingQuestion[]) => {
    setQuestionQueues((all) => {
      const prev = all[sessionId] || [];
      const next = updater(prev);
      if (next.length === 0 && prev.length === 0) return all;
      if (next.length === 0) {
        const { [sessionId]: _drop, ...rest } = all;
        return rest;
      }
      return { ...all, [sessionId]: next };
    });
  };

  const [models, setModels] = createSignal<ModelInfo[]>([]);
  // Model selection chosen before any session exists (e.g. on the home page).
  // Used as the default for `newSession()` and read by `selectedModel()`.
  // Persisted to localStorage so the user's last model choice survives app restarts.
  const STORAGE_KEY = 'ogcode-selected-model';
  const [pendingModel, setPendingModel] = createSignal<string>(
    typeof localStorage !== 'undefined' ? localStorage.getItem(STORAGE_KEY) || '' : ''
  );
  // The provider chosen alongside pendingModel. A model id can be served by more
  // than one provider, so the id alone does not pin an endpoint; persisted next
  // to the model so a restart resumes the same one.
  const PROVIDER_STORAGE_KEY = 'ogcode-selected-provider';
  const [pendingProvider, setPendingProvider] = createSignal<string>(
    typeof localStorage !== 'undefined' ? localStorage.getItem(PROVIDER_STORAGE_KEY) || '' : ''
  );

  // Model hotkey slots — up to 4 models that can be switched to with Alt+1–4.
  // Persisted in localStorage so assignments survive app restarts.
  const SLOTS_KEY = 'ogcode-model-slots';
  const NUM_SLOTS = 4;
  function loadModelSlots(): (string | null)[] {
    try {
      const raw = typeof localStorage !== 'undefined' ? localStorage.getItem(SLOTS_KEY) : null;
      if (raw) {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed)) {
          const slots: (string | null)[] = [];
          for (let i = 0; i < NUM_SLOTS; i++) {
            slots.push(typeof parsed[i] === 'string' ? (parsed[i] as string) : null);
          }
          return slots;
        }
      }
    } catch (_e) { /* ignore parse errors */ }
    return new Array(NUM_SLOTS).fill(null);
  }
  function saveModelSlots(slots: (string | null)[]) {
    try { localStorage.setItem(SLOTS_KEY, JSON.stringify(slots)); } catch (_e) { /* ignore quota errors */ }
  }
  const [modelSlots, setModelSlots] = createSignal<(string | null)[]>(loadModelSlots());

  // Transient model-switch popup state — set when the user switches models via
  // the Alt+1–4 hotkey. Auto-clears after a short delay so the popover only
  // flashes briefly to confirm which model is now active.
  interface SwitchPopup { modelId: string; slot: number }
  const [modelSwitchPopup, setModelSwitchPopup] = createSignal<SwitchPopup | null>(null);
  let switchPopupTimer: ReturnType<typeof setTimeout> | null = null;
  function showModelSwitchPopup(modelId: string, slot: number) {
    if (switchPopupTimer) clearTimeout(switchPopupTimer);
    setModelSwitchPopup({ modelId, slot });
    switchPopupTimer = setTimeout(() => setModelSwitchPopup(null), 1800);
  }

  function setModelSlot(slot: number, modelId: string | null) {
    if (slot < 0 || slot >= NUM_SLOTS) return;
    setModelSlots((prev) => {
      const next = [...prev];
      // Don't assign the same model to multiple slots — clear any existing slot
      // that already holds this model.
      if (modelId) {
        for (let i = 0; i < next.length; i++) {
          if (i !== slot && next[i] === modelId) next[i] = null;
        }
      }
      next[slot] = modelId;
      saveModelSlots(next);
      return next;
    });
  }
  // Two-tier polling:
  //   fastPollInterval — 3 s, runs only while the agent loop is active
  //   bgPollInterval   — 15 s, always runs for the active session so the UI
  //                      stays in sync even when the loop is idle or SSE drops
  let fastPollInterval: ReturnType<typeof setInterval> | null = null;
  let bgPollInterval: ReturnType<typeof setInterval> | null = null;
  let lastSSEUpdate = 0; // timestamp of last SSE-driven message refresh

  // Load models on mount
  getModels()
    .then((list) => setModels(list || []))
    .catch((e) => console.error('load models failed:', e));

  // Compute selected model: pendingModel is the latest explicit user selection and takes
  // priority so model changes take effect immediately without waiting for network round-trips.
  // Falls back to the session's persisted model, then to the enabled default.
  const selectedModel = (): string => {
    if (pendingModel()) return pendingModel();
    const sess = activeSession();
    if (sess?.model) return sess.model;
    const enabled = models().filter((m) => m.enabled);
    const defaults = enabled.filter((m) => m.default);
    if (defaults.length > 0) return defaults[0].id;
    if (enabled.length > 0) return enabled[0].id;
    return '';
  };

  // The provider paired with the selected model, in the same precedence order:
  // an explicit pending pick, then the session's stored provider, then the
  // catalog's own providerId for the resolved model. '' lets the server resolve
  // by model id.
  const selectedProvider = (): string => {
    if (pendingProvider()) return pendingProvider();
    const sess = activeSession();
    if (sess?.provider) return sess.provider;
    const id = selectedModel();
    return models().find((m) => m.id === id)?.providerId || '';
  };

  async function selectModel(modelId: string, providerId?: string) {
    // Set pendingModel immediately (optimistic) so selectedModel() reflects the change
    // before the network request completes — prevents the old model from being sent if
    // the user sends a prompt quickly after changing the model.
    setPendingModel(modelId);
    if (providerId) setPendingProvider(providerId);
    trackModelSelected({ model: modelId, provider: providerId || '', context: 'session' });
    // Persist the selection so it survives app restarts — this is the default model
    // for the home page and new sessions.
    try {
      localStorage.setItem(STORAGE_KEY, modelId);
      if (providerId) localStorage.setItem(PROVIDER_STORAGE_KEY, providerId);
    } catch (_e) { /* ignore quota errors */ }
    const sess = activeSession();
    if (!sess) return;
    try {
      const updated = await updateSession(sess.id, providerId ? { model: modelId, provider: providerId } : { model: modelId });
      setActiveSession(updated);
    } catch (e) {
      console.error('update model failed:', e);
    }
  }

  // Permission mode for the active session: 'ask' (prompt before every mutating
  // tool — the default), 'auto' (auto-run low-risk tools, ask only for risky
  // ones), or 'yolo' (run everything without asking or classifying). Persisted
  // on the session's `permission` field.
  const permissionMode = (): 'auto' | 'ask' | 'yolo' => {
    const p = activeSession()?.permission;
    return p === 'auto' || p === 'yolo' ? p : 'ask';
  };

  async function setPermissionMode(mode: 'auto' | 'ask' | 'yolo') {
    const sess = activeSession();
    if (!sess || (sess.permission ?? 'ask') === mode) return;
    setActiveSession({ ...sess, permission: mode }); // optimistic
    try {
      const updated = await updateSession(sess.id, { permission: mode });
      setActiveSession(updated);
      setSessions((list) => list.map((s) => (s.id === updated.id ? updated : s)));
    } catch (e) {
      console.error('update permission mode failed:', e);
      setActiveSession(sess); // revert on failure
    }
  }

  async function refresh() {
    const dir = server.directory();
    if (!dir) return;
    try {
      const list = await listSessions(dir);
      setSessions(list);
    } catch (e) {
      console.error('refresh sessions failed:', e);
    }
  }

  async function abort() {
    const sess = activeSession();
    if (!sess) return;

    // Stop the fast poll and clear loading state immediately.
    // The background poll keeps running so the session stays in sync.
    stopFastPoll();
    setLoadingSessionId('');

    try {
      // Tell server to cancel the request and all tool calls
      await abortSession(sess.id);
      console.info('abort request sent to server');
    } catch (e) {
      console.error('abort request failed:', e);
    }

    // Refresh messages to pick up the "aborted" finish state and cancelled tool calls
    try {
      const page = await fetchNewest(sess.id);
      if (page) applyNewestPage(page);
    } catch (e) {
      console.error('refresh after abort failed:', e);
    }
  }

  async function refreshModels() {
    try {
      const list = await getModels();
      setModels(list || []);
    } catch (e) {
      console.error('refresh models failed:', e);
    }
  }

  // Force a live re-fetch of every provider's catalogue from its endpoint
  // (POST /models/refresh clears the server-side cache first), then swap in the
  // result. Used after a credential or base-URL change so the provider's models
  // appear immediately, no restart. Unlike refreshModels — which just re-reads
  // the cached list — this guarantees a fresh fetch, and it rethrows so the
  // caller can tell "saved but could not fetch" apart from "could not save".
  async function reloadModels() {
    const list = await apiRefreshModels();
    setModels(list || []);
  }

  async function toggleModel(model: ModelInfo, enabled: boolean) {
    // Optimistic and identity-preserving: replace only the toggled model's
    // object, leaving every other model's reference intact. The settings list
    // then reconciles to a single-row update instead of re-mounting — no
    // flicker, no scroll jump. The server returns the full list, but we don't
    // swap it in: for an enable/disable it only ever differs in this one flag,
    // and a fresh all-new array is exactly what forced the re-mount before.
    const prev = models();
    setModels(prev.map((m) => (m.id === model.id ? { ...m, enabled } : m)));
    try {
      await setModelPreference({
        id: model.id,
        providerId: model.providerId,
        displayName: model.name,
        enabled,
        isCustom: model.isCustom,
        collection: model.collection,
      });
    } catch (e) {
      setModels(prev); // restore the exact prior objects on failure
      console.error('toggle model failed:', e);
    }
  }

  async function addCustomModel(id: string, providerId: string, displayName: string, collection?: string) {
    try {
      const updated = await setModelPreference({
        id,
        providerId,
        displayName: displayName || id,
        enabled: true,
        isCustom: true,
        collection: collection || '',
      });
      setModels(updated || []);
    } catch (e) {
      console.error('add custom model failed:', e);
    }
  }

  async function removeCustomModel(id: string, providerId?: string) {
    try {
      await deleteModelPreference(id, providerId || '');
      await refreshModels();
    } catch (e) {
      console.error('remove custom model failed:', e);
    }
  }

  async function selectSession(id: string) {
    const current = activeSession();
    const sameSession = current?.id === id;
    setSessionMissing(false);

    // Cancel any pending SSE refresh from previous session
    if (sseRefreshDebounce) {
      clearTimeout(sseRefreshDebounce);
      sseRefreshDebounce = null;
    }

    // Find in local list, or create a stub
    let session = sessions().find((s) => s.id === id);
    if (!session) {
      session = current?.id === id
        ? current
        : { id, projectId: '', directory: server.directory(), title: 'Loading...', createdAt: Date.now(), updatedAt: Date.now() };
    }
    // Switching sessions: restore the destination's cached transcript and
    // reset per-session UI state BEFORE the active session flips, so the
    // message list's row layout and the scroll restore run against the right
    // transcript in the same frame rather than against a cleared list.
    // setMessagesRaw is deliberate here: the merge wrapper drops pages that
    // do not match the session on screen — which is still the previous
    // session until setActiveSession runs — so it would drop the restore.
    if (!sameSession) {
      const cached = transcriptCache.get(id);
      if (cached) {
        setMessagesRaw(cached.messages);
        setHasOlder(cached.hasOlder);
      } else {
        setMessagesRaw([]);
        setHasOlder(false);
      }
      setLoadingOlder(false);
      // Drop unconfirmed bubbles too: a failed send in a session the user has
      // left would otherwise sit in memory for the life of the page.
      setOptimistic([]);
      // Clear pendingModel when switching sessions so the destination session's
      // own persisted model is used, not whatever was selected in the previous session.
      setPendingModel('');
      setPendingProvider('');
      // Stop any existing polling from previous session when switching
      stopPolling();
      setLoadingSessionId('');
      setCompacted(false);
      setLoopError(null);
      if (compactedTimer) { clearTimeout(compactedTimer); compactedTimer = null; }
      // Clear any lingering guidance indicator — guidance is per-session and
      // must not leak into the destination session's UI.
      if (guidanceTimer) { clearTimeout(guidanceTimer); guidanceTimer = null; }
      setGuidanceStatus('idle');
    }
    setActiveSession(session);
    // Re-entering the same session keeps cached messages and refreshes in place.
    try {
      const page = await getMessagesPage(id);
      // The user may have switched sessions while this was in flight. Everything
      // below belongs to `id`, including the setActiveSession further down that
      // would otherwise drag the view back to the session they just left.
      if (activeSession()?.id !== id) return;
      applyNewestPage(page);

      // Resolve the authoritative session record. The in-memory list is filtered to
      // the main project directory, so sessions created in task worktrees (which use
      // the worktree path as their directory) are not in it. Fall back to a direct
      // getSession fetch — it queries by session ID, not directory — so the task
      // session's real model is picked up instead of falling back to the default.
      //
      // The list is deliberately NOT re-fetched here: selecting a session used to
      // send a redundant listSessions request on every click. The sidebar is
      // refreshed elsewhere (refresh(), create, rename, permission-mode change)
      // plus the session.updated event, which patches changed rows in place; the
      // background poll only syncs messages, never the session list.
      let fresh = sessions().find((s) => s.id === id);
      if (!fresh) {
        try {
          fresh = await getSession(id);
        } catch (e) {
          // The message fetch returns an empty list rather than 404 for an unknown id,
          // so this direct fetch is where a genuinely missing session surfaces.
          // Without it the UI sits on the "Loading..." stub forever with a live
          // composer pointed at a session that doesn't exist.
          if (isNotFoundError(e) && activeSession()?.id === id) {
            setSessionMissing(true);
            return;
          }
          /* transient failure — leave the cached record in place */
        }
      }
      if (fresh) {
        setActiveSession(fresh);
      }

      // Restore any pending permission prompts for this session. Prompts raised
      // while the user was viewing another session were never dropped server-side
      // (the agent loop is still blocked on them), but the client may not have
      // seen the permission.requested event if it arrived while this session was
      // off-screen, or the queue was cleared before the per-session queue landed.
      // Fetching here makes the prompt reappear on return.
      try {
        const pending = await listPendingPermissions(id);
        if (activeSession()?.id !== id) return;
        // Merge, not replace: an SSE permission.requested for this session may
        // have landed while the fetch was in flight, and overwriting would drop
        // it. The backend list is authoritative for what is *still* pending, so
        // any local entry not in the list has already been answered/cancelled and
        // is dropped; entries the backend confirms are added.
        const fetchedIds = new Set(pending.map((p) => p.permissionId));
        setPermQueue(id, (prev) => {
          const kept = prev.filter((p) => fetchedIds.has(p.permissionId));
          const have = new Set(kept.map((p) => p.permissionId));
          const added = pending
            .filter((p) => !have.has(p.permissionId))
            .map((p) => ({
              permissionId: p.permissionId,
              tool: p.tool,
              pattern: p.patterns?.[0] ?? '',
              input: p.input,
            }));
          return [...kept, ...added];
        });
      } catch (e) {
        /* non-fatal — the SSE handler will still append future prompts */
      }

      // Restore any pending ask_user batch for this session, for the same reason
      // as the permission queue above: the loop is still blocked on it, but the
      // client may have missed the question.requested event while another session
      // was on screen. Merge, not replace — an SSE event may land mid-fetch.
      try {
        const pending = await listPendingQuestions(id);
        if (activeSession()?.id !== id) return;
        const fetchedIds = new Set(pending.map((q) => q.questionId));
        setQuestionQueue(id, (prev) => {
          const kept = prev.filter((q) => fetchedIds.has(q.questionId));
          const have = new Set(kept.map((q) => q.questionId));
          const added = pending.filter((q) => !have.has(q.questionId));
          return [...kept, ...added];
        });
      } catch (e) {
        /* non-fatal — the SSE handler will still append future batches */
      }

      // Always keep a background poll so the session stays in sync
      startBgPoll(id);

      // Upgrade to fast poll if the agent loop is still running
      if (isAgentLoopActive(page.messages)) {
        setLoadingSessionId(id);
        startPolling(id);
      }
    } catch (e) {
      console.error('load messages failed:', e);
    }
  }

  async function renameSession(id: string, title: string) {
    const trimmed = title.trim();
    try {
      const updated = await updateSession(id, { title: trimmed });
      setSessions((list) => list.map((s) => (s.id === id ? updated : s)));
      if (activeSession()?.id === id) setActiveSession(updated);
    } catch (e) {
      console.error('rename session failed:', e);
    }
  }

  async function newSession(model?: string, provider?: string) {
    stopPolling();
    setLoadingSessionId('');
    setCompacted(false);
    setLoopError(null);
    if (compactedTimer) { clearTimeout(compactedTimer); compactedTimer = null; }
    const session = await createSession(server.directory(), model || selectedModel(), provider || selectedProvider());
    trackSessionStarted({ model: session.model || '', provider: session.provider || '' });
    setSessions((prev) => [session, ...prev]);
    // The transcript is cleared raw, in the same update that puts the new
    // session on screen. setMessages merges, and a merge never takes an empty
    // list as a reason to clear (see mergeTranscript), so it kept the last
    // session's messages: hidden from the transcript, but read by everything
    // else — the context meter opened every new session on the old one's
    // context — and cached under the new session's id.
    batch(() => {
      setMessagesRaw([]);
      setOptimistic([]);
      setHasOlder(false);
      setLoadingOlder(false);
      setActiveSession(session);
    });
    return session;
  }

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

  function stopPolling() {
    stopFastPoll();
    stopBgPoll();
  }

  // Background poll: always active for the current session (15 s interval).
  // Keeps the message list in sync when SSE events are missed or the loop is idle.
  function startBgPoll(sessionId: string) {
    stopBgPoll();
    bgPollInterval = setInterval(async () => {
      if (activeSession()?.id !== sessionId) {
        stopBgPoll();
        return;
      }
      try {
        const page = await fetchNewest(sessionId);
        if (page) applyNewestPage(page);
      } catch (_e) {
        // background — non-critical, ignore errors
      }
    }, 15_000);
  }

  // Check if the agent loop is still active by looking at the last assistant message
  // and whether any tools are still running. A tool-result user message (role=user
  // with tool parts) is created BETWEEN loop iterations — the loop is still running,
  // it just hasn't created the next assistant message yet.
  function isAgentLoopActive(msgs: MessageWithParts[]): boolean {
    // Any running/pending tools means the loop is active
    if (messagesHaveRunningTools(msgs)) return true;
    // If the last message is a user text message (not a tool-result message), the loop
    // has received the prompt but hasn't created an assistant response yet — still active.
    if (msgs.length > 0) {
      const last = msgs[msgs.length - 1];
      if (last.info.role === 'user') {
        const hasText = (last.parts || []).some((p) => p.type === 'text');
        if (hasText) return true;
      }
    }
    // Scan from the end for the last assistant message
    for (let i = msgs.length - 1; i >= 0; i--) {
      if (msgs[i].info.role === 'assistant') {
        // An interruption record means the server already found this turn
        // abandoned and claimed it: no loop is running, and Resume is the way
        // forward. This has to be tested before the finish reason, because the
        // commonest interrupted shape keeps finish="tool_calls" — the turn the
        // model never got to carry on — which the check below would otherwise
        // read as a live loop, leaving the composer stuck in guidance mode on a
        // session nothing is working on.
        if (msgs[i].info.interrupted) return false;
        // Unfinished assistant = still streaming
        if (!msgs[i].info.finish && !msgs[i].info.error) return true;
        // Finished with "stop" or "error" = loop is done
        // Finished with "tool_calls" = loop will continue (but tools should have been caught above)
        if (msgs[i].info.finish === 'tool_calls') return true;
        // finish === "stop" or "error" or "aborted" — loop is done
        return false;
      }
    }
    // No assistant message yet — loop might not have started
    return false;
  }

  // Check if any message in the list has a tool part that is still running or pending.
  // If the last assistant has finished, stale tool statuses are ignored.
  function messagesHaveRunningTools(msgs: MessageWithParts[]): boolean {
    return messagesLiveTools(msgs).length > 0;
  }

  // The tool parts still running or pending, in transcript order. If the last
  // assistant has finished, stale tool statuses are ignored.
  function messagesLiveTools(msgs: MessageWithParts[]): ToolPartData[] {
    // Only treat tools as stale when the loop was explicitly cancelled or errored.
    // finish="stop" alongside pending tools means execution is still in progress.
    let toolsAreStale = false;
    for (let i = msgs.length - 1; i >= 0; i--) {
      if (msgs[i].info.role === 'assistant') {
        const finish = msgs[i].info.finish;
        // A claimed turn (see isAgentLoopActive) has no loop behind it either,
        // so its half-finished tools are stale for the same reason.
        if (finish === 'error' || finish === 'aborted' || msgs[i].info.interrupted) {
          toolsAreStale = true;
        }
        break;
      }
    }
    const live: ToolPartData[] = [];
    if (toolsAreStale) return live;
    for (const msg of msgs) {
      if (msg.parts) {
        for (const part of msg.parts) {
          if (part.type === 'tool') {
            const data = toolData(part);
            const status = data?.state?.status;
            if (status === 'running' || status === 'pending') live.push(data!);
          }
        }
      }
    }
    return live;
  }

  // A tool part's data. The server sends part data as an object, so it is
  // read in place: serializing and re-parsing it — tool output and all — just
  // to look at one field cost more than everything else a poll does.
  function toolData(part: { data: unknown }): ToolPartData | undefined {
    let data: any = part.data;
    if (typeof data === 'string') {
      try {
        data = JSON.parse(data);
      } catch {
        return undefined;
      }
    }
    return data ?? undefined;
  }

  // Fast poll: 3 s, runs only while the agent loop is active.
  // Stops itself (reverts to background poll) when the loop is done.
  function startPolling(sessionId: string) {
    stopFastPoll();
    fastPollInterval = setInterval(async () => {
      try {
        if (activeSession()?.id !== sessionId) {
          stopFastPoll();
          return;
        }
        // Skip if SSE delivered a fresh update in the last 2 s
        if (Date.now() - lastSSEUpdate < 2000) {
          return;
        }
        const page = await fetchNewest(sessionId);
        if (!page) {
          stopFastPoll();
          return;
        }
        applyNewestPage(page);

        const loopActive = isAgentLoopActive(page.messages);

        if (!loopActive) {
          setLoadingSessionId('');
          stopFastPoll(); // background poll keeps running
        } else {
          if (loadingSessionId() !== sessionId) {
            setLoadingSessionId(sessionId);
          }
        }
      } catch (e) {
        console.error('poll messages failed:', e);
      }
    }, 3000);
  }

  async function prompt(content: string, images?: ImagePartData[]) {
    const session = activeSession();
    if (!session) return;
    setLoadingSessionId(session.id);
    setLoopError(null);

    // One id for the message and every part of it. Recomputing 'temp-' + Date.now()
    // per part let the clock tick between them, so a part could end up pointing at
    // a message id that did not exist. The ticks key off this id too.
    const tempId = 'temp-' + Date.now();

    const imageParts = (images || []).map((img, i) => ({
      id: tempId + '-img-' + i,
      messageId: tempId,
      sessionId: session.id,
      type: 'image' as const,
      data: img,
      createdAt: Date.now(),
      updatedAt: Date.now(),
    }));

    // Optimistic: add user message immediately
    const tempUserMsg: MessageWithParts = {
      info: {
        id: tempId,
        sessionId: session.id,
        role: 'user',
        createdAt: Date.now(),
      },
      parts: [
        ...imageParts,
        {
          id: tempId + '-part',
          messageId: tempId,
          sessionId: session.id,
          type: 'text',
          data: { text: content },
          createdAt: Date.now(),
          updatedAt: Date.now(),
        },
      ],
    };
    setOptimistic((prev) => [...prev, tempUserMsg]);

    try {
      trackMessageSent({
        model: selectedModel(),
        provider: selectedProvider(),
        images: (images || []).length,
        length: content.length,
      });
      await sendPrompt(session.id, content, images, selectedModel(), window.innerWidth, window.innerHeight, selectedProvider());
      // Immediately fetch to get the real user message + start seeing assistant
      const page = await fetchNewest(session.id);
      if (!page) return;
      // Swap the bubble for the server's own copy in one update. Batched because
      // separately they would render an intermediate frame — the message twice
      // if the list lands first, or missing if the bubble is dropped first.
      batch(() => {
        applyNewestPage(page);
        setOptimistic((prev) => prev.filter((m) => m.info.id !== tempId));
      });
      // Ensure background poll is running, then start the fast poll for the loop
      startBgPoll(session.id);
      startPolling(session.id);
    } catch (e) {
      console.error('send prompt failed:', e);
      // The bubble stays in the optimistic list, unconfirmed, so mark what
      // happened to it — the server never took it.
      setFailedSends((prev) => new Set(prev).add(tempId));
      setLoadingSessionId('');
      trackErrorShown({ reason: e instanceof Error ? e.name : 'send_failed', surface: 'send' });
    }
  }

  // Resume a session whose last turn was cut short — a rate limit, a dropped
  // connection, a server restart. No new user message is sent: the server picks
  // the conversation up where it broke, so the turn is retried rather than
  // re-described. Returns the server's word on whether anything was resumed.
  async function resume(): Promise<{ resumed: boolean; message?: string }> {
    const session = activeSession();
    if (!session) return { resumed: false };
    setLoadingSessionId(session.id);
    setLoopError(null);
    try {
      const result = await resumeSession(session.id);
      if (!result?.resumed) {
        setLoadingSessionId('');
        return result ?? { resumed: false };
      }
      const page = await fetchNewest(session.id);
      if (!page) return { resumed: true };
      applyNewestPage(page);
      startBgPoll(session.id);
      startPolling(session.id);
      return result;
    } catch (e) {
      console.error('resume failed:', e);
      setLoadingSessionId('');
      const message = e instanceof Error ? e.message : String(e);
      return { resumed: false, message };
    }
  }

  // Mid-loop guidance: inject a new instruction into the running agent loop
  // without starting a new user turn. The guidance is delivered at the top of
  // the next loop iteration. When cancelTool is true, the currently-running
  // tool call is cancelled so the loop can act on the guidance immediately.
  // Returns true if the guidance was accepted (a loop was running), false if
  // no loop was running (the caller should fall back to a regular prompt).
  async function guidance(content: string, cancelTool?: boolean): Promise<boolean> {
    const session = activeSession();
    if (!session) return false;
    // Analytics for mid-loop steering: how many installs use it, and whether
    // they steer once or repeatedly. Deliberately NOT the guidance text — that
    // is the user's own prompt and can carry code, paths and secrets. Length is
    // a measure, not content.
    const n = (guidanceCount.get(session.id) ?? 0) + 1;
    guidanceCount.set(session.id, n);
    const track = (accepted: boolean) => capture('midloop_guidance_sent', {
      accepted,
      cancelled_tool: !!cancelTool,
      length: content.length,
      nth_in_session: n,
    });
    const deliveriesBefore = guidanceDeliveries.get(session.id) ?? 0;
    try {
      await sendGuidance(session.id, content, cancelTool);
      // Accepted by the server, whether or not the user has since navigated away.
      track(true);
      // Guard against session-switch race: if the user navigated to a different
      // session while the request was in flight, don't set the guidance indicator
      // on the destination session — guidance is per-session and must not leak.
      if (activeSession()?.id !== session.id) return true;
      // The loop already drained it while the request was in flight: the
      // "delivered" event is showing, and nothing is left to wait for.
      if ((guidanceDeliveries.get(session.id) ?? 0) !== deliveriesBefore) return true;
      // The server accepted the guidance — show the indicator until the loop
      // picks it up (loop.guidance: delivered) or the loop exits (loop.done).
      if (guidanceTimer) { clearTimeout(guidanceTimer); guidanceTimer = null; }
      setGuidanceStatus('queued');
      return true;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // 409 = no running loop — the caller can fall back to a normal prompt.
      // Worth recording: a user reaching for guidance when nothing is running
      // is a user who expected to be able to steer.
      if (msg.includes('409')) {
        track(false);
        return false;
      }
      console.error('send guidance failed:', e);
      return false;
    }
  }

  // Load sessions on mount
  createEffect(on(server.directory, (dir) => {
    if (dir) refresh();
  }));

  // On SSE reconnect, immediately re-fetch the active session so any messages
  // that arrived while the connection was down are not missed. Only on a
  // reconnect: keyed off `connected` this also ran on the first connection,
  // fetching the page selectSession was already fetching a second time on
  // every open.
  createEffect(on(server.reconnectTick, () => {
    const sess = activeSession();
    if (!sess) return;
    fetchNewest(sess.id).then((page) => {
      if (!page) return;
      applyNewestPage(page);
      lastSSEUpdate = Date.now();
    }).catch(() => {});
  }, { defer: true }));

  // SSE-driven real-time updates: when the backend publishes message.updated
  // or message.part.updated events for the active session, fetch fresh messages
  // immediately instead of waiting for the next poll tick.
  let sseRefreshDebounce: ReturnType<typeof setTimeout> | null = null;
  createEffect(on([server.eventTick, activeSession], ([_tick, sess]) => {
    // Cancel any pending SSE refresh from a previous session
    if (sseRefreshDebounce) {
      clearTimeout(sseRefreshDebounce);
      sseRefreshDebounce = null;
    }
    if (!sess) return;
    const last = server.lastEvent();
    if (!last) return;

    // Handle loop.done: the backend explicitly signals that the agent loop finished.
    // This is the most reliable way to detect completion — clear loading state immediately.
    if (last.type === 'loop.done') {
      const evtSessionId = last.properties?.sessionId;
      if (evtSessionId && evtSessionId === sess.id) {
        // Clear any lingering guidance indicator — the loop is done.
        if (guidanceTimer) { clearTimeout(guidanceTimer); guidanceTimer = null; }
        setGuidanceStatus('idle');
        const reason = last.properties?.reason || '';
        const errText = last.properties?.error || '';
        // Fetch final messages then clear loading
        fetchNewest(sess.id).then((page) => {
          if (!page) return;
          applyNewestPage(page);
          const msgs = page.messages;
          lastSSEUpdate = Date.now();
          setLoadingSessionId('');
          stopFastPoll(); // background poll keeps running
          // Surface the failure only when the transcript does not already
          // explain it. The stream-error and connection-dropped paths write
          // error/interrupted onto the assistant message and render their own
          // banner there; repeating it here would report one stop twice.
          //
          // The last message is not necessarily the assistant's — a turn that
          // died after its tool results were written ends on a user-role
          // message — so scan back for the assistant turn that would be
          // carrying the explanation.
          //
          // A stop that lands between messages (abort checked at the top of
          // the loop iteration, after the tool results were written) leaves no
          // aborted marker anywhere, so nothing explains it. reason==='aborted'
          // then renders the neutral "Generation cancelled" notice instead of
          // an error quoting Go's bare "context canceled". When the abort
          // landed mid-stream the backend marks the assistant message
          // finish==='aborted', the scan below finds it, and no banner draws —
          // the message shows its own notice.
          if (errText) {
            let explained = false;
            for (let i = msgs.length - 1; i >= 0; i--) {
              if (msgs[i].info.role !== 'assistant') continue;
              // finish==='aborted' renders its own "Generation cancelled"
              // notice on the message, so it explains the stop just as much as
              // error/interrupted do. Without it a cancelled turn drew both
              // banners for one event.
              const info = msgs[i].info;
              explained = !!(info.error || info.interrupted || info.finish === 'aborted');
              break;
            }
            if (!explained) {
              setLoopError({ reason: reason || 'error', message: errText });
              trackErrorShown({ reason: reason || 'error', surface: 'loop' });
            }
          }
        }).catch((e) => {
          console.error('loop.done refresh failed:', e);
          // Clear loading even on fetch failure — the loop IS done
          setLoadingSessionId('');
          stopFastPoll(); // background poll keeps running
          // Without this the refetch failing would swallow the very failure
          // this handler exists to surface: the loop stops, the spinner clears
          // and nothing says why — the original bug, back again on the one
          // path that cannot check whether a message already explains it.
          if (errText && activeSession()?.id === sess.id) {
            setLoopError({ reason: reason || 'error', message: errText });
            trackErrorShown({ reason: reason || 'error', surface: 'loop' });
          }
        });
      }
      return;
    }

    // Handle loop.compacted: context was auto-trimmed to fit the model's window
    if (last.type === 'loop.compacted') {
      const evtSessionId = last.properties?.sessionId;
      if (evtSessionId && evtSessionId === sess.id) {
        if (compactedTimer) clearTimeout(compactedTimer);
        setCompacted(true);
        trackCompactTriggered({ origin: 'auto' });
        compactedTimer = setTimeout(() => setCompacted(false), 5000);
      }
      return;
    }

    // Handle loop.guidance: mid-loop guidance was queued or delivered.
    // "queued" = guidance received by the server, waiting for the loop's next
    // iteration; "delivered" = the loop has picked it up and injected it. The
    // server publishes "queued" before the guidance can be drained, so the two
    // always arrive in that order.
    if (last.type === 'loop.guidance') {
      const evtSessionId = last.properties?.sessionId;
      const status = last.properties?.status;
      if (evtSessionId && status === 'delivered') {
        guidanceDeliveries.set(evtSessionId, (guidanceDeliveries.get(evtSessionId) ?? 0) + 1);
      }
      if (evtSessionId && evtSessionId === sess.id) {
        if (status === 'delivered') {
          // Loop picked up the guidance: say it was applied, then clear.
          if (guidanceTimer) clearTimeout(guidanceTimer);
          setGuidanceStatus('delivered');
          guidanceTimer = setTimeout(() => setGuidanceStatus('idle'), 2500);
        } else if (status === 'queued') {
          // Server received guidance, waiting for the loop's next iteration.
          if (guidanceTimer) { clearTimeout(guidanceTimer); guidanceTimer = null; }
          setGuidanceStatus('queued');
        }
      }
      return;
    }

    if (last.type !== 'message.updated' && last.type !== 'message.part.updated' && last.type !== 'message.deleted') return;
    // Only refresh if the event is for the active session
    const evtSessionId = last.properties?.sessionId || last.properties?.id;
    if (evtSessionId && evtSessionId !== sess.id) return;
    // Capture current session ID to detect if session changes before timer fires
    const targetSessionId = sess.id;
    // Debounce: coalesce rapid bursts of events into a single fetch
    sseRefreshDebounce = setTimeout(async () => {
      // Guard: if the user switched sessions while the timer was pending, discard
      if (activeSession()?.id !== targetSessionId) return;
      try {
        const page = await fetchNewest(targetSessionId);
        // Null when the session changed while it was in flight.
        if (!page) return;
        applyNewestPage(page);
        lastSSEUpdate = Date.now();
        // Don't clear loading here — loop.done is the authoritative completion signal.
        // Clearing on message.updated causes premature unblocking when the server
        // writes finish="stop" to the assistant message before executing tool calls.
      } catch (e) {
        console.error('SSE-triggered refresh failed:', e);
      }
    }, 150);
  }));

  // --- Model catalogue updates ---
  // The server refreshes every provider's catalogue in the background (at startup
  // and after a credential change) and publishes models.updated when the new list
  // lands. Re-read the list so an open picker reflects it without a manual
  // reload — this is what lets the fetch happen off the UI's critical path.
  let lastProcessedModelsTick = 0;
  createEffect(on(server.eventTick, (tick) => {
    if (tick === lastProcessedModelsTick) return;
    lastProcessedModelsTick = tick;
    const last = server.lastEvent();
    if (!last || last.type !== 'models.updated') return;
    refreshModels();
  }));

  // --- Session row updates ---
  // Utility calls (title generation, command risk assessment, compaction) spend
  // tokens recorded on the session row, not on a message, so nothing in the
  // message stream carries them. The session row itself also changes off the
  // message stream — most visibly a generated title, and the token totals after
  // a utility call. The server publishes session.updated whenever the row
  // changes; patch the event payload straight into the sidebar list so every
  // session stays current, and re-read the active session so its header and
  // token view reflect it too.
  // Debounced: the risk gate can fire several times in one Auto turn, and one
  // fetch of the accumulated total is enough.
  let lastProcessedSessionTick = 0;
  let sessionRowRefreshDebounce: ReturnType<typeof setTimeout> | null = null;
  createEffect(on(server.eventTick, (tick) => {
    if (tick === lastProcessedSessionTick) return;
    lastProcessedSessionTick = tick;
    const last = server.lastEvent();
    if (!last || last.type !== 'session.updated') return;
    const updated = last.properties as Session | undefined;
    const id = updated?.id;
    if (!id) return;
    setSessions((list) => list.map((s) => (s.id === id ? { ...s, ...updated } : s)));
    if (activeSession()?.id !== id) return;
    if (sessionRowRefreshDebounce) clearTimeout(sessionRowRefreshDebounce);
    sessionRowRefreshDebounce = setTimeout(() => {
      if (activeSession()?.id !== id) return;
      getSession(id).then((fresh) => {
        if (activeSession()?.id === id) setActiveSession(fresh);
      }).catch(() => { /* transient — keep the cached record */ });
    }, 150);
  }));

  // --- Tool permission prompts ---
  // The backend blocks a mutating tool call (bash/write/edit) until the user
  // approves it, publishing permission.requested and, on resolution,
  // permission.replied. We keep a per-session queue (keyed by session id, not by
  // the active session) so a prompt raised in session A survives a switch to
  // session B — the agent loop in A is still blocked on the reply. answer via
  // respondPermission. The tick-guard ensures each event is processed once.
  let lastProcessedPermTick = 0;
  createEffect(on(server.eventTick, (tick) => {
    if (tick === lastProcessedPermTick) return;
    lastProcessedPermTick = tick;
    const last = server.lastEvent();
    if (!last) return;
    const p = (last.properties as any) || {};
    if (last.type === 'permission.requested') {
      const sid = p.sessionId;
      if (!sid) return;
      setPermQueue(sid, (prev) =>
        prev.some((x) => x.permissionId === p.permissionId)
          ? prev
          : [...prev, { permissionId: p.permissionId, tool: p.tool, pattern: p.pattern, input: p.input }],
      );
    } else if (last.type === 'permission.replied') {
      const sid = p.sessionId;
      if (!sid) return;
      setPermQueue(sid, (prev) => prev.filter((x) => x.permissionId !== p.permissionId));
    } else if (last.type === 'question.requested') {
      // The whole batch travels in the event (the loop publishes the Request
      // struct itself), so the dialog can render it without a follow-up fetch.
      const sid = p.sessionId;
      if (!sid || !p.questionId) return;
      setQuestionQueue(sid, (prev) =>
        prev.some((x) => x.questionId === p.questionId)
          ? prev
          : [...prev, { questionId: p.questionId, sessionId: sid, questions: p.questions || [] }],
      );
    } else if (last.type === 'question.replied') {
      const sid = p.sessionId;
      if (!sid) return;
      setQuestionQueue(sid, (prev) => prev.filter((x) => x.questionId !== p.questionId));
    }
  }));

  // Resync on detected event drops: when the server's event sequence gaps (a
  // slow client overflowed the bus buffer), re-fetch the active session's
  // messages so the UI can't be left stale by a lost message.updated event.
  createEffect(on(server.resyncTick, (tick) => {
    if (!tick) return;
    const sess = activeSession();
    if (!sess) return;
    fetchNewest(sess.id).then((page) => {
      if (!page) return;
      applyNewestPage(page);
      lastSSEUpdate = Date.now();
    }).catch(() => {});
  }));

  async function respondPermission(permissionId: string, response: PermissionResponse) {
    const sess = activeSession();
    const tool = sess ? (permQueues()[sess.id] || []).find((x) => x.permissionId === permissionId)?.tool : undefined;
    // Optimistically dismiss so the UI feels instant; the backend also emits
    // permission.replied which reconciles any other client.
    if (sess) setPermQueue(sess.id, (prev) => prev.filter((x) => x.permissionId !== permissionId));
    if (!sess) return;
    trackPermissionPromptAnswered({ tool: tool || 'unknown', response });
    try {
      await replyPermission(sess.id, permissionId, response);
    } catch (e) {
      // 404 = already resolved/cancelled server-side — safe to ignore.
      const msg = e instanceof Error ? e.message : String(e);
      if (!msg.includes('404')) console.error('permission reply failed:', e);
    }
  }

  async function respondQuestion(questionId: string, answers: QuestionAnswerAPI[]) {
    const sess = activeSession();
    // Optimistic dismissal, same as a permission reply; the backend's
    // question.replied event reconciles any other client.
    if (sess) setQuestionQueue(sess.id, (prev) => prev.filter((x) => x.questionId !== questionId));
    if (!sess) return;
    try {
      await replyQuestion(sess.id, questionId, answers);
    } catch (e) {
      // 404 = already answered/cancelled server-side — safe to ignore.
      const msg = e instanceof Error ? e.message : String(e);
      if (!msg.includes('404')) console.error('question reply failed:', e);
    }
  }

  // Global hotkey listener: Alt+1–4 switches the active model to the one
  // registered in that slot. Uses e.code (Digit1–Digit4) for layout independence.
  // On macOS Option+1 sets e.key to "¡" but e.code stays "Digit1".
  const handleHotkey = (e: KeyboardEvent) => {
    if (!e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
    const code = e.code;
    let slot = -1;
    if (code === 'Digit1') slot = 0;
    else if (code === 'Digit2') slot = 1;
    else if (code === 'Digit3') slot = 2;
    else if (code === 'Digit4') slot = 3;
    if (slot < 0) return;
    const modelId = modelSlots()[slot];
    if (!modelId) return; // no model registered for this slot — do nothing
    e.preventDefault();
    e.stopPropagation();
    // Only switch if the model is currently enabled
    const target = models().find((m) => m.id === modelId);
    if (!target?.enabled) return;
    selectModel(modelId, target.providerId);
    showModelSwitchPopup(modelId, slot + 1);
  };

  onMount(() => {
    document.addEventListener('keydown', handleHotkey);
  });
  onCleanup(() => {
    document.removeEventListener('keydown', handleHotkey);
  });

  const value: SessionContextValue = {
    sessions,
    activeSession,
    sessionMissing,
    messages,
    loading,
    loadingOlder,
    hasOlder,
    loadOlder,
    hasRunningTools,
    liveTools,
    compacted,
    guidanceStatus,
    loopError,
    dismissLoopError: () => setLoopError(null),
    pendingPermissions,
    respondPermission,
    pendingQuestions,
    respondQuestion,
    models,
    selectedModel,
    selectedProvider,
    selectModel,
    permissionMode,
    setPermissionMode,
    selectSession,
    renameSession,
    newSession,
    prompt,
    sendFailed,
    guidance,
    abort,
    resume,
    refreshModels,
    reloadModels,
    toggleModel,
    addCustomModel,
    removeCustomModel,
    refresh,
    modelSlots,
    setModelSlot,
    modelSwitchPopup,
    showModelSwitchPopup,
  };

  return (
    <SessionContext.Provider value={value}>
      {props.children}
    </SessionContext.Provider>
  );
};

export function useSession() {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error('useSession must be used within SessionProvider');
  return ctx;
}