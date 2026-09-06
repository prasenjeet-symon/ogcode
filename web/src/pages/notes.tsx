import { createSignal, For, Show, onCleanup } from 'solid-js';
import { useNavigate } from '@solidjs/router';
import { useNote } from '../context/note';
import { useServer } from '../context/server';
import SessionSidebar from '../components/session-sidebar';
import PlanSidebar from '../components/plan-sidebar';
import { DrawerToggle } from '../components/sidebar-shell';
import type { Note } from '../api/client';

function Sidebar() {
  const server = useServer();
  return (
    <Show when={server.mode() === 'plan'} fallback={<SessionSidebar />}>
      <PlanSidebar />
    </Show>
  );
}

function formatTime(ts: number): string {
  const d = new Date(ts);
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return 'just now';
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 30) return `${diffDay}d ago`;
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
}

function toFilename(title: string): string {
  if (!title) return 'untitled.md';
  return title
    .toLowerCase()
    .replace(/[^a-z0-9\s-]/g, '')
    .trim()
    .replace(/\s+/g, '-')
    .slice(0, 48) + '.md';
}

function previewLines(content: string, max = 8): string[] {
  return content
    .split('\n')
    .slice(0, 30)
    .filter((l) => l.trim())
    .slice(0, max);
}

function NoteCard(props: { note: Note; onDelete: (e: MouseEvent) => void; onClick: () => void }) {
  const n = () => props.note;
  const lines = () => previewLines(n().content);

  return (
    <div
      onClick={props.onClick}
      class="group border border-[color:var(--border-subtle)] rounded-md overflow-hidden cursor-pointer
             hover:border-[color:var(--border-default)] transition-colors bg-[color:var(--bg-surface)]"
    >
      {/* Gist-style file header */}
      <div class="flex items-center gap-2 px-4 py-2.5 bg-[color:var(--bg-elevated)] border-b border-[color:var(--border-subtle)]">
        {/* File icon */}
        <svg class="w-4 h-4 text-zinc-500 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
          <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
        </svg>

        {/* Filename */}
        <span class="text-[13px] font-semibold text-[color:var(--accent)] hover:underline font-mono flex-1 min-w-0 truncate">
          {toFilename(n().title || n().query)}
        </span>

        {/* Status / version / date */}
        <div class="flex items-center gap-2 shrink-0">
          <Show when={n().status === 'generating'}>
            <span class="flex items-center gap-1 text-[11px] text-amber-400">
              <span class="w-1.5 h-1.5 rounded-full bg-amber-400 animate-pulse" />
              generating
            </span>
          </Show>
          <Show when={n().status === 'error'}>
            <span class="text-[11px] text-red-400">error</span>
          </Show>
          <Show when={(n().version ?? 0) > 0}>
            <span class="text-[11px] text-zinc-500 font-mono bg-[color:var(--bg-base)] px-1.5 py-0.5 rounded border border-[color:var(--border-subtle)]">
              v{n().version}
            </span>
          </Show>
          <span class="text-[11px] text-zinc-500">{formatTime(n().updatedAt)}</span>

          {/* Delete — visible on hover */}
          <button
            onClick={(e) => { e.stopPropagation(); props.onDelete(e); }}
            class="w-6 h-6 rounded flex items-center justify-center
                   opacity-0 group-hover:opacity-100
                   text-zinc-600 hover:text-red-400 hover:bg-red-500/10 transition"
            title="Delete note"
          >
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6M1 7h22M10 7V4a1 1 0 011-1h2a1 1 0 011 1v3" />
            </svg>
          </button>
        </div>
      </div>

      {/* Content preview — raw markdown lines like a gist */}
      <div class="px-4 py-3 min-h-[80px]">
        <Show when={n().status === 'generating' && !n().content}>
          <div class="flex items-center gap-2 text-[12px] text-amber-400/80 italic py-2">
            <div class="w-3 h-3 border border-amber-400 border-t-transparent rounded-full animate-spin" />
            Generating note…
          </div>
        </Show>

        <Show when={n().content}>
          <div class="font-mono text-[12px] leading-relaxed space-y-0.5 select-none">
            <For each={lines()}>
              {(line, i) => {
                const isHeading1 = line.startsWith('# ');
                const isHeading2 = line.startsWith('## ');
                const isHeading3 = line.startsWith('### ');
                const isCodeFence = line.startsWith('```');
                const isList = line.match(/^[-*+]\s/) || line.match(/^\d+\.\s/);
                const isBlockquote = line.startsWith('>');

                return (
                  <div
                    class={`truncate ${
                      isHeading1 ? 'text-zinc-100 font-bold text-[13px]' :
                      isHeading2 ? 'text-zinc-200 font-semibold' :
                      isHeading3 ? 'text-zinc-300 font-medium' :
                      isCodeFence ? 'text-zinc-600' :
                      isList ? 'text-zinc-400 pl-2' :
                      isBlockquote ? 'text-zinc-500 border-l-2 border-zinc-600 pl-2 italic' :
                      'text-zinc-400'
                    }`}
                  >
                    {line}
                  </div>
                );
              }}
            </For>
            <Show when={previewLines(n().content, 100).length > 8}>
              <div class="text-zinc-700 text-[11px] pt-1">…</div>
            </Show>
          </div>
        </Show>

        <Show when={n().status === 'error' && !n().content}>
          <p class="text-[12px] text-red-400/70 italic py-2">Generation failed</p>
        </Show>
      </div>

      {/* Footer: query description or manual badge */}
      <div class="px-4 py-2 border-t border-[color:var(--border-subtle)] flex items-center gap-1.5 bg-[color:var(--bg-base)]/30">
        <Show when={n().source === 'manual'} fallback={
          <>
            <svg class="w-3 h-3 text-zinc-600 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
            </svg>
            <span class="text-[11px] text-zinc-600 italic truncate">{n().query}</span>
          </>
        }>
          <svg class="w-3 h-3 text-zinc-600 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
          </svg>
          <span class="text-[11px] text-zinc-600">Manual note</span>
        </Show>
      </div>
    </div>
  );
}

