import { Show } from 'solid-js';
import { useDocIndex } from '../context/docindex';
import DialogShell, { DialogFooter } from './dialog-shell';

/**
 * The consent prompt the server raises when a project is larger than the
 * auto-index file limit and the user has not granted consent. It exists so a
 * big project is never indexed behind the user's back: the run costs tokens,
 * and this is where that cost is agreed to. The choice is remembered for the
 * project — the same switch as the one under Settings -> Project index.
 *
 * A centred layout: one outlined glyph, the question, one paragraph, and the
 * two answers in the footer beside the note that the choice is remembered.
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
          class="sm:max-w-[580px]"
        >
          <div class="flex justify-end px-4 pt-4">
            <button
              type="button"
              onClick={close}
              class="icon-btn"
              title="Close (Esc)"
              aria-label="Close"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>

          <div class="dlg-empty flex-1 min-h-0 overflow-y-auto">
            <div class="dlg-empty-icon">
              <svg class="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
              </svg>
            </div>

            <h2 id="idx-consent-title" class="dlg-empty-title">
              Index this project?
            </h2>
            <p id="idx-consent-desc" class="dlg-empty-desc">
              This project has more than {req().limit.toLocaleString()} files, which is more than
              ogcode indexes on its own. Indexing it once keeps every turn after that cheaper and
              faster — fewer tokens spent, quicker answers, and less time waiting before they
              arrive.
            </p>
          </div>

          <DialogFooter hint="Your choice is remembered for this project — you can change it under Settings → Project index.">
            <button type="button" onClick={close} class="dlg-btn">
              Not now
            </button>
            <button type="button" onClick={allow} class="dlg-btn dlg-btn-primary">
              Allow indexing
            </button>
          </DialogFooter>
        </DialogShell>
      )}
    </Show>
  );
}
