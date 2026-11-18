import { For, Show, createEffect, createMemo, createSignal, on, onCleanup, onMount, type JSX } from 'solid-js';
import { useSession, type PendingQuestion } from '../context/session';
import type { QuestionAPI, QuestionAnswerAPI } from '../api/client';
import { DialogBody, DialogFooter } from './dialog-shell';

/**
 * One ask_user batch, rendered where it belongs: in the transcript, as the body
 * of the tool call that raised it, and — once answered — as a small card of the
 * questions and the answers they got.
 *
 * The prompt is one implementation for both surfaces. `inline` is the card that
 * replaces the collapsed tool row while the batch is pending; `dialog` fills a
 * DialogShell for a batch the transcript cannot hold — a teammate's question
 * mirrored into this session, whose own page has no tool part for it.
 *
 * Only Submit answers. Closing or escaping does not: the loop is still blocked
 * waiting, so a prompt that dismissed itself would look like an answer that
 * never arrived. There is no close button for the same reason; a question the
 * user wants left open is answered by submitting it blank, which the tool
 * reports to the model as a non-answer rather than as silence.
 *
 * A question may carry a showWhen condition on an earlier answer, so the screens
 * on offer depend on what has been picked so far. Visibility is evaluated live
 * against the draft answers and mirrors the server's rule, so the two agree on
 * which screens were actually shown.
 */

export interface AskUserPromptProps {
  batch: PendingQuestion;
  variant?: 'inline' | 'dialog';
}

/** A chip for a keystroke, matching the composer's hint styling. */
function Kbd(props: { children: JSX.Element }) {
  return (
    <kbd class="inline-flex items-center justify-center min-w-4 h-4 px-1 rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px] leading-none text-[color:var(--text-tertiary)]">
      {props.children}
    </kbd>
  );
}