export default function NotesPage() {
  const noteCtx = useNote();
  const server = useServer();
  const navigate = useNavigate();
  const [search, setSearch] = createSignal('');
  const [creatingManual, setCreatingManual] = createSignal(false);

  const filtered = () => {
    const q = search().trim().toLowerCase();
    if (!q) return noteCtx.notes();
    return noteCtx.notes().filter(
      (n) => n.title.toLowerCase().includes(q) || n.query.toLowerCase().includes(q)
    );
  };

  const handleDelete = async (e: MouseEvent, id: string) => {
    e.stopPropagation();
    if (!confirm('Delete this note? This cannot be undone.')) return;
    await noteCtx.deleteNote(id);
  };

  const handleNewNote = async () => {
    if (creatingManual()) return;
    setCreatingManual(true);
    try {
      const n = await noteCtx.createManualNote();
      navigate(`/notes/${n.id}`);
    } catch (e) {
      console.error('create manual note failed:', e);
    } finally {
      setCreatingManual(false);
    }
  };

  return (
    <div class="flex h-dvh w-full">
      <Sidebar />

      <div class="page-enter flex-1 flex flex-col overflow-hidden relative bg-[color:var(--bg-base)]">

        {/* Header. On phones the search drops under the title row rather than
            squeezing beside it. */}
        <div class="shrink-0 border-b border-[color:var(--border-subtle)] px-3 sm:px-6 py-3 sm:py-4 flex flex-wrap items-center justify-between gap-2"
             style={{ 'padding-top': 'max(0.75rem, env(safe-area-inset-top))' }}>
          <div class="flex items-center gap-1 min-w-0">
            <DrawerToggle drawer={server.mode() === 'plan' ? 'plans' : 'sessions'} label="Open navigation" />
            <div class="min-w-0">
              <h1 class="text-[15px] font-semibold text-zinc-100">Project Notes</h1>
              <p class="text-[11px] text-zinc-500 mt-0.5">
                {noteCtx.notes().length > 0
                  ? `${noteCtx.notes().length} note${noteCtx.notes().length === 1 ? '' : 's'}`
                  : 'Write notes manually or save from chat'}
              </p>
            </div>
          </div>

          <div class="flex items-center gap-2 ml-auto">
            {/* New Note button */}
            <button
              onClick={handleNewNote}
              disabled={creatingManual()}
              class="h-8 px-3 flex items-center gap-1.5 rounded-md text-[12px] font-medium
                     bg-[color:var(--accent)] text-white disabled:opacity-50 hover:opacity-90 transition"
            >
              <Show when={creatingManual()} fallback={
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
                </svg>
              }>
                <div class="w-3.5 h-3.5 border-2 border-white/60 border-t-transparent rounded-full animate-spin" />
              </Show>
              New Note
            </button>

            {/* Search */}
            <div class="relative">
              <svg class="w-3.5 h-3.5 text-zinc-500 absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
              <input
                type="text"
                value={search()}
                onInput={(e) => setSearch(e.currentTarget.value)}
                placeholder="Search notes…"
                class="h-8 w-40 sm:w-48 pl-8 pr-3 bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)] rounded-md
                       text-[12px] text-zinc-200 placeholder-zinc-600
                       focus:outline-none focus:border-[color:var(--border-default)] transition"
              />
            </div>
          </div>
        </div>

        {/* Notes list */}
        <div class="flex-1 overflow-y-auto px-3 sm:px-6 py-5 flex flex-col">
          <Show when={filtered().length === 0}>
            <div class="flex flex-col items-center justify-center h-64 text-center gap-3">
              <svg class="w-10 h-10 text-zinc-700" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
              </svg>
              <div>
                <p class="text-[13px] font-medium text-zinc-400">
                  {search() ? 'No notes match your search' : 'No notes yet'}
                </p>
                <Show when={!search()}>
                  <p class="text-[12px] text-zinc-600 mt-1">
                    Click <span class="text-zinc-400 font-medium">New Note</span> to write manually, or hover a chat message and click <span class="text-zinc-400 font-medium">"Save to Notes"</span>
                  </p>
                </Show>
              </div>
            </div>
          </Show>

          <div class="max-w-3xl mx-auto space-y-4">
            <For each={filtered()}>
              {(n) => (
                <NoteCard
                  note={n}
                  onClick={() => navigate(`/notes/${n.id}`)}
                  onDelete={(e) => handleDelete(e, n.id)}
                />
              )}
            </For>
          </div>
        </div>
      </div>
    </div>
  );
}
