// PostHog analytics wrapper for the ogcode web UI.
//
// PostHog is always-on product analytics for ogcode itself — it is NOT a
// user-facing feature. The credentials are hardcoded below and baked into the
// web bundle. There is no settings UI, no config API, and no DB storage.
// All calls are no-ops until init() succeeds, so components can call
// capture/identify freely without worrying about whether analytics is on.
import posthog from 'posthog-js';

// Hardcoded PostHog project credentials.
const POSTHOG_API_KEY = 'phc_CGzEmfPURHyNWrG49yNJA7wY5io8URFu3sazRYTAXw6Z';
const POSTHOG_API_HOST = 'https://app.posthog.com';

let initialised = false;

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
    // Identify the current anonymous user
    posthog.identify();
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
      posthog.identify();
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