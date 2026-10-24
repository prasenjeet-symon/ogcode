import { For, Show } from 'solid-js';
import type { JSX } from 'solid-js';
import type { DrawerRow } from '../lib/board-tree';
import { formatAge, formatElapsed, isLive, kindLabel, statusMeta } from '../lib/board-tree';

// One row of the Agents drawer's tree: identity, kind, status, duration and the
// row's quiet actions (collapse, open, stop). Pure presentation — the drawer owns
// the roster, the ticking clock and the collapse/stop state, and hands them in
// through props, so this component fetches nothing, holds no signal and runs no
// timer.
//
// The indent guides are 1px borders on plain spans, not SVG. decorateRows hands
// over one rail per ancestor level, and the deepest one is this row's own
// connector: the elbow takes its column (plain rails render minus their last
// entry when depth > 0), its 6px stub sits at the vertical middle, and its guide
// stops there when this row is its parent's last visible child.
//
// Action buttons stopPropagation: the row itself is the open target, so a click
// that meant "confirm stop" or "collapse" must not open the session underneath.

export interface BoardRowProps {
  row: DrawerRow;                          // this row's handle + indent geometry
  counts: { total: number; live: number }; // agents below this one, and how many are live
  isMe: boolean;                           // this row is the session currently in view
  collapsed: boolean;                      // this row's subtree is collapsed
  stopArmed: boolean;                      // its Stop button is in the confirm state
  stopping: boolean;                       // its stop request is in flight
  now: number;                             // shared ticking clock (ms) for live durations
  onOpen: (row: DrawerRow) => void;
  onStop: (handle: string) => void;
  onToggleCollapse: (handle: string) => void;
}

