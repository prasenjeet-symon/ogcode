import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal, createEffect, on, onMount, onCleanup } from 'solid-js';
import {
  type Session,
  type MessageWithParts,
  type ModelInfo,
  type ImagePartData,
  listSessions,
  createSession,
  getSession,
  getMessages,
  sendPrompt,
  sendGuidance,
  replyPermission,
  type PermissionResponse,
  getModels,
  updateSession,
  abortSession,
  setModelPreference,
  deleteModelPreference,
} from '../api/client';
import { useServer } from './server';

function shallowEqualPart(a: any, b: any): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  if (a.type !== b.type) return false;
  if (a.updatedAt !== b.updatedAt) return false;
  // When timestamps match, the server hasn't modified the part — skip deep
  // data comparison to avoid JSON.stringify stack overflow on large tool output.
  return true;
}

function shallowEqualMessage(a: MessageWithParts, b: MessageWithParts): boolean {
  if (a === b) return true;
  if (a.info.id !== b.info.id) return false;
  if (a.info.finish !== b.info.finish) return false;
  if (a.info.error !== b.info.error) return false;
  const ap = a.parts || [];
  const bp = b.parts || [];
  if (ap.length !== bp.length) return false;
  for (let i = 0; i < ap.length; i++) {
    if (!shallowEqualPart(ap[i], bp[i])) return false;
  }
  return true;
}

export interface PendingPermission {
  permissionId: string;
  tool: string;
  pattern: string;
  input: string;
}

interface SessionContextValue {
  sessions: () => Session[];
  activeSession: () => Session | null;
  messages: () => MessageWithParts[];
  loading: () => boolean;
  hasRunningTools: () => boolean;
  compacted: () => boolean;
  guidanceActive: () => boolean;
  pendingPermissions: () => PendingPermission[];
  respondPermission: (permissionId: string, response: PermissionResponse) => Promise<void>;
  models: () => ModelInfo[];
  selectedModel: () => string;
  selectModel: (modelId: string) => void;
  selectSession: (id: string) => Promise<void>;
  newSession: (model?: string) => Promise<Session>;
  prompt: (content: string, images?: ImagePartData[]) => Promise<void>;
  guidance: (content: string, cancelTool?: boolean) => Promise<boolean>;
  abort: () => Promise<void>;
  refreshModels: () => Promise<void>;
  toggleModel: (model: ModelInfo, enabled: boolean) => Promise<void>;
  addCustomModel: (id: string, providerId: string, displayName: string, collection?: string) => Promise<void>;
  removeCustomModel: (id: string) => Promise<void>;
  refresh: () => void;
  memorySavedTokens: () => number;
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
  const [memorySavedTokens, setMemorySavedTokens] = createSignal(0);
  const [messagesRaw, setMessagesRaw] = createSignal<MessageWithParts[]>([]);
  const messages = messagesRaw;
  // Version counter: incremented on each session selection.
  // SSE handler ignores any event whose version doesn't match — this prevents
  // a race where an SSE event from a previous selection arrives after the new
  // session's API response overwrites the value.

  // Merge incoming messages with the existing array, keeping object references
  // for unchanged entries so SolidJS's <For> doesn't re-render the whole list
  // on every poll tick. Never merge across sessions — always verify we're updating
  // the same session before merging.
  // Uses functional updater to avoid stale reads when polling and SSE update concurrently.
  const mergeMessages = (prev: MessageWithParts[], incoming: MessageWithParts[]): MessageWithParts[] => {
    const currentSessionId = activeSession()?.id;

    // Safety check: if session changed or no messages yet, don't merge — just use incoming
    if (!prev || prev.length === 0 || !currentSessionId) {
      return incoming.map((m) => ({ info: m.info, parts: m.parts || [] }));
    }

    // Verify all messages are for the current session before merging
    for (const msg of incoming) {
      if (msg.info.sessionId !== currentSessionId) {
        // Message is for a different session — don't merge, return as-is
        return incoming.map((m) => ({ info: m.info, parts: m.parts || [] }));
      }
    }

    // Safe to merge now — all messages are for current session
    const prevById = new Map(prev.map((m) => [m.info.id, m]));
    return incoming.map((m) => {
      const normalized = { info: m.info, parts: m.parts || [] };
      const existing = prevById.get(m.info.id);
      if (!existing) return normalized;
      if (shallowEqualMessage(existing, normalized)) return existing;
      // Preserve part references for parts that didn't change
      const newParts = normalized.parts.map((p) => {
        const prevPart = (existing.parts || []).find((pp) => pp.id === p.id);
        if (prevPart && shallowEqualPart(prevPart, p)) return prevPart;
        return p;
      });
      return { info: m.info, parts: newParts };
    });
  };

