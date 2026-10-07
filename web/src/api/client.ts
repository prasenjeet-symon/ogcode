const BASE_URL = import.meta.env.VITE_API_URL || '';
const API = `${BASE_URL}/api`;

// Error carrying the HTTP status, so callers can tell "this resource does not
// exist" apart from "the request failed" and react accordingly (e.g. render a
// not-found screen instead of spinning forever).
export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

export function isNotFoundError(e: unknown): boolean {
  return e instanceof ApiError && e.status === 404;
}

export async function fetchAPI<T>(path: string, opts?: RequestInit): Promise<T> {
  const res = await fetch(`${API}${path}`, {
    headers: { 'Content-Type': 'application/json', ...opts?.headers },
    ...opts,
  });
  if (res.status === 204) return undefined as T;
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, `API error ${res.status}: ${text}`);
  }
  return res.json();
}

// Session API
export interface Session {
  id: string;
  projectId: string;
  directory: string;
  title: string;
  model?: string;
  provider?: string;
  sessionType?: string;
  permission?: string;
  compactionSummary?: string;
  utilityTokens?: TokenCounts;
  createdAt: number;
  updatedAt: number;
}

export function listSessions(directory?: string): Promise<Session[]> {
  const dir = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/session${dir}`);
}

export function createSession(directory?: string, model?: string, provider?: string): Promise<Session> {
  return fetchAPI('/session', {
    method: 'POST',
    body: JSON.stringify({ directory, model, provider }),
  });
}

export function updateSession(id: string, updates: { title?: string; model?: string; provider?: string; permission?: string }): Promise<Session> {
  return fetchAPI(`/session/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(updates),
  });
}

export function getSession(id: string): Promise<Session> {
  return fetchAPI(`/session/${id}`);
}

export function deleteSession(id: string): Promise<void> {
  return fetchAPI(`/session/${id}`, { method: 'DELETE' });
}

// Message API
export interface TokenCounts {
  total?: number;
  input?: number;
  output?: number;
  reasoning?: number;
  cacheRead?: number;
  cacheWrite?: number;
}

export interface MessageInfo {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant';
  agent?: string;
  parentId?: string;
  finish?: string;
  error?: string;
  interrupted?: Interruption;
  /** How far this turn got on its way to the model. Assistant messages only. */
  delivery?: Delivery;
  /**
   * Set on a message that belongs in the transcript but is never sent to a
   * model. Mid-loop guidance is recorded this way: the text already reached the
   * model as part of the running turn, so the record exists purely so the user
   * can see what they sent.
   */
  displayOnly?: boolean;
  cost?: number;
  tokens?: TokenCounts;
  createdAt: number;
  /** The model that answered (assistant messages; absent on older ones). */
  model?: string;
  /** The provider that served it. */
  provider?: string;
}

/**
 * How far a turn got on its way to the model, and how long the model took to
 * say its first word. It lives on the assistant message; the prompt it answers
 * is `parentId`, which is how the delivery ticks pair the two.
 */
export interface Delivery {
  /** When the winning request left for the provider. Re-stamped per retry. */
  dispatchedAt?: number;
  /** When the provider answered 200 and the stream opened. */
  connectedAt?: number;
  /** When the first content event arrived — text, reasoning or a tool call. */
  firstTokenAt?: number;
  /** Time to first token: the model's own latency, in ms. */
  ttftMs?: number;
  /** What ogcode spent before the request left — prompt build, compaction, backoff. */
  queuedMs?: number;
  /** How many attempts the connection took. Above 1 means the stream reopened. */
  attempts?: number;
  /** Which event opened the response. */
  firstTokenKind?: 'text' | 'reasoning' | 'tool';
}

export type InterruptReason =
  | 'rate_limit'
  | 'server_error'
  | 'network'
  | 'auth'
  | 'context'
  | 'crashed'
  | 'stalled'
  | 'fatal';

/** Why a turn stopped short, and whether picking it up again is worth trying. */
export interface Interruption {
  reason: InterruptReason;
  resumable: boolean;
  /** One sentence naming what to do about it. The raw provider error is in `error`. */
  detail?: string;
  /** Unix seconds the provider asked us to come back at; absent when it said nothing. */
  retryAfter?: number;
  /** The loop step the turn died on. */
  step?: number;
}

export interface Part {
  id: string;
  messageId: string;
  sessionId: string;
  type: 'text' | 'tool' | 'reasoning' | 'image';
  data: TextPartData | ToolPartData | ReasoningPartData | ImagePartData;
  createdAt: number;
  updatedAt: number;
}

export interface TextPartData {
  text: string;
}

export interface ToolPartData {
  tool: string;
  callId: string;
  state: ToolState;
}

export interface ToolState {
  status: 'pending' | 'running' | 'completed' | 'error' | 'denied';
  input: any;
  output?: string;
  error?: string;
  title?: string;
  metadata?: any;
  image?: { mediaType: string; data: string };
  // Epoch-ms timestamps for when the tool started/finished executing.
  time?: { start?: number; end?: number };
}

export interface ReasoningPartData {
  text: string;
  signature?: string;
  /** Opaque payload of a safety-redacted thinking block. Such a block, and one
   *  whose thinking text the model withheld, has no text to show. */
  redactedData?: string;
  /** The model that produced this block; blocks are only replayable to it. */
  model?: string;
}

// User-uploaded image attachment. Data is base64-encoded image bytes.
export interface ImagePartData {
  mediaType: string;
  data: string;
  name?: string;
}

export interface MessageWithParts {
  info: MessageInfo;
  parts: Part[];
}

/** Messages per transcript page — transcriptPageSize on the server. */
export const TRANSCRIPT_PAGE_SIZE = 300;

