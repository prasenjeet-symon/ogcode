import { createContext, useContext, type Accessor, type ParentComponent } from 'solid-js';
import { createSignal, createEffect, on } from 'solid-js';
import { useNavigate } from '@solidjs/router';
import { useServer } from './server';
import { getSession } from '../api/client';

// Desktop notifications: the browser's native Notification API, so finished
// loops and pending approvals reach the OS notification center while the
// ogcode tab is in the background. No native code — everything here runs in
// the page, and the browser handles delivery (and focus on click).
//
// Triggers:
//   loop.done            → a session's agent loop exited (finished or failed)
//   permission.requested → a tool call is waiting for approval
// Plan tasks already surface through the bell (NotificationProvider); their
// loops also publish loop.done, so listening only to loop.done here avoids
// double notifications for the same event.
//
// Two gates before any notification fires:
//   - permission: 'granted' — the user opted in via the banner
//     (DesktopNotificationBanner); the prompt must run inside a user gesture,
//     so the provider only *checks* state at boot.
//   - document.hidden — notifications are for when the user isn't looking at
//     the tab; while focused, in-app surfaces already cover the same events.
//
// Cross-tab: every ogcode tab receives the same SSE stream, so without
// coordination two open tabs would each raise a copy of every notification.
// The tab holding the Web Lock (`ogcode-desktop-notifications`) is the leader
// and the only one that notifies — and since the leader checks
// document.hidden, a *focused* leader tab suppresses notifications everywhere,
// which is right: the user is looking at ogcode. A tab without Web Locks
// support acts as leader; worst case is a duplicate, never a lost one.

export type DesktopPermissionState = 'default' | 'granted' | 'denied' | 'unsupported';

interface DesktopNotificationContextValue {
  permission: Accessor<DesktopPermissionState>;
  /** Must be called from a user gesture (banner button click). */
  requestPermission: () => Promise<void>;
}

const DesktopNotificationContext = createContext<DesktopNotificationContextValue>();

const LEADER_LOCK_NAME = 'ogcode-desktop-notifications';

// Session types whose loop.done events should not raise a desktop
// notification: they are background loops whose progress the user follows
// elsewhere (subagents surface through their parent session, notes through
// the notes page). Permission prompts notify regardless — they need an
// answer wherever they come from.
const SUPPRESSED_SESSION_TYPES = new Set(['subagent', 'note']);

// One notification per (kind, session) pair within a short window.
// permission.requested can re-fire while a permission is still pending, and a
// rapid follow-up loop can end within seconds of the first — keying on
// kind+session collapses those repeats into one notification.
const recentNotifications = new Map<string, number>();
const DEDUP_WINDOW_MS = 4_000;

function isDuplicate(kind: string, sessionId: string): boolean {
  const key = `${kind}:${sessionId}`;
  const now = Date.now();
  const last = recentNotifications.get(key);
  if (last !== undefined && now - last < DEDUP_WINDOW_MS) return true;
  recentNotifications.set(key, now);
  // Opportunistic cleanup so the map can't grow without bound across a
  // long-lived tab: entries older than the window are dead weight.
  for (const [k, at] of recentNotifications) {
    if (now - at >= DEDUP_WINDOW_MS) recentNotifications.delete(k);
  }
  return false;
}

