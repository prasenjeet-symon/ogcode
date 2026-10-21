import { Show } from 'solid-js';
import { useDocIndex } from '../context/docindex';
import DialogShell from './dialog-shell';

/**
 * The consent prompt the server raises when a project is larger than the
 * auto-index file limit and the user has not granted consent. It exists so a
 * big project is never indexed behind the user's back: the run costs tokens,
 * and this is where that cost is agreed to. The choice is remembered for the
 * project — the same switch as the one under Settings -> Project index.
 *
 * A centred layout: one glyph, the title, one paragraph, one CTA. The × in the
 * corner is the only way to decline, so the single button can stay the plain
 * answer to the question the dialog asks.
 */
export default function IndexConsentDialog() {
  const docIndex = useDocIndex();
  const request = () => docIndex.consentRequest();

  const allow = () => void docIndex.setConsent(true);
  const close = () => docIndex.dismissConsentRequest();

  return (
    <Show when={request()}>
      {(req) => (
        <DialogShell
          labelledBy="idx-consent-title"
          describedBy="idx-consent-desc"
          onClose={close}
          class="sm:w-[60%] sm:max-w-[560px]"
        >
          <div class="relative px-7 pt-9 pb-7 flex flex-col items-center text-center">
            <button
              type="button"
              onClick={close}
              class="icon-btn absolute right-3 top-3"
              title="Close (Esc)"
              aria-label="Close"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>

            <div class="w-14 h-14 rounded-2xl bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)] shadow-[inset_0_0_0_1px_var(--accent-ring)] flex items-center justify-center">
              <svg class="w-7 h-7" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.6">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
              </svg>
            </div>

            <h2 id="idx-consent-title" class="mt-5 text-base font-semibold text-[color:var(--text-primary)] tracking-[-0.01em]">
              Index this project?
            </h2>
            <p id="idx-consent-desc" class="mt-2 max-w-[400px] text-meta text-[color:var(--text-secondary)] leading-relaxed">
              This project has more than {req().limit.toLocaleString()} files, which is more than
              ogcode indexes on its own. Indexing it once keeps every turn after that cheaper and
              faster — fewer tokens spent, quicker answers, and less time waiting before they
              arrive. Your choice is remembered for this project.
            </p>

            <button
              type="button"
              onClick={allow}
              class="mt-6 h-9 px-5 rounded-lg text-meta font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] transition shadow-[var(--shadow-sm)]"
            >
              Allow indexing
            </button>
          </div>
        </DialogShell>
      )}
    </Show>
  );
}
