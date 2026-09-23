// PostHog feature flags, as reactive values.
//
// `posthog.ts` answers a flag query once; this turns that answer into
// something a component can render. The two cases it exists to handle are
// the asynchronous load — flags are fetched after the app is already on
// screen, so the first read is "no answer yet" — and the fail-safe: a flag
// that has not loaded, or whose load failed, is OFF, never on.
import { createMemo } from 'solid-js';
import { featureFlagEnabled, featureFlagsVersion } from './posthog';

/**
 * A boolean PostHog flag as a reactive accessor.
 *
 * `undefined` (not loaded, or the load failed) reads as `false`, so gating an
 * unreleased feature behind a flag can only ever leave it hidden — an
 * unreachable PostHog is not a way to turn the feature on.
 */
export function useFeatureFlag(key: string): () => boolean {
  return createMemo(() => {
    // Track flag deliveries: the SDK fetches them asynchronously, and again
    // after identify(), so without this read the memo would cache "off" from
    // the first paint and never re-evaluate.
    featureFlagsVersion();
    return featureFlagEnabled(key) === true;
  });
}
