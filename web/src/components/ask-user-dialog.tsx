import { createSignal, createMemo, createEffect, on, onMount, onCleanup, Show, For } from 'solid-js';
import { useSession, type PendingQuestion } from '../context/session';
import type { QuestionAPI, QuestionAnswerAPI } from '../api/client';

/**
 * The ask_user dialog. The agent puts a small batch of questions to the user and
 * blocks; the batch is rendered one question per screen inside this single
 * modal, with Back/Next between screens and a single Submit at the end.
 *
 * Only Submit answers — closing the dialog does not. The loop is still blocked
 * waiting, so a dialog that dismissed itself on Escape would look like an answer
 * that never arrived, and the user would have no way to tell. There is no close
 * button for the same reason; a question the user wants left open is answered by
 * submitting it blank, which the tool reports to the model as a non-answer rather
 * than as silence.
 */

export default function AskUserDialog() {
  const session = useSession();

  // The batch on screen. A new ask_user call replaces it (a turn is blocked on
  // one batch at a time); if several were somehow pending, the newest wins.
  const batch = createMemo<PendingQuestion | undefined>(() => {
    const qs = session.pendingQuestions();
    return qs.length ? qs[qs.length - 1] : undefined;
  });

  const [index, setIndex] = createSignal(0);
  // Per-question draft answers, keyed by question index — the batch is stable
  // while it is on screen, so index is a sound key.
  const [selected, setSelected] = createSignal<Record<number, string[]>>({});
  const [text, setText] = createSignal<Record<number, string>>({});

  const questions = () => batch()?.questions ?? [];
  const current = (): QuestionAPI | undefined => questions()[index()];
  const isLast = () => index() >= questions().length - 1;

  // Reset the draft whenever a different batch arrives, and start on screen one.
  createEffect(on(() => batch()?.questionId, () => {
    setIndex(0);
    setSelected({});
    setText({});
  }));

  const toggle = (qIdx: number, label: string, multi: boolean) => {
    setSelected((all) => {
      const prev = all[qIdx] || [];
      let next: string[];
      if (multi) {
        next = prev.includes(label) ? prev.filter((l) => l !== label) : [...prev, label];
      } else {
        next = prev.includes(label) ? [] : [label];
      }
      return { ...all, [qIdx]: next };
    });
  };

  const setAnswerText = (qIdx: number, value: string) => {
    setText((all) => ({ ...all, [qIdx]: value }));
  };

  const submit = () => {
    const b = batch();
    if (!b) return;
    const answers: QuestionAnswerAPI[] = questions().map((_, i) => {
      const sel = selected()[i] || [];
      const typed = (text()[i] || '').trim();
      return { selected: sel.length ? sel : undefined, text: typed || undefined };
    });
    session.respondQuestion(b.questionId, answers);
  };

  const onKey = (e: KeyboardEvent) => {
    if (!batch()) return;
    if (e.key === 'Escape') {
      // Escape must NOT answer, and must not fall through to the composer's own
      // Escape handler — that one aborts the running loop, which is exactly the
      // outcome this dialog exists to prevent. It is captured (see onMount) and
      // stopped here so neither happens; the batch stays pending and the user
      // can still answer it or stop the run from the composer deliberately.
      e.preventDefault();
      e.stopImmediatePropagation();
      return;
    }
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) submit();
  };
  // Capture phase, like the permission prompt: a bubbling listener would run
  // after the composer's handler had already aborted the loop.
  onMount(() => {
    document.addEventListener('keydown', onKey, true);
    onCleanup(() => document.removeEventListener('keydown', onKey, true));
  });

  return (
    <Show when={batch()}>
      <div
        class="fixed inset-0 z-50 bg-black/60 backdrop-blur-[2px] flex items-center justify-center p-4 modal-backdrop"
        onClick={(e) => e.stopPropagation()}
      >
          <div
            role="dialog"
            aria-modal="true"
            aria-label="Question from the agent"
            class="w-full max-w-[620px] bg-[color:var(--bg-surface)] border border-[color:var(--border-default)] rounded-2xl shadow-[0_24px_64px_rgba(0,0,0,0.6)] flex flex-col overflow-hidden max-h-[84vh] animate-scale-in"
          >
            {/* ---- Header ---- */}
            <div class="shrink-0 px-5 pt-4 pb-3 border-b border-[color:var(--border-subtle)]">
              <div class="flex items-start gap-2.5 min-w-0">
                <div class="w-7 h-7 rounded-lg bg-[color:var(--accent-soft)] flex items-center justify-center shrink-0 mt-0.5">
                  <svg class="w-4 h-4 text-[color:var(--accent)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M8.625 12a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0H8.25m4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0H12m4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0h-.375M21 12c0 4.556-4.03 8.25-9 8.25a9.764 9.764 0 01-2.555-.337A5.972 5.972 0 015.41 20.97a5.969 5.969 0 01-.474-.065 4.48 4.48 0 00.978-2.025c.09-.457-.133-.901-.467-1.226C3.93 16.178 3 14.189 3 12c0-4.556 4.03-8.25 9-8.25s9 3.694 9 8.25z" />
                  </svg>
                </div>
                <div class="min-w-0">
                  <h2 class="text-[14px] font-semibold text-[color:var(--text-primary)] leading-tight">
                    The agent has a question
                  </h2>
                  <p class="text-[11.5px] text-[color:var(--text-tertiary)] mt-1 leading-relaxed">
                    {questions().length === 1
                      ? 'Your turn pauses until it is answered.'
                      : `${questions().length} questions — your turn pauses until they are answered.`}
                  </p>
                </div>
              </div>
            </div>

            {/* ---- Progress ---- */}
            <Show when={questions().length > 1}>
              <div class="shrink-0 px-5 pt-3 flex items-center gap-2">
                <span class="text-[11px] text-[color:var(--text-tertiary)] font-medium tabular-nums">
                  {index() + 1} of {questions().length}
                </span>
                <div class="flex items-center gap-1.5">
                  <For each={questions()}>
                    {(_, i) => (
                      <button
                        type="button"
                        onClick={() => setIndex(i())}
                        aria-label={`Question ${i() + 1}`}
                        class={`h-1.5 rounded-full transition-all ${
                          i() === index()
                            ? 'w-5 bg-[color:var(--accent)]'
                            : 'w-1.5 bg-[color:var(--border-default)] hover:bg-[color:var(--text-tertiary)]'
                        }`}
                      />
                    )}
                  </For>
                </div>
              </div>
            </Show>

            {/* ---- Question ---- */}
            <div class="flex-1 overflow-y-auto px-5 py-4 min-h-0">
              <Show when={current()}>
                {(q) => (
                  <div class="flex flex-col gap-3">
                    <Show when={q().header}>
                      <h3 class="text-[13px] font-semibold text-[color:var(--text-primary)] leading-snug">
                        {q().header}
                      </h3>
                    </Show>
                    <p class="text-[13px] text-[color:var(--text-secondary)] leading-relaxed whitespace-pre-wrap">
                      {q().question}
                    </p>
                    <Show when={q().multiSelect}>
                      <p class="text-[11px] text-[color:var(--text-tertiary)]">Choose any that apply.</p>
                    </Show>

                    {/* Options. Selecting one is never exclusive with typing: the
                        free-text field below is always there, and the model is
                        told the options are a suggestion, not the full set. */}
                    <Show when={q().options?.length}>
                      <div class="flex flex-col gap-1.5 mt-0.5">
                        <For each={q().options}>
                          {(opt) => {
                            const on = () => (selected()[index()] || []).includes(opt.label);
                            return (
                              <button
                                type="button"
                                onClick={() => toggle(index(), opt.label, !!q().multiSelect)}
                                aria-pressed={on()}
                                class={`w-full text-left px-3 py-2 rounded-lg border transition flex items-start gap-2.5 ${
                                  on()
                                    ? 'border-[color:var(--accent)] bg-[color:var(--accent-soft)]'
                                    : 'border-[color:var(--border-default)] hover:border-[color:var(--text-tertiary)] hover:bg-[color:var(--bg-elevated)]'
                                }`}
                              >
                                <span
                                  class={`shrink-0 mt-0.5 w-4 h-4 flex items-center justify-center border ${
                                    q().multiSelect ? 'rounded-[4px]' : 'rounded-full'
                                  } ${on() ? 'border-[color:var(--accent)] bg-[color:var(--accent)]' : 'border-[color:var(--border-default)]'}`}
                                >
                                  <Show when={on()}>
                                    <svg class="w-3 h-3 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3">
                                      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                                    </svg>
                                  </Show>
                                </span>
                                <span class="min-w-0">
                                  <span class="block text-[12.5px] text-[color:var(--text-primary)] font-medium leading-snug">
                                    {opt.label}
                                  </span>
                                  <Show when={opt.description}>
                                    <span class="block text-[11.5px] text-[color:var(--text-tertiary)] mt-0.5 leading-snug">
                                      {opt.description}
                                    </span>
                                  </Show>
                                </span>
                              </button>
                            );
                          }}
                        </For>
                      </div>
                    </Show>

                    {/* Freeform — always available, whatever options were proposed. */}
                    <div class="flex flex-col gap-1.5 mt-1">
                      <label class="text-[11px] font-medium text-[color:var(--text-tertiary)] uppercase tracking-wide">
                        {q().options?.length ? 'Or answer in your own words' : 'Your answer'}
                      </label>
                      <textarea
                        value={text()[index()] || ''}
                        onInput={(e) => setAnswerText(index(), e.currentTarget.value)}
                        rows={q().options?.length ? 2 : 4}
                        placeholder="Type your answer, or leave blank…"
                        class="w-full resize-y bg-[color:var(--bg-base)] border border-[color:var(--border-default)] rounded-lg px-3 py-2 text-[12.5px] text-[color:var(--text-primary)] placeholder:text-[color:var(--text-tertiary)] focus:outline-none focus:border-[color:var(--accent)] transition"
                      />
                    </div>
                  </div>
                )}
              </Show>
            </div>

            {/* ---- Footer ---- */}
            <div class="shrink-0 px-5 py-3 border-t border-[color:var(--border-subtle)] flex items-center justify-between gap-3">
              <button
                type="button"
                onClick={() => setIndex((i) => Math.max(0, i - 1))}
                disabled={index() === 0}
                class="h-8 px-3 rounded-lg text-[12px] font-medium text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-[color:var(--text-secondary)]"
              >
                Back
              </button>
              <div class="flex items-center gap-2">
                <Show when={!isLast()}>
                  <button
                    type="button"
                    onClick={() => setIndex((i) => Math.min(questions().length - 1, i + 1))}
                    class="h-8 px-4 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-white hover:opacity-90 transition"
                  >
                    Next
                  </button>
                </Show>
                <Show when={isLast()}>
                  <button
                    type="button"
                    onClick={submit}
                    class="h-8 px-4 rounded-lg text-[12px] font-medium bg-[color:var(--accent)] text-white hover:opacity-90 transition"
                  >
                    {questions().length > 1 ? 'Submit answers' : 'Submit'}
                  </button>
                </Show>
              </div>
            </div>
          </div>
      </div>
    </Show>
  );
}
