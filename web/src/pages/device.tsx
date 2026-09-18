import { createSignal, For, Show, onCleanup, onMount } from 'solid-js';
import { useSearchParams } from '@solidjs/router';
import { useServer } from '../context/server';
import { getScrcpyStatus, getScrcpyDevices, type ScrcpyDevice, type ScrcpyStatus } from '../api/client';
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

// How often the panel re-checks ws-scrcpy. The stream itself runs in the
// iframe; this only gates whether the iframe is shown at all.
const POLL_MS = 10_000;

// Defaults match the emulator ~/.android-browser/start.sh brings up. The udid
// is only a fallback: the device list from adb overrides it (first live
// emulator wins) whenever the link carries no explicit ?udid=.
const DEFAULT_UDID = 'emulator-5554';

// The scrcpy server ws-scrcpy spawns inside the device listens on tcp:8886;
// the stream client reaches it through ws-scrcpy's adb-forward proxy. Verified
// against the running instance: ws://…/?action=proxy-adb&remote=tcp:8886&udid=…
// opens and carries the scrcpy handshake.
const SCRCPY_DEVICE_PORT = 8886;

/**
 * The player decodes the H.264 stream in the browser. ws-scrcpy matches the
 * `player` deep-link param against either the full name or the code name
 * (StreamClientScrcpy.getPlayerClass), so the short names below are what the
 * link carries. Order is the fallback chain — WebCodecs is the hardware
 * decoder and the default; the rest are software decoders that keep working
 * where VideoDecoder is missing.
 */
const PLAYERS = [
  { name: 'webcodecs', label: 'WebCodecs (hardware)', supported: () => typeof VideoDecoder !== 'undefined' && typeof (VideoDecoder as { isConfigSupported?: unknown }).isConfigSupported === 'function' },
  { name: 'mse', label: 'H264 Converter (MSE)', supported: () => typeof MediaSource !== 'undefined' && typeof MediaSource.isTypeSupported === 'function' && MediaSource.isTypeSupported('video/mp4; codecs="avc1.42E01E"') },
  { name: 'broadway', label: 'Broadway.js (WASM)', supported: () => typeof WebAssembly === 'object' && typeof WebAssembly.instantiate === 'function' },
  { name: 'tinyh264', label: 'Tiny H264 (WASM)', supported: () => typeof WebAssembly === 'object' && typeof WebAssembly.instantiate === 'function' },
] as const;

// First supported entry of PLAYERS — the WebCodecs default with a graceful
// fallback, mirroring ws-scrcpy's own per-player isSupported() checks.
const defaultPlayer = (): string => PLAYERS.find((p) => p.supported())?.name ?? PLAYERS[0].name;

/**
 * DevicePage embeds the ws-scrcpy stream through ogcode's own /scrcpy proxy so
 * the browser never talks cross-origin to the device UI. The deep link
 * (#!?action=stream&…) makes ws-scrcpy start the stream directly, skipping its
 * device picker. If ws-scrcpy is down the panel says how to start it — the
 * agent's adb "hands" work regardless; this screen is the eyes.
 */