  const setMessages = (next: MessageWithParts[] | ((prev: MessageWithParts[]) => MessageWithParts[])) => {
    if (typeof next === 'function') {
      // Use functional updater so mergeMessages reads the latest state
      setMessagesRaw((prev) => mergeMessages(prev, (next as (prev: MessageWithParts[]) => MessageWithParts[])(prev)));
    } else {
      setMessagesRaw((prev) => mergeMessages(prev, next));
    }
  };
  // Track which session is currently loading (not a global flag)
  const [loadingSessionId, setLoadingSessionId] = createSignal<string>('');
  // Compute loading state: true only if active session is the one loading
  const loading = () => loadingSessionId() === activeSession()?.id && loadingSessionId() !== '';

  // Check if any tools are currently running or pending.
  // If the last assistant message has finished (stop/error/aborted), stale tool
  // statuses shouldn't block the UI — the loop is done and won't update them.
  const hasRunningTools = (): boolean => {
    const msgs = messagesRaw();
    // Only treat tools as stale when the loop was explicitly cancelled or errored.
    // finish="stop" with pending tools means tools are about to execute — not stale.
    let toolsAreStale = false;
    for (let i = msgs.length - 1; i >= 0; i--) {
      if (msgs[i].info.role === 'assistant') {
        const finish = msgs[i].info.finish;
        if (finish === 'error' || finish === 'aborted') {
          toolsAreStale = true;
        }
        break;
      }
    }
    for (const msg of msgs) {
      if (msg.parts) {
        for (const part of msg.parts) {
          if (part.type === 'tool') {
            try {
              const toolData = JSON.parse(typeof part.data === 'string' ? part.data : JSON.stringify(part.data));
              const status = toolData?.state?.status;
              if (status === 'running' || status === 'pending') {
                if (toolsAreStale) continue;
                return true;
              }
            } catch (e) {
              // Ignore parse errors
            }
          }
        }
      }
    }
    return false;
  };

  // Transient flag: true for 5 s after the server auto-compacts the context window
  const [compacted, setCompacted] = createSignal(false);
  let compactedTimer: ReturnType<typeof setTimeout> | null = null;

  // Transient flag: true while mid-loop guidance is queued/delivered to the
  // running loop. Cleared when the loop picks it up (loop.guidance: delivered)
  // or when the loop exits.
  const [guidanceActive, setGuidanceActive] = createSignal(false);
  let guidanceTimer: ReturnType<typeof setTimeout> | null = null;
  const [pendingPermissions, setPendingPermissions] = createSignal<PendingPermission[]>([]);

