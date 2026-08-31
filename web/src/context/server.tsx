import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal } from 'solid-js';
import { getPath, getConfig, getVCS, getMode, getResources } from '../api/client';
import type { ResourceSample, ResourceActivity } from '../api/client';
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
  // Rolling window of this process's own CPU/memory samples, oldest first, and
  // the context needed to read them (cadence, core count, process uptime).
  resources: () => ResourceSample[];
  resourceMeta: () => ResourceMeta;
  // Monotonically increasing counter that ticks on every relevant SSE event.
  // Consumers use this as a reactive dependency to know when to re-fetch.
  eventTick: () => number;
  lastEvent: () => SSEEvent | null;
  // Ticks whenever a gap in the server's event sequence is detected (events were
  // dropped to a full buffer). Consumers should force a full state re-fetch.
  resyncTick: () => number;
}

export interface ResourceMeta {
  interval: number;
  cores: number;
  uptime: number;
  // What the server is busy with, when it has said. Null the rest of the time.
  activity: ResourceActivity | null;
}

// Mirrors the server's retention so the client window and the backfill it gets
// from /resources describe the same span of time.
const RESOURCE_RETAIN = 120;

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
  const [resources, setResources] = createSignal<ResourceSample[]>([]);
  const [resourceMeta, setResourceMeta] = createSignal<ResourceMeta>({
    interval: 2000,
    cores: 0,
    uptime: 0,
    activity: null,
  });
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

  // Backfill the resource window in one shot so the graph opens populated
  // instead of drawing itself one sample at a time off the SSE stream.
  getResources().then((snap) => {
    setResourceMeta({
      interval: snap.interval,
      cores: snap.cores,
      uptime: snap.uptime,
      activity: snap.activity ?? null,
    });
    if (snap.samples?.length) setResources(snap.samples.slice(-RESOURCE_RETAIN));
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
    } else if (event.type === 'server.resources') {
      appendResourceSample(event.properties);
      // Deliberately does NOT bump eventTick: that counter is a re-fetch signal
      // for consumers, and a telemetry frame arriving every couple of seconds
      // would have the whole app reloading its state on a timer.
    } else if (event.type === 'server.heartbeat') {
      // keep alive
    } else {
      setLastEvent(event);
      setEventTick((n) => n + 1);
    }
  });

  function appendResourceSample(props: any) {
    const sample: ResourceSample | undefined = props?.sample;
    if (!sample || typeof sample.at !== 'number') return;

    if (typeof props.interval === 'number') {
      setResourceMeta({
        interval: props.interval,
        cores: props.cores ?? 0,
        uptime: props.uptime ?? 0,
        activity: props.activity ?? null,
      });
    }

    setResources((prev) => {
      // The server's sampler and this stream's ticker run on independent
      // timers, so a frame can occasionally repeat the sample the previous one
      // carried. Keyed on `at`, a repeat is dropped rather than drawn twice.
      const last = prev[prev.length - 1];
      if (last && last.at >= sample.at) return prev;

      const next = [...prev, sample];
      // Drop anything older than the window. Sampling pauses when no client is
      // watching, so after a backgrounded tab reconnects the array can hold
      // pre-gap samples; plotted by index they would splice across the gap and
      // show a jump that never happened.
      const cutoff = sample.at - resourceMeta().interval * RESOURCE_RETAIN;
      const fresh = next.filter((s) => s.at >= cutoff);
      return fresh.length > RESOURCE_RETAIN ? fresh.slice(-RESOURCE_RETAIN) : fresh;
    });
  }

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
    resources,
    resourceMeta,
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