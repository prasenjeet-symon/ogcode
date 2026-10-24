import { Show, createSignal } from 'solid-js';
import type { AgentPartData } from '../api/client';

// One board post on the transcript. A post is not a chat message — it belongs
// to the turn it was written in or delivered with — so it renders as a compact
// quoted block rather than a bubble: the header says who wrote it and to whom,
// the body clamps to a few lines because the full text is already what the
// model read.
//
// Inbound text is the peer's text as stored; the "[Board post from handle …]"
// framing the model sees is added when the turn is converted, so it never
// appears here.

function formatAge(ts: number): string {
  const diffMin = Math.floor((Date.now() - ts) / 60000);
  if (diffMin < 1) return 'just now';
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  return new Date(ts).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

function BoardIcon() {
  return (
    <svg class="w-3 h-3 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 6h16M4 12h10M4 18h13" />
    </svg>
  );
}

export default function AgentPart(props: { data: AgentPartData; createdAt?: number }) {
  const [expanded, setExpanded] = createSignal(false);
  const inbound = () => props.data.direction === 'in';
  const text = () => props.data.text ?? '';
  // Header routing: an inbound post goes to the session reading it, an outbound
  // one to the handle it named or, with no recipient, to the whole board.
  const route = () => {
    const handle = props.data.handle || 'board';
    if (inbound()) return { from: handle, to: 'you' };
    return { from: 'you', to: props.data.to || 'everyone' };
  };
  const kind = () => (props.data.kind && props.data.kind !== 'note' ? props.data.kind : '');
  const long = () => text().length > 320 || text().split('\n').length > 4;

  return (
    <div class="board-part my-1.5 border-l-2 border-[color:var(--border-subtle)] pl-3">
      <div class="flex items-center flex-wrap gap-x-1.5 gap-y-0.5 text-micro text-[color:var(--text-tertiary)]">
        <span class={inbound() ? 'text-[color:var(--text-secondary)]' : 'text-[color:var(--text-tertiary)]'}>
          <BoardIcon />
        </span>
        <span class="font-mono">{route().from}</span>
        <span aria-hidden="true">→</span>
        <span class="font-mono">{route().to}</span>
        <Show when={kind()}>
          <span aria-hidden="true">·</span>
          <span>{kind()}</span>
        </Show>
        <Show when={props.data.topic}>
          <span aria-hidden="true">·</span>
          <span>#{props.data.topic}</span>
        </Show>
        <Show when={props.data.seq}>
          <span aria-hidden="true">·</span>
          <span class="tabular-nums">seq {props.data.seq}</span>
        </Show>
        <Show when={props.createdAt}>
          <span aria-hidden="true">·</span>
          <span>{formatAge(props.createdAt!)}</span>
        </Show>
      </div>
      <div
        class={`mt-1 text-meta whitespace-pre-wrap break-words leading-[1.6] text-[color:var(--text-secondary)] ${
          long() && !expanded() ? 'line-clamp-4' : ''
        }`}
      >
        {text()}
      </div>
      <Show when={long()}>
        <button
          type="button"
          onClick={() => setExpanded(!expanded())}
          class="mt-0.5 text-micro text-[color:var(--text-tertiary)] hover:text-[color:var(--text-secondary)] transition-colors"
        >
          {expanded() ? 'Show less' : 'Show more'}
        </button>
      </Show>
    </div>
  );
}