  const [models, setModels] = createSignal<ModelInfo[]>([]);
  // Model selection chosen before any session exists (e.g. on the home page).
  // Used as the default for `newSession()` and read by `selectedModel()`.
  // Persisted to localStorage so the user's last model choice survives app restarts.
  const STORAGE_KEY = 'ogcode-selected-model';
  const [pendingModel, setPendingModel] = createSignal<string>(
    typeof localStorage !== 'undefined' ? localStorage.getItem(STORAGE_KEY) || '' : ''
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

  async function selectModel(modelId: string) {
    // Set pendingModel immediately (optimistic) so selectedModel() reflects the change
    // before the network request completes — prevents the old model from being sent if
    // the user sends a prompt quickly after changing the model.
    setPendingModel(modelId);
    // Persist the selection so it survives app restarts — this is the default model
    // for the home page and new sessions.
    try { localStorage.setItem(STORAGE_KEY, modelId); } catch (_e) { /* ignore quota errors */ }
    const sess = activeSession();
    if (!sess) return;
    try {
      const updated = await updateSession(sess.id, { model: modelId });
      setActiveSession(updated);
    } catch (e) {
      console.error('update model failed:', e);
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
      const msgs = await getMessages(sess.id);
      setMessages(msgs);
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

  async function toggleModel(model: ModelInfo, enabled: boolean) {
    try {
      const updated = await setModelPreference({
        id: model.id,
        providerId: model.providerId,
        displayName: model.name,
        enabled,
        isCustom: model.isCustom,
        collection: model.collection,
      });
      setModels(updated || []);
    } catch (e) {
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

  async function removeCustomModel(id: string) {
    try {
      await deleteModelPreference(id);
      await refreshModels();
    } catch (e) {
      console.error('remove custom model failed:', e);
    }
  }

  async function selectSession(id: string) {
    const current = activeSession();
    const sameSession = current?.id === id;

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
    setActiveSession(session);

    // Clear pendingModel when switching sessions so the destination session's
    // own persisted model is used, not whatever was selected in the previous session.
    if (!sameSession) {
      // Use the locally-cached value as a starting point; the API fetch below will
      // replace it with the authoritative value, and SSE events will accumulate on top.
      setMemorySavedTokens(session.memoryTokensSaved ?? 0);
      setPendingModel('');
    }

    // Stop any existing polling from previous session when switching
    if (!sameSession) {
      stopPolling();
      setLoadingSessionId('');
      setCompacted(false);
      if (compactedTimer) { clearTimeout(compactedTimer); compactedTimer = null; }
      // Clear any lingering guidance indicator — guidance is per-session and
      // must not leak into the destination session's UI.
      if (guidanceTimer) { clearTimeout(guidanceTimer); guidanceTimer = null; }
      setGuidanceActive(false);
      setMessages([]);
    }
    // Re-entering the same session keeps cached messages and refreshes in place.
    try {
      const msgs = await getMessages(id);
      setMessages(msgs);

      // Fetch the authoritative session record so we have the real memoryTokensSaved,
      // not the potentially-stale cached value. listSessions filters by the main
      // project directory, so sessions created in task worktrees (which use the
      // worktree path as their directory) won't appear in that list. Fall back to a
      // direct getSession fetch — it queries by session ID, not directory — so the
      // task session's real model is picked up instead of falling back to the default.
      const sessionsList = await listSessions(server.directory());
      setSessions(sessionsList);
      let fresh = sessionsList.find((s) => s.id === id);
      if (!fresh) {
        try {
          fresh = await getSession(id);
        } catch (_e) { /* session may not exist yet — ignore */ }
      }
      if (fresh) {
        setActiveSession(fresh);
        setMemorySavedTokens(fresh.memoryTokensSaved ?? 0);
      }

      // Always keep a background poll so the session stays in sync
      startBgPoll(id);

      // Upgrade to fast poll if the agent loop is still running
      if (isAgentLoopActive(msgs)) {
        setLoadingSessionId(id);
        startPolling(id);
      }
    } catch (e) {
      console.error('load messages failed:', e);
    }
  }

  async function newSession(model?: string) {
    stopPolling();
    setLoadingSessionId('');
    setCompacted(false);
    if (compactedTimer) { clearTimeout(compactedTimer); compactedTimer = null; }
    const session = await createSession(server.directory(), model || selectedModel());
    setSessions((prev) => [session, ...prev]);
    setActiveSession(session);
    setMessages([]);
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
        const msgs = await getMessages(sessionId);
        if (activeSession()?.id !== sessionId) return;
        setMessages(msgs);
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
    // Only treat tools as stale when the loop was explicitly cancelled or errored.
    // finish="stop" alongside pending tools means execution is still in progress.
    let toolsAreStale = false;
    for (let i = msgs.length - 1; i >= 0; i--) {
      if (msgs[i].info.role === 'assistant') {
        const finish = msgs[i].info.finish;
        if (finish === 'error' || finish === 'aborted') {
          toolsAreStale = true;
        }
        break;
      }
    }
    for (const msg of msgs) {
      if (msg.parts) {
        for (const part of msg.parts) {
          if (part.type === 'tool') {
            try {
              const toolData = JSON.parse(typeof part.data === 'string' ? part.data : JSON.stringify(part.data));
              const status = toolData?.state?.status;
              if (status === 'running' || status === 'pending') {
                if (toolsAreStale) continue;
                return true;
              }
            } catch (e) {
              // Ignore parse errors
            }
          }
        }
      }
    }
    return false;
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
        const msgs = await getMessages(sessionId);
        setMessages(msgs);

        const loopActive = isAgentLoopActive(msgs);

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

    const imageParts = (images || []).map((img, i) => ({
      id: 'temp-img-' + Date.now() + '-' + i,
      messageId: 'temp-' + Date.now(),
      sessionId: session.id,
      type: 'image' as const,
      data: img,
      createdAt: Date.now(),
      updatedAt: Date.now(),
    }));

    // Optimistic: add user message immediately
    const tempUserMsg: MessageWithParts = {
      info: {
        id: 'temp-' + Date.now(),
        sessionId: session.id,
        role: 'user',
        createdAt: Date.now(),
      },
      parts: [
        ...imageParts,
        {
          id: 'temp-part-' + Date.now(),
          messageId: 'temp-' + Date.now(),
          sessionId: session.id,
          type: 'text',
          data: { text: content },
          createdAt: Date.now(),
          updatedAt: Date.now(),
        },
      ],
    };
    setMessages((prev) => [...prev, tempUserMsg]);

    try {
      await sendPrompt(session.id, content, images, selectedModel(), window.innerWidth, window.innerHeight);
      // Immediately fetch to get the real user message + start seeing assistant
      const msgs = await getMessages(session.id);
      setMessages(msgs);
      // Ensure background poll is running, then start the fast poll for the loop
      startBgPoll(session.id);
      startPolling(session.id);
    } catch (e) {
      console.error('send prompt failed:', e);
      setLoadingSessionId('');
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
    try {
      await sendGuidance(session.id, content, cancelTool);
      // Guard against session-switch race: if the user navigated to a different
      // session while the request was in flight, don't set the guidance indicator
      // on the destination session — guidance is per-session and must not leak.
      if (activeSession()?.id !== session.id) return true;
      // The server accepted the guidance — show the indicator until the loop
      // picks it up (loop.guidance: delivered) or the loop exits (loop.done).
      if (guidanceTimer) { clearTimeout(guidanceTimer); guidanceTimer = null; }
      setGuidanceActive(true);
      return true;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // 409 = no running loop — the caller can fall back to a normal prompt.
      if (msg.includes('409')) return false;
      console.error('send guidance failed:', e);
      return false;
    }
  }

  // Load sessions on mount
  createEffect(on(server.directory, (dir) => {
    if (dir) refresh();
  }));

  // On SSE reconnect, immediately re-fetch the active session so any messages
  // that arrived while the connection was down are not missed.
  createEffect(on(server.connected, (isConnected) => {
    if (!isConnected) return;
    const sess = activeSession();
    if (!sess) return;
    getMessages(sess.id).then((msgs) => {
      if (activeSession()?.id !== sess.id) return;
      setMessages(msgs);
      lastSSEUpdate = Date.now();
    }).catch(() => {});
  }));

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
        setGuidanceActive(false);
        // Fetch final messages then clear loading
        getMessages(sess.id).then((msgs) => {
          if (activeSession()?.id !== sess.id) return;
          setMessages(msgs);
          lastSSEUpdate = Date.now();
          setLoadingSessionId('');
          stopFastPoll(); // background poll keeps running
        }).catch((e) => {
          console.error('loop.done refresh failed:', e);
          // Clear loading even on fetch failure — the loop IS done
          setLoadingSessionId('');
          stopFastPoll(); // background poll keeps running
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
        compactedTimer = setTimeout(() => setCompacted(false), 5000);
      }
      return;
    }

    // Handle loop.guidance: mid-loop guidance was queued or delivered.
    // "queued" = guidance received by the server, waiting for the loop's next
    // iteration; "delivered" = the loop has picked it up and injected it.
    if (last.type === 'loop.guidance') {
      const evtSessionId = last.properties?.sessionId;
      if (evtSessionId && evtSessionId === sess.id) {
        const status = last.properties?.status;
        if (status === 'delivered') {
          // Loop picked up the guidance — clear the indicator shortly.
          if (guidanceTimer) clearTimeout(guidanceTimer);
          guidanceTimer = setTimeout(() => setGuidanceActive(false), 3000);
        } else if (status === 'queued') {
          // Server received guidance, waiting for the loop's next iteration.
          if (guidanceTimer) { clearTimeout(guidanceTimer); guidanceTimer = null; }
          setGuidanceActive(true);
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
        const msgs = await getMessages(targetSessionId);
        // Double-check session is still active before writing
        if (activeSession()?.id !== targetSessionId) return;
        setMessages(msgs);
        lastSSEUpdate = Date.now();
        // Don't clear loading here — loop.done is the authoritative completion signal.
        // Clearing on message.updated causes premature unblocking when the server
        // writes finish="stop" to the assistant message before executing tool calls.
      } catch (e) {
        console.error('SSE-triggered refresh failed:', e);
      }
    }, 150);
  }));

  // SSE handler for memory.savings.  Use the numeric event-tick to guard against
  // re-reactive firings (e.g. on activeSession change) that would otherwise
  // double-count a delta against the freshly-fetched persisted value.
  let lastProcessedMemoryTick = 0;
  createEffect(on(server.eventTick, (tick) => {
    if (tick === lastProcessedMemoryTick) return;
    lastProcessedMemoryTick = tick;

    const sess = activeSession();
    if (!sess) return;
    const last = server.lastEvent();
    if (!last || last.type !== 'memory.savings') return;
    const evtSessionId = (last.properties as any)?.sessionId;
    if (!evtSessionId || evtSessionId !== sess.id) return;
    const saved = Number((last.properties as any)?.savedTokens ?? 0);
    setMemorySavedTokens((prev) => prev + saved);
  }));

  // --- Tool permission prompts ---
  // The backend blocks a mutating tool call (bash/write/edit) until the user
  // approves it, publishing permission.requested and, on resolution,
  // permission.replied. We keep a per-session queue and answer via
  // respondPermission. The tick-guard ensures each event is processed once.
  let lastProcessedPermTick = 0;
  createEffect(on(server.eventTick, (tick) => {
    if (tick === lastProcessedPermTick) return;
    lastProcessedPermTick = tick;
    const last = server.lastEvent();
    if (!last) return;
    const p = (last.properties as any) || {};
    if (last.type === 'permission.requested') {
      const sess = activeSession();
      if (!sess || p.sessionId !== sess.id) return;
      setPendingPermissions((prev) =>
        prev.some((x) => x.permissionId === p.permissionId)
          ? prev
          : [...prev, { permissionId: p.permissionId, tool: p.tool, pattern: p.pattern, input: p.input }],
      );
    } else if (last.type === 'permission.replied') {
      setPendingPermissions((prev) => prev.filter((x) => x.permissionId !== p.permissionId));
    }
  }));

  // Permission prompts are per-session — drop the queue when switching sessions.
  createEffect(on(activeSession, () => setPendingPermissions([])));

  // Resync on detected event drops: when the server's event sequence gaps (a
  // slow client overflowed the bus buffer), re-fetch the active session's
  // messages so the UI can't be left stale by a lost message.updated event.
  createEffect(on(server.resyncTick, (tick) => {
    if (!tick) return;
    const sess = activeSession();
    if (!sess) return;
    getMessages(sess.id).then((msgs) => {
      if (activeSession()?.id !== sess.id) return;
      setMessages(msgs);
      lastSSEUpdate = Date.now();
    }).catch(() => {});
  }));

  async function respondPermission(permissionId: string, response: PermissionResponse) {
    const sess = activeSession();
    // Optimistically dismiss so the UI feels instant; the backend also emits
    // permission.replied which reconciles any other client.
    setPendingPermissions((prev) => prev.filter((x) => x.permissionId !== permissionId));
    if (!sess) return;
    try {
      await replyPermission(sess.id, permissionId, response);
    } catch (e) {
      // 404 = already resolved/cancelled server-side — safe to ignore.
      const msg = e instanceof Error ? e.message : String(e);
      if (!msg.includes('404')) console.error('permission reply failed:', e);
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
    const enabled = models().some((m) => m.id === modelId && m.enabled);
    if (!enabled) return;
    selectModel(modelId);
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
    messages,
    loading,
    hasRunningTools,
    compacted,
    guidanceActive,
    pendingPermissions,
    respondPermission,
    models,
    selectedModel,
    selectModel,
    selectSession,
    newSession,
    prompt,
    guidance,
    abort,
    refreshModels,
    toggleModel,
    addCustomModel,
    removeCustomModel,
    refresh,
    memorySavedTokens,
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