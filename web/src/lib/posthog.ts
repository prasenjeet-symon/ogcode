// PostHog analytics wrapper for the ogcode web UI.
//
// This module lazily initialises the PostHog JS SDK when the server reports
// analytics is enabled and an API key is configured. All calls are no-ops
// until init() succeeds, so components can call capture/identify freely
// without worrying about whether analytics is on.
import posthog from 'posthog-js';
import { getConfig, getPostHogConfig, type PostHogConfig } from '../api/client';

let initialised = false;

/** Initialise PostHog if the backend reports it is enabled. Safe to call once. */
export async function initPostHog(): Promise<void> {
  if (initialised) return;
  try {
    const config = await getConfig();
    if (!config.posthogEnabled) return;
    const phConfig = await getPostHogConfig();
    if (!phConfig.apiKey) return;
    posthog.init(phConfig.apiKey, {
      api_host: phConfig.apiHost || 'https://app.posthog.com',
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

/** Capture a custom event with optional properties. No-op if disabled. */
export function capture(event: string, properties?: Record<string, any>): void {
  if (!initialised) return;
  try {
    posthog.capture(event, properties);
  } catch {
    // swallow
  }
}

/** Identify the current user. No-op if disabled. */
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

/** Reset the current user identity (e.g. on logout). No-op if disabled. */
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

export type { PostHogConfig };