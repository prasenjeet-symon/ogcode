import { onCleanup } from 'solid-js';

export interface SSEEvent {
  type: string;
  // Monotonic per-connection sequence stamped by the bus on every event (absent
  // on control frames like server.connected/heartbeat). A gap means the server
  // dropped events to a full buffer and the client should resync.
  seq?: number;
  properties?: any;
}

// The wait before opening a new stream once the browser has given up on the
// last one, doubling while each new one is turned away too, up to the cap.
const GIVEN_UP_RETRY_MS = 1000;
const GIVEN_UP_RETRY_MAX_MS = 30_000;

// The server writes a heartbeat every 5 s (handleEvent in event_routes.go), so
// an open stream that stays silent this long is dead without the browser
// having been told — a laptop that slept, a proxy that kept its side open
// after the server went away — and is replaced. Four missed heartbeats, so a
// slow frame or a busy tab never trips it.
const SILENCE_LIMIT_MS = 20_000;
const SILENCE_CHECK_MS = 5000;

export function createSSE(url: string, onEvent: (event: SSEEvent) => void, onDrop?: () => void) {
  const BASE_URL = import.meta.env.VITE_API_URL || '';
  let es: EventSource | undefined;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let retryDelay = GIVEN_UP_RETRY_MS;
  let disposed = false;
  // When the open stream last delivered anything. Wall-clock time on purpose:
  // it keeps counting while the machine sleeps, which is when streams die.
  let lastHeard = Date.now();

  const open = () => {
    retryTimer = undefined;
    if (disposed) return;
    const source = new EventSource(`${BASE_URL}/api${url}`);
    es = source;

    source.onmessage = (e) => {
      lastHeard = Date.now();
      try {
        const event = JSON.parse(e.data) as SSEEvent;
        onEvent(event);
      } catch (_err) {
        if (e.data && e.data.trim()) {
          console.warn('SSE: failed to parse event data:', e.data.slice(0, 200));
        }
      }
    };

    // Connected: a later give-up starts again from the shortest wait.
    source.onopen = () => {
      lastHeard = Date.now();
      retryDelay = GIVEN_UP_RETRY_MS;
    };

    // Do NOT close or recreate on an ordinary error — the browser reconnects
    // automatically using the retry interval sent by the server (200 ms), and
    // a new EventSource would only race it. The browser stops for good when it
    // is answered with anything but a 200 event stream — a proxy or tunnel's
    // 502 while the server restarts — and leaves the source CLOSED; only then
    // is a new one opened, after a wait that grows while the answer stays the
    // same. The caller is told the stream dropped either way, both to show it
    // and to tell the reconnect that follows from the first connection.
    source.onerror = () => {
      if (source !== es) return;
      onDrop?.();
      if (source.readyState !== EventSource.CLOSED || disposed || retryTimer) return;
      retryTimer = setTimeout(open, retryDelay);
      retryDelay = Math.min(retryDelay * 2, GIVEN_UP_RETRY_MAX_MS);
    };
  };

  open();

  // Replace an open stream that has gone silent. Closing it fires no error, so
  // the drop is reported here; the new stream's server.connected reports the
  // reconnect as usual. A stream the browser is still reconnecting is left to
  // it.
  const watchdog = setInterval(() => {
    if (!es || es.readyState !== EventSource.OPEN) return;
    if (Date.now() - lastHeard < SILENCE_LIMIT_MS) return;
    es.close();
    onDrop?.();
    open();
  }, SILENCE_CHECK_MS);

  // Only close when the component tree is torn down (app exit / hot-reload).
  onCleanup(() => {
    disposed = true;
    clearInterval(watchdog);
    if (retryTimer) clearTimeout(retryTimer);
    es?.close();
  });
}
