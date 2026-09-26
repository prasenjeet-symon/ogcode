import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal, createEffect, on } from 'solid-js';
import { type Note, listNotes, createNote as createNoteAPI, deleteNote as deleteNoteAPI, getNote, updateNote as updateNoteAPI } from '../api/client';
import { useServer } from './server';

interface NoteContextValue {
  notes: () => Note[];
  loading: () => boolean;
  refresh: () => Promise<void>;
  createNote: (query: string, model?: string | null, sessionId?: string, viewportWidth?: number, viewportHeight?: number, provider?: string) => Promise<Note>;
  createManualNote: () => Promise<Note>;
  updateNote: (id: string, title: string, content: string) => Promise<Note>;
  deleteNote: (id: string) => Promise<void>;
  refreshNote: (id: string) => Promise<Note | null>;
}

const NoteContext = createContext<NoteContextValue>();

export const NoteProvider: ParentComponent = (props) => {
  const server = useServer();
  const [notes, setNotes] = createSignal<Note[]>([]);
  const [loading, setLoading] = createSignal(false);

  async function refresh() {
    const dir = server.directory();
    if (!dir) return;
    try {
      const list = await listNotes(dir);
      setNotes(list || []);
    } catch (e) {
      console.error('refresh notes failed:', e);
    }
  }

  async function createNote(query: string, model?: string | null, sessionId?: string, viewportWidth?: number, viewportHeight?: number, provider?: string): Promise<Note> {
    setLoading(true);
    try {
      const n = await createNoteAPI(query, server.directory(), model || undefined, sessionId, viewportWidth, viewportHeight, undefined, provider || undefined);
      setNotes((prev) => prev.find((x) => x.id === n.id) ? prev : [n, ...prev]);
      return n;
    } finally {
      setLoading(false);
    }
  }

  async function createManualNote(): Promise<Note> {
    setLoading(true);
    try {
      const n = await createNoteAPI('', server.directory(), undefined, undefined, undefined, undefined, 'manual');
      setNotes((prev) => prev.find((x) => x.id === n.id) ? prev : [n, ...prev]);
      return n;
    } finally {
      setLoading(false);
    }
  }

  async function updateNote(id: string, title: string, content: string): Promise<Note> {
    const n = await updateNoteAPI(id, title, content);
    setNotes((prev) => prev.map((x) => (x.id === id ? n : x)));
    return n;
  }

  async function deleteNote(id: string) {
    await deleteNoteAPI(id);
    setNotes((prev) => prev.filter((n) => n.id !== id));
  }

  async function refreshNote(id: string): Promise<Note | null> {
    try {
      const n = await getNote(id);
      if (!n) return null;
      setNotes((prev) => {
        const exists = prev.find((x) => x.id === id);
        if (exists) return prev.map((x) => (x.id === id ? n : x));
        return [n, ...prev];
      });
      return n;
    } catch {
      return null;
    }
  }

  // Load notes when directory changes
  createEffect(on(server.directory, (dir) => {
    if (dir) refresh();
  }));

  // React to SSE note events
  createEffect(on(server.eventTick, () => {
    const last = server.lastEvent();
    if (!last) return;

    if (last.type === 'note.created') {
      const n = last.properties as Note | undefined;
      if (n?.id) {
        setNotes((prev) => prev.find((x) => x.id === n.id) ? prev : [n, ...prev]);
      }
      return;
    }

    if (last.type === 'note.updated') {
      const sessionId = (last.properties as any)?.sessionId;
      if (!sessionId) return;
      const existing = notes().find((n) => n.sessionId === sessionId);
      if (existing) refreshNote(existing.id);
      return;
    }

    if (last.type === 'note.deleted') {
      const deletedId = (last.properties as any)?.id;
      if (deletedId) setNotes((prev) => prev.filter((n) => n.id !== deletedId));
      return;
    }
  }));

  // Refresh on SSE reconnect
  createEffect(on(server.connected, (isConnected) => {
    if (isConnected) refresh();
  }));

  const value: NoteContextValue = {
    notes,
    loading,
    refresh,
    createNote,
    createManualNote,
    updateNote,
    deleteNote,
    refreshNote,
  };

  return (
    <NoteContext.Provider value={value}>
      {props.children}
    </NoteContext.Provider>
  );
};

export function useNote() {
  const ctx = useContext(NoteContext);
  if (!ctx) throw new Error('useNote must be used within NoteProvider');
  return ctx;
}
