import { createMemo, createSignal, Show } from 'solid-js';
import { useSession } from '../context/session';
import { formatTokens } from './token-pill';

// Ring geometry: a 14px circle, stroke drawn as a fraction of its circumference.
const RING_R = 5.5;
const RING_C = 2 * Math.PI * RING_R;

type Tone = 'ok' | 'warm' | 'hot';

// How full the model's context was on its last request, against the window of
// the model the next request will use, with the point where the loop compacts.
// The size is the last step's whole prompt — uncached input plus both cache
// variants — the exact figure the agent loop compares with that same compaction
// point, so the meter and the loop cannot disagree about when it happens.
export default function ContextMeter() {
  const session = useSession();

  // Hover shows the breakdown on desktop; touch has no hover, so a tap toggles
  // it there — the same interaction as the token pill beside it.
  const isCoarse = () => window.matchMedia('(hover: none)').matches;
  const [hovered, setHovered] = createSignal(false);
  const [pinned, setPinned] = createSignal(false);
  const showBreakdown = () => pinned() || (!isCoarse() && hovered());

  // The newest assistant step that reported usage.
  const last = createMemo(() => {
    const msgs = session.messages();
    for (let i = msgs.length - 1; i >= 0; i--) {
      const { role, tokens } = msgs[i].info;
      if (role !== 'assistant' || !tokens) continue;
      const used = (tokens.input ?? 0) + (tokens.cacheRead ?? 0) + (tokens.cacheWrite ?? 0);
      if (used > 0) return { used, cached: tokens.cacheRead ?? 0 };
    }
    return null;
  });

  // The model the next request goes to. Its id can be served by more than one
  // provider, so match the provider too when it is known.
  const model = createMemo(() => {
    const id = session.selectedModel();
    const providerId = session.selectedProvider();
    const list = session.models();
    return list.find((m) => m.id === id && m.providerId === providerId) ?? list.find((m) => m.id === id);
  });

  const used = () => last()?.used ?? 0;
  const windowSize = () => model()?.contextWindow ?? 0;
  const compactAt = () => model()?.compactAtTokens ?? 0;

  // With an unknown window the compaction point is the only ceiling there is.
  const ceiling = () => windowSize() || compactAt();
  const fraction = () => (ceiling() > 0 ? Math.min(1, used() / ceiling()) : 0);
  const percent = () => Math.round(fraction() * 100);

  // Colour by distance to compaction, not to the window: compaction is what
  // actually happens next.
  const tone = (): Tone => {
    const c = compactAt();
    if (!c) return 'ok';
    const r = used() / c;
    return r >= 0.9 ? 'hot' : r >= 0.7 ? 'warm' : 'ok';
  };
  const toneStroke = () =>
    tone() === 'hot' ? 'var(--color-red-400, #f87171)'
      : tone() === 'warm' ? 'var(--color-amber-400, #fbbf24)'
        : 'var(--accent)';

  const valueText = () =>
    windowSize() > 0
      ? `${used().toLocaleString()} of ${windowSize().toLocaleString()} tokens (${percent()}%)`
      : `${used().toLocaleString()} tokens, context window unknown`;

  return (
    <Show when={last()}>
      <div
        class="group relative flex items-center gap-1.5 h-7 px-2 rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] cursor-default select-none overflow-visible"
        role="meter"
        aria-label="Context used by the last request"
        aria-valuemin={0}
        aria-valuemax={ceiling() || undefined}
        aria-valuenow={used()}
        aria-valuetext={valueText()}
        onClick={() => { if (isCoarse()) setPinned((v) => !v); }}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      >
        <svg class="w-3.5 h-3.5 shrink-0 -rotate-90" viewBox="0 0 14 14" aria-hidden="true">
          <circle cx="7" cy="7" r={RING_R} fill="none" stroke="var(--border-default)" stroke-width="2" />
          <circle
            cx="7"
            cy="7"
            r={RING_R}
            fill="none"
            stroke={toneStroke()}
            stroke-width="2"
            stroke-linecap="round"
            stroke-dasharray={`${(fraction() * RING_C).toFixed(2)} ${RING_C.toFixed(2)}`}
            style={{ transition: 'stroke-dasharray 300ms ease, stroke 300ms ease' }}
          />
        </svg>
        <span class="text-micro font-medium text-zinc-300 tabular-nums">
          {formatTokens(used())}
          <Show when={windowSize() > 0}>
            <span class="text-zinc-500 hidden sm:inline"> / {formatTokens(windowSize())}</span>
          </Show>
        </span>

        {/* Hover/tap breakdown */}
        <div
          class="absolute top-full right-0 mt-1.5 w-60 p-3 rounded-lg border border-[color:var(--border-default)] bg-[color:var(--bg-overlay)] shadow-xl transition"
          classList={{
            'opacity-0 pointer-events-none': !showBreakdown(),
            'opacity-100 pointer-events-auto': showBreakdown(),
          }}
          style={{ 'z-index': 9999 }}
        >
          <div class="flex items-baseline justify-between mb-2">
            <span class="text-micro uppercase tracking-wider text-zinc-500 font-semibold">Context</span>
            <Show when={windowSize() > 0}>
              <span class="text-micro text-zinc-400 tabular-nums">{percent()}% of window</span>
            </Show>
          </div>

          {/* Usage bar, with a tick where the loop compacts. */}
          <div class="relative h-1.5 mb-2.5 rounded-full bg-[color:var(--border-subtle)]">
            <div
              class="absolute inset-y-0 left-0 rounded-full"
              style={{ width: `${percent()}%`, background: toneStroke(), transition: 'width 300ms ease' }}
            />
            <Show when={ceiling() > 0 && compactAt() > 0 && compactAt() < ceiling()}>
              <div
                class="absolute -top-0.5 -bottom-0.5 w-px bg-zinc-400"
                style={{ left: `${Math.min(100, (compactAt() / ceiling()) * 100)}%` }}
                title="Auto-compacts here"
              />
            </Show>
          </div>

          <Row label="Last request" value={used().toLocaleString()} />
          <Row label="of which cached" value={(last()?.cached ?? 0).toLocaleString()} sub dim={(last()?.cached ?? 0) === 0} />
          <Row label="Window" value={windowSize() > 0 ? windowSize().toLocaleString() : 'unknown'} dim={windowSize() === 0} />
          <Row label="Auto-compacts at" value={compactAt() > 0 ? compactAt().toLocaleString() : '—'} />

          <p class="mt-2 pt-2 border-t border-[color:var(--border-subtle)] text-micro leading-snug text-zinc-500">
            Size of the model's last request.
            <Show when={windowSize() === 0}> The model's window is unknown, so the fallback limit applies.</Show>
          </p>
        </div>
      </div>
    </Show>
  );
}

function Row(props: { label: string; value: string; dim?: boolean; sub?: boolean }) {
  return (
    <div class="flex items-center justify-between py-0.5" classList={{ 'opacity-40': props.dim }}>
      <span class="text-meta text-zinc-400" classList={{ 'pl-3': props.sub }}>{props.label}</span>
      <span class="text-meta font-mono tabular-nums text-zinc-200">{props.value}</span>
    </div>
  );
}
