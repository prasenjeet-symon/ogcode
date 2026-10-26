import { For, createEffect, createMemo, createResource, createSignal, on, onCleanup, Show } from 'solid-js';
import { useSession } from '../context/session';
import { useServer } from '../context/server';
import { getSessionTokens, getSessionUsage, type MessageWithParts, type ModelUsage } from '../api/client';
import { formatUSD, includedNote } from '../lib/money';

interface Totals {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
  utility: number;    // the effective tokens spent on utility work
  teammates: number;  // the effective tokens spent by spawned teammates
  effective: number;  // the pill's figure: everything but cache reads
  total: number;      // every token, cache reads included
}

export function formatTokens(n: number): string {
  if (n < 1000) return n.toString();
  if (n < 10_000) return (n / 1000).toFixed(2).replace(/\.?0+$/, '') + 'K';
  if (n < 1_000_000) return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'K';
  return (n / 1_000_000).toFixed(2).replace(/\.?0+$/, '') + 'M';
}

// Every token consumed: uncached input, both cache variants and output — what
// providers report as total_tokens. Mirrors session.TokenCounts.Consumed on the
// server, so the breakdown's "All tokens", `ogcode run`'s total and the stored
// per-step totals agree. Reasoning is billed inside output, so it is never
// added on top.
function consumed(input = 0, cacheRead = 0, cacheWrite = 0, output = 0): number {
  return input + cacheRead + cacheWrite + output;
}

// The tokens spent fresh: uncached input, cache writes and output — everything
// but cache reads. Mirrors session.TokenCounts.Effective. Cache reads are the
// cheapest tokens and on a long session most of the total, so the pill leads
// with this figure and leaves them to the breakdown.
function effective(input = 0, cacheWrite = 0, output = 0): number {
  return input + cacheWrite + output;
}