export function AskUserPrompt(props: AskUserPromptProps) {
  const session = useSession();
  const dialog = () => props.variant === 'dialog';

  let cardEl: HTMLDivElement | undefined;

  const [pos, setPos] = createSignal(0);
  // Per-question draft answers, keyed by question index — the batch is stable
  // while it is on screen, so index is a sound key.
  const [selected, setSelected] = createSignal<Record<number, string[]>>({});
  const [text, setText] = createSignal<Record<number, string>>({});

  const questions = () => props.batch.questions ?? [];

  // Draft answers keyed by question id, for evaluating showWhen. Mirrors the
  // server's Condition.Match: an option list matches any of its labels selected,
  // no list matches any answer at all, and `not` inverts either.
  const answersById = () => {
    const sel = selected();
    const txt = text();
    const byId: Record<string, { selected: string[]; text: string }> = {};
    questions().forEach((q, i) => {
      if (!q.id) return;
      byId[q.id] = { selected: sel[i] || [], text: (txt[i] || '').trim() };
    });
    return byId;
  };

  const matches = (q: QuestionAPI): boolean => {
    const c = q.showWhen;
    if (!c || !c.question) return true;
    const a = answersById()[c.question];
    const sel = a?.selected ?? [];
    let hit: boolean;
    if (!c.options || c.options.length === 0) {
      hit = sel.length > 0 || !!a?.text;
    } else {
      hit = sel.some((s) => c.options!.includes(s));
    }
    return c.not ? !hit : hit;
  };

  // The real question indices the user is shown, in order. A condition only
  // names an earlier question, so a screen behind a skipped branch is skipped
  // too — its condition reads a blank answer.
  const visibleIdx = () => questions().map((_, i) => i).filter((i) => matches(questions()[i]));
  const total = () => visibleIdx().length;
  const curIdx = () => visibleIdx()[pos()] ?? -1;
  const current = (): QuestionAPI | undefined => questions()[curIdx()];
  const isLast = () => pos() >= total() - 1;

  // Reset the draft whenever a different batch arrives, and start on screen one.
  createEffect(on(() => props.batch.questionId, () => {
    setPos(0);
    setSelected({});
    setText({});
  }));

  // A user can change an earlier answer and shrink the visible set out from under
  // the current position; keep it on the last screen rather than off the end.
  createEffect(() => {
    const n = total();
    if (n > 0 && pos() > n - 1) setPos(n - 1);
  });

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
    const answers: QuestionAnswerAPI[] = questions().map((_, i) => {
      const sel = selected()[i] || [];
      const typed = (text()[i] || '').trim();
      return { selected: sel.length ? sel : undefined, text: typed || undefined };
    });
    session.respondQuestion(props.batch.questionId, answers);
  };

  const onKey = (e: KeyboardEvent) => {
    // A layer above the transcript owns the keyboard while it is open. Only our
    // own dialog counts as ours; anything else keeps its Escape.
    const modal = document.querySelector('[aria-modal="true"]');
    if (modal && !(cardEl && modal.contains(cardEl))) return;
    if (document.body.classList.contains('drawer-open')) return;

    if (e.key === 'Escape') {
      // Escape must NOT answer, and must not fall through to the composer's own
      // Escape handler — that one aborts the running loop, which is exactly the
      // outcome this prompt exists to prevent. Captured (see onMount) and stopped
      // here so neither happens; the batch stays pending and the user can still
      // answer it, or stop the run from the composer deliberately.
      e.preventDefault();
      e.stopImmediatePropagation();
      return;
    }
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      if (e.defaultPrevented) return;
      e.preventDefault();
      submit();
    }
  };
  // Capture phase, like the permission prompt: a bubbling listener would run
  // after the composer's handler had already aborted the loop.
  onMount(() => {
    document.addEventListener('keydown', onKey, true);
    onCleanup(() => document.removeEventListener('keydown', onKey, true));
  });

  // ---- Header text (inline only) -------------------------------------------

  const subtitle = () => {
    const n = total() === 1
      ? 'your turn pauses until it is answered'
      : `${total()} questions — your turn pauses until they are answered`;
    const head = questions()[0]?.header;
    return head ? `${head} · ${n}` : n;
  };

  // ---- Body ----------------------------------------------------------------

  const body = () => (
    <Show
      when={current()}
      fallback={
        <p class="text-[12px] text-[color:var(--text-tertiary)]">
          No screen in this batch applies yet — submit to tell the agent so.
        </p>
      }
    >
      {(q) => (
        <div class="flex flex-col gap-2.5">
          <Show when={q().header}>
            <div
              class={dialog()
                ? 'text-[13px] font-semibold text-[color:var(--text-primary)] leading-snug'
                : 'text-micro font-medium uppercase tracking-[.09em] text-[color:var(--text-muted)]'}
            >
              {q().header}
            </div>
          </Show>
          <p
            class={`whitespace-pre-wrap ${dialog()
              ? 'text-[13px] text-[color:var(--text-secondary)] leading-relaxed'
              : 'text-[12.5px] text-[color:var(--text-secondary)] leading-[1.62]'}`}
          >
            {q().question}
          </p>
          <Show when={q().multiSelect}>
            <p class="text-[11px] text-[color:var(--text-tertiary)] -mt-1">Choose any that apply.</p>
          </Show>

          {/* Options. Selecting one is never exclusive with typing: the
              free-text field below is always there, and the model is told the
              options are a suggestion, not the full set. */}
          <Show when={q().options?.length}>
            <div class="flex flex-col gap-1.5">
              <For each={q().options}>
                {(opt) => {
                  const on = () => (selected()[curIdx()] || []).includes(opt.label);
                  return (
                    <button
                      type="button"
                      onClick={() => toggle(curIdx(), opt.label, !!q().multiSelect)}
                      aria-pressed={on()}
                      class={`w-full text-left rounded-lg border transition flex items-start gap-2.5 ${
                        dialog() ? 'px-3 py-2' : 'px-2.5 py-[7px]'
                      } ${
                        on()
                          ? 'border-[color:var(--accent)] bg-[color:var(--accent-soft)]'
                          : 'border-[color:var(--border-default)] hover:border-[color:var(--border-strong)] hover:bg-[color:var(--bg-elevated)]'
                      }`}
                    >
                      <span
                        class={`shrink-0 mt-px flex items-center justify-center border ${
                          dialog() ? 'w-4 h-4' : 'w-[15px] h-[15px]'
                        } ${q().multiSelect ? 'rounded-[3px]' : 'rounded-full'} ${
                          on() ? 'border-[color:var(--accent)] bg-[color:var(--accent)]' : 'border-[color:var(--border-default)]'
                        }`}
                      >
                        <Show when={on()}>
                          <svg class="w-2.5 h-2.5 text-[color:var(--on-primary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.4">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                          </svg>
                        </Show>
                      </span>
                      <span class="min-w-0">
                        <span
                          class={`block font-medium leading-snug text-[color:var(--text-primary)] ${dialog() ? 'text-[12.5px]' : 'text-[12px]'}`}
                        >
                          {opt.label}
                        </span>
                        <Show when={opt.description}>
                          <span class="block text-[11px] text-[color:var(--text-tertiary)] mt-0.5 leading-snug">
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
          <div class="flex flex-col gap-1.5">
            <label
              class={`font-medium uppercase text-[color:var(--text-muted)] ${
                dialog() ? 'text-[11px] tracking-wide' : 'text-micro tracking-[.09em]'
              }`}
            >
              {q().options?.length ? 'Or answer in your own words' : 'Your answer'}
            </label>
            <textarea
              value={text()[curIdx()] || ''}
              onInput={(e) => setAnswerText(curIdx(), e.currentTarget.value)}
              rows={q().options?.length ? 2 : dialog() ? 4 : 3}
              placeholder="Type your answer, or leave blank…"
              class={`w-full resize-y bg-[color:var(--bg-base)] border border-[color:var(--border-default)] rounded-lg text-[color:var(--text-primary)] placeholder:text-[color:var(--text-tertiary)] transition focus:outline-none focus:border-[color:var(--accent)] ${
                dialog() ? 'px-3 py-2 text-[12.5px]' : 'px-2.5 py-1.5 text-[12px]'
              }`}
            />
          </div>
        </div>
      )}
    </Show>
  );

  // ---- Progress ------------------------------------------------------------

  const progress = () => (
    <Show when={total() > 1}>
      <div class={`flex items-center gap-2 ${dialog() ? 'shrink-0 px-6 pt-3' : 'px-3 pt-2'}`}>
        <span class="text-[10.5px] text-[color:var(--text-tertiary)] font-medium tabular-nums">
          {pos() + 1} of {total()}
        </span>
        <div class="flex items-center gap-1.5">
          <For each={visibleIdx()}>
            {(_, i) => (
              <button
                type="button"
                onClick={() => setPos(i())}
                aria-label={`Question ${i() + 1}`}
                class={`h-1.5 rounded-full transition-all ${
                  i() === pos()
                    ? 'w-5 bg-[color:var(--accent)]'
                    : 'w-1.5 bg-[color:var(--border-default)] hover:bg-[color:var(--text-tertiary)]'
                }`}
              />
            )}
          </For>
        </div>
      </div>
    </Show>
  );

  // ---- Actions -------------------------------------------------------------

  const hint = () => (
    <span class="flex items-center gap-1 text-[10.5px] text-[color:var(--text-tertiary)]">
      <Kbd>⌘</Kbd>
      <Kbd>↵</Kbd>
      <span class="ml-0.5">to submit</span>
    </span>
  );

  const actions = () => (
    <>
      <Show when={total() > 1}>
        <button
          type="button"
          onClick={() => setPos((i) => Math.max(0, i - 1))}
          disabled={pos() === 0}
          class={dialog()
            ? 'dlg-btn'
            : 'h-7 px-2.5 rounded-md border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] text-[11.5px] font-medium text-[color:var(--text-secondary)] transition-colors hover:bg-[color:var(--bg-hover)] hover:text-[color:var(--text-primary)] disabled:opacity-40 disabled:cursor-not-allowed'}
        >
          Back
        </button>
      </Show>
      <Show
        when={!isLast()}
        fallback={
          <button
            type="button"
            onClick={submit}
            class={dialog()
              ? 'dlg-btn dlg-btn-primary'
              : 'h-7 px-3 rounded-md bg-[color:var(--accent)] text-[11.5px] font-semibold text-[color:var(--on-primary)] transition-colors hover:bg-[color:var(--accent-hover)]'}
          >
            {total() > 1 ? 'Submit answers' : 'Submit'}
          </button>
        }
      >
        <button
          type="button"
          onClick={() => setPos((i) => Math.min(total() - 1, i + 1))}
          class={dialog()
            ? 'dlg-btn dlg-btn-primary'
            : 'h-7 px-3 rounded-md bg-[color:var(--accent)] text-[11.5px] font-semibold text-[color:var(--on-primary)] transition-colors hover:bg-[color:var(--accent-hover)]'}
        >
          Next
        </button>
      </Show>
    </>
  );

  return (
    <Show
      when={dialog()}
      fallback={
        <div
          ref={(el) => (cardEl = el)}
          data-ask-user-card
          role="group"
          aria-label="The agent's question"
          class="rounded-xl border border-[color:var(--border-default)] border-l-2 border-l-[color:var(--accent)] bg-[color:var(--bg-surface)] shadow-sm overflow-hidden"
        >
          {/* The prompt is its own heading: it is the body of the tool call, and
              the collapsed row above it is gone while it waits. */}
          <div class="flex items-start gap-2.5 px-3 py-2 border-b border-[color:var(--border-subtle)] bg-[color:var(--accent-soft)]">
            <span class="mt-px w-6 h-6 shrink-0 rounded-md border border-[color:var(--border-default)] bg-[color:var(--bg-surface)] text-[color:var(--accent-ink)] flex items-center justify-center">
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M8.625 12a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0H8.25m4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0H12m4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0h-.375M21 12c0 4.556-4.03 8.25-9 8.25a9.764 9.764 0 01-2.555-.337A5.972 5.972 0 015.41 20.97a5.969 5.969 0 01-.474-.065 4.48 4.48 0 00.978-2.025c.09-.457-.133-.901-.467-1.226C3.93 16.178 3 14.189 3 12c0-4.556 4.03-8.25 9-8.25s9 3.694 9 8.25z" />
              </svg>
            </span>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-1.5 min-w-0">
                <span class="text-[12.5px] font-semibold text-[color:var(--text-primary)] leading-tight truncate">
                  The agent asked you a question
                </span>
                <Show when={props.batch.teammate}>
                  <span
                    class="shrink-0 rounded bg-[color:var(--bg-hover)] px-1.5 py-0.5 font-mono text-micro text-[color:var(--text-tertiary)]"
                    title={`Question asked by teammate ${props.batch.teammate}`}
                  >
                    {props.batch.teammate}
                  </span>
                </Show>
              </div>
              <div class="text-[10.75px] text-[color:var(--text-tertiary)] mt-1 truncate" title={subtitle()}>
                {subtitle()}
              </div>
            </div>
            <span class="shrink-0 mt-1 flex items-center gap-1.5">
              <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] animate-pulse" />
              <span class="text-[10.5px] font-medium text-[color:var(--text-tertiary)]">waiting</span>
            </span>
          </div>

          {progress()}

          <div class="px-3 pt-2.5 pb-2.5">{body()}</div>

          <div class="flex items-center justify-between gap-2 px-2.5 py-1.5 border-t border-[color:var(--border-subtle)] bg-[color:var(--bg-base)]">
            {hint()}
            <div class="flex items-center gap-1.5">{actions()}</div>
          </div>
        </div>
      }
    >
      <div ref={(el) => (cardEl = el)} class="flex flex-col">
        {progress()}
        <DialogBody>{body()}</DialogBody>
        <DialogFooter hint={hint()}>{actions()}</DialogFooter>
      </div>
    </Show>
  );
}

// ---------------------------------------------------------------------------
// The answered batch, as history
// ---------------------------------------------------------------------------

export interface AskUserMeta {
  questions: QuestionAPI[];
  answers: QuestionAnswerAPI[];
  /** One flag per question: whether its screen was shown. Absent = all shown. */
  visible?: boolean[];
}

/**
 * Reads the resolved batch out of a completed ask_user part's metadata. The
 * tool writes `askUserQuestions` / `askUserAnswers` / `askUserVisible`; a part
 * from an older server carries none of them, and falls back to the raw blocks.
 */
export function askUserMeta(metadata: unknown): AskUserMeta | null {
  if (!metadata || typeof metadata !== 'object') return null;
  const m = metadata as Record<string, unknown>;
  const questions = m.askUserQuestions;
  const answers = m.askUserAnswers;
  if (!Array.isArray(questions) || questions.length === 0) return null;
  if (!Array.isArray(answers)) return null;
  const qs = questions as QuestionAPI[];
  if (!qs.every((q) => q && typeof q === 'object' && typeof (q as QuestionAPI).question === 'string')) return null;
  const visible = Array.isArray(m.askUserVisible) ? (m.askUserVisible as boolean[]) : undefined;
  return { questions: qs, answers: answers as QuestionAnswerAPI[], visible };
}

/**
 * The batch after it was answered, read back as a card: each screen that was
 * shown with the question as it was put and the answer as it was given, then
 * one dim line naming the screens the branching skipped. It sits under the tool
 * row whether or not the row is open — the question and the answer are the
 * reader's own turn, not tool output — while the raw input and output stay one
 * click down in the disclosure.
 */
export function AskUserAnswers(props: AskUserMeta) {
  const rows = createMemo(() =>
    props.questions.map((q, i) => ({
      q,
      answer: props.answers[i] ?? {},
      shown: !props.visible || props.visible[i] !== false,
    })),
  );
  const shown = () => rows().filter((r) => r.shown);
  const skipped = () => rows().filter((r) => !r.shown);
  const blank = (a: QuestionAnswerAPI) => !(a.selected?.length || a.text);

  return (
    <div class="rounded-lg border border-[color:var(--border-subtle)] border-l-2 border-l-[color:var(--accent)] bg-[color:var(--bg-elevated)] px-3 py-2">
      <For each={shown()}>
        {(row, i) => (
          <div class={`flex flex-col gap-1 ${i() > 0 ? 'mt-2.5 pt-2.5 border-t border-[color:var(--border-subtle)]' : ''}`}>
            <div class="text-micro font-medium uppercase tracking-[.09em] text-[color:var(--text-muted)]">
              {row.q.header || 'Question'}
            </div>
            <div class="text-[12px] text-[color:var(--text-secondary)] leading-relaxed whitespace-pre-wrap">
              {row.q.question}
            </div>
            <div class="flex flex-wrap items-center gap-x-1.5 gap-y-1 mt-0.5">
              <For each={row.answer.selected ?? []}>
                {(label) => (
                  <span class="inline-flex items-center gap-1 h-5 px-2 rounded-full bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)] text-[11px] font-medium">
                    <svg class="w-2.5 h-2.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                    </svg>
                    {label}
                  </span>
                )}
              </For>
              <Show when={blank(row.answer)}>
                <span class="text-[11.5px] italic text-[color:var(--text-muted)]">No answer given</span>
              </Show>
            </div>
            <Show when={row.answer.text}>
              <div class="mt-0.5 pl-2 border-l border-[color:var(--border-strong)] text-[12px] text-[color:var(--text-primary)] leading-relaxed whitespace-pre-wrap">
                {row.answer.text}
              </div>
            </Show>
          </div>
        )}
      </For>
      <Show when={skipped().length > 0}>
        <div class="mt-2.5 pt-2 border-t border-dashed border-[color:var(--border-subtle)] text-[10.5px] text-[color:var(--text-muted)] leading-relaxed">
          Skipped — {skipped().map((r) => r.q.header || 'a screen whose condition did not apply').join(' · ')}
        </div>
      </Show>
    </div>
  );
}

/**
 * The "jump to the question" pill. It stands in for the unread-count pill while
 * a batch is pending and the reader has scrolled its card out of view: a blocked
 * question matters more than the messages below it.
 */
export function JumpToQuestion(props: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={props.onClick}
      title="Jump to the question waiting for your answer"
      aria-label="Jump to the question waiting for your answer"
      class="pointer-events-auto inline-flex items-center gap-1.5 h-7 pl-2.5 pr-2 rounded-full border border-[color:var(--accent)] bg-[color:var(--bg-overlay)]/90 backdrop-blur-sm text-micro font-medium text-[color:var(--text-primary)] shadow-lg shadow-black/30 hover:bg-[color:var(--bg-hover)] anim-enter"
    >
      <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] animate-pulse shrink-0" />
      <span>The agent has a question</span>
      <svg class="w-3 h-3 text-[color:var(--text-tertiary)] shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
        <path stroke-linecap="round" stroke-linejoin="round" d="M19 14l-7 7m0 0l-7-7m7 7V3" />
      </svg>
    </button>
  );
}
