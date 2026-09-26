import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal, createEffect, on } from 'solid-js';
import {
  type DocSummary, type ModelInfo, type ExcludeEntry, type IndexFile,
  getDocIndexBuildStatus, buildDocIndex, getIndexedDocs, getIndexFiles, getModels,
  getExcludes, addExclude, deleteExclude,
} from '../api/client';
import { useServer } from './server';

interface IndexProgress {
  total: number;
  completed: number;
  failed: number;
  percent: number;
}

interface DocIndexContextValue {
  docs: () => DocSummary[];
  files: () => IndexFile[];
  loading: () => boolean;
  building: () => boolean;
  models: () => ModelInfo[];
  selectedModel: () => string;
  selectModel: (id: string) => void;
  refresh: () => Promise<void>;
  build: (rebuild?: boolean) => Promise<void>;
  excludes: () => ExcludeEntry[];
  loadExcludes: () => Promise<void>;
  addExclude: (pattern: string) => Promise<void>;
  deleteExclude: (id: string) => Promise<void>;
  progress: () => IndexProgress | null;
}

const DocIndexContext = createContext<DocIndexContextValue>();

export const DocIndexProvider: ParentComponent = (props) => {
  const server = useServer();
  const [docs, setDocs] = createSignal<DocSummary[]>([]);
  const [files, setFiles] = createSignal<IndexFile[]>([]);
  const [loading, setLoading] = createSignal(false);
  const [building, setBuilding] = createSignal(false);
  const [models, setModels] = createSignal<ModelInfo[]>([]);
  const [selectedModelId, setSelectedModelId] = createSignal<string>('');
  const [excludes, setExcludes] = createSignal<ExcludeEntry[]>([]);
  const [progress, setProgress] = createSignal<IndexProgress | null>(null);

  // Load models whenever the directory changes
  createEffect(on(server.directory, () => {
    getModels()
      .then((list) => setModels(list || []))
      .catch((e) => console.error('docindex: load models failed:', e));
  }));

  const selectedModel = (): string => {
    const id = selectedModelId();
    const enabled = models().filter((m) => m.enabled);
    if (id && enabled.some((m) => m.id === id)) return id;
    const def = enabled.find((m) => m.default) || enabled[0];
    return def?.id || '';
  };

  // The provider that serves the selected model. Two providers can serve the
  // same model id, so naming the provider is what pins the index run to one
  // endpoint rather than letting the server re-infer it.
  const selectedProvider = (): string => {
    const id = selectedModel();
    return models().find((m) => m.id === id)?.providerId || '';
  };

  const selectModel = (id: string) => setSelectedModelId(id);

  async function refresh() {
    const dir = server.directory();
    if (!dir) return;
    setLoading(true);
    try {
      const [d, f] = await Promise.all([
        getIndexedDocs(dir),
        getIndexFiles(dir),
      ]);
      setDocs(d || []);
      setFiles(f || []);
    } catch (e) {
      console.error('docindex refresh failed:', e);
    } finally {
      setLoading(false);
    }
  }

  async function loadExcludes() {
    const dir = server.directory();
    if (!dir) return;
    try {
      const list = await getExcludes(dir);
      setExcludes(list || []);
    } catch (e) {
      console.error('load excludes failed:', e);
    }
  }

  async function handleAddExclude(pattern: string) {
    const dir = server.directory();
    if (!dir || !pattern.trim()) return;
    try {
      const entry = await addExclude(dir, pattern.trim());
      setExcludes((prev) => [...prev, entry].sort((a, b) => a.pattern.localeCompare(b.pattern)));
    } catch (e) {
      console.error('add exclude failed:', e);
    }
  }

  async function handleDeleteExclude(id: string) {
    try {
      await deleteExclude(id);
      setExcludes((prev) => prev.filter((e) => e.id !== id));
    } catch (e) {
      console.error('delete exclude failed:', e);
    }
  }

  // Poll for progress while building
  let progressTimer: ReturnType<typeof setInterval> | null = null;

  // Consecutive one-second polls reporting "not running" before the build is
  // treated as over. Covers the gap between starting a build and the server
  // reporting it.
  const idleTicksBeforeDone = 5;

  // Ticks seen with the build not running, since the last one that was. Used to
  // decide when "not running" means finished rather than not started yet.
  let idleTicks = 0;

  function startProgressPolling() {
    stopProgressPolling();
    idleTicks = 0;
    progressTimer = setInterval(async () => {
      try {
        const status = await getDocIndexBuildStatus();
        if (status.running) {
          idleTicks = 0;
          setProgress({
            total: status.total ?? 0,
            completed: status.completed ?? 0,
            failed: status.failed ?? 0,
            percent: status.percent ?? 0,
          });
          return;
        }
        // Not running. This poller is the fallback for a missed SSE event, so it
        // cannot rely on docindex.built to stop it — that is the very thing it
        // exists to cover for. Left to itself it polled once a second forever
        // and left building() stuck true, showing a build that had finished.
        //
        // A few such ticks are tolerated first: the poll starts before the
        // server has registered the build, so the opening seconds legitimately
        // report not-running and stopping there would cancel the UI for a build
        // that is about to begin.
        idleTicks++;
        if (idleTicks < idleTicksBeforeDone) return;
        setBuilding(false);
        setProgress(null);
        stopProgressPolling();
        refresh();
      } catch {
        // ignore polling errors
      }
    }, 1000);
  }

  function stopProgressPolling() {
    if (progressTimer !== null) {
      clearInterval(progressTimer);
      progressTimer = null;
    }
  }

  async function build(rebuild = false) {
    if (building()) return;
    setBuilding(true);
    setProgress({ total: 0, completed: 0, failed: 0, percent: 0 });
    startProgressPolling();
    try {
      await buildDocIndex(server.directory() || undefined, rebuild, selectedModel() || undefined, selectedProvider() || undefined);
    } catch (e) {
      console.error('start docindex build failed:', e);
      setBuilding(false);
      setProgress(null);
      stopProgressPolling();
    }
  }

  // Load when directory changes
  createEffect(on(server.directory, (dir) => {
    if (dir) refresh();
  }));

  // Refresh on SSE reconnect; always check build status on reconnect for crash recovery.
  createEffect(on(server.connected, (isConnected) => {
    if (!isConnected) return;
    refresh();
    getDocIndexBuildStatus()
      .then((status) => {
        if (status.running) {
          setBuilding(true);
          setProgress({
            total: status.total ?? 0,
            completed: status.completed ?? 0,
            failed: status.failed ?? 0,
            percent: status.percent ?? 0,
          });
          startProgressPolling();
        } else if (building()) {
          setBuilding(false);
          setProgress(null);
          stopProgressPolling();
        }
      })
      .catch((e) => console.error('docindex: build status check failed:', e));
  }, { defer: true }));

  // When the build finishes, refresh doc list and clear building state
  createEffect(on(server.eventTick, () => {
    const last = server.lastEvent();
    if (!last || last.type !== 'docindex.built') return;
    setBuilding(false);
    setProgress(null);
    stopProgressPolling();
    refresh();
  }));

  // Also listen for progress events via SSE for real-time updates
  createEffect(on(server.eventTick, () => {
    const last = server.lastEvent();
    if (!last || last.type !== 'docindex.progress') return;
    const props = last.properties;
    if (props) {
      const total = typeof props.total === 'number' ? props.total : 0;
      const completed = typeof props.completed === 'number' ? props.completed : 0;
      const failed = typeof props.failed === 'number' ? props.failed : 0;
      const percent = total > 0 ? Math.round(((completed + failed) / total) * 100) : 0;
      setProgress({ total, completed, failed, percent });

      if (props.phase === 'done') {
        setBuilding(false);
        setProgress(null);
        stopProgressPolling();
        refresh();
      }
    }
  }));

  const value: DocIndexContextValue = {
    docs,
    files,
    loading,
    building,
    models,
    selectedModel,
    selectModel,
    refresh,
    build,
    excludes,
    loadExcludes,
    addExclude: handleAddExclude,
    deleteExclude: handleDeleteExclude,
    progress,
  };

  return (
    <DocIndexContext.Provider value={value}>
      {props.children}
    </DocIndexContext.Provider>
  );
};

export function useDocIndex() {
  const ctx = useContext(DocIndexContext);
  if (!ctx) throw new Error('useDocIndex must be used within DocIndexProvider');
  return ctx;
}