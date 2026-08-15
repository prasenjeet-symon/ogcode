import { onCleanup } from 'solid-js';

export interface SSEEvent {
  type: string;
  // Monotonic per-connection sequence stamped by the bus on every event (absent
  // on control frames like server.connected/heartbeat). A gap means the server
  // dropped events to a full buffer and the client should resync.
  seq?: number;
  properties?: any;
}

export function createSSE(url: string, onEvent: (event: SSEEvent) => void) {
  const BASE_URL = import.meta.env.VITE_API_URL || '';
  const es = new EventSource(`${BASE_URL}/api${url}`);

  es.onmessage = (e) => {
    try {
      const event = JSON.parse(e.data) as SSEEvent;
      onEvent(event);
    } catch (_err) {
      if (e.data && e.data.trim()) {
        console.warn('SSE: failed to parse event data:', e.data.slice(0, 200));
      }
    }
  };

  // Do NOT close or recreate on error — the browser reconnects automatically
  // using the retry interval sent by the server (200 ms). Closing here would
  // create a new EventSource and lose all registered handlers temporarily.
  es.onerror = () => {
    // browser will reconnect on its own
  };

  // Only close when the component tree is torn down (app exit / hot-reload).
  onCleanup(() => es.close());
}
