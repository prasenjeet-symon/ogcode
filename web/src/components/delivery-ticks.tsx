import { Match, Switch, createMemo } from 'solid-js';
import type { MessageWithParts } from '../api/client';
import { useSession } from '../context/session';

/**
 * How far a prompt got on its way to the model, in the vocabulary of a
 * messaging app: one tick sent, two ticks delivered, two green ticks answered.
 *
 * The state is not stored on the prompt — it is read from the assistant message
 * the loop created to answer it, which is the record the server actually owns.
 * Only the first step of a turn can point back at a human prompt (from step 2
 * on, the preceding user-role message is the loop's own tool-result message),
 * so this pairing never has to reason about steps.
 */
export type TickState = 'pending' | 'sent' | 'delivered' | 'responding' | 'failed';

const LABEL: Record<TickState, string> = {
  pending: 'Sending…',
  sent: 'Sent — waiting for the model',
  delivered: 'Delivered to the model',
  responding: 'Model is responding',
  failed: 'Not delivered',
};

/** formatLatency renders a first-token time as "842ms" / "1.4s" / "12s". */
export function formatLatency(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const sec = ms / 1000;
  return sec < 10 ? `${sec.toFixed(1)}s` : `${Math.round(sec)}s`;
}

export default function DeliveryTicks(props: { msg: MessageWithParts }) {
  const session = useSession();

  // The answering message. Read from the unfiltered list on purpose: the
  // rendered list drops assistant messages that have no parts yet, which is
  // exactly the shape this message has when the stream first connects.
  const reply = createMemo(() =>
    session.messages().find(
      (m) => m.info.role === 'assistant' && m.info.parentId === props.msg.info.id,
    ),
  );

  const state = createMemo<TickState>(() => {
    const info = props.msg.info;
    // Still optimistic — the POST has not come back yet.
    if (info.id.startsWith('temp-')) {
      return session.sendFailed(info.id) ? 'failed' : 'pending';
    }

    const answer = reply();
    if (!answer) return 'sent';
    const d = answer.info.delivery;

    // The model spoke. Terminal: a turn that errors or is cancelled after its
    // first token still started answering, and the error has its own banner.
    if (d?.firstTokenAt) return 'responding';
    if (answer.info.error || answer.info.interrupted) return 'failed';
    if (d?.connectedAt) return 'delivered';
    // Finished without ever opening a stream — a crash or a restart swept it up.
    if (answer.info.finish) return 'failed';
    return 'sent';
  });

  const tone = createMemo(() => {
    switch (state()) {
      case 'responding':
        return 'var(--success)';
      case 'failed':
        return 'var(--danger)';
      case 'delivered':
        return 'var(--text-secondary)';
      default:
        return 'var(--text-muted)';
    }
  });

  const title = createMemo(() => {
    const base = LABEL[state()];
    const d = reply()?.info.delivery;
    if (state() === 'responding' && d?.ttftMs != null) {
      const parts = [`first token in ${formatLatency(d.ttftMs)}`];
      if (d.queuedMs) parts.push(`${formatLatency(d.queuedMs)} queued`);
      if (d.attempts && d.attempts > 1) parts.push(`${d.attempts} attempts`);
      return `${base} — ${parts.join(', ')}`;
    }
    if (state() === 'failed') {
      const detail = reply()?.info.interrupted?.detail;
      if (detail) return `${base} — ${detail}`;
    }
    return base;
  });

  return (
    <span
      class="inline-flex items-center shrink-0"
      style={{ color: tone() }}
      title={title()}
      aria-label={title()}
      role="img"
    >
      <Switch>
        <Match when={state() === 'pending'}>
          <svg class="w-3.5 h-3.5" viewBox="0 0 20 16" fill="none" aria-hidden="true">
            <circle cx="10" cy="8" r="5.4" stroke="currentColor" stroke-width="1.5" />
            <path d="M10 5.2V8l1.9 1.4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </Match>
        <Match when={state() === 'failed'}>
          <svg class="w-3.5 h-3.5" viewBox="0 0 20 16" fill="none" aria-hidden="true">
            <circle cx="10" cy="8" r="5.4" stroke="currentColor" stroke-width="1.5" />
            <path d="M10 5.3v3.2M10 10.6v.1" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" />
          </svg>
        </Match>
        <Match when={state() === 'sent'}>
          <svg class="w-3.5 h-3.5" viewBox="0 0 20 16" fill="none" aria-hidden="true">
            <path d="M4.4 8.6 8 12.2 15 4.4" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </Match>
        <Match when={state() === 'delivered' || state() === 'responding'}>
          <svg class="w-3.5 h-3.5" viewBox="0 0 20 16" fill="none" aria-hidden="true">
            <path d="M1.4 8.6 5 12.2 12 4.4" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" />
            <path d="M7.4 8.6 11 12.2 18 4.4" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </Match>
      </Switch>
    </span>
  );
}