/**
 * Messages a refresh asks for: a poll, a live update, the fetch after a prompt.
 * Those only need the newest few — the merge keeps everything older that is
 * held — where the full page of a long session is megabytes, fetched every few
 * seconds while a turn streams.
 */
export const REFRESH_SIZE = 50;

/** One page of a transcript, oldest first. */
export interface MessagesPage {
  messages: MessageWithParts[];
  /** Whether messages older than this page exist. */
  hasOlder: boolean;
}

// The server says whether anything older remains in the X-Has-Older header,
// so the body stays the bare array every other caller reads. Only a response
// without it — a cross-origin server that does not expose it — falls back to
// guessing from the page's length against the size asked for.
async function fetchMessagesPage(path: string, size: number): Promise<MessagesPage> {
  const res = await fetch(`${API}${path}`, { headers: { 'Content-Type': 'application/json' } });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, `API error ${res.status}: ${text}`);
  }
  const messages: MessageWithParts[] = await res.json();
  const header = res.headers.get('X-Has-Older');
  return {
    messages,
    hasOlder: header === null ? messages.length >= size : header === 'true',
  };
}

// The query for one page: the page just older than `before`, or the newest
// one; `limit` messages at most, a full page when unset.
function pageQuery(before?: string, limit?: number): string {
  const q = new URLSearchParams();
  if (before) q.set('before', before);
  if (limit) q.set('limit', String(limit));
  const s = q.toString();
  return s ? `?${s}` : '';
}

/**
 * The newest page of a session's transcript, or the page just older than
 * `before`; `limit` messages at most (a full page by default).
 */
export function getMessagesPage(sessionId: string, before?: string, limit?: number): Promise<MessagesPage> {
  return fetchMessagesPage(`/session/${sessionId}/message${pageQuery(before, limit)}`, limit || TRANSCRIPT_PAGE_SIZE);
}