export default function TokenPill(props: { messages?: () => MessageWithParts[] } = {}) {
  const session = useSession();
  const events = useServer();
  const getMessages = () => props.messages ? props.messages() : session.messages();

  // Hover shows the breakdown on desktop; touch has no hover, so a tap
  // toggles it there. The two paths drive the same popover.
  const isCoarse = () => window.matchMedia('(hover: none)').matches;
  const [hovered, setHovered] = createSignal(false);
  const [pinned, setPinned] = createSignal(false);
  const showBreakdown = () => pinned() || (!isCoarse() && hovered());

  // The local sum of the messages this view holds. It is what the plan view
  // always uses, and the session view's stand-in — until the server total
  // arrives, or when it cannot be read.
  const localTotals = createMemo<Totals>(() => {
    const out: Totals = {
      input: 0, output: 0, reasoning: 0, cacheRead: 0, cacheWrite: 0, utility: 0, teammates: 0, effective: 0, total: 0,
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
    // Utility work (titles, risk checks, compaction, sub-agents, deep search,
    // turn-memory summaries) spends tokens recorded on the session row rather
    // than on a message. Fold them into the components so the totals below
    // include them, and keep the utility subtotal — counted like the pill, so
    // it is a part of it — to show how much of the session was utility work.
    // Only when the caller did not supply its own message list (the plan view
    // does).
    if (!props.messages) {
      const u = session.activeSession()?.utilityTokens;
      if (u) {
        out.input      += u.input ?? 0;
        out.output     += u.output ?? 0;
        out.reasoning  += u.reasoning ?? 0;
        out.cacheRead  += u.cacheRead ?? 0;
        out.cacheWrite += u.cacheWrite ?? 0;
        out.utility = effective(u.input, u.cacheWrite, u.output);
      }
    }
    out.effective = effective(out.input, out.cacheWrite, out.output);
    out.total = consumed(out.input, out.cacheRead, out.cacheWrite, out.output);
    return out;
  });

  // A teammate's steps land on its own transcript, so its share of the figures
  // above climbs while the parent's own token totals stand still — and the
  // resource key would never change. `tick` is the extra reason to refetch:
  // bumped by a board event, and on a beat while a teammate is still working.
  const [tick, setTick] = createSignal(0);

  // The session view's authoritative totals, summed by the server over the
  // WHOLE transcript. The client only ever holds the newest transcript page, so
  // on a session longer than one page its own sum is short; the server reads
  // every step without loading the transcript. Keyed on the session id and
  // refetched on every token total move, so it tracks the session as it runs.
  // The plan view passes its own message list and has no session row to read.
  const [server] = createResource(
    () => {
      const id = session.activeSession()?.id;
      return props.messages || !id ? false : `${id}|${localTotals().total}|${tick()}`;
    },
    (key: string) => getSessionTokens(key.slice(0, key.indexOf('|'))).catch(() => undefined),
  );

  const totals = createMemo<Totals>(() => {
    const st = server();
    if (!st) return localTotals();
    // The server reports the session's own tokens; its spawned teammates arrive
    // beside them, one entry each. Fold them in: the pill is what running this
    // session spent, and a teammate is work the session did. Utility is already
    // inside input, output and cache writes, so it is never added again — the
    // subtotal folds the teammates' own utility share in the same way.
    let input = st.input, output = st.output, reasoning = st.reasoning;
    let cacheRead = st.cacheRead, cacheWrite = st.cacheWrite, utility = st.utility, mates = 0;
    for (const t of st.teammates ?? []) {
      input      += t.input;
      output     += t.output;
      reasoning  += t.reasoning;
      cacheRead  += t.cacheRead;
      cacheWrite += t.cacheWrite;
      utility    += t.utility;
      mates      += t.effective;
    }
    return {
      input, output, reasoning, cacheRead, cacheWrite, utility,
      teammates: mates,
      effective: effective(input, cacheWrite, output),
      total: consumed(input, cacheRead, cacheWrite, output),
    };
  });

  // Every teammate the session spawned, as the token endpoint reports them.
  const teammates = () => server()?.teammates ?? [];
  // While one is RUNNING its spend keeps climbing with nothing in the parent
  // transcript to show it, so the token figure is read again on a beat. An idle
  // child is settled — its spend no longer moves — so only a running one keeps
  // the beat, matching the drawer's disjoint chips (running = live − idle).
  const teammateRunning = createMemo(() =>
    teammates().some((t) => t.status === 'running'));

  const hasData = () => totals().total > 0;

  // What the session cost, priced on the server at the model each step ran on.
  // Refetched whenever the token total moves — once per finished step or
  // utility call — so it never lags the counts beside it. Not for the plan
  // view, which passes its own message list and has no session row to price.
  const [usage] = createResource(
    () => {
      const id = session.activeSession()?.id;
      return !props.messages && id && totals().total > 0 ? `${id}|${totals().total}` : false;
    },
    (key: string) => getSessionUsage(key.slice(0, key.indexOf('|'))).catch(() => undefined),
  );
  // Only per-token billing goes in the pill; a plan's list-price value is a
  // comparison, not a charge, so it stays in the breakdown. A teammate's spend
  // counts as the session's: it ran because this session spawned it.
  const teammateUsage = () => usage()?.teammates ?? [];
  const billed = () =>
    (usage()?.totals.costUsd ?? 0) + teammateUsage().reduce((n, t) => n + t.totals.costUsd, 0);
  const includedValue = () =>
    (usage()?.totals.includedListUsd ?? 0) + teammateUsage().reduce((n, t) => n + t.totals.includedListUsd, 0);
  // A teammate's charge, joined from the usage scan by its own session id.
  const teammateCost = (sessionId: string) =>
    teammateUsage().find((t) => t.sessionId === sessionId)?.totals.costUsd ?? 0;
  const firstIncluded = () => usage()?.models.find((m) => m.billing === 'included');

  // A board event is what announces a teammate starting, posting or stopping —
  // the moments its spend can have moved. Deferred so the first render is not a
  // reason to refetch what it just read, and debounced:
  // the burst one spawn produces folds into one read, not one per event.
  let sseTimer: ReturnType<typeof setTimeout> | null = null;
  createEffect(on(events.eventTick, () => {
    const last = events.lastEvent();
    if (!last || !last.type.startsWith('board.')) return;
    if (sseTimer) clearTimeout(sseTimer);
    sseTimer = setTimeout(() => {
      sseTimer = null;
      setTick((n) => n + 1);
    }, 150);
  }, { defer: true }));
  onCleanup(() => {
    if (sseTimer) clearTimeout(sseTimer);
  });

  createEffect(() => {
    if (!teammateRunning()) return;
    const id = setInterval(() => setTick((n) => n + 1), 5000);
    onCleanup(() => clearInterval(id));
  });

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
          {formatTokens(totals().effective)}
        </span>
        <span class="text-micro text-zinc-500 hidden sm:inline">tokens</span>
        <Show when={billed() > 0}>
          <span class="text-micro text-zinc-600" aria-hidden="true">·</span>
          <span class="text-micro font-medium text-zinc-300 tabular-nums">{formatUSD(billed())}</span>
        </Show>

        {/* Hover/tap breakdown */}
        <div
          class="absolute top-full right-0 mt-1.5 w-64 p-3 rounded-lg border border-[color:var(--border-default)] bg-[color:var(--bg-overlay)] shadow-xl transition"
          classList={{
            'opacity-0 pointer-events-none': !showBreakdown(),
            'opacity-100 pointer-events-auto': showBreakdown(),
          }}
          style={{ 'z-index': 9999 }}
        >
          <div class="text-micro uppercase tracking-wider text-zinc-500 font-semibold mb-2">Session usage</div>
          {/* Input + Output + Cache write = Effective total, the pill's figure.
              Cache reads sit apart below it: the cheapest tokens, and on a long
              session most of them. Adding them back gives All tokens, the
              providers' total_tokens. The "of which" rows are parts of the row
              above them, not extra tokens. */}
          <Row label="Input" value={totals().input} dot="bg-[color:var(--accent)]" />
          <Row label="Output" value={totals().output} dot="bg-emerald-400" />
          <Row label="of which reasoning" value={totals().reasoning} dot="bg-violet-400" sub dim={totals().reasoning === 0} />
          <Row label="Cache write" value={totals().cacheWrite} dot="bg-orange-400" dim={totals().cacheWrite === 0} />
          <div class="mt-2 pt-2 border-t border-[color:var(--border-subtle)] flex items-center justify-between">
            <span class="text-micro font-semibold text-zinc-200">Effective total</span>
            <span class="text-meta font-mono tabular-nums text-zinc-100">
              {totals().effective.toLocaleString()}
            </span>
          </div>
          <Row label="of which utility" value={totals().utility} dot="bg-sky-400" sub dim={totals().utility === 0} />
          <Row label="of which teammates" value={totals().teammates} dot="bg-cyan-400" sub dim={totals().teammates === 0} />
          <div class="mt-2 pt-2 border-t border-[color:var(--border-subtle)]">
            <Row label="Cache read" value={totals().cacheRead} dot="bg-amber-400" dim={totals().cacheRead === 0} />
            <div class="flex items-center justify-between py-0.5">
              <span class="text-micro text-zinc-500">All tokens</span>
              <span class="text-meta font-mono tabular-nums text-zinc-400">
                {totals().total.toLocaleString()}
              </span>
            </div>
          </div>
          {/* Cost, cache reads included at their own rate. Per-token billing
              is a charge; a plan's or a local model's figure is its plan value:
              what the same tokens would cost at list price, marked as such. */}
          <Show when={usage()}>
            {(u) => (
              <div class="mt-2 pt-2 border-t border-[color:var(--border-subtle)]">
                <Show when={u().models.some((m) => m.billing === 'metered') || billed() > 0}>
                  <div class="flex items-center justify-between py-0.5">
                    <span class="text-micro font-semibold text-zinc-200">Cost</span>
                    <span class="text-meta font-mono tabular-nums text-zinc-100">
                      {formatUSD(billed(), true)}
                    </span>
                  </div>
                </Show>
                <Show when={firstIncluded()}>
                  {(m) => (
                    <>
                      <div class="flex items-center justify-between py-0.5">
                        <span class="text-micro text-zinc-400">Plan value</span>
                        <span class="text-meta font-mono tabular-nums text-zinc-300">
                          {includedValue() > 0 ? `≈ ${formatUSD(includedValue(), true)}` : 'unknown'}
                        </span>
                      </div>
                      <div class="text-micro text-zinc-500">{includedNote(m())}</div>
                    </>
                  )}
                </Show>
                <Show when={u().models.length > 1}>
                  <div class="mt-1.5 space-y-px">
                    <For each={u().models}>{(m) => <ModelCost model={m} />}</For>
                  </div>
                </Show>
                <Show when={u().totals.unpricedEffective > 0}>
                  <div class="mt-1 text-micro text-zinc-500">
                    No published price for {u().totals.unpricedEffective.toLocaleString()} tokens
                  </div>
                </Show>
                <Show when={u().totals.unknownEffective > 0}>
                  <div class="mt-1 text-micro text-zinc-500">
                    No provider recorded for {u().totals.unknownEffective.toLocaleString()} tokens
                  </div>
                </Show>
              </div>
            )}
          </Show>
          {/* The teammates this session spawned, one line each: the part of the
              figures above they spent, and their charge when one was billed. A
              teammate still working says so; a settled one needs no label. */}
          <Show when={teammates().length > 0}>
            <div class="mt-2 pt-2 border-t border-[color:var(--border-subtle)]">
              <div class="text-micro uppercase tracking-wider text-zinc-500 font-semibold mb-1">Teammates</div>
              <For each={teammates()}>
                {(t) => (
                  <div class="flex items-center justify-between gap-2 py-px">
                    <div class="flex items-center gap-1.5 min-w-0">
                      <span class="text-micro font-mono text-zinc-400 truncate" title={t.handle}>{t.handle}</span>
                      <Show when={t.status === 'running'}>
                        <span class="text-micro text-[color:var(--accent-ink)] shrink-0">running</span>
                      </Show>
                    </div>
                    <div class="flex items-center gap-2 shrink-0">
                      <span class="text-micro font-mono tabular-nums text-zinc-300">{formatTokens(t.effective)}</span>
                      <Show when={teammateCost(t.sessionId) > 0}>
                        <span class="text-micro font-mono tabular-nums text-zinc-500">
                          {formatUSD(teammateCost(t.sessionId), true)}
                        </span>
                      </Show>
                    </div>
                  </div>
                )}
              </For>
            </div>
          </Show>
        </div>
      </div>
    </Show>
  );
}