export default function DevicePage() {
  const server = useServer();
  const [searchParams, setSearchParams] = useSearchParams();

  const udid = () => {
    const v = searchParams.udid;
    return typeof v === 'string' && v ? v : '';
  };
  const player = () => {
    const v = searchParams.player;
    if (typeof v === 'string' && v && PLAYERS.some((p) => p.name === v)) return v;
    return '';
  };

  const [status, setStatus] = createSignal<ScrcpyStatus | null>(null);
  const [checking, setChecking] = createSignal(true);
  const [devices, setDevices] = createSignal<ScrcpyDevice[] | null>(null);
  const [picking, setPicking] = createSignal(false);
  const [playerOpen, setPlayerOpen] = createSignal(false);
  // Restart remounts the iframe: ws-scrcpy re-runs its deep link on load, so a
  // fresh mount is what actually restarts the stream (nudging src by a hash
  // param wouldn't — same-document navigation doesn't re-fire window.onload).
  const [streamLive, setStreamLive] = createSignal(true);
  const [copied, setCopied] = createSignal(false);

  let timer: ReturnType<typeof setInterval> | undefined;
  const refresh = () => {
    getScrcpyStatus()
      .then((st) => {
        setStatus(st);
        setChecking(false);
      })
      .catch(() => {
        setStatus({ up: false, target: '' });
        setChecking(false);
      });
    getScrcpyDevices()
      .then((res) => setDevices(res.devices))
      .catch(() => setDevices([]));
  };

  onMount(() => {
    refresh();
    timer = setInterval(refresh, POLL_MS);
  });
  onCleanup(() => {
    if (timer) clearInterval(timer);
  });

  // Which device to stream. An explicit ?udid= wins; otherwise the first
  // attached (state "device") emulator, then any other live device, then the
  // historical default — so arriving with a bare /device streams something
  // when one is attached instead of a dead serial. A device that is present
  // but not ready still streams: ws-scrcpy shows its own waiting state.
  const attached = () => (devices() ?? []).filter((d) => d.state === 'device');
  const resolvedUdid = () => {
    const explicit = udid();
    if (explicit) return explicit;
    const list = devices() ?? [];
    return (
      attached().find((d) => d.serial.startsWith('emulator-'))?.serial ??
      attached()[0]?.serial ??
      list[0]?.serial ??
      DEFAULT_UDID
    );
  };

  // The WebSocket URL the stream client connects to: same origin (so it
  // survives ogcode's /scrcpy proxy untouched), routed by ws-scrcpy's
  // proxy-adb handler to the scrcpy server on the device (tcp:8886). wss on
  // https pages, matching ws-scrcpy's own URL construction.
  const streamWsUrl = () =>
    `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/scrcpy/?action=proxy-adb&remote=tcp:${SCRCPY_DEVICE_PORT}&udid=${encodeURIComponent(resolvedUdid())}`;

  // ws-scrcpy's stream deep link requires action, udid, player and ws; the
  // player param picks the decoder (WebCodecs by default) and fitToScreen
  // scales the stream to the panel instead of the device's native size.
  const streamSrc = () =>
    `/scrcpy/#!${new URLSearchParams({
      action: 'stream',
      udid: resolvedUdid(),
      player: player() || defaultPlayer(),
      ws: streamWsUrl(),
      fitToScreen: '1',
    }).toString()}`;

  const up = () => status()?.up ?? false;

  const restart = () => {
    // Unmount then remount on the next tick — the toggle is what reloads the
    // iframe; reassigning an identical src would not.
    setStreamLive(false);
    setTimeout(() => setStreamLive(true), 50);
    refresh();
  };

  // Switching devices remounts the iframe on the new deep link (same mechanic
  // as restart) and rewrites the URL so the screen stays linkable.
  const selectDevice = (serial: string) => {
    setPicking(false);
    if (serial === resolvedUdid()) return;
    setSearchParams(
      { udid: serial, player: player() || undefined },
      { replace: true },
    );
    setStreamLive(false);
    setTimeout(() => setStreamLive(true), 50);
    refresh();
  };

  const selectPlayer = (name: string) => {
    setPlayerOpen(false);
    if (name === (player() || defaultPlayer())) return;
    setSearchParams(
      { udid: udid() || undefined, player: name },
      { replace: true },
    );
    setStreamLive(false);
    setTimeout(() => setStreamLive(true), 50);
  };

  const deepLink = () =>
    `${window.location.origin}/device?${new URLSearchParams({
      udid: resolvedUdid(),
      player: player() || defaultPlayer(),
    }).toString()}`;

  const copyLink = async () => {
    try {
      await navigator.clipboard.writeText(deepLink());
      setCopied(true);
      setTimeout(() => setCopied(false), 1400);
    } catch {
      // clipboard unavailable — nothing to do
    }
  };

  const headerBtn =
    'h-8 px-3 rounded-lg text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:border-[color:var(--border-default)] disabled:opacity-50 disabled:cursor-not-allowed transition flex items-center gap-1.5 shrink-0';

  return (
    <div class="flex h-dvh w-full">
      <Sidebar />

      <div class="flex-1 flex flex-col overflow-hidden bg-[color:var(--bg-base)]">
        {/* ---- Header ---- */}
        <header class="shrink-0 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] pl-2 pr-2 sm:pl-3 sm:pr-2.5 h-12 flex items-center gap-2 sm:gap-3"
                style={{ [ 'padding-top']: 'env(safe-area-inset-top)' }}>
          <DrawerToggle drawer={server.mode() === 'plan' ? 'plans' : 'sessions'} label="Open navigation" />
          <div class="flex items-center gap-2.5 min-w-0">
            <div class="w-6 h-6 rounded-md bg-[color:var(--accent-soft)] flex items-center justify-center shrink-0">
              <svg class="w-3.5 h-3.5 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M10.5 1.5H8.25A2.25 2.25 0 006 3.75v16.5a2.25 2.25 0 002.25 2.25h7.5A2.25 2.25 0 0018 20.25V3.75a2.25 2.25 0 00-2.25-2.25H13.5m-3 0V3h3V1.5m-3 0h3m-3 18.75h3" />
              </svg>
            </div>
            <div class="min-w-0">
              <h1 class="text-[13px] font-semibold text-[color:var(--text-primary)] leading-tight">Device</h1>
              <p class="text-[10px] text-[color:var(--text-muted)] font-mono truncate leading-tight" title={resolvedUdid()}>
                {resolvedUdid()} · {player() || defaultPlayer()}
              </p>
            </div>
          </div>

          {/* Device picker: lists what adb sees; picking one remounts the
              stream on that device's deep link. */}
          <div class="relative shrink-0">
            <button
              onClick={() => setPicking(!picking())}
              class="h-8 pl-2.5 pr-2 rounded-lg text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:border-[color:var(--border-default)] transition flex items-center gap-1.5"
              title="Choose which attached device to stream"
            >
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M10.5 1.5H8.25A2.25 2.25 0 006 3.75v16.5a2.25 2.25 0 002.25 2.25h7.5A2.25 2.25 0 0018 20.25V3.75a2.25 2.25 0 00-2.25-2.25H13.5m-3 0V3h3V1.5m-3 0h3m-3 18.75h3" />
              </svg>
              <span class="font-mono max-w-[110px] sm:max-w-[180px] truncate" title={resolvedUdid()}>
                {resolvedUdid()}
              </span>
              <svg class="w-3 h-3 text-[color:var(--text-tertiary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
              </svg>
            </button>
            <Show when={picking()}>
              <div class="fixed inset-0 z-30" onClick={() => setPicking(false)} />
              <div class="absolute left-0 top-9 z-40 w-64 rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] shadow-[var(--shadow-lg)] overflow-hidden">
                <p class="px-3 pt-2.5 pb-1 text-[10px] uppercase tracking-wide text-[color:var(--text-muted)]">Attached devices</p>
                <Show
                  when={(devices() ?? []).length > 0}
                  fallback={<p class="px-3 pb-3 text-[11px] text-[color:var(--text-tertiary)]">No devices found via adb.</p>}
                >
                  <ul class="max-h-64 overflow-y-auto pb-1.5">
                    <For each={devices()}>
                      {(d) => (
                        <li>
                          <button
                            onClick={() => selectDevice(d.serial)}
                            class={`w-full text-left px-3 py-2 flex items-center gap-2 transition ${
                              d.serial === resolvedUdid()
                                ? 'bg-[color:var(--accent-soft)] text-[color:var(--text-primary)]'
                                : 'hover:bg-[color:var(--bg-surface)] text-[color:var(--text-secondary)]'
                            } ${d.state !== 'device' ? 'opacity-60' : ''}`}
                          >
                            <span
                              class={`w-1.5 h-1.5 rounded-full shrink-0 ${d.state === 'device' ? 'bg-emerald-400' : 'bg-zinc-600'}`}
                              title={d.state}
                            />
                            <span class="min-w-0 flex-1">
                              <span class="block text-[12px] font-mono truncate">{d.serial}</span>
                              <Show when={d.model}>
                                <span class="block text-[10px] text-[color:var(--text-tertiary)] truncate">{d.model}</span>
                              </Show>
                            </span>
                            {d.state !== 'device' && (
                              <span class="text-[10px] text-[color:var(--text-muted)] shrink-0">{d.state}</span>
                            )}
                            {d.serial === resolvedUdid() && (
                              <svg class="w-3.5 h-3.5 text-[color:var(--accent)] shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                                <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                              </svg>
                            )}
                          </button>
                        </li>
                      )}
                    </For>
                  </ul>
                </Show>
              </div>
            </Show>
          </div>

          {/* Player picker: which in-browser decoder renders the H.264 stream.
              WebCodecs is the default; the rest are fallbacks for browsers
              without VideoDecoder. Picking one remounts the stream. */}
          <div class="relative shrink-0 hidden sm:block">
            <button
              onClick={() => setPlayerOpen(!playerOpen())}
              class="h-8 pl-2.5 pr-2 rounded-lg text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:border-[color:var(--border-default)] transition flex items-center gap-1.5"
              title="Which browser decoder renders the stream"
            >
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 10.5l4.72-4.72a.75.75 0 011.28.53v11.38a.75.75 0 01-1.28.53l-4.72-4.72M4.5 7.5h6.75a2.25 2.25 0 012.25 2.25v4.5a2.25 2.25 0 01-2.25 2.25H4.5a2.25 2.25 0 01-2.25-2.25v-6A2.25 2.25 0 014.5 7.5z" />
              </svg>
              <span class="max-w-[120px] truncate">
                {PLAYERS.find((p) => p.name === (player() || defaultPlayer()))?.label ?? 'WebCodecs (hardware)'}
              </span>
              <svg class="w-3 h-3 text-[color:var(--text-tertiary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
              </svg>
            </button>
            <Show when={playerOpen()}>
              <div class="fixed inset-0 z-30" onClick={() => setPlayerOpen(false)} />
              <div class="absolute left-0 top-9 z-40 w-64 rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] shadow-[var(--shadow-lg)] overflow-hidden">
                <p class="px-3 pt-2.5 pb-1 text-[10px] uppercase tracking-wide text-[color:var(--text-muted)]">Stream decoder</p>
                <ul class="pb-1.5">
                  <For each={PLAYERS}>
                    {(p) => (
                      <li>
                        <button
                          onClick={() => selectPlayer(p.name)}
                          class={`w-full text-left px-3 py-2 flex items-center gap-2 transition ${
                            p.name === (player() || defaultPlayer())
                              ? 'bg-[color:var(--accent-soft)] text-[color:var(--text-primary)]'
                              : 'hover:bg-[color:var(--bg-surface)] text-[color:var(--text-secondary)]'
                          }`}
                        >
                          <span class="min-w-0 flex-1">
                            <span class="block text-[12px]">{p.label}</span>
                            <span class="block text-[10px] text-[color:var(--text-tertiary)]">
                              {p.supported() ? 'supported by this browser' : 'not supported here'}
                            </span>
                          </span>
                          {p.name === (player() || defaultPlayer()) && (
                            <svg class="w-3.5 h-3.5 text-[color:var(--accent)] shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                              <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                            </svg>
                          )}
                        </button>
                      </li>
                    )}
                  </For>
                </ul>
              </div>
            </Show>
          </div>

          <div class="flex-1" />

          <button
            onClick={copyLink}
            class={headerBtn}
            title="Copy a link that opens this device screen"
          >
            <Show when={copied()} fallback={
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M8 5H6a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2v-1M8 5a2 2 0 002 2h2a2 2 0 002-2M8 5a2 2 0 012-2h2a2 2 0 012 2m0 0h2a2 2 0 012 2v3" />
              </svg>
            }>
              <span class="text-[10px] text-[color:var(--success)] shrink-0">copied</span>
            </Show>
            <span class="hidden md:inline">{copied() ? 'Copied' : 'Copy link'}</span>
          </button>

          <button
            onClick={restart}
            disabled={!up()}
            class="h-8 pl-2.5 pr-3 rounded-lg text-[12px] font-medium bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:border-[color:var(--border-default)] disabled:opacity-50 disabled:cursor-not-allowed transition flex items-center gap-1.5 shrink-0"
            title="Reload the stream"
          >
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
            <span class="hidden md:inline">Restart</span>
          </button>
        </header>

        {/* ---- Body ---- */}
        <Show
          when={!checking()}
          fallback={
            <div class="flex-1 flex items-center justify-center">
              <div class="flex flex-col items-center gap-3">
                <div class="w-5 h-5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                <p class="text-[12px] text-[color:var(--text-tertiary)]">Checking the device UI…</p>
              </div>
            </div>
          }
        >
          <Show
            when={up()}
            fallback={
              <div class="flex-1 flex flex-col items-center justify-center text-center px-8">
                <div class="w-14 h-14 rounded-2xl bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-4">
                  <svg class="w-6 h-6 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.4">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M10.5 1.5H8.25A2.25 2.25 0 006 3.75v16.5a2.25 2.25 0 002.25 2.25h7.5A2.25 2.25 0 0018 20.25V3.75a2.25 2.25 0 00-2.25-2.25H13.5m-3 0V3h3V1.5m-3 0h3m-3 18.75h3" />
                  </svg>
                </div>
                <p class="text-[14px] font-semibold text-[color:var(--text-primary)]">Device UI is down</p>
                <p class="text-[12px] text-[color:var(--text-tertiary)] mt-1.5 max-w-[360px] leading-relaxed">
                  ws-scrcpy serves the screen stream; ogcode proxies it under <code class="font-mono text-[11px]">/scrcpy</code> but
                  never starts it. The agent can still drive the device over adb — only the eyes are missing.
                </p>
                <Show when={status()?.target}>
                  <p class="text-[11px] text-[color:var(--text-muted)] font-mono mt-3">
                    not reachable at {status()!.target}
                  </p>
                </Show>
                <div class="mt-5 flex items-center gap-2">
                  <button
                    onClick={restart}
                    class="h-8 pl-2.5 pr-3.5 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] transition flex items-center gap-1.5 shadow-[var(--shadow-sm)]"
                  >
                    <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                    </svg>
                    Check again
                  </button>
                </div>
                <p class="text-[11px] text-[color:var(--text-muted)] mt-4 max-w-[360px] leading-relaxed">
                  Start it with <code class="font-mono text-[11px] text-[color:var(--text-secondary)]">~/.android-browser/start.sh</code>,
                  which also brings up the emulator. The panel reconnects on its own once the port answers.
                </p>
              </div>
            }
          >
            <div class="flex-1 min-h-0">
              <Show when={streamLive()}>
                <iframe
                  class="w-full h-full border-0"
                  src={streamSrc()}
                  title="ws-scrcpy device stream"
                  allow="clipboard-write"
                />
              </Show>
            </div>
          </Show>
        </Show>
      </div>
    </div>
  );
}