export function sendPrompt(sessionId: string, content: string, images?: ImagePartData[], model?: string, viewportWidth?: number, viewportHeight?: number, provider?: string): Promise<void> {
  const body: Record<string, unknown> = { content };
  if (images && images.length > 0) body.images = images;
  if (model) body.model = model;
  if (provider) body.provider = provider;
  if (viewportWidth) body.viewportWidth = viewportWidth;
  if (viewportHeight) body.viewportHeight = viewportHeight;
  return fetchAPI(`/session/${sessionId}/prompt`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export type PermissionResponse = 'once' | 'always' | 'reject';

// Answer a pending tool-permission request. The agent loop is blocked waiting on
// this reply; the backend returns 404 when the request is already gone (already
// answered or cancelled), which the caller can safely ignore.
export function replyPermission(sessionId: string, permissionId: string, response: PermissionResponse): Promise<void> {
  return fetchAPI(`/session/${sessionId}/permission/${permissionId}`, {
    method: 'POST',
    body: JSON.stringify({ response }),
  });
}

// List the pending (unanswered) permission requests for a session. The UI uses
// this to restore the approval queue when switching back to a session — the
// agent loop stays blocked on each request even while it is off-screen.
export function listPendingPermissions(sessionId: string): Promise<PendingPermissionAPI[]> {
  return fetchAPI(`/session/${sessionId}/permission`);
}

export interface PendingPermissionAPI {
  permissionId: string;
  sessionId: string;
  tool: string;
  input: string;
  patterns: string[];
}

// One option the model proposed for a question. The dialog always offers a
// free-text field as well, so this set is a suggestion, never a constraint.
export interface QuestionOptionAPI {
  label: string;
  description?: string;
}

// A showWhen condition gates a screen on an answer to an earlier question in
// the same batch. The dialog evaluates it live, so the user only sees the
// branch that applies.
export interface QuestionConditionAPI {
  // The id of an earlier question in this batch.
  question: string;
  // Option labels of that question. The screen shows when any was selected;
  // omitted matches any answer at all (a selection or typed text).
  options?: string[];
  // Invert the match: show when none of the options was selected.
  not?: boolean;
}

export interface QuestionAPI {
  // A short slug a later question can branch on via showWhen.
  id?: string;
  header?: string;
  question: string;
  options?: QuestionOptionAPI[];
  multiSelect?: boolean;
  // Show this screen only when an earlier answer matches. Absent = always show.
  showWhen?: QuestionConditionAPI;
}

// A whole ask_user batch as sent to the UI. It is rendered as one dialog with a
// screen per question.
export interface PendingQuestionAPI {
  questionId: string;
  sessionId: string;
  questions: QuestionAPI[];
}

// The reply to one question: option labels chosen, plus whatever the user typed.
// Either may be empty — an empty answer means "no preference, proceed".
export interface QuestionAnswerAPI {
  selected?: string[];
  text?: string;
}

// Answer a pending ask_user batch. The agent loop is blocked waiting on this
// reply; the backend returns 404 when the batch is already gone (already
// answered or cancelled), which the caller can safely ignore.
export function replyQuestion(sessionId: string, questionId: string, answers: QuestionAnswerAPI[]): Promise<void> {
  return fetchAPI(`/session/${sessionId}/question/${questionId}`, {
    method: 'POST',
    body: JSON.stringify({ answers }),
  });
}

// List the pending (unanswered) ask_user batches for a session. The UI uses this
// to restore the dialog when switching back to a session — the agent loop stays
// blocked on the batch even while it is off-screen.
export function listPendingQuestions(sessionId: string): Promise<PendingQuestionAPI[]> {
  return fetchAPI(`/session/${sessionId}/question`);
}

export function abortSession(sessionId: string): Promise<void> {
  return fetchAPI(`/session/${sessionId}/abort`, { method: 'POST' });
}

export interface ResumeResult {
  resumed: boolean;
  message?: string;
}

/**
 * Restart the agent loop on a session whose last turn was cut short, without
 * sending a new prompt. The conversation up to the break is kept.
 */
export function resumeSession(sessionId: string): Promise<ResumeResult> {
  return fetchAPI(`/session/${sessionId}/resume`, { method: 'POST' });
}

// Mid-loop guidance: inject a new instruction into a running agent loop without
// starting a new user turn. The guidance is delivered to the loop at the top of
// its next iteration. When cancelTool is true, the currently-running tool call
// is cancelled so the loop can act on the guidance immediately. Returns 409
// when no loop is running for the session — the caller should fall back to a
// regular prompt in that case.
export function sendGuidance(sessionId: string, content: string, cancelTool?: boolean): Promise<void> {
  const body: Record<string, unknown> = { content };
  if (cancelTool) body.cancelTool = true;
  return fetchAPI(`/session/${sessionId}/guidance`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

// Config API
export interface ConfigInfo {
  directory: string;
  port: number;
  /** PostHog id the website stamped into the install command, when there is one. */
  installId?: string;
  /** Whether the server found the notes feature flag on for this install. */
  notesEnabled?: boolean;
  /** Whether the server found the device-panel feature flag on for this install. */
  devicePanelEnabled?: boolean;
  /**
   * Whether the server found the plan-mode feature flag on for this install.
   * Gates only the plan-mode instability warning — plan mode itself is
   * reachable either way.
   */
  planModeEnabled?: boolean;
}

export function getConfig(): Promise<ConfigInfo> {
  return fetchAPI('/config');
}

// Resource usage API
export interface ResourceSample {
  at: number;
  /** Resident set size in bytes — what the OS actually holds for the process. */
  rss: number;
  /** Bytes of live Go heap objects. */
  heapInUse: number;
  /** All memory the Go runtime holds from the OS, minus what it released back. */
  goTotal: number;
  /** Top-style: 100 is one saturated core, so it can exceed 100. */
  cpuPercent: number;
  goroutines: number;
}

export interface ResourceSnapshot {
  /** Milliseconds between samples. */
  interval: number;
  cores: number;
  /** Milliseconds since the process started. */
  uptime: number;
  samples: ResourceSample[];
}

export function getResources(): Promise<ResourceSnapshot> {
  return fetchAPI('/resources');
}

// Provider config API
export interface ProviderConfig {
  providerId: string;
  apiKey: string;       // "__SET__" if stored in DB, "" otherwise
  baseUrl: string;        // the persisted value — what the edit form shows
  effectiveBaseUrl: string; // the endpoint the provider is actually calling
  updatedAt: number;
  envKeySet: boolean;     // env var (e.g. ANTHROPIC_API_KEY) is present
  envBaseURLSet: boolean; // env var (e.g. OPENAI_BASE_URL) is present
}

export function getProviderConfigs(): Promise<ProviderConfig[]> {
  return fetchAPI('/providers/config');
}

export function setProviderConfig(id: string, cfg: Omit<ProviderConfig, 'providerId' | 'updatedAt' | 'envKeySet' | 'envBaseURLSet' | 'effectiveBaseUrl'>): Promise<ProviderConfig> {
  return fetchAPI(`/providers/config/${id}`, {
    method: 'POST',
    body: JSON.stringify(cfg),
  });
}

// OGX — the OGLAB plan. Sign-up, payment and plan state live on the web
// side; these calls only start the browser hand-off, report whether the
// resulting token is stored locally, and re-read the plan from the gateway.
// The token itself never reaches the UI.
export interface OGXStatus {
  connected: boolean;
  email?: string;
  plan?: string;
  connectedAt?: number;
  /** Set by refreshOGX: what the live check against the gateway came to. */
  check?: 'ok' | 'revoked' | 'unreachable';
  /** With check "ok": how many models the plan grants right now. */
  models?: number;
}

export function getOGXStatus(): Promise<OGXStatus> {
  return fetchAPI('/ogx/status');
}

/** Asks the gateway what the plan is now and records any change, so a plan
 *  bought or lapsed since connecting shows up without a reconnect. */
export function refreshOGX(): Promise<OGXStatus> {
  return fetchAPI('/ogx/refresh', { method: 'POST' });
}

/** Mints a connect state and returns the OGLAB URL to open in a new tab. */
export function startOGXConnect(): Promise<{ url: string }> {
  return fetchAPI('/ogx/connect', { method: 'POST' });
}

export function disconnectOGX(): Promise<OGXStatus> {
  return fetchAPI('/ogx', { method: 'DELETE' });
}

export interface ValidateResult {
  ok: boolean;
  error?: string;
}

// Tests whether the given credentials work by making a minimal call to the
// provider. Does not persist anything.
export function validateProviderConfig(id: string, cfg: { apiKey: string; baseUrl: string }): Promise<ValidateResult> {
  return fetchAPI(`/providers/config/${id}/validate`, {
    method: 'POST',
    body: JSON.stringify(cfg),
  });
}

// Ollama runtime status — used by the onboarding gate to treat a running
// local Ollama instance as already configured (zero-config flow).
export interface OllamaStatus {
  installed: boolean; // ollama binary found on $PATH
  running: boolean;   // Ollama server responded to a health probe
  baseUrl: string;    // detected/expected base URL
}

export function getOllamaStatus(): Promise<OllamaStatus> {
  return fetchAPI('/providers/ollama/status');
}

// Pricing API — returns model ID → USD per 1 million input tokens
export function getProviderPricing(provider: string): Promise<Record<string, number>> {
  return fetchAPI(`/pricing?provider=${encodeURIComponent(provider)}`);
}

// Usage API — what models cost, priced on the server (internal/usage).
//
// billing says how a model's tokens are paid for: 'metered' is billed per token
// at a known price, 'included' is a flat plan (OGX, Ollama Cloud) or a local
// model that bills nothing per token, 'unpriced' is billed per token at a price
// nothing publishes, and 'unknown' is work whose provider was never recorded
// on a model nothing configured serves today — never counted as billed.
export type Billing = 'metered' | 'included' | 'unpriced' | 'unknown';

export interface UsageTokens {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
  /** Everything but cache reads, as the token pill counts. */
  effective: number;
  /** Everything, cache reads included. */
  total: number;
}

export interface ModelUsage extends UsageTokens {
  provider: string;
  model: string;
  /** The catalogue's display name, when it knows the model. */
  name?: string;
  billing: Billing;
  /**
   * Set when `provider` is empty (work from before providers were recorded)
   * and a configured provider serves the model: the row is billed as that one.
   */
  inferredProvider?: string;
  /**
   * The endpoint host when the provider slot points somewhere other than its
   * own default (the OpenAI slot at api.z.ai); absent on default endpoints.
   */
  host?: string;
  /** Agent steps plus utility calls. */
  calls: number;
  sessions: number;
  /** Billed per token: 0 for included usage, null when the price is unknown. */
  costUsd: number | null;
  /** Included usage at the model's list price; null otherwise. */
  listUsd: number | null;
}

export interface UsageTotals extends UsageTokens {
  calls: number;
  sessions: number;
  /** Everything billed per token at a known price. */
  costUsd: number;
  /** Plan and local usage at list price. */
  includedListUsd: number;
  includedEffective: number;
  /** Effective tokens billed at a price nothing publishes, left out of costUsd. */
  unpricedEffective: number;
  /** Effective tokens whose provider is unknown, also left out of costUsd. */
  unknownEffective: number;
}

export interface SessionUsage {
  models: ModelUsage[];
  totals: UsageTotals;
}

export interface ProjectUsage extends UsageTokens {
  /** The workspace directory. */
  project: string;
  name: string;
  costUsd: number;
  includedListUsd: number;
}

export interface UsageDay {
  /** YYYY-MM-DD in the server's local time. */
  day: string;
  input: number;
  output: number;
  effective: number;
  costUsd: number;
  includedListUsd: number;
}

export interface UsageSummary {
  from: number;
  to: number;
  /** The project the summary is narrowed to; absent for all projects. */
  project?: string;
  /** When the ledger's oldest row was spent; 0 when it is empty. */
  first: number;
  models: ModelUsage[];
  projects: ProjectUsage[];
  days: UsageDay[];
  totals: UsageTotals;
}

/** One session's spend, each step priced at the model that answered it. */
export function getSessionUsage(sessionId: string): Promise<SessionUsage> {
  return fetchAPI(`/session/${sessionId}/usage`);
}

/**
 * A session's whole-transcript token totals — every assistant step plus the
 * session's utility work. Read from the server rather than summed from the
 * messages the client holds, whose window is only the newest transcript page
 * on a long session.
 */
export interface SessionTokens {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
  utility: number;
  effective: number;
  total: number;
}

export function getSessionTokens(sessionId: string): Promise<SessionTokens> {
  return fetchAPI(`/session/${sessionId}/token`);
}

/**
 * Spend since `from` (unix ms; 0 = all of it) in one project — the workspace
 * directory, its task worktrees included — or, with no project, across every
 * project on this machine.
 */
export function getUsageSummary(from = 0, project = ''): Promise<UsageSummary> {
  const q = new URLSearchParams();
  if (from > 0) q.set('from', String(from));
  if (project) q.set('project', project);
  const qs = q.toString();
  return fetchAPI(`/usage${qs ? `?${qs}` : ''}`);
}

// Path API
export interface PathInfo {
  home: string;
  directory: string;
  state: string;
}

export function getPath(): Promise<PathInfo> {
  return fetchAPI('/path');
}

// VCS API
export interface VCSInfo {
  branch: string;
  isGitRepo: boolean;
  hasRemote: boolean;
  ghInstalled: boolean;
}

export function getVCS(): Promise<VCSInfo> {
  return fetchAPI('/vcs');
}

// Models API
export interface ModelInfo {
  id: string;
  name: string;
  providerId: string;
  default: boolean;
  enabled: boolean;
  isCustom: boolean;
  // Collection is an optional group name for custom models added via an
  // OpenAI-compatible provider (Gemini, DeepSeek, Groq, …) so they can be
  // grouped together in the UI. Empty for built-in models.
  collection: string;
  inputPricePerM: number;
  outputPricePerM: number;
  // The context window the agent loop sizes compaction against (catalogue, else
  // learned from an overflow error; absent = unknown), and the request size at
  // which the loop compacts. Read by the context meter.
  contextWindow?: number;
  compactAtTokens?: number;
}

export function getModels(): Promise<ModelInfo[]> {
  return fetchAPI('/models');
}

// Force the server to clear each provider's cached catalogue and re-fetch it
// live from the endpoint, returning the updated list. Used after a credential
// or base-URL change so new models appear without restarting ogcode.
export function refreshModels(): Promise<ModelInfo[]> {
  return fetchAPI('/models/refresh', { method: 'POST' });
}

export interface ModelPreference {
  id: string;
  providerId: string;
  displayName: string;
  enabled: boolean;
  isCustom: boolean;
  collection: string;
}

export function setModelPreference(pref: ModelPreference): Promise<ModelInfo[]> {
  return fetchAPI('/models/preference', {
    method: 'POST',
    body: JSON.stringify(pref),
  });
}

export function deleteModelPreference(id: string, providerId: string): Promise<void> {
  return fetchAPI(`/models/preference/${encodeURIComponent(id)}?providerId=${encodeURIComponent(providerId)}`, {
    method: 'DELETE',
  });
}

// Theme API
export interface Theme {
  directory: string;
  primaryColor: string;
  accent: string;
  accentHover: string;
  accentSoft: string;
  accentRing: string;
  onPrimary: string;
  glow: string;
  tint: string;
}

export function getTheme(directory?: string): Promise<Theme> {
  const dir = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/theme${dir}`);
}

export function setTheme(primaryColor: string, directory?: string): Promise<Theme> {
  return fetchAPI('/theme', {
    method: 'POST',
    body: JSON.stringify({ primaryColor, directory }),
  });
}

export function deleteTheme(directory: string): Promise<void> {
  return fetchAPI(`/theme/${encodeURIComponent(directory)}`, { method: 'DELETE' });
}

// Mode API
export interface ModeInfo {
  mode: string;
}

export function getMode(): Promise<ModeInfo> {
  return fetchAPI('/mode');
}

// Git sync API — whether the working-dir branch is in sync with its upstream.
export interface GitSyncStatus {
  isRepo: boolean;
  branch: string;
  hasUpstream: boolean;
  upstream: string;
  ahead: number;
  behind: number;
  fetched: boolean;
  fetchError?: string;
}

export function getGitSync(): Promise<GitSyncStatus> {
  return fetchAPI('/git/sync');
}

// Git working-tree & commit diff API.
export interface GitFileStatus {
  path: string;
  x: string;
  y: string;
  staged: boolean;
}

export interface GitCommit {
  sha: string;
  short: string;
  message: string;
  author: string;
  time: string;
}

export function getGitStatus(directory?: string): Promise<{ isRepo: boolean; files: GitFileStatus[] }> {
  const params = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/git/status${params}`);
}

export function getGitCommits(directory?: string, n?: number): Promise<GitCommit[]> {
  const parts: string[] = [];
  if (directory) parts.push(`directory=${encodeURIComponent(directory)}`);
  if (n) parts.push(`n=${n}`);
  const params = parts.length ? `?${parts.join('&')}` : '';
  return fetchAPI(`/git/commits${params}`);
}

export function getGitCommitDiff(sha: string, directory?: string): Promise<{ diff: string }> {
  const params = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/git/commit/${encodeURIComponent(sha)}${params}`);
}

export function getGitFileDiff(path: string, staged: boolean, directory?: string): Promise<{ diff: string }> {
  const parts: string[] = [`path=${encodeURIComponent(path)}`];
  if (staged) parts.push('staged=true');
  if (directory) parts.push(`directory=${encodeURIComponent(directory)}`);
  return fetchAPI(`/git/diff?${parts.join('&')}`);
}

export function stageGitFiles(paths: string[], directory?: string): Promise<{ ok: boolean }> {
  const params = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/git/stage${params}`, {
    method: 'POST',
    body: JSON.stringify({ paths }),
  });
}

export function unstageGitFiles(paths: string[], directory?: string): Promise<{ ok: boolean }> {
  const params = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/git/unstage${params}`, {
    method: 'POST',
    body: JSON.stringify({ paths }),
  });
}

export function commitGitChanges(message: string, directory?: string): Promise<{ ok: boolean }> {
  const params = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/git/commit${params}`, {
    method: 'POST',
    body: JSON.stringify({ message }),
  });
}

// Plan API
export interface Plan {
  id: string;
  sessionId: string;
  projectId: string;
  directory: string;
  title: string;
  status: 'open' | 'locked';
  model?: string;
  provider?: string;
  compactionSummary?: string;
  breakdownStatus?: '' | 'in_progress' | 'completed' | 'failed';
  breakdownWarnings?: string;
  allTasksCompleted?: boolean;
  createdAt: number;
  updatedAt: number;
}

export interface Task {
  id: string;
  planId: string;
  sessionId?: string;
  parentTaskId?: string;
  title: string;
  description: string;
  effort: 'S' | 'M' | 'L' | 'XL';
  complexity: 'low' | 'medium' | 'high';
  status: 'pending' | 'in_progress' | 'completed' | 'failed';
  dependencies: string[];
  branchName: string;
  worktreePath?: string;
  prUrl?: string;
  prNumber?: number;
  prError?: string;
  model?: string;
  provider?: string;
  orderIndex: number;
  createdAt: number;
  updatedAt: number;
}

export function listPlans(directory?: string): Promise<Plan[]> {
  const dir = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/plans${dir}`);
}

export function createPlan(directory?: string, title?: string, model?: string, provider?: string): Promise<Plan> {
  return fetchAPI('/plans', {
    method: 'POST',
    body: JSON.stringify({ directory, title, model, provider }),
  });
}

export function getPlan(id: string): Promise<Plan> {
  return fetchAPI(`/plans/${id}`);
}

export function updatePlan(id: string, updates: { title?: string; model?: string; provider?: string }): Promise<Plan> {
  return fetchAPI(`/plans/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(updates),
  });
}