// One model's share of a session that used several: its charge, or its
// list-price value when a plan or a local run covered it.
function ModelCost(props: { model: ModelUsage }) {
  const value = () => {
    const m = props.model;
    if (m.billing === 'metered') return formatUSD(m.costUsd ?? 0, true);
    if (m.billing === 'included') return m.listUsd != null ? `≈ ${formatUSD(m.listUsd, true)}` : 'included';
    return m.billing === 'unknown' ? 'unknown' : 'no price';
  };
  return (
    <div class="flex items-center justify-between gap-2 py-px">
      <span class="text-micro text-zinc-500 truncate" title={props.model.model}>
        {props.model.name || props.model.model || 'default model'}
      </span>
      <span class="text-micro font-mono tabular-nums text-zinc-400 shrink-0">{value()}</span>
    </div>
  );
}

function Row(props: { label: string; value: number; dot: string; dim?: boolean; sub?: boolean }) {
  return (
    <div class="flex items-center justify-between py-0.5" classList={{ 'opacity-30': props.dim }}>
      <div class="flex items-center gap-1.5" classList={{ 'pl-3': props.sub }}>
        <span class={`w-1.5 h-1.5 rounded-full ${props.dot}`} />
        <span class="text-meta text-zinc-400">{props.label}</span>
      </div>
      <span class="text-meta font-mono tabular-nums text-zinc-200">{props.value.toLocaleString()}</span>
    </div>
  );
}