export const DesktopNotificationProvider: ParentComponent = (props) => {
  const server = useServer();
  const navigate = useNavigate();
  const [permission, setPermission] = createSignal<DesktopPermissionState>('default');

  // Leader election: held by exactly one tab at a time (the browser hands the
  // lock to the next requester when the holder closes). The callback runs the
  // moment the lock is GRANTED — that's where leadership is claimed; the
  // outer promise only resolves when the lock is released, so nothing is set
  // in .then. A tab that loses the race (another tab already holds it) waits
  // in the request queue and claims leadership the moment the holder exits.
  // Non-Web-Locks browsers default to leader so notifications never go
  // missing — at worst a duplicate.
  let leader = true;
  if (typeof navigator !== 'undefined' && navigator.locks) {
    leader = false;
    navigator.locks.request(LEADER_LOCK_NAME, () => {
      leader = true;
      return new Promise(() => { /* hold the lock until this tab closes */ });
    }).catch(() => { leader = true; });
  }

  // Check permission state once at boot (never prompts — that requires a
  // user gesture, which the banner provides).
  createEffect(() => {
    if (typeof Notification === 'undefined') {
      setPermission('unsupported');
    } else {
      setPermission(Notification.permission as DesktopPermissionState);
    }
  });

  const requestPermission = async () => {
    if (typeof Notification === 'undefined') return;
    try {
      setPermission((await Notification.requestPermission()) as DesktopPermissionState);
    } catch {
      // Some browsers reject requestPermission outside a gesture; the banner
      // only calls this from a click, but keep the failure non-fatal.
    }
  };

  const notify = (kind: string, sessionId: string, title: string, body: string) => {
    if (permission() !== 'granted') return;
    if (typeof Notification === 'undefined') return;
    if (!document.hidden) return;
    if (!isLeader()) return;
    if (isDuplicate(kind, sessionId)) return;

    try {
      const n = new Notification(title, {
        body,
        icon: '/favicon.svg',
        tag: `${kind}:${sessionId}`,
      });
      n.onclick = () => {
        window.focus();
        navigate(`/session/${sessionId}`);
        n.close();
      };
      // Auto-dismiss so notifications don't pile up in centers that honour
      // auto-close; others keep them until clicked.
      n.onshow = () => setTimeout(() => n.close(), 12_000);
    } catch {
      // Notification construction can throw on some platforms; never fatal.
    }
  };

  // Resolve a session's title and type for notification bodies and
  // suppression. Cached because events can arrive in bursts for the same
  // session, and the lookup round-trips to /session/:id each time otherwise.
  const sessionCache = new Map<string, { title: string; sessionType: string }>();
  const sessionInfo = async (sessionId: string) => {
    const cached = sessionCache.get(sessionId);
    if (cached) return cached;
    try {
      const s = await getSession(sessionId);
      const info = { title: (s?.title || '').trim(), sessionType: s?.sessionType || '' };
      sessionCache.set(sessionId, info);
      return info;
    } catch {
      return { title: '', sessionType: '' };
    }
  };

  const trunc = (s: string, max: number) => (s.length > max ? `${s.slice(0, max - 1)}…` : s);

  const isLeader = () => leader;

  // --- SSE event handling -------------------------------------------------
  // Follows the app-wide pattern (see NotificationProvider): one
  // `on(server.eventTick, …, { defer: true })` effect reading
  // server.lastEvent() — which is exactly the event that bumped the tick.
  // `defer` skips the initial run so nothing fires on provider mount.
  createEffect(on(server.eventTick, () => {
    const last = server.lastEvent();
    if (!last) return;
    const props = last.properties as Record<string, any> | undefined;

    if (last.type === 'loop.done') {
      const sessionId = props?.sessionId;
      if (!sessionId) return;
      // The user aborted this loop themselves — they know it stopped. The
      // abort usually publishes reason:'aborted', but if the stream call was
      // still in flight it fails first with a context-cancellation error
      // (same race the in-app loop-error banner papers over by checking the
      // transcript's finish==='aborted'), so match that error text too.
      if (props?.reason === 'aborted') return;
      const errText = props?.error ? String(props.error) : '';
      if (errText && /context canceled|context deadline exceeded/i.test(errText)) return;

      sessionInfo(sessionId).then(({ title, sessionType }) => {
        if (SUPPRESSED_SESSION_TYPES.has(sessionType)) return;
        if (errText) {
          const err = trunc(errText, 160);
          notify(
            'loop-done',
            sessionId,
            'Ogcode — session failed',
            trunc(title ? `“${title}” failed: ${err}` : err, 200),
          );
        } else {
          notify(
            'loop-done',
            sessionId,
            'Ogcode',
            trunc(title ? `“${title}” finished.` : 'Agent loop finished.', 200),
          );
        }
      });
    } else if (last.type === 'permission.requested') {
      const sessionId = props?.sessionId;
      if (!sessionId) return;
      const tool = props?.tool || 'a tool';

      sessionInfo(sessionId).then(({ title }) => {
        const suffix = title ? ` — “${title}”` : '';
        notify(
          'permission',
          sessionId,
          'Ogcode — approval needed',
          trunc(`${tool} is waiting for your approval${suffix}.`, 200),
        );
      });
    }
  }, { defer: true }));
  // -------------------------------------------------------------------------

  const value: DesktopNotificationContextValue = { permission, requestPermission };
  return <DesktopNotificationContext.Provider value={value}>{props.children}</DesktopNotificationContext.Provider>;
};

export function useDesktopNotifications() {
  const ctx = useContext(DesktopNotificationContext);
  if (!ctx) throw new Error('useDesktopNotifications must be used within DesktopNotificationProvider');
  return ctx;
}