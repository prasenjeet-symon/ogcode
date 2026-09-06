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
  permission?: string;
  compactionSummary?: string;
  memoryTokensSaved?: number;
  createdAt: number;
  updatedAt: number;
}

export function listSessions(directory?: string): Promise<Session[]> {
  const dir = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/session${dir}`);
}

export function createSession(directory?: string, model?: string): Promise<Session> {
  return fetchAPI('/session', {
    method: 'POST',
    body: JSON.stringify({ directory, model }),
  });
}

export function updateSession(id: string, updates: { title?: string; model?: string; permission?: string }): Promise<Session> {
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
  cost?: number;
  tokens?: TokenCounts;
  createdAt: number;
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

export function getMessages(sessionId: string): Promise<MessageWithParts[]> {
  return fetchAPI(`/session/${sessionId}/message`);
}

export function sendPrompt(sessionId: string, content: string, images?: ImagePartData[], model?: string, viewportWidth?: number, viewportHeight?: number): Promise<void> {
  const body: Record<string, unknown> = { content };
  if (images && images.length > 0) body.images = images;
  if (model) body.model = model;
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
  memoryEnabled: boolean;
  memoryProvider: string;
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

export interface ResourceActivity {
  /** What the process is busy with, e.g. "embedding memory". */
  label: string;
  done: number;
  total: number;
}

export interface ResourceSnapshot {
  /** Milliseconds between samples. */
  interval: number;
  cores: number;
  /** Milliseconds since the process started. */
  uptime: number;
  /** Absent when nothing long-running is labelling itself. */
  activity?: ResourceActivity | null;
  samples: ResourceSample[];
}

export function getResources(): Promise<ResourceSnapshot> {
  return fetchAPI('/resources');
}

// Memory config API
export interface MemoryConfig {
  enabled: boolean;
  updatedAt: number;
}

export function getMemoryConfig(): Promise<MemoryConfig> {
  return fetchAPI('/memory/config');
}

export function setMemoryConfig(cfg: Omit<MemoryConfig, 'updatedAt'>): Promise<MemoryConfig> {
  return fetchAPI('/memory/config', {
    method: 'POST',
    body: JSON.stringify(cfg),
  });
}

// Re-embed every stored memory document and graph node against the current
// embedding model. Use after switching embedding providers, which invalidates
// existing vectors (dimension or model mismatch).
export function reindexMemory(): Promise<{ status: string }> {
  return fetchAPI('/memory/reindex', { method: 'POST' });
}

// Wipe all memory tables (documents, nodes, edges, collections). Destructive,
// irreversible — confirm before calling.
export function resetMemory(): Promise<{ status: string }> {
  return fetchAPI('/memory/reset', { method: 'POST' });
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

// Free-tier providers sourced from the shared community key pool (a public
// GitHub-hosted JSON of OpenAI-compatible providers). Their presence lets the
// onboarding gate skip the credential wizard — the user can start chatting
// immediately without configuring any keys. Keys are never exposed by the
// backend; only the collection, base URL, and default model are returned.
export interface FreeProvider {
  collection: string;
  baseUrl: string;
  defaultModel: string;
}

export function getFreeProviders(): Promise<FreeProvider[]> {
  return fetchAPI('/providers/free');
}

// Pricing API — returns model ID → USD per 1 million input tokens
export function getProviderPricing(provider: string): Promise<Record<string, number>> {
  return fetchAPI(`/pricing?provider=${encodeURIComponent(provider)}`);
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

export function deleteModelPreference(id: string): Promise<void> {
  return fetchAPI(`/models/preference/${encodeURIComponent(id)}`, { method: 'DELETE' });
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
  orderIndex: number;
  createdAt: number;
  updatedAt: number;
}

export function listPlans(directory?: string): Promise<Plan[]> {
  const dir = directory ? `?directory=${encodeURIComponent(directory)}` : '';
  return fetchAPI(`/plans${dir}`);
}

export function createPlan(directory?: string, title?: string, model?: string): Promise<Plan> {
  return fetchAPI('/plans', {
    method: 'POST',
    body: JSON.stringify({ directory, title, model }),
  });
}

export function getPlan(id: string): Promise<Plan> {
  return fetchAPI(`/plans/${id}`);
}

export function updatePlan(id: string, updates: { title?: string; model?: string }): Promise<Plan> {
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

export function sendPlanPrompt(id: string, content: string, model?: string, viewportWidth?: number, viewportHeight?: number): Promise<void> {
  const body: Record<string, unknown> = { content };
  if (model) body.model = model;
  if (viewportWidth) body.viewportWidth = viewportWidth;
  if (viewportHeight) body.viewportHeight = viewportHeight;
  return fetchAPI(`/plans/${id}/prompt`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export function getPlanMessages(id: string, before?: string): Promise<MessageWithParts[]> {
  const params = before ? `?before=${encodeURIComponent(before)}` : '';
  return fetchAPI(`/plans/${id}/message${params}`);
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

export function createNote(query: string, directory?: string, model?: string, sessionId?: string, viewportWidth?: number, viewportHeight?: number, source?: string): Promise<Note> {
  const body: Record<string, unknown> = { query, directory, model };
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

export function transformText(text: string, instruction: string, model?: string): Promise<{ result: string }> {
  return fetchAPI('/notes/transform', {
    method: 'POST',
    body: JSON.stringify({ text, instruction, model }),
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

export interface SearchConfig {
  enabled: boolean;
  // Which search backend answers web_search/fetch_page: the built-in native
  // engine, or a third-party provider (Tavily).
  provider: SearchProvider;
  // Tavily API key. On read it is the sentinel '__SET__' when a key is stored
  // (never the real value) or '' when none is. On write, echo '__SET__' back to
  // keep the stored key untouched.
  tavilyApiKey: string;
  // True when TAVILY_API_KEY is set in the server's environment (read-only hint).
  tavilyEnvKeySet?: boolean;
  // Deep-research pipeline tuning (see settings → web search).
  fetchTopK: number;
  pageChars: number;
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

export function buildDocIndex(directory?: string, rebuild = false, model?: string): Promise<{ running: boolean }> {
  return fetchAPI('/docindex/build', {
    method: 'POST',
    body: JSON.stringify({ directory, rebuild, model }),
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
}

export function listSkills(): Promise<Skill[]> {
  return fetchAPI('/skills');
}