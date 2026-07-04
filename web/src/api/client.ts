const BASE_URL = import.meta.env.VITE_API_URL || '';
const API = `${BASE_URL}/api`;

export async function fetchAPI<T>(path: string, opts?: RequestInit): Promise<T> {
  const res = await fetch(`${API}${path}`, {
    headers: { 'Content-Type': 'application/json', ...opts?.headers },
    ...opts,
  });
  if (res.status === 204) return undefined as T;
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`API error ${res.status}: ${text}`);
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
  cost?: number;
  tokens?: TokenCounts;
  createdAt: number;
}

export interface Part {
  id: string;
  messageId: string;
  sessionId: string;
  type: 'text' | 'tool' | 'reasoning';
  data: TextPartData | ToolPartData | ReasoningPartData;
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
  status: 'pending' | 'running' | 'completed' | 'error';
  input: any;
  output?: string;
  error?: string;
  title?: string;
  metadata?: any;
}

export interface ReasoningPartData {
  text: string;
}

export interface MessageWithParts {
  info: MessageInfo;
  parts: Part[];
}

export function getMessages(sessionId: string): Promise<MessageWithParts[]> {
  return fetchAPI(`/session/${sessionId}/message`);
}

export function sendPrompt(sessionId: string, content: string, model?: string, viewportWidth?: number, viewportHeight?: number): Promise<void> {
  const body: Record<string, unknown> = { content };
  if (model) body.model = model;
  if (viewportWidth) body.viewportWidth = viewportWidth;
  if (viewportHeight) body.viewportHeight = viewportHeight;
  return fetchAPI(`/session/${sessionId}/prompt`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export function replyPermission(sessionId: string, permissionId: string, response: string): Promise<void> {
  return fetchAPI(`/session/${sessionId}/permission/${permissionId}`, {
    method: 'POST',
    body: JSON.stringify({ response }),
  });
}

export function abortSession(sessionId: string): Promise<void> {
  return fetchAPI(`/session/${sessionId}/abort`, { method: 'POST' });
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

// Provider config API
export interface ProviderConfig {
  providerId: string;
  apiKey: string;       // "__SET__" if stored in DB, "" otherwise
  baseUrl: string;
  updatedAt: number;
  envKeySet: boolean;     // env var (e.g. ANTHROPIC_API_KEY) is present
  envBaseURLSet: boolean; // env var (e.g. OPENAI_BASE_URL) is present
}

export function getProviderConfigs(): Promise<ProviderConfig[]> {
  return fetchAPI('/providers/config');
}

export function setProviderConfig(id: string, cfg: Omit<ProviderConfig, 'providerId' | 'updatedAt' | 'envKeySet' | 'envBaseURLSet'>): Promise<ProviderConfig> {
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
  inputPricePerM: number;
  outputPricePerM: number;
}

export function getModels(): Promise<ModelInfo[]> {
  return fetchAPI('/models');
}

export interface ModelPreference {
  id: string;
  providerId: string;
  displayName: string;
  enabled: boolean;
  isCustom: boolean;
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
export interface SearchConfig {
  enabled: boolean;
  useRealProfile: boolean;
  updatedAt?: number;
}

export function getSearchConfig(): Promise<SearchConfig> {
  return fetchAPI('/search/config');
}

export function setSearchConfig(cfg: Omit<SearchConfig, 'updatedAt'>): Promise<SearchConfig> {
  return fetchAPI('/search/config', {
    method: 'POST',
    body: JSON.stringify(cfg),
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