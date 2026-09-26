import { createSignal, Show, onMount, onCleanup } from 'solid-js';
import { getVersion, type VersionResponse } from '../api/client';
import MarkdownContent from './markdown-content';

// Bottom-right toast announcing a newer release. Styled from the app's design
// tokens (flat elevated surface) rather than raw zinc classes so it matches
// every other floating panel. Release notes are collapsed to a two-line
// preview; the full markdown is one click away inside a scroll box.
export default function UpdateNotification() {
  const [versionInfo, setVersionInfo] = createSignal<VersionResponse | null>(null);
  const [isVisible, setIsVisible] = createSignal(true);
  const [showNotes, setShowNotes] = createSignal(false);
  const [copied, setCopied] = createSignal(false);

  let dismissedUntil = 0;
  let copyTimer: number | undefined;

  // Restore the dismiss window before the first version check resolves, so a
  // card dismissed minutes ago does not flash in again on reload.
  onMount(() => {
    const stored = localStorage.getItem('ogcode-update-dismissed-until');
    if (stored) dismissedUntil = parseInt(stored, 10);
    checkForUpdateCheck();
  });

  onCleanup(() => clearTimeout(copyTimer));

  async function checkForUpdateCheck() {
    try {
      const info = await getVersion();
      setVersionInfo(info);
      // Stay hidden if there is nothing to announce or the user dismissed it.
      if (!info.updateAvailable || Date.now() < dismissedUntil) {
        setIsVisible(false);
      }
    } catch (err) {
      console.error('Failed to check version:', err);
    }
  }

  function dismissFor(ms: number) {
    setIsVisible(false);
    dismissedUntil = Date.now() + ms;
    localStorage.setItem('ogcode-update-dismissed-until', dismissedUntil.toString());
  }

  function handleDismiss() {
    dismissFor(24 * 60 * 60 * 1000); // 24 hours
  }

  function handleDismissPermanent() {
    dismissFor(7 * 24 * 60 * 60 * 1000); // 7 days
  }

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(versionInfo()?.installCommand || '');
      setCopied(true);
      clearTimeout(copyTimer);
      copyTimer = window.setTimeout(() => setCopied(false), 1400);
    } catch (err) {
      console.error('Failed to copy install command:', err);
    }
  }

  // Latest version without its leading "v", so we can prefix a single one.
  const latestVersion = () => (versionInfo()?.latestVersion || '').replace(/^v/, '');

  const shouldHide = () => !isVisible() || !versionInfo()?.updateAvailable;

  return (
    <Show when={!shouldHide()}>
      <section
        class="update-toast fixed bottom-4 right-4 z-50 w-[22rem] max-w-[calc(100vw-2rem)] overflow-hidden rounded-xl border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)]"
        aria-label="Update available"
      >
        {/* Header */}
        <header class="flex items-start gap-3 px-4 pb-3 pt-4">
          <span class="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-[color:var(--accent-soft)] text-[color:var(--accent)]">
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 19V5M5 12l7-7 7 7" />
            </svg>
          </span>
          <div class="min-w-0 flex-1">
            <div class="text-[13px] font-semibold text-[color:var(--text-primary)]">Update available</div>
            <div class="mt-0.5 flex items-center gap-1.5 text-[11px] text-[color:var(--text-tertiary)]">
              <span class="font-mono">v{versionInfo()?.version}</span>
              <svg class="h-3 w-3 shrink-0 opacity-60" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M5 12h14M13 6l6 6-6 6" />
              </svg>
              <span class="font-mono font-medium text-[color:var(--text-secondary)]">v{latestVersion()}</span>
            </div>
          </div>
          <button
            onClick={handleDismiss}
            class="-mr-1 -mt-1 flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-[color:var(--text-tertiary)] transition-colors hover:bg-[color:var(--bg-hover)] hover:text-[color:var(--text-primary)]"
            title="Dismiss for 24 hours"
            aria-label="Dismiss for 24 hours"
          >
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </header>

        {/* Release notes — collapsed to a two-line preview by default */}
        <Show when={versionInfo()?.releaseNotes}>
          <div class="px-4 pb-3">
            <button
              onClick={() => setShowNotes((v) => !v)}
              class="flex w-full items-center justify-between text-[11px] font-medium uppercase tracking-wide text-[color:var(--text-tertiary)] transition-colors hover:text-[color:var(--text-secondary)]"
              aria-expanded={showNotes()}
            >
              What's new
              <svg
                class="h-3 w-3 transition-transform duration-200"
                classList={{ 'rotate-180': showNotes() }}
                fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"
              >
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 9l6 6 6-6" />
              </svg>
            </button>
            <Show
              when={showNotes()}
              fallback={
                <div
                  class="mt-2 cursor-pointer"
                  onClick={() => setShowNotes(true)}
                >
                  <MarkdownContent
                    text={versionInfo()?.releaseNotes || ''}
                    class="prose-chat-preview line-clamp-2 text-[color:var(--text-tertiary)]"
                  />
                </div>
              }
            >
              <div class="update-notes mt-2 max-h-40 overflow-y-auto rounded-lg border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] p-3 text-[color:var(--text-secondary)]">
                <MarkdownContent text={versionInfo()?.releaseNotes || ''} class="text-[12px]" />
              </div>
            </Show>
          </div>
        </Show>

        {/* Install command */}
        <Show when={versionInfo()?.installCommand}>
          <div class="px-4 pb-4">
            <div class="mb-1.5 flex items-center justify-between">
              <span class="text-[11px] font-medium uppercase tracking-wide text-[color:var(--text-tertiary)]">Install command</span>
              <button
                onClick={handleCopy}
                class="code-block-copy"
                classList={{ 'is-copied': copied() }}
                title={copied() ? 'Copied' : 'Copy to clipboard'}
              >
                <Show when={!copied()}>
                  <svg class="mr-1 h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                  </svg>
                </Show>
                {copied() ? 'Copied' : 'Copy'}
              </button>
            </div>
            <code class="block truncate rounded-lg border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] px-3 py-2 font-mono text-[12px] text-[color:var(--text-secondary)]">
              {versionInfo()?.installCommand}
            </code>
          </div>
        </Show>

        {/* Footer */}
        <footer class="flex items-center justify-between border-t border-[color:var(--border-subtle)] px-4 py-2.5">
          <button
            onClick={handleDismissPermanent}
            class="text-[11px] text-[color:var(--text-muted)] transition-colors hover:text-[color:var(--text-secondary)]"
          >
            Don't show again
          </button>
          <a
            href={versionInfo()?.releaseUrl}
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1 text-[11px] font-medium text-[color:var(--accent)] transition-colors hover:text-[color:var(--accent-hover)]"
          >
            Release notes
            <svg class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
            </svg>
          </a>
        </footer>
      </section>
    </Show>
  );
}
