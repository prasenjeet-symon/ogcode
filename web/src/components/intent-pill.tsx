import { For, Show, createEffect, createMemo, createSignal, onCleanup } from 'solid-js';
import { Portal } from 'solid-js/web';
import { useSession } from '../context/session';
import type { IntentStep } from '../api/client';

// The forward-looking plan the agent is working through, shown in the session
// header. The transcript only ever reports what already happened — a tool row
// appears after the fact — so this pill is the one place the plan's shape and
// the agent's current heading are visible before the turn is over. The agent
// writes it with the update_intent tool, the server clears it at the start of
// each turn and pushes intent.changed, so the pill always reflects this turn's
// live plan.

function PillIcon(props: { done: boolean }) {
  return (
    <Show
      when={props.done}
      fallback={
        <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M4 6h.01M4 12h.01M4 18h.01" />
          <path d="M9 6h11M9 12h11M9 18h7" />
        </svg>
      }
    >
      <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="M8.3 12.3l2.5 2.5 4.9-5.4" />
      </svg>
    </Show>
  );
}

function StepGlyph(props: { status: string }) {
  switch (props.status) {
    case 'done':
      return (
        <svg class="glyph" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path d="M8.3 12.3l2.5 2.5 4.9-5.4" />
        </svg>
      );
    case 'dropped':
      return (
        <svg class="glyph" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path d="M8.9 8.9l6.2 6.2M15.1 8.9l-6.2 6.2" />
        </svg>
      );
    case 'active':
      return (
        <svg class="glyph" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <circle class="dot" cx="12" cy="12" r="3.3" fill="currentColor" stroke="none" />
        </svg>
      );
    default:
      return (
        <svg class="glyph" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
        </svg>
      );
  }
}

// A step says how many skills and MCP servers the plan matcher paired with it.
// The dot is the legend, the number is the count, and hovering names what the
// count stands for — the names ride on the step itself, so the tooltip costs no
// extra request. The label is the accessible name only, so a screen reader
// still reads a number as what it counts.
const matchLabel = (n: number, kind: string) => `${n} ${kind}${n === 1 ? '' : 's'} matched this step`;
const matchTitle = (names: string[] | undefined, n: number, kind: string) =>
  names && names.length > 0
    ? `${kind === 'skill' ? 'Skills' : 'MCP servers'}: ${names.join(', ')}`
    : matchLabel(n, kind);

export default function IntentPill() {
  const session = useSession();
  const [open, setOpen] = createSignal(false);
  const [pos, setPos] = createSignal<{ top: number; right: number; phone: boolean } | null>(null);
  let triggerRef: HTMLButtonElement | undefined;

  const steps = () => session.activeSession()?.intent?.steps ?? [];
  const total = () => steps().length;
  const doneCount = () => steps().filter((s) => s.status === 'done').length;
  const active = () => steps().find((s) => s.status === 'active');
  const nextPending = () => steps().find((s) => s.status === 'pending');
  const allDone = () =>
    total() > 0 && steps().every((s) => s.status === 'done' || s.status === 'dropped');
  const heading = () =>
    allDone() ? 'Plan complete' : (active()?.step ?? nextPending()?.step ?? 'Planning…');

  const toggleOpen = () => {
    if (open()) { setOpen(false); return; }
    const r = triggerRef?.getBoundingClientRect();
    if (r) {
      const W = 340, GAP = 6, M = 8;
      const vw = window.innerWidth;
      const phone = window.matchMedia('(max-width: 640px)').matches;
      const right = phone ? M : Math.min(Math.max(vw - r.right, M), Math.max(M, vw - W - M));
      setPos({ top: r.bottom + GAP, right, phone });
    }
    setOpen(true);
  };

  // Escape closes the popover — and only it: the composer reads Escape as "stop
  // the agent", which must not fire while the checklist is in the way.
  createEffect(() => {
    if (!open()) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.preventDefault();
      e.stopPropagation();
      setOpen(false);
      triggerRef?.focus();
    };
    window.addEventListener('keydown', onKey, true);
    onCleanup(() => window.removeEventListener('keydown', onKey, true));
  });

  // Switching sessions swaps out the plan under the popover; close it rather
  // than let the new session's steps appear beneath an open panel.
  createEffect(() => {
    session.activeSession()?.id;
    setOpen(false);
  });

  const countLabel = createMemo(() => `${doneCount()}/${total()}`);

  return (
    <Show when={total() > 0}>
      <div class="relative shrink-0">
        <button
          ref={triggerRef}
          type="button"
          onClick={toggleOpen}
          aria-haspopup="dialog"
          aria-expanded={open()}
          aria-label={`Agent plan: ${heading()}`}
          title="The agent's current plan — click to see every step"
          class="intent-pill"
          classList={{ 'is-done': allDone(), 'is-open': open() }}
        >
          <PillIcon done={allDone()} />
          <span class="txt">{heading()}</span>
          <span class="count">{countLabel()}</span>
          <svg class="chev" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M6 9l6 6 6-6" />
          </svg>
        </button>

        <Show when={open()}>
          <Portal>
            <div class="fixed inset-0 z-[210]" onClick={() => setOpen(false)} />
            <div
              role="dialog"
              aria-label="Agent plan"
              class="intent-pop fixed z-[211]"
              classList={{ 'is-phone': pos()?.phone ?? false }}
              style={{
                top: `${pos()?.top ?? 0}px`,
                right: `${pos()?.right ?? 0}px`,
              }}
            >
              <div class="pop-hd">
                <span class="pop-t">Current plan</span>
                <span class="pop-c">{countLabel()}</span>
              </div>
              <For each={steps()}>
                {(s: IntentStep) => (
                  <div class="step" classList={{ [`s-${s.status}`]: true }}>
                    <StepGlyph status={s.status} />
                    <span class="lbl">{s.step}</span>
                    <Show when={s.matchedSkills || s.matchedMCPs}>
                      <span class="mch">
                        <Show when={s.matchedSkills}>
                          <span
                            class="m sk"
                            aria-label={matchLabel(s.matchedSkills!, 'skill')}
                            title={matchTitle(s.matchedSkillNames, s.matchedSkills!, 'skill')}
                          >
                            <span class="d" />
                            {s.matchedSkills}
                          </span>
                        </Show>
                        <Show when={s.matchedMCPs}>
                          <span
                            class="m mcp"
                            aria-label={matchLabel(s.matchedMCPs!, 'MCP server')}
                            title={matchTitle(s.matchedMCPNames, s.matchedMCPs!, 'MCP server')}
                          >
                            <span class="d" />
                            {s.matchedMCPs}
                          </span>
                        </Show>
                      </span>
                    </Show>
                  </div>
                )}
              </For>
            </div>
          </Portal>
        </Show>
      </div>
    </Show>
  );
}
