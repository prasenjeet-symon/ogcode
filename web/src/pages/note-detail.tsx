import { createSignal, Show, onMount, onCleanup, For } from 'solid-js';
import { useNavigate, useParams } from '@solidjs/router';
import { useNote } from '../context/note';
import { useServer } from '../context/server';
import { type Note, type NoteVersion, type ModelInfo, listNoteVersions, downloadNoteExport, getModels } from '../api/client';
import MarkdownContent from '../components/markdown-content';
import NoteEditor from '../components/note-editor';
import ModelSelector from '../components/model-selector';
import SessionSidebar from '../components/session-sidebar';
import PlanSidebar from '../components/plan-sidebar';
import { DrawerToggle } from '../components/sidebar-shell';

function Sidebar() {
  const server = useServer();
  return (
    <Show when={server.mode() === 'plan'} fallback={<SessionSidebar />}>
      <PlanSidebar />
    </Show>
  );
}

function formatDate(ts: number): string {
  return new Date(ts).toLocaleString(undefined, {
    month: 'short', day: 'numeric', year: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}

function formatDateShort(ts: number): string {
  const d = new Date(ts);
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return 'just now';
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

export default function NoteDetailPage() {
  const params = useParams<{ id: string }>();
  const noteCtx = useNote();
  const server = useServer();
  const navigate = useNavigate();
  const [note, setNote] = createSignal<Note | null>(null);
  const [notFound, setNotFound] = createSignal(false);
  const [deleting, setDeleting] = createSignal(false);
  const [showHistory, setShowHistory] = createSignal(false);
  const [versions, setVersions] = createSignal<NoteVersion[]>([]);
  const [previewVersion, setPreviewVersion] = createSignal<NoteVersion | null>(null);
  const [loadingVersions, setLoadingVersions] = createSignal(false);

  const [isEditing, setIsEditing] = createSignal(false);
  const [editTitle, setEditTitle] = createSignal('');
  const [editContent, setEditContent] = createSignal('');
  const [saving, setSaving] = createSignal(false);
  const [aiModels, setAiModels] = createSignal<ModelInfo[]>([]);
  const [selectedModel, setSelectedModel] = createSignal('');

  onMount(async () => {
    const n = await noteCtx.refreshNote(params.id);
    if (!n) {
      setNotFound(true);
    } else {
      setNote(n);
      if (n.source === 'manual' && !n.content) {
        setEditTitle(n.title === 'Untitled' ? '' : n.title);
        setEditContent('');
        setIsEditing(true);
      }
    }
    getModels().then(ms => {
      const enabled = (ms || []).filter(m => m.enabled);
      setAiModels(enabled);
      if (enabled.length > 0) setSelectedModel(enabled[0].id);
    }).catch(() => {});
  });

  async function loadVersions() {
    setLoadingVersions(true);
    try {
      const vs = await listNoteVersions(params.id);
      setVersions(vs || []);
    } catch (e) {
      console.error('load versions failed:', e);
    } finally {
      setLoadingVersions(false);
    }
  }

  let pollTimer: ReturnType<typeof setInterval> | null = null;
  const startPoll = () => {
    if (pollTimer) return;
    pollTimer = setInterval(async () => {
      const n = await noteCtx.refreshNote(params.id);
      if (n) {
        setNote(n);
        if (n.status !== 'generating') {
          stopPoll();
          loadVersions();
        }
      }
    }, 2500);
  };
  const stopPoll = () => {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  };
  onCleanup(stopPoll);

  const currentNote = () => {
    const n = note();
    if (n?.status === 'generating') startPoll();
    else stopPoll();
    return n;
  };

  function enterEditMode() {
    const n = note();
    if (!n) return;
    setEditTitle(n.title || '');
    setEditContent(n.content || '');
    setIsEditing(true);
    setPreviewVersion(null);
  }

  async function handleSave() {
    const n = note();
    if (!n) return;
    setSaving(true);
    try {
      const updated = await noteCtx.updateNote(n.id, editTitle(), editContent());
      setNote(updated);
      setIsEditing(false);
      loadVersions();
    } catch (e) {
      console.error('save note failed:', e);
    } finally {
      setSaving(false);
    }
  }

  function handleCancel() {
    setIsEditing(false);
  }

  const handleExport = async () => {
    if (!note()) return;
    try {
      await downloadNoteExport(note()!.id);
    } catch (e) {
      console.error('export note failed:', e);
    }
  };

  const handleDelete = async () => {
    if (!note()) return;
    if (!confirm('Delete this note? This cannot be undone.')) return;
    setDeleting(true);
    try {
      await noteCtx.deleteNote(note()!.id);
      navigate('/notes');
    } catch (e) {
      console.error('delete note failed:', e);
      setDeleting(false);
    }
  };

  const handleToggleHistory = () => {
    const next = !showHistory();
    setShowHistory(next);
    if (next && versions().length === 0) loadVersions();
    if (!next) setPreviewVersion(null);
  };

  const displayContent = () => previewVersion()?.content ?? currentNote()?.content ?? '';

  return (
    <div class="flex h-dvh w-full">
      <Sidebar />

      <div class="flex-1 flex flex-col overflow-hidden relative bg-[color:var(--bg-base)]">
        <Show when={notFound()}>
          <div class="flex-1 flex items-center justify-center text-zinc-500 text-[14px]">
            Note not found.{' '}
            <button onClick={() => navigate('/notes')} class="ml-1 text-[color:var(--accent)] hover:underline">
              Back to notes
            </button>
          </div>
        </Show>

        <Show when={!notFound()}>
          {/* Header */}
          <div class="shrink-0 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)]">

            {/* Top bar: breadcrumb + actions. On phones the badges wrap below
                the breadcrumb rather than crowding it. */}
            <div class="flex flex-wrap items-center gap-2 px-3 sm:px-4 pt-3 pb-2"
                 style={{ 'padding-top': 'max(0.75rem, env(safe-area-inset-top))' }}>
              <DrawerToggle drawer={server.mode() === 'plan' ? 'plans' : 'sessions'} label="Open navigation" />
              <button
                onClick={() => navigate('/notes')}
                class="flex items-center gap-1.5 text-[12px] text-zinc-500 hover:text-zinc-200 transition shrink-0"
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
                </svg>
                Notes
              </button>
              <svg class="w-3 h-3 text-zinc-700 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
              </svg>
              <span class="text-[12px] text-zinc-400 truncate flex-1 min-w-0">
                {currentNote()?.title || currentNote()?.query || '…'}
              </span>

              {/* Status badge */}
              <Show when={currentNote()?.status === 'generating'}>
                <span class="flex items-center gap-1.5 text-[11px] font-medium text-amber-400 bg-amber-400/10 border border-amber-400/20 rounded-full px-2.5 py-0.5 shrink-0">
                  <span class="w-1.5 h-1.5 rounded-full bg-amber-400 animate-pulse" />
                  Generating…
                </span>
              </Show>
              <Show when={currentNote()?.status === 'error'}>
                <span class="flex items-center gap-1.5 text-[11px] font-medium text-red-400 bg-red-400/10 border border-red-400/20 rounded-full px-2.5 py-0.5 shrink-0">
                  <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M4.93 19h14.14c1.54 0 2.5-1.67 1.73-3L13.73 4.99c-.77-1.33-2.69-1.33-3.46 0L3.2 16c-.77 1.33.19 3 1.73 3z" />
                  </svg>
                  Error
                </span>
              </Show>
              <Show when={currentNote()?.status === 'done' && !isEditing()}>
                <span class="flex items-center gap-1.5 text-[11px] font-medium text-emerald-400 bg-emerald-400/10 border border-emerald-400/20 rounded-full px-2.5 py-0.5 shrink-0">
                  <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                  Ready
                </span>
              </Show>

              {/* History button — hidden while editing */}
              <Show when={currentNote() && (currentNote()!.version ?? 0) > 0 && !isEditing()}>
                <button
                  onClick={handleToggleHistory}
                  class={`flex items-center gap-1.5 text-[11px] font-medium px-2.5 py-1 rounded-md border transition shrink-0
                    ${showHistory()
                      ? 'text-[color:var(--accent)] bg-[color:var(--accent-soft)] border-[color:var(--accent)]/30'
                      : 'text-zinc-400 bg-[color:var(--bg-elevated)] border-[color:var(--border-subtle)] hover:text-zinc-200 hover:border-[color:var(--border-default)]'
                    }`}
                  title="Version history"
                >
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                  v{currentNote()!.version}
                </button>
              </Show>

              {/* Edit button */}
              <Show when={currentNote()?.status === 'done' && !isEditing()}>
                <button
                  onClick={enterEditMode}
                  class="shrink-0 w-7 h-7 rounded-md flex items-center justify-center text-zinc-500 hover:text-[color:var(--accent)] hover:bg-[color:var(--accent-soft)] transition"
                  title="Edit note"
                >
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                  </svg>
                </button>
              </Show>

              {/* Cancel / Model picker / Save when editing */}
              <Show when={isEditing()}>
                <button
                  onClick={handleCancel}
                  class="shrink-0 h-7 px-2.5 rounded-md text-[11px] text-zinc-400 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)]/50 transition"
                >
                  Cancel
                </button>
                <ModelSelector
                  selectedModel={() => selectedModel()}
                  models={() => aiModels()}
                  onSelect={setSelectedModel}
                  placement="bottom"
                />
                <button
                  onClick={handleSave}
                  disabled={saving()}
                  class="shrink-0 h-7 px-3 rounded-md text-[11px] font-medium bg-[color:var(--accent)] text-white disabled:opacity-50 hover:opacity-90 transition"
                >
                  {saving() ? 'Saving…' : 'Save'}
                </button>
              </Show>

              {/* Export — hidden while editing */}
              <Show when={currentNote() && !isEditing()}>
                <button
                  onClick={handleExport}
                  class="shrink-0 w-7 h-7 rounded-md flex items-center justify-center text-zinc-500 hover:text-emerald-400 hover:bg-emerald-500/10 transition"
                  title="Export note"
                >
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
                  </svg>
                </button>
              </Show>

              {/* Delete — hidden while editing */}
              <Show when={currentNote() && !isEditing()}>
                <button
                  onClick={handleDelete}
                  disabled={deleting()}
                  class="shrink-0 w-7 h-7 rounded-md flex items-center justify-center text-zinc-500 hover:text-red-400 hover:bg-red-500/10 disabled:opacity-40 transition"
                  title="Delete note"
                >
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6M1 7h22M10 7V4a1 1 0 011-1h2a1 1 0 011 1v3" />
                  </svg>
                </button>
              </Show>
            </div>

            {/* Title + meta */}
            <div class="px-6 pb-4">
              <Show when={isEditing()} fallback={
                <h1 class="text-[20px] font-semibold text-zinc-100 leading-snug tracking-tight">
                  {currentNote()?.title || currentNote()?.query || '…'}
                </h1>
              }>
                <input
                  type="text"
                  value={editTitle()}
                  onInput={e => setEditTitle(e.currentTarget.value)}
                  placeholder="Untitled"
                  class="w-full text-[20px] font-semibold text-zinc-100 leading-snug tracking-tight
                         bg-transparent border-none outline-none placeholder-zinc-600"
                  onKeyDown={e => { if (e.key === 'Enter') e.preventDefault(); }}
                />
              </Show>
              <Show when={currentNote() && !isEditing()}>
                <div class="flex items-center gap-2 mt-2 flex-wrap">
                  <span class="flex items-center gap-1.5 text-[11px] text-zinc-500">
                    <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
                    </svg>
                    {formatDate(currentNote()!.createdAt)}
                  </span>
                  <Show when={currentNote()?.source !== 'manual' && currentNote()?.query}>
                    <span class="text-zinc-700">·</span>
                    <span class="flex items-center gap-1.5 text-[11px] text-zinc-500 min-w-0">
                      <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M9.879 7.519c1.171-1.025 3.071-1.025 4.242 0 1.172 1.025 1.172 2.687 0 3.712-.203.179-.43.326-.67.442-.745.361-1.45.999-1.45 1.827v.75M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9 5.25h.008v.008H12v-.008z" />
                      </svg>
                      <span class="italic truncate" title={currentNote()!.query}>
                        {currentNote()!.query}
                      </span>
                    </span>
                  </Show>
                  <Show when={currentNote()?.source === 'manual'}>
                    <span class="text-zinc-700">·</span>
                    <span class="flex items-center gap-1.5 text-[11px] text-zinc-500">
                      <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                      </svg>
                      Manual note
                    </span>
                  </Show>
                </div>
              </Show>
            </div>
          </div>

          {/* Body: content + optional history panel */}
          <div class="flex-1 flex overflow-hidden">
            {/* Main content */}
            <div class="flex-1 overflow-y-auto px-6 py-6">
              <div class="max-w-3xl mx-auto flex flex-col min-h-full">
                <Show when={!currentNote()}>
                  <div class="flex items-center justify-center h-32">
                    <div class="w-5 h-5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                  </div>
                </Show>

                <Show when={currentNote()?.status === 'generating' && !currentNote()?.content}>
                  <div class="flex flex-col items-center justify-center h-48 text-center gap-3">
                    <div class="w-6 h-6 border-2 border-amber-400 border-t-transparent rounded-full animate-spin" />
                    <p class="text-[13px] text-zinc-500">The AI agent is researching and writing your note…</p>
                  </div>
                </Show>

                {/* Edit mode */}
                <Show when={isEditing()}>
                  <NoteEditor
                    content={editContent()}
                    onChange={setEditContent}
                    model={selectedModel()}
                    autofocus
                  />
                </Show>

                {/* View mode */}
                <Show when={!isEditing()}>
                  <Show when={previewVersion()}>
                    <div class="mb-4 flex items-center gap-2 px-3 py-2 rounded-lg bg-amber-400/5 border border-amber-400/20 text-[12px] text-amber-400">
                      <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                      </svg>
                      Viewing v{previewVersion()!.version} — {formatDate(previewVersion()!.createdAt)}
                      <button
                        onClick={() => setPreviewVersion(null)}
                        class="ml-auto text-amber-400/60 hover:text-amber-400 transition"
                      >
                        Back to current
                      </button>
                    </div>
                  </Show>

                  <Show when={displayContent()}>
                    <MarkdownContent text={displayContent()} />
                  </Show>

                  <Show when={currentNote()?.status === 'done' && !currentNote()?.content && currentNote()?.source === 'manual'}>
                    <div class="flex flex-col items-center justify-center h-48 text-center gap-3">
                      <svg class="w-10 h-10 text-zinc-700" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                      </svg>
                      <div>
                        <p class="text-[13px] font-medium text-zinc-400">This note is empty</p>
                        <p class="text-[12px] text-zinc-600 mt-1">Click Edit to start writing</p>
                      </div>
                    </div>
                  </Show>

                  <Show when={currentNote()?.status === 'error' && !currentNote()?.content}>
                    <div class="flex flex-col items-center justify-center h-48 text-center gap-2">
                      <svg class="w-8 h-8 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                      </svg>
                      <p class="text-[13px] text-zinc-500">Note generation failed.</p>
                    </div>
                  </Show>
                </Show>
              </div>
            </div>

            {/* Version history panel */}
            <Show when={showHistory() && !isEditing()}>
              <div class="w-64 shrink-0 border-l border-[color:var(--border-subtle)] flex flex-col bg-[color:var(--bg-surface)]">
                <div class="shrink-0 px-4 py-3 border-b border-[color:var(--border-subtle)] flex items-center justify-between">
                  <span class="text-[12px] font-semibold text-zinc-300">Version History</span>
                  <button
                    onClick={() => { setShowHistory(false); setPreviewVersion(null); }}
                    class="w-5 h-5 flex items-center justify-center text-zinc-500 hover:text-zinc-200 transition"
                  >
                    <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                    </svg>
                  </button>
                </div>

                <div class="flex-1 overflow-y-auto py-2">
                  <Show when={loadingVersions()}>
                    <div class="flex justify-center py-8">
                      <div class="w-4 h-4 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                    </div>
                  </Show>

                  <Show when={!loadingVersions() && versions().length === 0}>
                    <p class="text-[12px] text-zinc-600 px-4 py-6 text-center">No versions yet</p>
                  </Show>

                  <For each={versions()}>
                    {(v) => {
                      const isCurrent = () => v.version === currentNote()?.version;
                      const isPreviewing = () => previewVersion()?.id === v.id;
                      return (
                        <button
                          onClick={() => setPreviewVersion(isPreviewing() ? null : v)}
                          class={`w-full text-left px-4 py-3 transition border-b border-[color:var(--border-subtle)] last:border-0
                            ${isPreviewing()
                              ? 'bg-[color:var(--accent-soft)]'
                              : 'hover:bg-[color:var(--bg-hover)]/50'
                            }`}
                        >
                          <div class="flex items-center justify-between gap-2 mb-0.5">
                            <span class={`text-[12px] font-semibold ${isPreviewing() ? 'text-[color:var(--accent)]' : 'text-zinc-200'}`}>
                              v{v.version}
                            </span>
                            <Show when={isCurrent()}>
                              <span class="text-[10px] text-emerald-400 font-medium">current</span>
                            </Show>
                          </div>
                          <span class="text-[11px] text-zinc-500">{formatDateShort(v.createdAt)}</span>
                          <p class="text-[11px] text-zinc-600 mt-1 line-clamp-2 leading-relaxed">
                            {v.content.replace(/^#+ .+\n?/m, '').replace(/[#*`>]/g, '').trim().slice(0, 80)}
                          </p>
                        </button>
                      );
                    }}
                  </For>
                </div>
              </div>
            </Show>
          </div>
        </Show>
      </div>
    </div>
  );
}
