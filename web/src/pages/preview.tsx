import { createSignal, For, Show, onCleanup, onMount } from 'solid-js';
import { useLocation, useSearchParams } from '@solidjs/router';
import { useServer } from '../context/server';
import { getPreviewServices, type PreviewService } from '../api/client';
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

// How often the grid re-checks which loopback services are up. The services
// themselves run in the iframes; this only gates which tiles are live.
const POLL_MS = 10_000;

// Where the ports the user added by hand are kept, so a service that is not
// running yet still has a tile after a reload.
const STORAGE_KEY = 'ogcode-preview-ports';

// loadManualPorts reads the hand-added ports from localStorage, dropping
// anything that is not a usable port. A corrupt value reads as none.
function loadManualPorts(): number[] {
  if (typeof localStorage === 'undefined') return [];
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    const arr: unknown = raw ? JSON.parse(raw) : [];
    if (!Array.isArray(arr)) return [];
    return arr.filter((n): n is number => Number.isInteger(n) && n >= 1 && n <= 65535);
  } catch {
    return [];
  }
}

function saveManualPorts(ports: number[]): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(ports));
  } catch {
    // storage full or unavailable — the ports still work this session
  }
}

// validPort narrows a value to a usable loopback port.
function validPort(n: number): boolean {
  return Number.isInteger(n) && n >= 1 && n <= 65535;
}

/**
 * PreviewPage shows every live service the agent has started — several apps on
 * different loopback ports — as a grid of tiles, each labelled with the page's
 * own title. Clicking a tile expands it into a live iframe in place, so several
 * apps can be watched without leaving the page. The services are proxied
 * through ogcode's own /preview/<port>/ route, so the browser never talks
 * cross-origin to them.
 */
