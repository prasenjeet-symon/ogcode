// PostHog analytics wrapper for the ogcode web UI.
//
// PostHog is always-on product analytics for ogcode itself — it is NOT a
// user-facing feature. The credentials are hardcoded below and baked into the
// web bundle. There is no settings UI, no config API, and no DB storage.
// All calls are no-ops until init() succeeds, so components can call
// capture/identify freely without worrying about whether analytics is on.
import { createSignal } from 'solid-js';
import posthog from 'posthog-js';

// Hardcoded PostHog project credentials.
const POSTHOG_API_KEY = 'phc_CGzEmfPURHyNWrG49yNJA7wY5io8URFu3sazRYTAXw6Z';
const POSTHOG_API_HOST = 'https://app.posthog.com';

let initialised = false;

// Bumped every time PostHog hands us a fresh set of flags — on the first load
// and again after identify() or an explicit reload. Anything rendering a flag
// watches this so it re-evaluates when the answer arrives: the SDK fetches
// flags asynchronously, so a component that reads one at mount has almost
// always read "not loaded yet" first.
const [flagsVersion, setFlagsVersion] = createSignal(0);

// Stable distinct ID for the browser user, persisted in localStorage.
// PostHog's project is configured with "Require identified users", so
// anonymous traffic is dropped server-side — every install must identify
// itself with a real ID on load.
function currentDistinctId(): string {
  try {
    let id = localStorage.getItem('ph_ogcode_distinct_id');
    if (!id) {
      id = typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(36).slice(2)}`;
      localStorage.setItem('ph_ogcode_distinct_id', id);
    }
    return id;
  } catch {
    // localStorage may be unavailable (private mode) — fall back to a per-load id
    return `ui-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  }
}

/** Initialise PostHog. Safe to call once. */
export async function initPostHog(): Promise<void> {
  if (initialised) return;
  if (!POSTHOG_API_KEY) return;
  try {
    posthog.init(POSTHOG_API_KEY, {
      api_host: POSTHOG_API_HOST,
      autocapture: false,
      capture_pageview: true,
      persistence: 'localStorage+cookie',
      disable_session_recording: false,
    });
    initialised = true;
    // Flags load asynchronously and again after identify(), so re-evaluate flag
    // readers on every delivery. Subscribing here — inside the SDK's own setup
    // rather than from a component — is what makes a flag read safe whenever it
    // happens, including a deep link to a page that mounts before this runs.
    posthog.onFeatureFlags(() => setFlagsVersion((v) => v + 1));
    setFlagsVersion((v) => v + 1);
    // Identify with a stable per-install ID (localStorage-persisted) so the
    // "Require identified users" project setting doesn't drop our events.
    posthog.identify(currentDistinctId());
  } catch {
    // analytics should never break the app
  }
}

/** Capture a custom event with optional properties. No-op if not initialised. */
export function capture(event: string, properties?: Record<string, any>): void {
  if (!initialised) return;
  try {
    posthog.capture(event, properties);
  } catch {
    // swallow
  }
}

/** Identify the current user. No-op if not initialised. */
export function identify(distinctId?: string, properties?: Record<string, any>): void {
  if (!initialised) return;
  try {
    if (distinctId) {
      posthog.identify(distinctId, properties);
    } else {
      // No explicit ID given — fall back to the stable per-install ID
      posthog.identify(currentDistinctId(), properties);
    }
  } catch {
    // swallow
  }
}

/** Reset the current user identity (e.g. on logout). No-op if not initialised. */
export function resetPostHog(): void {
  if (!initialised) return;
  try {
    posthog.reset();
  } catch {
    // swallow
  }
}

/** Returns whether PostHog has been initialised and is active. */
export function posthogActive(): boolean {
  return initialised;
}

/**
 * Whether a boolean feature flag is enabled for this install.
 *
 * `undefined` means "no answer yet" — either flags have not loaded or the load
 * failed — and is deliberately distinct from `false`. PostHog caches flags in
 * localStorage, so only an install's first visit is ever `undefined`.
 */
export function featureFlagEnabled(key: string): boolean | undefined {
  if (!initialised) return undefined;
  try {
    return posthog.isFeatureEnabled(key);
  } catch {
    return undefined;
  }
}

/**
 * A counter that changes whenever PostHog delivers new flag values. Read it
 * inside a reactive scope to re-evaluate a flag when the answer arrives.
 */
export function featureFlagsVersion(): number {
  return flagsVersion();
}