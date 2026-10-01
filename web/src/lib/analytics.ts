// Product analytics event taxonomy for the ogcode web UI.
//
// The app used to be a black box: PostHog autocapture is off on purpose (see
// lib/posthog.ts), so the only thing the UI ever reported was a single
// mid-loop-guidance event, and nothing about sessions, models, permissions or
// compactions reached the dashboard. This module is the one place that names
// the events the app emits, so the app funnels are built against a fixed
// vocabulary rather than whatever string each call site happens to type.
//
// Two rules hold for every event here, and they are why this exists as named
// functions instead of raw capture() calls scattered through the UI:
//
//  1. Never send content. A prompt, a file path, a command, a summary — none of
//     it leaves the machine. Lengths and counts are measures, not content, and
//     they are what make an event useful without carrying anything private.
//  2. Keep properties low-cardinality. Model and provider ids, tool names and
//     fixed enums are safe; anything a user typed is not.
//
// Every call is a no-op until PostHog has initialised, so callers never guard.
import { capture } from './posthog';

/** A new session was created. `model`/`provider` are the resolved values the
 *  session was opened with, or "default" when the server picks. */
export function trackSessionStarted(props: { model: string; provider: string }): void {
  capture('session_started', {
    model: props.model || 'default',
    provider: props.provider || 'default',
  });
}

/** The user sent a prompt. `length` is a character count and `images` a count —
 *  deliberately not the text, which can carry code, paths and secrets. */
export function trackMessageSent(props: {
  model: string;
  provider: string;
  images: number;
  length: number;
}): void {
  capture('message_sent', {
    model: props.model || 'default',
    provider: props.provider || 'default',
    images: props.images,
    length: props.length,
  });
}

/** The user switched the model. `context` says which surface (a chat session,
 *  a plan, or onboarding) so the two flows can be told apart. */
export function trackModelSelected(props: {
  model: string;
  provider: string;
  context: 'session' | 'plan' | 'onboarding';
}): void {
  capture('model_selected', {
    model: props.model || 'default',
    provider: props.provider || 'default',
    context: props.context,
  });
}

/** The user answered a tool-permission prompt. `tool` is the tool id (bash,
 *  write, edit, …) and `response` the outcome — no command or path. */
export function trackPermissionPromptAnswered(props: {
  tool: string;
  response: 'once' | 'always' | 'reject';
}): void {
  capture('permission_prompt_answered', {
    tool: props.tool || 'unknown',
    response: props.response,
  });
}

/** The context window was compacted. `origin` distinguishes the server's own
 *  auto-compaction from a manual `compact_context` call. */
export function trackCompactTriggered(props: { origin: 'auto' | 'manual' }): void {
  capture('compact_triggered', { origin: props.origin });
}

/** A live-preview service was opened. `source` says whether it was expanded in
 *  place, opened in a new tab, or opened as the result of publishing a port. */
export function trackPreviewOpened(props: {
  port: number;
  source: 'tile' | 'new_tab' | 'publish';
}): void {
  capture('preview_opened', { port: props.port, source: props.source });
}

/** An error was surfaced to the user. `reason` is the loop's own exit reason or
 *  a fixed label — never the error text, which can quote file paths. */
export function trackErrorShown(props: { reason: string; surface: 'loop' | 'send' }): void {
  capture('error_shown', {
    reason: props.reason || 'unknown',
    surface: props.surface,
  });
}