export default function PreviewPage() {
  const server = useServer();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();

  // The port named by the URL: the agent hands back a /preview/<port>/ URL, and
  // clicking it in chat is a client-side navigation that lands here with the
  // port in the PATH (the server never proxies that click — only a real
  // document load hits the proxy). A ?port= query spells the same thing.
  const deepLinkPort = (): number => {
    const m = /^\/preview\/(\d+)/.exec(location.pathname);
    const fromPath = m ? Number(m[1]) : NaN;
    if (validPort(fromPath)) return fromPath;
    const v = searchParams.port;
    const n = typeof v === 'string' ? Number(v) : NaN;
    return validPort(n) ? n : 0;
  };

  const [manualPorts, setManualPorts] = createSignal<number[]>(loadManualPorts());
  const [services, setServices] = createSignal<PreviewService[]>([]);
  const [checking, setChecking] = createSignal(true);
  // Which tile is expanded into a live iframe in place. Seeded from a deep link
  // so a URL the agent handed back opens that app.
  const [expanded, setExpanded] = createSignal(0);
  // Expanding remounts the iframe: a fresh mount is what actually reloads an
  // embedded service (nudging src by a hash param wouldn't — same-document
  // navigation doesn't re-fire window.onload).
  const [live, setLive] = createSignal(true);

  // The ports the grid must list whether or not they are discovered: the ones
  // the user added, plus any the URL names. Everything else is auto-detected.
  const requested = (): number[] => {
    const set = new Set(manualPorts());
    const d = deepLinkPort();
    if (d) set.add(d);
    return [...set].sort((a, b) => a - b);
  };

  let timer: ReturnType<typeof setInterval> | undefined;
  const refresh = () => {
    getPreviewServices(requested())
      .then((r) => {
        setServices(r.services ?? []);
        setChecking(false);
      })
      .catch(() => {
        // Keep the last list: a blip must not blank the grid.
        setChecking(false);
      });
  };

  onMount(() => {
    // Normalize /preview/<port>/… to /preview?port=<port> so an agent's
    // handed-back link settles into the page's canonical URL.
    const m = /^\/preview\/(\d+)/.exec(location.pathname);
    if (m && validPort(Number(m[1]))) {
      setSearchParams({ port: m[1] }, { replace: true });
    }
    const d = deepLinkPort();
    if (d) setExpanded(d);
    refresh();
    timer = setInterval(refresh, POLL_MS);
  });
  onCleanup(() => {
    if (timer) clearInterval(timer);
  });

  const addPort = (raw: string) => {
    const n = Number(raw);
    if (!validPort(n)) return;
    setManualPorts((prev) => {
      const next = prev.includes(n) ? prev : [...prev, n].sort((a, b) => a - b);
      saveManualPorts(next);
      return next;
    });
    setChecking(true);
    refresh();
  };

  const removePort = (n: number) => {
    setManualPorts((prev) => {
      const next = prev.filter((p) => p !== n);
      saveManualPorts(next);
      return next;
    });
    setExpanded((cur) => (cur === n ? 0 : cur));
    setChecking(true);
    refresh();
  };

  const toggle = (n: number) => {
    setExpanded((cur) => (cur === n ? 0 : n));
    // Remount the iframes so re-opening a tile shows the service's current state.
    setLive(false);
    setTimeout(() => setLive(true), 50);
  };

  const reloadAll = () => {
    setLive(false);
    setTimeout(() => setLive(true), 50);
    refresh();
  };

  const upCount = () => services().filter((s) => s.up).length;
  const label = (svc: PreviewService) => svc.title || `127.0.0.1:${svc.port}`;

  const headerBtn =
    'h-8 px-3 rounded-lg text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:border-[color:var(--border-default)] disabled:opacity-50 disabled:cursor-not-allowed transition flex items-center gap-1.5 shrink-0';
  const iconBtn =
    'h-7 w-7 rounded-md flex items-center justify-center text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)] transition shrink-0';

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
                <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 15.75l5.159-5.159a2.25 2.25 0 013.182 0l5.159 5.159m-1.5-1.5l1.409-1.409a2.25 2.25 0 013.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 001.5-1.5V6a1.5 1.5 0 00-1.5-1.5H3.75A1.5 1.5 0 002.25 6v12a1.5 1.5 0 001.5 1.5zm10.5-11.25h.008v.008h-.008V8.25zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
              </svg>
            </div>
            <div class="min-w-0">
              <h1 class="text-[13px] font-semibold text-[color:var(--text-primary)] leading-tight">Preview</h1>
              <p class="text-[10px] text-[color:var(--text-muted)] font-mono truncate leading-tight">
                {services().length ? `${services().length} service${services().length === 1 ? '' : 's'} · ${upCount()} up` : 'scanning…'}
              </p>
            </div>
          </div>

          {/* Add-port form: a hand-added port gets a tile whether or not the
              server discovers it, so a service that is not listening yet is
              still watchable once it starts. */}
          <form
            class="relative shrink-0 flex items-center"
            onSubmit={(e) => {
              e.preventDefault();
              const input = e.currentTarget.querySelector('input');
              addPort(input?.value ?? '');
              if (input) input.value = '';
            }}
          >
            <input
              type="text"
              inputmode="numeric"
              placeholder="add port"
              class="h-8 w-24 pl-2.5 pr-2 rounded-lg text-[12px] font-mono bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-primary)] placeholder:text-[color:var(--text-muted)] focus:outline-none focus:border-[color:var(--border-default)] transition"
              title="A loopback port to add to the grid"
            />
          </form>

          <div class="flex-1" />

          <button onClick={reloadAll} class={headerBtn} title="Re-check every service">
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
            <span class="hidden md:inline">Reload</span>
          </button>
        </header>

        {/* ---- Body ---- */}
        <Show
          when={!checking()}
          fallback={
            <div class="flex-1 flex items-center justify-center">
              <div class="flex flex-col items-center gap-3">
                <div class="w-5 h-5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                <p class="text-[12px] text-[color:var(--text-tertiary)]">Scanning for local services…</p>
              </div>
            </div>
          }
        >
          <Show
            when={services().length}
            fallback={
              <div class="flex-1 flex flex-col items-center justify-center text-center px-8">
                <div class="w-14 h-14 rounded-2xl bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-4">
                  <svg class="w-6 h-6 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.4">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 15.75l5.159-5.159a2.25 2.25 0 013.182 0l5.159 5.159m-1.5-1.5l1.409-1.409a2.25 2.25 0 013.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 001.5-1.5V6a1.5 1.5 0 00-1.5-1.5H3.75A1.5 1.5 0 002.25 6v12a1.5 1.5 0 001.5 1.5zm10.5-11.25h.008v.008h-.008V8.25zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
                  </svg>
                </div>
                <p class="text-[14px] font-semibold text-[color:var(--text-primary)]">No local services</p>
                <p class="text-[12px] text-[color:var(--text-tertiary)] mt-1.5 max-w-[400px] leading-relaxed">
                  A service the agent starts on a loopback port shows up here on its own, reachable at
                  <code class="font-mono text-[11px]"> /preview/&lt;port&gt;/</code>. You can also add a port above by hand.
                </p>
              </div>
            }
          >
            <div class="flex-1 overflow-y-auto p-3 sm:p-4">
              <div class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-3 sm:gap-4 items-start">
                <For each={services()}>
                  {(svc) => {
                    const open = () => expanded() === svc.port;
                    return (
                      <div
                        class={`rounded-xl border bg-[color:var(--bg-surface)] overflow-hidden transition ${
                          open()
                            ? 'col-span-full border-[color:var(--border-default)] shadow-[var(--shadow-sm)]'
                            : 'border-[color:var(--border-subtle)] hover:border-[color:var(--border-default)]'
                        }`}
                      >
                        {/* Tile header — the whole row toggles the inline view. */}
                        <div class="flex items-center gap-3 p-3">
                          <button
                            onClick={() => toggle(svc.port)}
                            class="flex items-center gap-3 min-w-0 flex-1 text-left"
                            title={open() ? 'Collapse' : 'Open in this page'}
                          >
                            <div class="w-9 h-9 rounded-lg bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] flex items-center justify-center shrink-0">
                              <svg class="w-4 h-4 text-[color:var(--text-secondary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.6">
                                <path stroke-linecap="round" stroke-linejoin="round" d="M12 21a9 9 0 100-18 9 9 0 000 18zM3.6 9h16.8M3.6 15h16.8M12 3a15 15 0 010 18 15 15 0 010-18z" />
                              </svg>
                            </div>
                            <div class="min-w-0 flex-1">
                              <p class="text-[13px] font-semibold text-[color:var(--text-primary)] truncate leading-tight" title={label(svc)}>
                                {label(svc)}
                              </p>
                              <p class="text-[11px] font-mono text-[color:var(--text-muted)] truncate leading-tight mt-0.5">
                                127.0.0.1:{svc.port}
                              </p>
                            </div>
                            <span
                              class={`w-1.5 h-1.5 rounded-full shrink-0 ${svc.up ? 'bg-emerald-400' : 'bg-zinc-600'}`}
                              title={svc.up ? 'listening' : 'not reachable'}
                            />
                            <svg class={`w-3.5 h-3.5 text-[color:var(--text-muted)] shrink-0 transition-transform ${open() ? 'rotate-180' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                              <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
                            </svg>
                          </button>

                          <div class="flex items-center gap-0.5 shrink-0">
                            <a
                              href={`/preview/${svc.port}/`}
                              target="_blank"
                              rel="noopener noreferrer"
                              class={iconBtn}
                              title="Open in a new tab"
                            >
                              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                                <path stroke-linecap="round" stroke-linejoin="round" d="M13.5 6H5.25A2.25 2.25 0 003 8.25v10.5A2.25 2.25 0 005.25 21h10.5A2.25 2.25 0 0018 18.75V10.5m-10.5 6L21 3m0 0h-5.25M21 3v5.25" />
                              </svg>
                            </a>
                            <Show when={svc.source === 'manual'}>
                              <button
                                onClick={() => removePort(svc.port)}
                                class={iconBtn}
                                title="Remove from the grid"
                              >
                                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                                </svg>
                              </button>
                            </Show>
                          </div>
                        </div>

                        {/* Expanded: the live view, in place. */}
                        <Show when={open()}>
                          <div class="border-t border-[color:var(--border-subtle)] h-[68vh] min-h-[360px] bg-white">
                            <Show
                              when={svc.up}
                              fallback={
                                <div class="h-full flex flex-col items-center justify-center text-center px-8 bg-[color:var(--bg-surface)]">
                                  <div class="w-12 h-12 rounded-xl bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-3">
                                    <svg class="w-5 h-5 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                                      <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                                    </svg>
                                  </div>
                                  <p class="text-[13px] font-semibold text-[color:var(--text-primary)]">Nothing is listening</p>
                                  <p class="text-[12px] text-[color:var(--text-tertiary)] mt-1.5 max-w-[380px] leading-relaxed">
                                    No service answers on <code class="font-mono text-[11px]">127.0.0.1:{svc.port}</code>.
                                    Start the process on that port and the tile reconnects on its own.
                                  </p>
                                </div>
                              }
                            >
                              <Show when={live()}>
                                <iframe
                                  class="w-full h-full border-0 bg-white"
                                  src={`/preview/${svc.port}/`}
                                  title={`Live preview of 127.0.0.1:${svc.port}`}
                                  allow="clipboard-write; autoplay; fullscreen"
                                />
                              </Show>
                            </Show>
                          </div>
                        </Show>
                      </div>
                    );
                  }}
                </For>
              </div>
            </div>
          </Show>
        </Show>
      </div>
    </div>
  );
}
