import { createSignal, createEffect, createMemo, on, For, Show, onCleanup, onMount } from 'solid-js';
import { createStore, reconcile } from 'solid-js/store';
import { useLocation, useNavigate, useSearchParams } from '@solidjs/router';
import { useServer } from '../context/server';
import {
  getPreviewServices,
  publishPreviewPort,
  unpublishPreviewPort,
  type PreviewService,
} from '../api/client';
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

// parsePort reads a port the way the server does: 1–5 digits, no sign and no
// leading zero, in 1–65535. Number() alone would also take "0x1F90", "1e3" or
// " 80 ", naming a port the user never typed.
function parsePort(v: unknown): number {
  if (typeof v !== 'string' || !/^[1-9][0-9]{0,4}$/.test(v)) return 0;
  const n = Number(v);
  return n <= 65535 ? n : 0;
}

// isLoopbackHost reports whether a hostname names the machine the browser runs
// on — which is where a *.localhost preview link lands, whatever machine serves
// this page.
function isLoopbackHost(hostname: string): boolean {
  const h = hostname.toLowerCase().replace(/^\[|\]$/g, '');
  return h === 'localhost' || h.endsWith('.localhost') || h === '::1' || /^127\.\d+\.\d+\.\d+$/.test(h);
}

function hostnameOf(url: string): string {
  try {
    return new URL(url).hostname;
  } catch {
    return '';
  }
}

function originOf(url: string): string {
  try {
    return new URL(url).origin;
  } catch {
    return '';
  }
}

// errorText pulls the server's own message out of an API error ("API error
// 400: {"error":"…"}"), falling back to the raw text.
function errorText(e: unknown): string {
  const msg = e instanceof Error ? e.message : String(e);
  const m = /\{.*\}\s*$/s.exec(msg);
  if (m) {
    try {
      const j = JSON.parse(m[0]);
      if (typeof j.error === 'string') return j.error;
    } catch {
      /* not JSON */
    }
  }
  return msg;
}

const panel = 'h-full flex flex-col items-center justify-center text-center px-8 bg-[color:var(--bg-surface)]';
const panelIcon =
  'w-12 h-12 rounded-xl bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-3';
const panelTitle = 'text-[13px] font-semibold text-[color:var(--text-primary)]';
const panelText = 'text-[12px] text-[color:var(--text-tertiary)] mt-1.5 max-w-[380px] leading-relaxed';

/**
 * TileBody is an expanded tile's live area: the service in an iframe when it can
 * be shown, otherwise a panel saying why not — not published yet, nothing
 * listening, or not a web page.
 */
function TileBody(props: { svc: PreviewService; busy: boolean; onPublish: () => void }) {
  // Once the app is showing, keep it. The poll's probe gives up after two
  // seconds, and a dev server compiling a page (Next.js on a first hit) can
  // take longer — flipping the tile to "down" then would tear the frame out and
  // reload the app when it came back. Opening, reloading or un-publishing the
  // tile is what re-decides.
  const embed = createMemo<boolean>(
    (prev) => props.svc.published && (prev || (props.svc.up && props.svc.html)),
    false,
  );
  // The frame gets allow-same-origin below, which is safe only because a
  // preview is a DIFFERENT origin from this page — the grant then gives the app
  // its own origin, never ours. Should a URL ever resolve to this page's own
  // origin, it is not embedded at all.
  const crossOrigin = () => originOf(props.svc.url) !== window.location.origin;

  return (
    <Show
      when={props.svc.published}
      fallback={
        <div class={panel}>
          <div class={panelIcon}>
            <svg class="w-5 h-5 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" />
            </svg>
          </div>
          <p class={panelTitle}>Not previewed yet</p>
          <p class={panelText}>
            Port <code class="font-mono text-[11px]">{props.svc.port}</code> is not published, so its preview link is
            closed. Previewing it serves whatever listens on{' '}
            <code class="font-mono text-[11px]">127.0.0.1:{props.svc.port}</code> to anyone who can reach this
            server's preview address.
          </p>
          <button
            onClick={() => props.onPublish()}
            disabled={props.busy}
            class="mt-3 h-8 px-3 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-white hover:opacity-90 disabled:opacity-50 disabled:cursor-not-allowed transition"
          >
            {props.busy ? 'Publishing…' : `Preview port ${props.svc.port}`}
          </button>
        </div>
      }
    >
      <Show
        when={embed() && crossOrigin()}
        fallback={
          <Show
            when={props.svc.up}
            fallback={
              <div class={panel}>
                <div class={panelIcon}>
                  <svg class="w-5 h-5 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                  </svg>
                </div>
                <p class={panelTitle}>Nothing is listening</p>
                <p class={panelText}>
                  No service answers on <code class="font-mono text-[11px]">127.0.0.1:{props.svc.port}</code>.
                  Start the process on that port and the tile reconnects on its own.
                </p>
              </div>
            }
          >
            <div class={panel}>
              <div class={panelIcon}>
                <svg class="w-5 h-5 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" />
                </svg>
              </div>
              <p class={panelTitle}>Not an embeddable page</p>
              <p class={panelText}>
                This service answered, but not with a web page it can show here — or it redirects somewhere else.{' '}
                <a href={props.svc.url} target="_blank" rel="noopener noreferrer" class="text-[color:var(--accent)] hover:underline">
                  Open it in a new tab
                </a>{' '}
                instead.
              </p>
            </div>
          </Show>
        }
      >
        {/*
          The service runs at its own origin (<port>.<preview-domain>), so
          allow-same-origin hands it ITS origin — the storage, cookies and
          same-origin requests an app needs — and never this page's. Without it
          the frame gets an opaque origin: localStorage throws, cookies vanish,
          and every request the app makes to its own server turns cross-origin.
        */}
        <iframe
          class="w-full h-full border-0 bg-white"
          src={props.svc.url}
          title={`Live preview of 127.0.0.1:${props.svc.port}`}
          allow="clipboard-write; autoplay; fullscreen"
          sandbox="allow-scripts allow-same-origin allow-forms allow-modals allow-downloads allow-popups allow-popups-to-escape-sandbox"
        />
      </Show>
    </Show>
  );
}