export default function BoardRow(props: BoardRowProps): JSX.Element {
  const h = () => props.row.h;
  const meta = () => statusMeta(h().status);
  const kind = () => h().kind;
  // The deepest rail is this row's own elbow, so it never renders as a straight
  // guide; the rest run through untouched.
  const rails = () => (props.row.depth > 0 ? props.row.rails.slice(0, -1) : props.row.rails);

  // Live rows show how long the agent has been on the board; terminal ones how
  // long ago they finished.
  const elapsed = () => {
    const e = formatElapsed(h(), props.now);
    return e ? `· ${e}` : '';
  };
  const age = () => formatAge(h().updatedAt);

  const countsTitle = () =>
    props.counts.live > 0
      ? `${props.counts.live} of ${props.counts.total} descendants still working`
      : `${props.counts.total} descendant${props.counts.total === 1 ? '' : 's'}, none running`;

  const kindChip = () =>
    kind() === 'teammate'
      ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)]'
      : kind() === 'task'
        ? 'bg-sky-400/10 text-sky-300'
        : 'bg-[color:var(--bg-elevated)] text-[color:var(--text-tertiary)]';

  // Only teammates and task children can be stopped, and only while running.
  const stoppable = () =>
    (h().kind === 'teammate' || h().kind === 'task') && !!h().sessionId && h().status === 'running';

  // A key or click aimed at one of the row's buttons belongs to the button, not
  // to the row: nested controls must never open the session behind them.
  const insideButton = (e: Event): boolean =>
    e.target instanceof Element && e.target.closest('button') !== null;

  const onRowClick = (e: MouseEvent) => {
    if (insideButton(e) || !h().sessionId) return;
    props.onOpen(props.row);
  };

  const onRowKeyDown = (e: KeyboardEvent) => {
    if (insideButton(e) || !h().sessionId) return;
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    props.onOpen(props.row);
  };

  // Unarmed arm, armed confirm — the drawer owns the armed state and reads the
  // handle back to decide which of the two this press is.
  const stop = (e: MouseEvent) => {
    e.stopPropagation();
    props.onStop(h().handle);
  };

  return (
    <div
      role="treeitem"
      aria-level={props.row.depth + 1}
      aria-expanded={props.row.hasChildren ? !props.collapsed : undefined}
      aria-selected={props.isMe}
      tabindex="-1"
      data-board-handle={h().handle}
      class="group relative flex items-start gap-2 rounded-lg py-1.5 pl-1 pr-1.5 transition-colors hover:bg-[color:var(--bg-hover)] outline-none focus-visible:ring-1 focus-visible:ring-[color:var(--accent-ring)]"
      classList={{ 'cursor-pointer': !!h().sessionId }}
      onClick={onRowClick}
      onKeyDown={onRowKeyDown}
    >
      <For each={rails()}>
        {(rail) => (
          <span
            class="w-3 shrink-0 self-stretch"
            classList={{ 'border-l border-[color:var(--border-subtle)]': rail }}
            aria-hidden="true"
          />
        )}
      </For>

      <Show when={props.row.depth > 0}>
        <span class="w-3 shrink-0 self-stretch flex flex-col" aria-hidden="true">
          <span class="relative flex-1 border-l border-[color:var(--border-subtle)]">
            <span class="absolute left-0 -bottom-px w-1.5 h-px bg-[color:var(--border-subtle)]" />
          </span>
          <span class="flex-1" classList={{ 'border-l border-[color:var(--border-subtle)]': !props.row.last }} />
        </span>
      </Show>

      <span class="relative w-1.5 h-1.5 rounded-full shrink-0 mt-1.5" title={meta().label} aria-hidden="true">
        <span class={`absolute inset-0 rounded-full ${meta().dot}`} />
        <Show when={h().status === 'running'}>
          <span class="absolute inset-0 rounded-full animate-pulse-ring" />
        </Show>
      </span>

      <div class="min-w-0 flex-1" classList={{ 'opacity-60': !isLive(h()) }}>
        <div class="flex items-baseline gap-1.5 min-w-0">
          <span
            class={`text-ui text-[color:var(--text-primary)] font-medium truncate min-w-0 ${h().title ? '' : 'font-mono'}`}
            title={h().title}
          >
            {h().title || h().handle}
          </span>
          <Show when={h().title}>
            <span class="text-micro font-mono text-[color:var(--text-muted)] truncate min-w-0" title={h().handle}>
              {h().handle}
            </span>
          </Show>
          <Show when={props.isMe}>
            <span class="px-1 rounded bg-[color:var(--accent-soft)] text-[color:var(--accent-ink)] text-micro font-medium shrink-0">
              you
            </span>
          </Show>
        </div>
        <div class="text-micro text-[color:var(--text-muted)] flex items-center gap-1.5 flex-wrap">
          <span class={`px-1 rounded text-micro uppercase shrink-0 ${kindChip()}`}>
            {kindLabel(kind())}
          </span>
          <span class={`shrink-0 ${meta().text}`}>{meta().label}</span>
          <Show when={elapsed()}>
            <span class="shrink-0 tabular-nums">{elapsed()}</span>
          </Show>
          <Show when={age()}>
            <span class="shrink-0">{age()}</span>
          </Show>
          <Show when={h().model}>
            {(m) => (
              <span class="text-micro font-mono text-[color:var(--text-muted)] max-w-[14ch] truncate shrink-0" title={m()}>
                {m()}
              </span>
            )}
          </Show>
          <Show when={props.counts.total > 0}>
            <span class="shrink-0 tabular-nums" title={countsTitle()}>
              {props.counts.live > 0 ? `${props.counts.live}/${props.counts.total}` : `${props.counts.total}`} spawned
            </span>
          </Show>
        </div>
      </div>

      <div class="ml-auto shrink-0 flex items-center gap-0.5">
        <Show when={props.row.hasChildren}>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              props.onToggleCollapse(h().handle);
            }}
            title={props.collapsed ? 'Expand subtree' : 'Collapse subtree'}
            aria-label={(props.collapsed ? 'Expand' : 'Collapse') + ' subtree'}
            class="icon-btn h-6! w-6! min-w-6!"
          >
            <svg
              class="w-3.5 h-3.5 shrink-0 transition-transform"
              classList={{ '-rotate-90': props.collapsed }}
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M6 9l6 6 6-6" />
            </svg>
          </button>
        </Show>

        <div
          class="flex items-center gap-0.5 transition-opacity"
          classList={{
            'opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-within:opacity-100':
              !props.stopArmed && !props.stopping,
          }}
        >
          <Show when={h().sessionId && !props.isMe}>
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                props.onOpen(props.row);
              }}
              title={'Open session ' + h().handle}
              aria-label={'Open session ' + h().handle}
              class="icon-btn h-6! w-6! min-w-6!"
            >
              <svg class="w-3.5 h-3.5 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M7 17L17 7M7 7h10v10" />
              </svg>
            </button>
          </Show>

          <Show when={stoppable()}>
            {props.stopping ? (
              <button
                type="button"
                disabled
                onClick={(e) => e.stopPropagation()}
                class="px-1.5 h-6 rounded-md text-micro font-medium text-red-300 bg-red-500/12 ring-1 ring-inset ring-red-500/35 flex items-center gap-1.5 cursor-not-allowed opacity-70"
              >
                <span class="w-3 h-3 border-2 border-current border-t-transparent rounded-full animate-spin" aria-hidden="true" />
                Stopping…
              </button>
            ) : props.stopArmed ? (
              <button
                type="button"
                onClick={stop}
                title="Confirm stop"
                aria-label="Confirm stop"
                class="px-1.5 h-6 rounded-md text-micro font-medium text-red-300 bg-red-500/12 ring-1 ring-inset ring-red-500/35"
              >
                Stop?
              </button>
            ) : (
              <button
                type="button"
                onClick={stop}
                title={'Stop ' + h().handle}
                aria-label={'Stop ' + h().handle}
                class="text-micro text-[color:var(--text-tertiary)] hover:text-red-400 transition-colors"
              >
                Stop
              </button>
            )}
          </Show>
        </div>
      </div>
    </div>
  );
}