export function deletePlan(id: string): Promise<void> {
  return fetchAPI(`/plans/${id}`, { method: 'DELETE' });
}

export function lockPlan(id: string): Promise<Plan> {
  return fetchAPI(`/plans/${id}/lock`, { method: 'POST' });
}

export function sendPlanPrompt(id: string, content: string, model?: string, viewportWidth?: number, viewportHeight?: number, provider?: string): Promise<void> {
  const body: Record<string, unknown> = { content };
  if (model) body.model = model;
  if (provider) body.provider = provider;
  if (viewportWidth) body.viewportWidth = viewportWidth;
  if (viewportHeight) body.viewportHeight = viewportHeight;
  return fetchAPI(`/plans/${id}/prompt`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

/**
 * The newest page of a plan's transcript, or the page just older than
 * `before`; `limit` messages at most (a full page by default).
 */
export function getPlanMessagesPage(id: string, before?: string, limit?: number): Promise<MessagesPage> {
  return fetchMessagesPage(`/plans/${id}/message${pageQuery(before, limit)}`, limit || TRANSCRIPT_PAGE_SIZE);
}

export function abortPlan(id: string): Promise<void> {
  return fetchAPI(`/plans/${id}/abort`, { method: 'POST' });
}

export async function downloadPlanExport(id: string): Promise<void> {
  const res = await fetch(`${API}/plans/${id}/export`);
  if (!res.ok) throw new Error(`Export failed: ${res.status}`);
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  const disp = res.headers.get('Content-Disposition') || '';
  const match = disp.match(/filename="(.+)"/);
  a.download = match?.[1] || 'plan.md';
  a.click();
  URL.revokeObjectURL(url);
}

// Task API
export function listTasks(planId: string): Promise<Task[]> {
  return fetchAPI(`/plans/${planId}/tasks`);
}

export function createTasks(planId: string, tasks: Array<{
  title: string;
  description?: string;
  effort?: string;
  complexity?: string;
  dependencies?: string[];
  orderIndex?: number;
}>): Promise<Task[]> {
  return fetchAPI(`/plans/${planId}/tasks`, {
    method: 'POST',
    body: JSON.stringify({ tasks }),
  });
}

export function getTask(id: string): Promise<Task> {
  return fetchAPI(`/tasks/${id}`);
}

export function updateTask(id: string, updates: {
  title?: string;
  description?: string;
  effort?: string;
  complexity?: string;
  status?: string;
  branchName?: string;
  model?: string;
  provider?: string;
}): Promise<Task> {
  return fetchAPI(`/tasks/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(updates),
  });
}

export function startTask(id: string): Promise<Task> {
  return fetchAPI(`/tasks/${id}/start`, { method: 'POST' });
}

export function completeTask(id: string): Promise<Task> {
  return fetchAPI(`/tasks/${id}/complete`, { method: 'POST' });
}

export function failTask(id: string): Promise<Task> {
  return fetchAPI(`/tasks/${id}/fail`, { method: 'POST' });
}

export function retryTask(id: string): Promise<Task> {
  return fetchAPI(`/tasks/${id}/retry`, { method: 'POST' });
}

// Notes API
export interface Note {
  id: string;
  directory: string;
  title: string;
  query: string;
  content: string;
  sessionId?: string;
  status: 'generating' | 'done' | 'error';
  source: 'ai' | 'manual';
  version: number;
  createdAt: number;
  updatedAt: number;
}

export interface NoteVersion {
  id: string;
  noteId: string;
  version: number;
  content: string;
  createdAt: number;
}

export function listNotes(directory?: string): Promise<Note[]> {
  const dir = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/notes${dir}`);
}

export function createNote(query: string, directory?: string, model?: string, sessionId?: string, viewportWidth?: number, viewportHeight?: number, source?: string, provider?: string): Promise<Note> {
  const body: Record<string, unknown> = { query, directory, model };
  if (provider) body.provider = provider;
  if (sessionId) body.sessionId = sessionId;
  if (viewportWidth) body.viewportWidth = viewportWidth;
  if (viewportHeight) body.viewportHeight = viewportHeight;
  if (source) body.source = source;
  return fetchAPI('/notes', {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export function getNote(id: string): Promise<Note> {
  return fetchAPI(`/notes/${id}`);
}

export function updateNote(id: string, title: string, content: string): Promise<Note> {
  return fetchAPI(`/notes/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ title, content }),
  });
}

export function deleteNote(id: string): Promise<void> {
  return fetchAPI(`/notes/${id}`, { method: 'DELETE' });
}

export function transformText(text: string, instruction: string, model?: string, provider?: string): Promise<{ result: string }> {
  return fetchAPI('/notes/transform', {
    method: 'POST',
    body: JSON.stringify({ text, instruction, model, provider }),
  });
}

export function listNoteVersions(noteId: string): Promise<NoteVersion[]> {
  return fetchAPI(`/notes/${noteId}/versions`);
}

export async function downloadNoteExport(noteId: string): Promise<void> {
  const res = await fetch(`${API}/notes/${noteId}/export`);
  if (!res.ok) throw new Error(`Export failed: ${res.status}`);
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  const disp = res.headers.get('Content-Disposition') || '';
  const match = disp.match(/filename="(.+)"/);
  a.download = match?.[1] || 'note.md';
  a.click();
  URL.revokeObjectURL(url);
}

// Version API
export interface VersionInfo {
  version: string;
  commit: string;
  date: string;
  goVersion: string;
}

export interface UpdateInfo {
  latestVersion: string;
  updateAvailable: boolean;
  releaseUrl: string;
  publishedAt: string;
  releaseNotes: string;
  installCommand: string;
}

export interface VersionResponse {
  version: string;
  commit: string;
  date: string;
  goVersion: string;
  latestVersion: string;
  updateAvailable: boolean;
  releaseUrl: string;
  publishedAt: string;
  releaseNotes: string;
  installCommand: string;
}

export function getVersion(): Promise<VersionResponse> {
  return fetchAPI('/version');
}

export function checkForUpdate(): Promise<UpdateInfo> {
  return fetchAPI('/version/check', { method: 'POST' });
}

// Search Config API
export type SearchProvider = 'native' | 'tavily';

// Which engine leads the native backend: '' = HTTP (the fast default) first
// with Safari as fallback, 'native' = HTTP only, 'safari' = Safari first.
export type SearchBrowser = '' | 'native' | 'safari';

export interface SearchConfig {
  enabled: boolean;
  // Which search backend answers web_search/fetch_page: the built-in native
  // engine, or a third-party provider (Tavily).
  provider: SearchProvider;
  // Tavily API key. On read it is the sentinel '__SET__' when a key is stored
  // (never the real value) or '' when none is. On write, echo '__SET__' back to
  // keep the stored key untouched.
  tavilyApiKey: string;
  // Native-backend tuning: how many pages a deep search extracts, how many
  // characters are kept per page, and which engine leads. OGCODE_SEARCH_*
  // environment variables override these when set.
  fetchTopK: number;
  pageChars: number;
  browser: SearchBrowser;
  // True when TAVILY_API_KEY is set in the server's environment (read-only hint).
  tavilyEnvKeySet?: boolean;
  updatedAt?: number;
}

export function getSearchConfig(): Promise<SearchConfig> {
  return fetchAPI('/search/config');
}

export function setSearchConfig(
  cfg: Omit<SearchConfig, 'updatedAt' | 'tavilyEnvKeySet'>,
): Promise<SearchConfig> {
  return fetchAPI('/search/config', {
    method: 'POST',
    body: JSON.stringify(cfg),
  });
}

// validateSearchKey tests a third-party search key without persisting it. Send
// '__SET__' (or '') to test the already-stored key. Always resolves with the
// outcome; it does not throw on an invalid key.
export function validateSearchKey(tavilyApiKey: string): Promise<{ ok: boolean; error?: string }> {
  return fetchAPI('/search/config/validate', {
    method: 'POST',
    body: JSON.stringify({ tavilyApiKey }),
  });
}

// ─── Doc Index API ───

export interface DocPageEntry {
  id: string;
  docPath: string;
  pageNum: number;
  keywords: string[];
  labels: string[];
  indexedAt: number;
}

export interface DocSummary {
  docPath: string;
  pageCount: number;
  pages?: DocPageEntry[]; // omitted from the docs listing; present only when full pages are attached
  indexedAt: number;
}

export interface DocIndexBuildStatus {
  running: boolean;
  total?: number;
  completed?: number;
  failed?: number;
  percent?: number;
}

export function getDocIndexBuildStatus(): Promise<DocIndexBuildStatus> {
  return fetchAPI('/docindex/build');
}

export function buildDocIndex(directory?: string, rebuild = false, model?: string, provider?: string): Promise<{ running: boolean }> {
  return fetchAPI('/docindex/build', {
    method: 'POST',
    body: JSON.stringify({ directory, rebuild, model, provider }),
  });
}

export function getIndexedDocs(directory?: string): Promise<DocSummary[]> {
  const params = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/docindex/docs${params}`);
}

export interface IndexFile {
  path: string;
  indexed: boolean;
  pageCount: number;
  indexedAt: number;
}

export function getIndexFiles(directory?: string): Promise<IndexFile[]> {
  const params = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/docindex/files${params}`);
}

export interface DocContent {
  path: string;
  content: string;
  size: number;
  truncated: boolean;
  binary: boolean;
}

export function getDocContent(docPath: string, directory?: string): Promise<DocContent> {
  const dir = directory ? `&directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/docindex/docs/content?path=${encodeURIComponent(docPath)}${dir}`);
}

// What the index recorded for one file: its pages with the labels and keywords
// each was filed under. Rejects with a 404 ApiError for a file not in the index.
export function getIndexedDoc(docPath: string, directory?: string): Promise<DocSummary> {
  const dir = directory ? `&directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/docindex/docs/entry?path=${encodeURIComponent(docPath)}${dir}`);
}

// URL an <img> loads a workspace image from — the Markdown preview rewrites a
// document's relative image paths to it. Images only; the server refuses the rest.
export function docAssetURL(docPath: string, directory?: string): string {
  const dir = directory ? `&directory=${encodeURIComponent(directory)}` : '';
  return `${API}/docindex/docs/raw?path=${encodeURIComponent(docPath)}${dir}`;
}

export interface IndexPlan {
  total: number;
  pending: number;
  indexed: number;
  stale: number;
  pdf: number;
  docx: number;
  text: number;
  pendingPdf: number;
  pendingDocx: number;
  pendingText: number;
}

export function getIndexPlan(directory?: string): Promise<IndexPlan> {
  const params = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/docindex/preview${params}`);
}

export interface GitignoreRule {
  line: number;
  pattern: string;
  negated: boolean;
}

export interface GitignoreInfo {
  path: string;
  exists: boolean;
  rules: GitignoreRule[];
  nested: string[];
  truncated: boolean;
}

export function getGitignoreInfo(directory?: string): Promise<GitignoreInfo> {
  const params = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/docindex/gitignore${params}`);
}

export interface ExcludeEntry {
  id: string;
  directory: string;
  pattern: string;
  createdAt: number;
  // True for a pattern ogcode ships rather than one the user wrote. Derived
  // from the shipped list server-side, so it cannot drift from that list.
  default?: boolean;
}

export function getExcludes(directory?: string): Promise<ExcludeEntry[]> {
  const params = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/docindex/excludes${params}`);
}

export function addExclude(directory: string, pattern: string): Promise<ExcludeEntry> {
  return fetchAPI('/docindex/excludes', {
    method: 'POST',
    body: JSON.stringify({ directory, pattern }),
  });
}

export function deleteExclude(id: string): Promise<void> {
  return fetchAPI(`/docindex/excludes/${id}`, { method: 'DELETE' });
}

// Skills API
export interface Skill {
  name: string;
  description: string;
  source: string;
  // False when the skill is disabled (denied) for this project: it is then
  // withheld from the agent's prompt entirely, so its frontmatter costs no
  // tokens. The settings list still shows it, switched off, so it can be
  // turned back on.
  enabled: boolean;
}

export function listSkills(): Promise<Skill[]> {
  return fetchAPI('/skills');
}

// Enable or disable a skill for this project. Disabling writes a "deny" rule
// into the project's ogcode.json and hides the skill from the agent; enabling
// removes it. Returns the skill's updated state. Takes effect on the next turn
// — no restart.
export function setSkillEnabled(name: string, enabled: boolean): Promise<Skill> {
  return fetchAPI(`/skills/${encodeURIComponent(name)}`, {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  });
}

// MCP API
export interface MCPServer {
  name: string;
  // Transport ogcode uses to reach the server: "stdio" | "http" | "sse".
  transport: string;
  // What the server is — the command line (stdio) or URL (http/sse). Never a
  // secret; headers and tokens are excluded.
  target: string;
  // Where the config lives after any toggle: "project" | "global".
  scope: string;
  // How it authenticates: "oauth" (tokens on disk, kept when disabled),
  // "headers" (static token in config), or "none".
  auth: string;
  // False when disabled: ogcode neither connects to it nor exposes its tools,
  // so nothing about it reaches the agent (no tokens spent on tool schemas).
  // Config and stored auth tokens are untouched.
  enabled: boolean;
  // Live connection state (meaningful only when enabled).
  connected: boolean;
  toolCount: number;
  // Last connect error, if any (e.g. server down, or awaiting OAuth).
  error?: string;
}

export function listMCPServers(): Promise<MCPServer[]> {
  return fetchAPI('/mcp');
}

// Enable or disable an MCP server for this project. Disabling tears down the
// connection and removes its tools from the agent's toolset; enabling
// reconnects and registers them. Writes the choice into the project's
// ogcode.json; auth tokens are never touched. Takes effect on the next turn.
export function setMCPEnabled(name: string, enabled: boolean): Promise<MCPServer> {
  return fetchAPI(`/mcp/${encodeURIComponent(name)}`, {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  });
}

// Scrcpy API
// State of the separately-run ws-scrcpy device UI that /scrcpy proxies to.
export interface ScrcpyStatus {
  // True when ws-scrcpy answers HTTP on its port, so the panel can embed it.
  up: boolean;
  // Where the reverse proxy forwards (default http://127.0.0.1:8000).
  target: string;
}

export function getScrcpyStatus(): Promise<ScrcpyStatus> {
  return fetchAPI('/scrcpy/status');
}

// One adb-attached device the device picker can stream.
export interface ScrcpyDevice {
  // The adb serial ("emulator-5554", a USB serial, an IP:port).
  serial: string;
  // adb's word for it: "device", "offline", "unauthorized", ….
  state: string;
  // Human name adb -l reports ("" when it does not).
  model: string;
}

export function getScrcpyDevices(): Promise<{ devices: ScrcpyDevice[] }> {
  return fetchAPI('/scrcpy/devices');
}

// Preview API
// One loopback service the preview grid offers.
export interface PreviewService {
  // The loopback port the service listens on.
  port: number;
  // The service's own <title>, or '' when it served none (the grid falls back
  // to the port).
  title: string;
  // Where the reverse proxy dials (always http://127.0.0.1:<port>).
  target: string;
  // True when the service answered, so the grid can embed it rather than
  // greying the tile. Always false for an unpublished port, which the server
  // never probes.
  up: boolean;
  // True when the service answered with a text/html page. A service that is
  // up but served JSON or plain text (a raw API, a non-HTML listener), or that
  // redirects off its own origin, is not embeddable, so the grid shows it as
  // up-but-not-embeddable instead of loading a white box.
  html: boolean;
  // True when the port is served at its preview hostname: the agent handed
  // back its live-preview URL, or the user added it. A port the page only
  // named (a ?port= deep link) is listed unpublished, so the user can add it —
  // its hostname answers 403 until they do.
  published: boolean;
  // The URL the browser opens to reach the service at its own origin —
  // http://<port>.<preview-domain>/ prefixed with this server's scheme and
  // port. Each service answers at the root of that origin so an app that reads
  // its own location (Next.js and friends) boots normally.
  url: string;
}

// getPreviewServices lists the services for the preview grid: the ports
// published for this directory — announced by the agent or added by the user —
// plus the ports given here (a ?port= deep link), so the grid can offer to
// publish a port it was pointed at. Scoping to a directory keeps the grid to
// the project in view rather than every service on the machine.
export function getPreviewServices(ports: number[], directory?: string): Promise<{ services: PreviewService[] }> {
  const params = new URLSearchParams();
  if (ports.length) params.set('ports', ports.join(','));
  if (directory) params.set('directory', directory);
  const q = params.toString();
  return fetchAPI(`/preview/services${q ? `?${q}` : ''}`);
}

// publishPreviewPort adds a port to this project's previews: its tile is
// listed and its preview hostname starts being served.
export function publishPreviewPort(port: number, directory?: string): Promise<{ port: number; url: string }> {
  return fetchAPI('/preview/ports', {
    method: 'POST',
    body: JSON.stringify({ port, directory }),
  });
}

// unpublishPreviewPort takes a port off the previews: its tile goes and its
// preview hostname stops being served (until the agent announces it again).
export function unpublishPreviewPort(port: number): Promise<void> {
  return fetchAPI(`/preview/ports/${port}`, { method: 'DELETE' });
}