/**
 * PreviewPage shows the services published for this project — ports the agent
 * handed back a live-preview URL for, or ones added here by hand — as a grid of
 * tiles, each labelled with the page's own title. Clicking a tile expands it
 * into a live iframe in place, so several apps can be watched without leaving
 * the page. Each service is served at its own origin (<port>.<preview-domain>),
 * so the iframe loads the app exactly as it would run on its own.
 */
export default function PreviewPage() {
  const server = useServer();
  const location = useLocation();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  // The port named by the URL: a legacy /preview/<port>/ link is still a
  // client-side navigation that lands here with the port in the PATH. A ?port=
  // query spells the same thing.
  const pathPort = (): number => {
    const m = /^\/preview\/([^/]+)/.exec(location.pathname);
    return m ? parsePort(m[1]) : 0;
  };
  const deepLinkPort = (): number => pathPort() || parsePort(searchParams.port);

  // Reconciled in place, keyed by port. <For> keys its rows by object identity,
  // so swapping in the fresh objects each poll returns would remount every tile
  // — and reload the app in the open iframe every ten seconds.
  const [services, setServices] = createStore<PreviewService[]>([]);
  const [checking, setChecking] = createSignal(true);
  // Which tile is expanded into a live iframe in place. Seeded from a deep link
  // so a URL the agent handed back opens that app.
  const [expanded, setExpanded] = createSignal(0);
  // Expanding remounts the iframe: a fresh mount is what actually reloads an
  // embedded service (nudging src by a hash param wouldn't — same-document
  // navigation doesn't re-fire window.onload).
  const [live, setLive] = createSignal(true);
  const [addValue, setAddValue] = createSignal('');
  const [notice, setNotice] = createSignal('');
  // The port a publish/unpublish is in flight for, so its buttons can't double-fire.
  const [busyPort, setBusyPort] = createSignal(0);

  // The ports the grid must list whether or not they are published: any the
  // URL names. Everything else is a port this project published.
  const requested = (): number[] => {
    const d = deepLinkPort();
    return d ? [d] : [];
  };

  let timer: ReturnType<typeof setInterval> | undefined;
  const refresh = (ports: number[] = requested()) =>
    getPreviewServices(ports, server.directory())
      .then((r) => {
        setServices(reconcile(r.services ?? [], { key: 'port' }));
        setChecking(false);
      })
      .catch(() => {
        // Keep the last list: a blip must not blank the grid.
        setChecking(false);
      });

  // The grid is scoped to the project in view, so the workspace has to be read
  // before the first query and re-read when it changes — otherwise switching
  // projects would keep the previous project's tiles. on() tracks the directory
  // alone, so the location read inside refresh — which changes on its own — does
  // not also re-trigger it. This also stands in for the initial fetch, so
  // onMount does not call refresh itself.
  createEffect(
    on(
      () => server.directory(),
      () => refresh(),
    ),
  );

  onMount(() => {
    // Normalize /preview/<port>/… to /preview?port=<port> so an agent's
    // handed-back link settles into the page's canonical URL.
    const p = pathPort();
    if (p) navigate(`/preview?port=${p}`, { replace: true });
    const d = deepLinkPort();
    if (d) setExpanded(d);
    timer = setInterval(() => refresh(), POLL_MS);
  });
  onCleanup(() => {
    if (timer) clearInterval(timer);
  });

  const remount = () => {
    setLive(false);
    setTimeout(() => setLive(true), 50);
  };

  const publish = async (n: number) => {
    setBusyPort(n);
    setNotice('');
    try {
      await publishPreviewPort(n, server.directory() || undefined);
      setExpanded(n);
      remount();
      await refresh(Array.from(new Set([...requested(), n])));
    } catch (e) {
      setNotice(errorText(e));
    } finally {
      setBusyPort(0);
    }
  };

  const removeTile = async (svc: PreviewService) => {
    const n = svc.port;
    if (svc.published) {
      if (!confirm(`Stop previewing port ${n}? Its preview link stops working until the port is added again.`)) return;
      setBusyPort(n);
      try {
        await unpublishPreviewPort(n);
      } catch (e) {
        setNotice(errorText(e));
        setBusyPort(0);
        return;
      }
      setBusyPort(0);
    }
    // A deep-linked port lives in the URL: removing its tile must drop the link
    // too, or requested() adds it back on the next refresh. The /preview/:port
    // route means the pathname can be what names it, and setSearchParams cannot
    // change the pathname — so navigate to the plain page, which clears both.
    const wasDeepLink = n === deepLinkPort();
    if (wasDeepLink) navigate('/preview', { replace: true });
    setExpanded((cur) => (cur === n ? 0 : cur));
    refresh(wasDeepLink ? [] : requested());
  };

  const submitAdd = (e: Event) => {
    e.preventDefault();
    const n = parsePort(addValue().trim());
    if (!n) {
      setNotice('Enter a port between 1 and 65535.');
      return;
    }
    setAddValue('');
    publish(n);
  };

  const toggle = (n: number) => {
    setExpanded((cur) => (cur === n ? 0 : n));
    // Remount the iframes so re-opening a tile shows the service's current state.
    remount();
  };

  const reloadAll = () => {
    remount();
    refresh();
  };

  const upCount = () => services.filter((s) => s.up).length;
  const label = (svc: PreviewService) => svc.title || `127.0.0.1:${svc.port}`;

  // A *.localhost preview link resolves to the machine the BROWSER runs on. When
  // this page itself was reached by some other name, the browser is probably on
  // another machine, and the tiles will point it at its own loopback — say so
  // rather than leave the user staring at a connection error.
  const loopbackOnlyPreviews = createMemo(() => {
    if (isLoopbackHost(window.location.hostname)) return '';
    const h = services.map((s) => hostnameOf(s.url)).find((x) => x && isLoopbackHost(x));
    return h ? h.replace(/^[^.]+\./, '*.') : '';
  });

  const headerBtn =
    'h-8 px-3 rounded-lg text-[12px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:border-[color:var(--border-default)] disabled:opacity-50 disabled:cursor-not-allowed transition flex items-center gap-1.5 shrink-0';
  const iconBtn =
    'h-7 w-7 rounded-md flex items-center justify-center text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)] disabled:opacity-50 transition shrink-0';

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
                {checking()
                  ? 'loading…'
                  : services.length
                    ? `${services.length} service${services.length === 1 ? '' : 's'} · ${upCount()} up`
                    : 'nothing yet'}
              </p>
            </div>
          </div>

          <div class="flex-1" />

          {/* Add a port by hand: a service the agent did not start (or did not
              hand back) is previewed only once someone chooses to. */}
          <form onSubmit={submitAdd} class="flex items-center gap-1.5 shrink-0">
            <input
              value={addValue()}
              onInput={(e) => {
                setAddValue(e.currentTarget.value);
                setNotice('');
              }}
              inputmode="numeric"
              maxlength={5}
              placeholder="Port"
              aria-label="Port to preview"
              class="h-8 w-[64px] sm:w-[80px] px-2.5 rounded-lg text-[12px] font-mono bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] text-[color:var(--text-primary)] placeholder:text-[color:var(--text-muted)] focus:outline-none focus:border-[color:var(--border-default)]"
            />
            <button type="submit" class={headerBtn} disabled={!addValue().trim() || busyPort() !== 0} title="Preview the service on this port">
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
              </svg>
              <span class="hidden md:inline">Add</span>
            </button>
          </form>

          <button onClick={reloadAll} class={headerBtn} title="Re-check every service">
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
            <span class="hidden md:inline">Reload</span>
          </button>
        </header>

        <Show when={notice()}>
          <div class="shrink-0 px-3 sm:px-4 py-2 text-[12px] border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] text-[color:var(--text-secondary)] flex items-center gap-2">
            <span class="flex-1 min-w-0">{notice()}</span>
            <button onClick={() => setNotice('')} class={iconBtn} title="Dismiss">
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </Show>

        {/* ---- Body ---- */}
        <Show
          when={!checking()}
          fallback={
            <div class="flex-1 flex items-center justify-center">
              <div class="flex flex-col items-center gap-3">
                <div class="w-5 h-5 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin" />
                <p class="text-[12px] text-[color:var(--text-tertiary)]">Loading services…</p>
              </div>
            </div>
          }
        >
          <Show
            when={services.length}
            fallback={
              <div class="flex-1 flex flex-col items-center justify-center text-center px-8">
                <div class="w-14 h-14 rounded-2xl bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-4">
                  <svg class="w-6 h-6 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.4">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 15.75l5.159-5.159a2.25 2.25 0 013.182 0l5.159 5.159m-1.5-1.5l1.409-1.409a2.25 2.25 0 013.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 001.5-1.5V6a1.5 1.5 0 00-1.5-1.5H3.75A1.5 1.5 0 002.25 6v12a1.5 1.5 0 001.5 1.5zm10.5-11.25h.008v.008h-.008V8.25zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
                  </svg>
                </div>
                <p class="text-[14px] font-semibold text-[color:var(--text-primary)]">No services</p>
                <p class="text-[12px] text-[color:var(--text-tertiary)] mt-1.5 max-w-[400px] leading-relaxed">
                  A service the agent starts in this project shows up here once it hands you a URL, at the service's own
                  hostname. To preview one you started yourself, add its port above.
                </p>
              </div>
            }
          >
            <div class="flex-1 overflow-y-auto p-3 sm:p-4">
              <Show when={loopbackOnlyPreviews()}>
                <div class="mb-3 sm:mb-4 rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] px-3 py-2.5 text-[12px] leading-relaxed text-[color:var(--text-secondary)]">
                  Preview links use <code class="font-mono text-[11px]">{loopbackOnlyPreviews()}</code>, which a browser
                  resolves to its own machine. You opened ogcode at{' '}
                  <code class="font-mono text-[11px]">{window.location.hostname}</code>, so unless this browser runs on
                  the ogcode machine the previews will not load here. Open ogcode through an SSH tunnel (
                  <code class="font-mono text-[11px]">ssh -L</code>), or set{' '}
                  <code class="font-mono text-[11px]">OGCODE_PREVIEW_DOMAIN</code> on the server to a wildcard DNS name
                  that points at it.
                </div>
              </Show>
              <div class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-3 sm:gap-4 items-start">
                <For each={services}>
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
                                {svc.published ? '' : ' · not previewed'}
                              </p>
                            </div>
                            <span
                              class={`w-1.5 h-1.5 rounded-full shrink-0 ${
                                !svc.published
                                  ? 'border border-zinc-500'
                                  : svc.up
                                    ? 'bg-emerald-400'
                                    : 'bg-zinc-600'
                              }`}
                              title={!svc.published ? 'not published' : svc.up ? 'listening' : 'not reachable'}
                            />
                            <svg class={`w-3.5 h-3.5 text-[color:var(--text-muted)] shrink-0 transition-transform ${open() ? 'rotate-180' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                              <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
                            </svg>
                          </button>

                          <div class="flex items-center gap-0.5 shrink-0">
                            <Show when={svc.published}>
                              <a
                                href={svc.url}
                                target="_blank"
                                rel="noopener noreferrer"
                                class={iconBtn}
                                title="Open in a new tab"
                              >
                                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                                  <path stroke-linecap="round" stroke-linejoin="round" d="M13.5 6H5.25A2.25 2.25 0 003 8.25v10.5A2.25 2.25 0 005.25 21h10.5A2.25 2.25 0 0018 18.75V10.5m-10.5 6L21 3m0 0h-5.25M21 3v5.25" />
                                </svg>
                              </a>
                            </Show>
                            <button
                              onClick={() => removeTile(svc)}
                              disabled={busyPort() === svc.port}
                              class={iconBtn}
                              title={svc.published ? 'Stop previewing this port' : 'Remove from the grid'}
                            >
                              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                              </svg>
                            </button>
                          </div>
                        </div>

                        {/* Expanded: the live view, in place. */}
                        <Show when={open()}>
                          <div class="border-t border-[color:var(--border-subtle)] h-[68vh] min-h-[360px] bg-[color:var(--bg-surface)]">
                            <Show when={live()}>
                              <TileBody svc={svc} busy={busyPort() === svc.port} onPublish={() => publish(svc.port)} />
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
