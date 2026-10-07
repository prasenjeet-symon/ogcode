import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal, createEffect, on } from 'solid-js';
import { getPath, getConfig, getVCS, getMode, getResources } from '../api/client';
import type { ResourceSample } from '../api/client';
import { createSSE, type SSEEvent } from '../api/sse';
import { projectName } from '../lib/paths';

interface ServerContextValue {
  directory: () => string;
  branch: () => string;
  isGitRepo: () => boolean;
  hasRemote: () => boolean;
  ghInstalled: () => boolean;
  mode: () => 'build' | 'plan';
  // Whether the event stream is up: true from the server's server.connected
  // until the stream next drops.
  connected: () => boolean;
  searchRunning: () => boolean;
  // Whether the server resolved the notes feature flag as on for this install.
  // The server owns the flag; the browser never queries PostHog for it.
  notesEnabled: () => boolean;
  // Whether the server resolved the device-panel feature flag as on for this
  // install. Same ownership model as notesEnabled.
  devicePanelEnabled: () => boolean;
  // Whether the server resolved the plan-mode feature flag as on for this
  // install. Same ownership model; it gates only the plan-mode instability
  // warning, never plan mode itself.
  planModeEnabled: () => boolean;
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
  // Ticks each time the event stream comes back after dropping — never on the
  // first connection. Whatever was published while it was down never arrived,
  // so consumers re-fetch what they show.
  reconnectTick: () => number;
}

export interface ResourceMeta {
  interval: number;
  cores: number;
  uptime: number;
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
  const [searchRunning, setSearchRunning] = createSignal(false);
  const [notesEnabled, setNotesEnabled] = createSignal(false);
  const [devicePanelEnabled, setDevicePanelEnabled] = createSignal(false);
  const [planModeEnabled, setPlanModeEnabled] = createSignal(false);
  const [eventTick, setEventTick] = createSignal(0);
  const [lastEvent, setLastEvent] = createSignal<SSEEvent | null>(null);
  const [resyncTick, setResyncTick] = createSignal(0);
  const [reconnectTick, setReconnectTick] = createSignal(0);
  // Set when the stream errors, so the server.connected that follows is known
  // to be a reconnect rather than the first connection.
  let dropped = false;
  const [resources, setResources] = createSignal<ResourceSample[]>([]);
  const [resourceMeta, setResourceMeta] = createSignal<ResourceMeta>({
    interval: 2000,
    cores: 0,
    uptime: 0,
  });
  // Highest event seq seen on this connection, for drop detection. Reset to 0 on
  // reconnect (a new EventSource restarts the server's per-connection numbering).
  let lastSeq = 0;

  // Load server info. The workspace name names the browser tab too, so a window
  // full of ogcode tabs is told apart by the project each one serves rather
  // than the single word baked into index.html (which stays as the pre-load
  // title — the app has no directory until this resolves, and a stale one from
  // a previous project would be worse than the product's own name).
  getPath().then((info) => {
    setDirectory(info.directory);
    if (info.directory) document.title = projectName(info.directory);
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
    });
    if (snap.samples?.length) setResources(snap.samples.slice(-RESOURCE_RETAIN));
  }).catch(() => { /* ignore */ });

  // Pulled out of the initial load so both the first fetch and every reconnect
  // re-read it: the server decides the notes flag in the background, so a
  // stream that dropped may have missed the notes.changed that announced it.
  function loadConfig() {
    getConfig().then((config) => {
      setSearchRunning((config as any).searchRunning ?? false);
      setNotesEnabled(config.notesEnabled ?? false);
      setDevicePanelEnabled(config.devicePanelEnabled ?? false);
      setPlanModeEnabled(config.planModeEnabled ?? false);
    }).catch(() => { /* ignore */ });
  }
  loadConfig();

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
      if (dropped) {
        dropped = false;
        setReconnectTick((n) => n + 1);
      }
    } else if (event.type === 'server.resources') {
      appendResourceSample(event.properties);
      // Deliberately does NOT bump eventTick: that counter is a re-fetch signal
      // for consumers, and a telemetry frame arriving every couple of seconds
      // would have the whole app reloading its state on a timer.
    } else if (event.type === 'server.heartbeat') {
      // keep alive
    } else if (event.type === 'notes.changed') {
      // The server flipped the notes feature flag (its background refresher
      // polls PostHog). Flip the gate immediately; consumers keyed on
      // notesEnabled load or clear their state in response.
      setNotesEnabled((event.properties as any)?.enabled ?? false);
    } else if (event.type === 'device-panel.changed') {
      // Same contract as notes.changed, for the device panel's flag.
      setDevicePanelEnabled((event.properties as any)?.enabled ?? false);
    } else if (event.type === 'plan-mode.changed') {
      // Same contract, for the plan-mode flag that gates the instability warning.
      setPlanModeEnabled((event.properties as any)?.enabled ?? false);
    } else {
      setLastEvent(event);
      setEventTick((n) => n + 1);
    }
  }, () => {
    // The stream is down until the server's next server.connected — the
    // browser retries on its own — and that one will be a reconnect.
    dropped = true;
    setConnected(false);
  });

  // A reconnect means whatever was published while the stream was down never
  // arrived — including a notes.changed. Re-read the config so the flag is
  // current even if that event was missed. Deferred so it does not run on the
  // initial pass (the first load already fetched it).
  createEffect(on(reconnectTick, () => { loadConfig(); }, { defer: true }));

  function appendResourceSample(props: any) {
    const sample: ResourceSample | undefined = props?.sample;
    if (!sample || typeof sample.at !== 'number') return;

    if (typeof props.interval === 'number') {
      setResourceMeta({
        interval: props.interval,
        cores: props.cores ?? 0,
        uptime: props.uptime ?? 0,
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
    searchRunning,
    notesEnabled,
    devicePanelEnabled,
    planModeEnabled,
    resources,
    resourceMeta,
    eventTick,
    lastEvent,
    resyncTick,
    reconnectTick,
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