import { createMemo, Show } from 'solid-js';
import { useSession, type PendingQuestion } from '../context/session';
import DialogShell, { DialogHeader } from './dialog-shell';
import { AskUserPrompt } from './ask-user-card';

/**
 * The ask_user dialog, now only a fallback.
 *
 * A batch this session's own agent raised renders inline in the transcript, as
 * the body of its ask_user tool row (see ask-user-card.tsx): the question sits
 * where the agent asked it, and the answers are read back in place once given.
 *
 * The modal is left for the one batch that has no row here at all — a
 * teammate's question mirrored into the session it was spawned from, or one
 * from a server old enough not to say which call raised it. Without the dialog
 * it would be invisible while its teammate stayed blocked.
 *
 * Which of the two a batch gets is decided by WHO ASKED, never by whether its
 * row is in `messages()` yet. `question.requested` reaches this client the
 * moment the loop blocks, but the tool part it renders against only lands with
 * the message refresh a debounce later (`session.tsx`), so a transcript test
 * answers "no row" for that whole window: the dialog opened over the page and
 * unmounted again ~150ms into every question — a full-screen backdrop flash.
 * `sessionId` is the session the batch belongs to (a mirror is filed under its
 * `ownerSessionId`), so a batch filed under this page's session is exactly the
 * one whose inline row exists, or is about to.
 *
 * Only Submit answers, here as inline — closing does not. The loop is still
 * blocked waiting, so a dialog that dismissed itself on Escape would look like
 * an answer that never arrived. The shell is told the same thing by omission:
 * no onClose, so Escape and the backdrop are inert.
 */
export default function AskUserDialog() {
  const session = useSession();

  // The newest batch with no row of its own here: a teammate's question
  // mirrored in, or one from a server that predates `callId` (both keep the
  // dialog — the safe default, since such a batch would otherwise block the
  // loop with nothing on screen to answer it). A turn blocks on one batch at a
  // time, so there is at most one.
  const batch = createMemo<PendingQuestion | undefined>(() => {
    const here = session.activeSession()?.id;
    const qs = session.pendingQuestions();
    for (let i = qs.length - 1; i >= 0; i--) {
      const q = qs[i];
      if (q.callId && q.sessionId === here) continue;
      return q;
    }
    return undefined;
  });

  return (
    <Show when={batch()}>
      {(b) => (
        <DialogShell labelledBy="ask-user-title" widthClass="sm:max-w-[620px]">
          <DialogHeader
            id="ask-user-title"
            title="The agent has a question"
            description="Your turn pauses until it is answered."
            icon={
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M8.625 12a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0H8.25m4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0H12m4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0h-.375M21 12c0 4.556-4.03 8.25-9 8.25a9.764 9.764 0 01-2.555-.337A5.972 5.972 0 015.41 20.97a5.969 5.969 0 01-.474-.065 4.48 4.48 0 00.978-2.025c.09-.457-.133-.901-.467-1.226C3.93 16.178 3 14.189 3 12c0-4.556 4.03-8.25 9-8.25s9 3.694 9 8.25z" />
              </svg>
            }
            aside={
              <Show when={b().teammate}>
                <span
                  class="rounded bg-[color:var(--bg-hover)] px-1.5 py-0.5 font-mono text-micro text-[color:var(--text-tertiary)]"
                  title={`Question asked by teammate ${b().teammate}`}
                >
                  {b().teammate}
                </span>
              </Show>
            }
          />
          <AskUserPrompt batch={b()} variant="dialog" />
        </DialogShell>
      )}
    </Show>
  );
}
