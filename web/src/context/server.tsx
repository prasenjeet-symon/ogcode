import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal } from 'solid-js';
import { getPath, getConfig, getVCS, getMode } from '../api/client';
import { createSSE, type SSEEvent } from '../api/sse';

interface ServerContextValue {
  directory: () => string;
  branch: () => string;
  isGitRepo: () => boolean;
  hasRemote: () => boolean;
  ghInstalled: () => boolean;
  mode: () => 'build' | 'plan';
  connected: () => boolean;
  memoryEnabled: () => boolean;
  memoryProvider: () => string;
  searchRunning: () => boolean;
  // Monotonically increasing counter that ticks on every relevant SSE event.
  // Consumers use this as a reactive dependency to know when to re-fetch.
  eventTick: () => number;
  lastEvent: () => SSEEvent | null;
  // Ticks whenever a gap in the server's event sequence is detected (events were
  // dropped to a full buffer). Consumers should force a full state re-fetch.
  resyncTick: () => number;
}

const ServerContext = createContext<ServerContextValue>();

export const ServerProvider: ParentComponent = (props) => {
  const [directory, setDirectory] = createSignal('');
  const [branch, setBranch] = createSignal('');
  const [isGitRepo, setIsGitRepo] = createSignal(true);
  const [hasRemote, setHasRemote] = createSignal(true);
  const [ghInstalled, setGhInstalled] = createSignal(true);
  const [mode, setMode] = createSignal<'build' | 'plan'>('build');
  const [connected, setConnected] = createSignal(false);
  const [memoryEnabled, setMemoryEnabled] = createSignal(false);
  const [memoryProvider, setMemoryProvider] = createSignal('');
  const [searchRunning, setSearchRunning] = createSignal(false);
  const [eventTick, setEventTick] = createSignal(0);
  const [lastEvent, setLastEvent] = createSignal<SSEEvent | null>(null);
  const [resyncTick, setResyncTick] = createSignal(0);
  // Highest event seq seen on this connection, for drop detection. Reset to 0 on
  // reconnect (a new EventSource restarts the server's per-connection numbering).
  let lastSeq = 0;

  // Load server info
  getPath().then((info) => {
    setDirectory(info.directory);
  }).catch(() => { /* ignore */ });

  // Load VCS info
  getVCS().then((info) => {
    if (info.branch) setBranch(info.branch);
    setIsGitRepo(info.isGitRepo ?? true);
    setHasRemote(info.hasRemote ?? true);
    setGhInstalled(info.ghInstalled ?? true);
  }).catch(() => { /* ignore */ });

  // Load server mode
  getMode().then((info) => {
    if (info.mode) setMode(info.mode as 'build' | 'plan');
  }).catch(() => { /* ignore */ });

  getConfig().then((config) => {
    setMemoryEnabled(config.memoryEnabled);
    setMemoryProvider(config.memoryProvider ?? '');
    setSearchRunning((config as any).searchRunning ?? false);
  }).catch(() => { /* ignore */ });

  // Connect to SSE
  createSSE('/event', (event) => {
    // Drop detection: the bus stamps a monotonic seq on every event. A jump
    // beyond lastSeq+1 means the server dropped events to a full buffer, so ask
    // consumers to resync. Out-of-order or duplicate seqs (possible under
    // concurrent publishes) at worst cause a harmless extra resync. Control
    // frames (connected/config/heartbeat) carry no seq and are skipped.
    const seq = typeof event.seq === 'number' ? event.seq : 0;
    if (seq > 0) {
      if (lastSeq !== 0 && seq > lastSeq + 1) {
        setResyncTick((n) => n + 1);
      }
      if (seq > lastSeq) lastSeq = seq;
    } else if (event.type === 'server.connected') {
      // New connection → the server restarts seq numbering; reset our tracker so
      // the first real event doesn't look like a gap.
      lastSeq = 0;
    }

    if (event.type === 'server.connected') {
      setConnected(true);
    } else if (event.type === 'server.config') {
      setMemoryEnabled(!!event.properties?.memoryEnabled);
      setMemoryProvider(event.properties?.memoryProvider ?? '');
    } else if (event.type === 'server.heartbeat') {
      // keep alive
    } else {
      setLastEvent(event);
      setEventTick((n) => n + 1);
    }
  });

  const value: ServerContextValue = {
    directory,
    branch,
    isGitRepo,
    hasRemote,
    ghInstalled,
    mode,
    connected,
    memoryEnabled,
    memoryProvider,
    searchRunning,
    eventTick,
    lastEvent,
    resyncTick,
  };

  return (
    <ServerContext.Provider value={value}>
      {props.children}
    </ServerContext.Provider>
  );
};

export function useServer() {
  const ctx = useContext(ServerContext);
  if (!ctx) throw new Error('useServer must be used within ServerProvider');
  return ctx;
}