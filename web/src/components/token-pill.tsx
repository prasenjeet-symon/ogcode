import { createMemo, createSignal, Show } from 'solid-js';
import { useSession } from '../context/session';
import type { MessageWithParts } from '../api/client';

interface Totals {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
}

function formatTokens(n: number): string {
  if (n < 1000) return n.toString();
  if (n < 10_000) return (n / 1000).toFixed(2).replace(/\.?0+$/, '') + 'K';
  if (n < 1_000_000) return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'K';
  return (n / 1_000_000).toFixed(2).replace(/\.?0+$/, '') + 'M';
}

export default function TokenPill(props: { messages?: () => MessageWithParts[] } = {}) {
  const session = useSession();
  const getMessages = () => props.messages ? props.messages() : session.messages();

  // Hover shows the breakdown on desktop; touch has no hover, so a tap
  // toggles it there. The two paths drive the same popover.
  const isCoarse = () => window.matchMedia('(hover: none)').matches;
  const [hovered, setHovered] = createSignal(false);
  const [pinned, setPinned] = createSignal(false);
  const showBreakdown = () => pinned() || (!isCoarse() && hovered());

  const totals = createMemo<Totals>(() => {
    const out: Totals = {
      input: 0, output: 0, reasoning: 0, cacheRead: 0, cacheWrite: 0, total: 0,
    };
    for (const m of getMessages()) {
      const t = m.info.tokens;
      if (!t) continue;
      out.input      += t.input ?? 0;
      out.output     += t.output ?? 0;
      out.reasoning  += t.reasoning ?? 0;
      out.cacheRead  += t.cacheRead ?? 0;
      out.cacheWrite += t.cacheWrite ?? 0;
    }
    // Include cache variants so total reflects all tokens consumed, not just uncached input.
    out.total = out.input + out.cacheRead + out.cacheWrite + out.output;
    return out;
  });

  const hasData = () => totals().total > 0;

  return (
    <Show when={hasData()}>
      <div
        class="group relative flex items-center gap-1.5 h-7 px-2 rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] cursor-default select-none overflow-visible"
        onClick={() => { if (isCoarse()) setPinned((v) => !v); }}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      >
        <svg class="w-3 h-3 text-zinc-500 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
          <path stroke-linecap="round" stroke-linejoin="round" d="M9 17v-6a2 2 0 012-2h2a2 2 0 012 2v6m-6 0h6m-9 0h12M5 21h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v14a2 2 0 002 2z" />
        </svg>
        <span class="text-micro font-medium text-zinc-300 tabular-nums">
          {formatTokens(totals().total)}
        </span>
        <span class="text-micro text-zinc-500 hidden sm:inline">tokens</span>

        {/* Hover/tap breakdown */}
        <div
          class="absolute top-full right-0 mt-1.5 w-56 p-3 rounded-lg border border-[color:var(--border-default)] bg-[color:var(--bg-overlay)] shadow-xl transition"
          classList={{
            'opacity-0 pointer-events-none': !showBreakdown(),
            'opacity-100 pointer-events-auto': showBreakdown(),
          }}
          style={{ 'z-index': 9999 }}
        >
          <div class="text-micro uppercase tracking-wider text-zinc-500 font-semibold mb-2">Session usage</div>
          <Row label="Input" value={totals().input} dot="bg-[color:var(--accent)]" />
          <Row label="Output" value={totals().output} dot="bg-emerald-400" />
          <Row label="Reasoning" value={totals().reasoning} dot="bg-violet-400" dim={totals().reasoning === 0} />
          <Row label="Cache read" value={totals().cacheRead} dot="bg-amber-400" dim={totals().cacheRead === 0} />
          <Row label="Cache write" value={totals().cacheWrite} dot="bg-orange-400" dim={totals().cacheWrite === 0} />
          <div class="mt-2 pt-2 border-t border-[color:var(--border-subtle)] flex items-center justify-between">
            <span class="text-micro font-semibold text-zinc-200">Total</span>
            <span class="text-meta font-mono tabular-nums text-zinc-100">
              {totals().total.toLocaleString()}
            </span>
          </div>
        </div>
      </div>
    </Show>
  );
}

function Row(props: { label: string; value: number; dot: string; dim?: boolean }) {
  return (
    <div class="flex items-center justify-between py-0.5" classList={{ 'opacity-30': props.dim }}>
      <div class="flex items-center gap-1.5">
        <span class={`w-1.5 h-1.5 rounded-full ${props.dot}`} />
        <span class="text-meta text-zinc-400">{props.label}</span>
      </div>
      <span class="text-meta font-mono tabular-nums text-zinc-200">{props.value.toLocaleString()}</span>
    </div>
  );
}
