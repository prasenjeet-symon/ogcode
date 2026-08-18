import { Show } from 'solid-js';

/**
 * Floating "jump to latest" affordance, shown while the user is scrolled up.
 *
 * It must be rendered inside a non-scrolling wrapper that spans the message
 * column. The earlier version was `fixed left-1/2`, which centres on the
 * viewport rather than the conversation — with the 260px sidebar expanded it
 * sat well left of centre, and it jumped sideways whenever the sidebar
 * collapsed to 48px.
 */
export default function JumpToLatest(props: { count: number; onClick: () => void }) {
  const label = () => (props.count > 0 ? `${props.count} new` : 'Jump to latest');
  const description = () =>
    props.count > 0
      ? `${props.count} new message${props.count === 1 ? '' : 's'} — jump to latest`
      : 'Jump to latest message';

  return (
    <button
      type="button"
      onClick={props.onClick}
      title={description()}
      aria-label={description()}
      class="pointer-events-auto inline-flex items-center gap-1.5 h-7 pl-1.5 pr-2.5 rounded-full border border-[color:var(--border-subtle)] bg-[color:var(--bg-overlay)]/90 backdrop-blur-sm text-[11px] font-medium text-zinc-300 shadow-lg shadow-black/30 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] anim-enter"
    >
      <span class="w-4 h-4 rounded-full bg-[color:var(--accent)] text-[color:var(--on-primary)] flex items-center justify-center shrink-0">
        <svg class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3">
          <path stroke-linecap="round" stroke-linejoin="round" d="M19 14l-7 7m0 0l-7-7m7 7V3" />
        </svg>
      </span>
      <span class="tabular-nums">
        <Show when={props.count > 0} fallback={label()}>
          <span class="text-[color:var(--accent)]">{props.count}</span> new
        </Show>
      </span>
    </button>
  );
}
