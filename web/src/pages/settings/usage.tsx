import { For, Show, createEffect, createMemo, createResource, createSignal, type JSX } from 'solid-js';
import { getUsageSummary, type ModelUsage, type ProjectUsage, type UsageSummary } from '../../api/client';
import { formatTokens } from '../../components/token-pill';
import { useServer } from '../../context/server';
import { formatUSD, includedNote, providerTag, providerTitle } from '../../lib/money';
import { projectName } from '../../lib/paths';
import { Chip, EmptyState, Group, Row, Spinner, Tag, matches, useShell } from './ui';

// ---------------------------------------------------------------------------
// Usage — what models cost, in this project or across every project.
//
// The figures come from the global spend ledger (internal/usage), which every
// ogcode process on this machine writes to, priced on the server. The page
// opens on this project, since that is what a page inside a project's settings
// reads as, and says in words which projects and which period every figure
// covers. Two kinds of money appear and are never blended into one headline:
//   · Billed — what per-token providers charged, at their price;
//   · Plan value (list price) — what OGX, Ollama Cloud and local models did,
//     priced at the vendor's list price: the bill a plan spared, not a charge.
// ---------------------------------------------------------------------------

type RangeId = 'today' | '7d' | '30d' | 'all';

const RANGES: { id: RangeId; label: string }[] = [
  { id: 'today', label: 'Today' },
  { id: '7d', label: '7 days' },
  { id: '30d', label: '30 days' },
  { id: 'all', label: 'All time' },
];

// Which projects the page totals: this workspace (its task worktrees
// included), every project, or one other project opened from the list.
type Scope = 'this' | 'all' | { project: string };

// Kept across visits in this tab, so returning to the page shows the period
// and projects last looked at.
let lastRange: RangeId = '30d';
let lastScope: Scope = 'this';

const RANGE_WORDS: Record<RangeId, string> = {
  today: 'today',
  '7d': 'the last 7 days',
  '30d': 'the last 30 days',
  all: 'all time',
};

/** Local midnight at the start of a range, in ms; 0 for all time. */
function rangeStart(id: RangeId): number {
  if (id === 'all') return 0;
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  if (id === '7d') d.setDate(d.getDate() - 6);
  if (id === '30d') d.setDate(d.getDate() - 29);
  return d.getTime();
}

// The chart's two series, from the validated categorical order (blue, orange):
// checked for colour-blind separation and contrast on the app's dark surface.
const BILLED = '#3987e5';
const COVERED = '#d95926';

export default function UsageSettings() {
  const shell = useShell();
  createEffect(() => shell.report({ noun: 'models' }));

  const server = useServer();
  const [range, setRange] = createSignal<RangeId>(lastRange);
  const [scope, setScope] = createSignal<Scope>(lastScope);

  // The directory the summary is narrowed to: '' for every project, and
  // undefined while this workspace's own path is still loading.
  const projectDir = (): string | undefined => {
    const s = scope();
    if (s === 'all') return '';
    if (s === 'this') return server.directory() || undefined;
    return s.project;
  };
  const [data] = createResource(
    () => {
      const dir = projectDir();
      return dir === undefined ? false : { range: range(), dir };
    },
    (k) => getUsageSummary(rangeStart(k.range), k.dir),
  );
  // The previous view stays on screen, dimmed, while the next one loads, so
  // switching never flashes an empty page.
  const summary = () => data.latest;

  const pick = (id: RangeId) => {
    lastRange = id;
    setRange(id);
  };
  const choose = (next: Scope) => {
    lastScope = next;
    setScope(next);
  };
  // Opening a project from the all-projects list. This workspace is 'this',
  // so its chip lights up rather than a second one for the same place.
  const open = (dir: string) => choose(dir === server.directory() ? 'this' : { project: dir });

  const other = () => {
    const s = scope();
    return typeof s === 'object' ? s.project : '';
  };
  const scopeName = () => {
    const s = scope();
    if (s === 'all') return 'all projects';
    return projectName(s === 'this' ? server.directory() : s.project);
  };

  const q = () => shell.query();
  const visibleModels = createMemo(() =>
    (summary()?.models ?? []).map((m) => ({
      m,
      hidden: !matches(q(), m.name, m.model, providerTag(m)),
    })),
  );

  return (
    <div>
      {/* One filter row, above everything it scopes: which projects, then
          which period. */}
      <div class="flex items-center gap-1.5 px-4 pb-1.5 flex-wrap">
        <Chip active={scope() === 'this'} onClick={() => choose('this')} title={server.directory()}>
          This project
        </Chip>
        <Chip active={scope() === 'all'} onClick={() => choose('all')}>
          All projects
        </Chip>
        <Show when={other()}>
          <Chip active onClick={() => choose('all')} title="Back to all projects">
            {projectName(other())}
            <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </Chip>
        </Show>
        <span class="w-px h-4 mx-1.5 bg-[color:var(--border-default)]" aria-hidden="true" />
        <For each={RANGES}>
          {(r) => (
            <Chip active={range() === r.id} onClick={() => pick(r.id)}>
              {r.label}
            </Chip>
          )}
        </For>
        <Show when={data.loading && summary()}>
          <Spinner class="w-3.5 h-3.5 ml-1 text-[color:var(--text-muted)]" />
        </Show>
      </div>

      {/* What every figure below covers, in words, so a total is never read
          as some other project's. */}
      <p class="px-4 pb-3 text-meta text-[color:var(--text-tertiary)]">
        Showing <span class="font-medium text-[color:var(--text-secondary)]">{scopeName()}</span>
        <Show when={scope() === 'all' && (summary()?.projects.length ?? 0) > 0}>
          {` (${summary()!.projects.length} ${summary()!.projects.length === 1 ? 'project' : 'projects'})`}
        </Show>
        {` · ${RANGE_WORDS[range()]}`}
        <Show when={summary()?.totals.sessions}>
          {(n) => ` · ${n().toLocaleString()} ${n() === 1 ? 'session' : 'sessions'}`}
        </Show>
      </p>

      <Show
        when={summary()}
        fallback={
          <Show
            when={data.error}
            fallback={
              <div class="py-20 flex justify-center text-[color:var(--text-muted)]">
                <Spinner />
              </div>
            }
          >
            <EmptyState title="Couldn't load usage" body="The server did not answer. Try again in a moment." />
          </Show>
        }
      >
        {(s) => (
          <div class="transition-opacity duration-150" classList={{ 'opacity-60': data.loading }}>
            <Show
              when={s().models.length > 0}
              fallback={
                <EmptyState
                  title={
                    scope() === 'all'
                      ? (range() === 'all' ? 'No usage yet' : 'No usage in this period')
                      : (range() === 'all' ? 'No usage in this project yet' : 'No usage in this project in this period')
                  }
                  body={
                    scope() === 'all'
                      ? (range() === 'all'
                          ? 'Spend shows up here as soon as a session runs. Every project you open is counted, its earlier sessions included.'
                          : 'Nothing ran in this period. Pick a longer range to see earlier work.')
                      : 'Pick a longer range, or switch to All projects to see the rest.'
                  }
                  icon={CHART_ICON}
                />
              }
            >
              <Overview summary={s()} />
              <DailySpend summary={s()} range={range()} />

              <Group
                id="usage-models"
                title="By model"
                description={
                  <>
                    Priced at each model's current price: the provider's own listing, or the model catalogue.
                    OGX and Ollama Cloud are flat plans and local models are free, so for those the figure is
                    what the same tokens would cost on the vendor's own API. Cache reads are priced at their
                    discounted rate. Sessions from before ogcode recorded a provider are priced as the provider
                    that serves the model today, marked inferred; where none does, the provider shows as not
                    recorded and the usage is never counted as billed.
                  </>
                }
              >
                <For each={visibleModels()}>{(v) => <ModelRow m={v.m} hidden={v.hidden} />}</For>
              </Group>

              <Show when={scope() === 'all' && s().projects.length > 0}>
                <Group
                  id="usage-projects"
                  title="By project"
                  description="Open a project to see its usage alone. Every workspace opened with this version is counted, its earlier sessions included."
                >
                  <For each={s().projects}>
                    {(p) => (
                      <ProjectRow
                        p={p}
                        current={p.project === server.directory()}
                        hidden={!matches(q(), p.name, p.project)}
                        onOpen={() => open(p.project)}
                      />
                    )}
                  </For>
                </Group>
              </Show>
            </Show>
          </div>
        )}
      </Show>
    </div>
  );
}

const CHART_ICON =
  'M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z';

/** A captioned block in the page's own style, for content that is not a list
 *  of setting rows (so the search never hides it). */
function Section(props: { title: string; action?: JSX.Element; children: JSX.Element }) {
  return (
    <section class="pt-7 first:pt-1">
      <div class="flex items-end gap-2 px-4 pb-1.5">
        <h2 class="text-micro font-medium uppercase tracking-[0.06em] text-[color:var(--text-muted)]">{props.title}</h2>
        <div class="flex-1" />
        <Show when={props.action}>{props.action}</Show>
      </div>
      <div class="rounded-[10px] bg-[color:var(--bg-elevated)]/40 overflow-hidden">{props.children}</div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Overview: the figures the page leads with — money on the first row, tokens
// on the second. Output gets its own tile because it is priced several times
// higher than input, so a small output count can still be most of a bill.
// ---------------------------------------------------------------------------

function Overview(props: { summary: UsageSummary }) {
  const t = () => props.summary.totals;
  return (
    <Section title="Overview">
      <div class="grid grid-cols-2 sm:grid-cols-6 gap-px bg-[color:var(--border-subtle)]">
        <Tile
          class="sm:col-span-3"
          label="Billed"
          value={formatUSD(t().costUsd)}
          helper={billedNote(t())}
        />
        <Tile
          class="sm:col-span-3"
          label="Plan value (list price)"
          value={t().includedEffective > 0 ? `≈ ${formatUSD(t().includedListUsd)}` : formatUSD(0)}
          helper="not charged: covered by your plans"
        />
        <Tile
          class="sm:col-span-2"
          label="Input"
          value={formatTokens(t().input)}
          helper={t().cacheWrite > 0 ? `+ ${formatTokens(t().cacheWrite)} cache writes` : 'uncached'}
        />
        <Tile
          class="sm:col-span-2"
          label="Output"
          value={formatTokens(t().output)}
          helper={t().reasoning > 0 ? `incl. ${formatTokens(t().reasoning)} reasoning` : 'generated'}
        />
        <Tile class="col-span-2 sm:col-span-2" label="Cache reads" value={formatTokens(t().cacheRead)} helper="discounted rate" />
      </div>
    </Section>
  );
}

/** Under Billed: what the figure leaves out, when anything. */
function billedNote(t: UsageSummary['totals']): string {
  const out: string[] = [];
  if (t.unknownEffective > 0) out.push(`${formatTokens(t.unknownEffective)} tokens with no provider recorded`);
  if (t.unpricedEffective > 0) out.push(`${formatTokens(t.unpricedEffective)} unpriced`);
  return out.length ? `excludes ${out.join(', ')}` : 'charged per token';
}

function Tile(props: { label: string; value: string; helper: string; class?: string }) {
  return (
    <div class={`px-4 py-3 bg-[color-mix(in_srgb,var(--bg-elevated)_40%,var(--bg-base))] ${props.class ?? ''}`}>
      <div class="text-micro text-[color:var(--text-tertiary)]">{props.label}</div>
      <div class="mt-1 text-[1.35rem] leading-tight font-semibold tracking-[-0.01em] text-[color:var(--text-primary)]">
        {props.value}
      </div>
      <div class="mt-0.5 text-micro text-[color:var(--text-muted)] truncate">{props.helper}</div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

function ModelRow(props: { m: ModelUsage; hidden: boolean }) {
  const m = () => props.m;
  const facts = () => {
    const parts = [
      `${m().sessions.toLocaleString()} ${m().sessions === 1 ? 'session' : 'sessions'}`,
      `${formatTokens(m().input)} in`,
      `${formatTokens(m().output)} out`,
    ];
    if (m().cacheWrite > 0) parts.push(`${formatTokens(m().cacheWrite)} cache writes`);
    if (m().cacheRead > 0) parts.push(`${formatTokens(m().cacheRead)} cache reads`);
    if (m().billing === 'included') parts.push(includedNote(m()));
    return parts.join(' · ');
  };
  return (
    <Row
      hidden={props.hidden}
      label={
        <span class="flex items-center gap-2 min-w-0">
          <span class="truncate" title={m().model}>{m().name || m().model || 'Default model'}</span>
          <span class="shrink-0" title={providerTitle(m())}>
            <Tag tone="muted">{providerTag(m())}</Tag>
          </span>
        </span>
      }
      helper={facts()}
    >
      <Cost billing={m().billing} cost={m().costUsd} list={m().listUsd} />
    </Row>
  );
}

function ProjectRow(props: { p: ProjectUsage; current: boolean; hidden: boolean; onOpen: () => void }) {
  const p = () => props.p;
  return (
    <Row
      hidden={props.hidden}
      onClick={props.onOpen}
      label={
        <span class="flex items-center gap-2 min-w-0">
          <span class="truncate" title={p().project}>{p().name || p().project}</span>
          <Show when={props.current}>
            <Tag tone="accent">This project</Tag>
          </Show>
        </span>
      }
      helper={`${formatTokens(p().input)} in · ${formatTokens(p().output)} out · ${formatTokens(p().cacheRead)} cache reads`}
    >
      <span class="flex flex-col items-end gap-0.5">
        <span class="text-meta font-mono tabular-nums text-[color:var(--text-secondary)]">{formatUSD(p().costUsd)}</span>
        <Show when={p().includedListUsd > 0}>
          <span class="text-micro font-mono tabular-nums text-[color:var(--text-muted)]">
            ≈ {formatUSD(p().includedListUsd)} plan value
          </span>
        </Show>
      </span>
    </Row>
  );
}

/** A model's cost cell: the charge, or a plan's list-price value marked as one. */
function Cost(props: { billing: ModelUsage['billing']; cost: number | null; list: number | null }) {
  return (
    <span class="flex flex-col items-end gap-0.5">
      <Show
        when={props.billing !== 'unpriced' && props.billing !== 'unknown'}
        fallback={
          <span class="text-meta text-[color:var(--text-muted)]">
            {props.billing === 'unknown' ? 'Unknown' : 'No price'}
          </span>
        }
      >
        <Show
          when={props.billing === 'included'}
          fallback={
            <span class="text-meta font-mono tabular-nums text-[color:var(--text-secondary)]">
              {formatUSD(props.cost ?? 0)}
            </span>
          }
        >
          <span class="text-meta font-mono tabular-nums text-[color:var(--text-secondary)]">
            {props.list != null ? `≈ ${formatUSD(props.list)}` : '—'}
          </span>
          <Tag tone="muted">Included</Tag>
        </Show>
      </Show>
    </span>
  );
}

// ---------------------------------------------------------------------------
// Daily spend: stacked columns — billed per token, then plan value at
// list price — with a hover/focus readout and a table twin.
// ---------------------------------------------------------------------------

interface Point {
  key: string;
  label: string; // axis label
  long: string; // tooltip / table label
  billed: number;
  covered: number;
  input: number;
  output: number;
}

function dayKey(d: Date): string {
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${d.getFullYear()}-${m}-${day}`;
}

function parseDay(key: string): Date {
  const [y, m, d] = key.split('-').map(Number);
  return new Date(y, m - 1, d);
}

const short = (d: Date) => d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });

/** Every day of the range, including the ones nothing ran on, folded into
 *  weeks when the range is long enough that daily columns would turn to hair. */
function buildPoints(s: UsageSummary, range: RangeId): { points: Point[]; weekly: boolean } {
  const byDay = new Map(s.days.map((d) => [d.day, d]));
  const end = new Date();
  end.setHours(0, 0, 0, 0);
  let start: Date;
  if (range === 'all') {
    if (s.days.length === 0) return { points: [], weekly: false };
    start = parseDay(s.days[0].day);
  } else {
    start = new Date(rangeStart(range));
  }

  const days: Point[] = [];
  for (const d = new Date(start); d <= end; d.setDate(d.getDate() + 1)) {
    const key = dayKey(d);
    const v = byDay.get(key);
    days.push({
      key,
      label: short(d),
      long: d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' }),
      billed: v?.costUsd ?? 0,
      covered: v?.includedListUsd ?? 0,
      input: v?.input ?? 0,
      output: v?.output ?? 0,
    });
  }
  if (days.length <= 45) return { points: days, weekly: false };

  const weeks: Point[] = [];
  for (let i = 0; i < days.length; i += 7) {
    const chunk = days.slice(i, i + 7);
    const first = parseDay(chunk[0].key);
    weeks.push({
      key: chunk[0].key,
      label: short(first),
      long: `Week of ${short(first)}`,
      billed: chunk.reduce((a, p) => a + p.billed, 0),
      covered: chunk.reduce((a, p) => a + p.covered, 0),
      input: chunk.reduce((a, p) => a + p.input, 0),
      output: chunk.reduce((a, p) => a + p.output, 0),
    });
  }
  return { points: weeks, weekly: true };
}

/** Round axis ticks: 0 and up to three clean steps covering max. */
function niceTicks(max: number): number[] {
  if (max <= 0) return [0];
  const raw = max / 3;
  const exp = Math.floor(Math.log10(raw));
  const f = raw / 10 ** exp;
  // toPrecision drops float noise (0.025000000000000001), which the tick
  // labels would otherwise print.
  const step = Number(((f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10) * 10 ** exp).toPrecision(6));
  const ticks: number[] = [];
  for (let v = 0; v < max + step * 0.999; v += step) ticks.push(Number(v.toPrecision(6)));
  return ticks;
}

/** A tick in as many decimals as its step needs: $0.05 steps show two, $2.5
 *  steps one, whole-dollar steps none. */
function tickLabel(v: number, step: number): string {
  if (v === 0) return '$0';
  if (v >= 1000) return '$' + Math.round(v).toLocaleString('en-US');
  const decimals = (String(step).split('.')[1] ?? '').length;
  return '$' + v.toFixed(Math.min(decimals, 6));
}

function DailySpend(props: { summary: UsageSummary; range: RangeId }) {
  const built = createMemo(() => buildPoints(props.summary, props.range));
  const points = () => built().points;
  const max = createMemo(() => Math.max(0, ...points().map((p) => p.billed + p.covered)));
  const ticks = createMemo(() => niceTicks(max()));
  const top = () => ticks()[ticks().length - 1] || 1;
  const step = () => (ticks().length > 1 ? ticks()[1] : top());
  const [active, setActive] = createSignal<number | null>(null);

  // x labels: about six, always including the last column.
  const labelEvery = () => Math.max(1, Math.ceil(points().length / 6));
  const showLabel = (i: number) => i === points().length - 1 || (i % labelEvery() === 0 && points().length - 1 - i >= labelEvery() / 2);

  // A single column is a number, not a trend; the overview already says it.
  return (
    <Show when={points().length > 1 && max() > 0}>
      <Section
        title={built().weekly ? 'By week' : 'By day'}
        action={
          <div class="flex items-center gap-3 text-micro text-[color:var(--text-tertiary)]">
            <LegendKey color={BILLED} label="Billed" />
            <LegendKey color={COVERED} label="Plan value (list price)" />
          </div>
        }
      >
        <div class="px-4 pt-4 pb-3">
          {/* The frame includes the x-axis band, so nothing scrolls inside it. */}
          <div class="relative h-[176px] select-none" onMouseLeave={() => setActive(null)}>
            {/* gridlines + y ticks */}
            <For each={ticks()}>
              {(v) => (
                <div
                  class="absolute left-0 right-0 flex items-center"
                  style={{ bottom: `calc(22px + ${(v / top()) * 146}px)`, transform: 'translateY(50%)' }}
                >
                  <span class="w-12 shrink-0 pr-2 text-right text-micro tabular-nums text-[color:var(--text-muted)]">
                    {tickLabel(v, step())}
                  </span>
                  <span class="flex-1 h-px bg-[color:var(--border-subtle)]" />
                </div>
              )}
            </For>

            {/* columns */}
            <div class="absolute left-12 right-0 top-[8px] bottom-[22px] flex items-end gap-[2px]">
              <For each={points()}>
                {(p, i) => {
                  const covered = () => (p.covered / top()) * 100;
                  const billed = () => (p.billed / top()) * 100;
                  const on = () => active() === i();
                  return (
                    <button
                      type="button"
                      class="relative flex-1 min-w-0 h-full flex flex-col items-center justify-end focus:outline-none"
                      aria-label={`${p.long}: billed ${formatUSD(p.billed, true)}, plan value about ${formatUSD(p.covered, true)}, ${p.input.toLocaleString()} input and ${p.output.toLocaleString()} output tokens`}
                      onMouseEnter={() => setActive(i())}
                      onFocus={() => setActive(i())}
                      onBlur={() => setActive(null)}
                    >
                      <span
                        class="w-full max-w-[24px] flex flex-col justify-end h-full transition-[filter] duration-100"
                        style={{ filter: on() ? 'brightness(1.25)' : undefined }}
                      >
                        <Show when={p.covered > 0}>
                          <span
                            class="block w-full rounded-t-[4px]"
                            style={{ height: `${covered()}%`, background: COVERED, 'min-height': '2px' }}
                          />
                        </Show>
                        {/* the 2px surface gap between stacked segments */}
                        <Show when={p.covered > 0 && p.billed > 0}>
                          <span class="block w-full h-[2px] shrink-0" />
                        </Show>
                        <Show when={p.billed > 0}>
                          <span
                            class="block w-full"
                            classList={{ 'rounded-t-[4px]': p.covered === 0 }}
                            style={{ height: `${billed()}%`, background: BILLED, 'min-height': '2px' }}
                          />
                        </Show>
                      </span>
                    </button>
                  );
                }}
              </For>
            </div>

            {/* x labels */}
            <div class="absolute left-12 right-0 bottom-0 h-[18px]">
              <For each={points()}>
                {(p, i) => (
                  <Show when={showLabel(i())}>
                    <span
                      class="absolute top-0 -translate-x-1/2 whitespace-nowrap text-micro text-[color:var(--text-muted)]"
                      style={{ left: `${((i() + 0.5) / points().length) * 100}%` }}
                    >
                      {p.label}
                    </span>
                  </Show>
                )}
              </For>
            </div>

            {/* readout for the hovered or focused column */}
            <Show when={active() !== null ? points()[active()!] : undefined}>
              {(p) => {
                const left = () => Math.min(88, Math.max(12, ((active()! + 0.5) / points().length) * 100));
                return (
                  <div
                    class="absolute top-0 z-10 -translate-x-1/2 pointer-events-none rounded-md border border-[color:var(--border-default)]
                           bg-[color:var(--bg-overlay)] shadow-lg px-2.5 py-2 min-w-[11rem]"
                    style={{ left: `calc(3rem + (100% - 3rem) * ${left() / 100})` }}
                  >
                    <div class="text-micro text-[color:var(--text-tertiary)] mb-1">{p().long}</div>
                    <ReadoutRow color={BILLED} value={formatUSD(p().billed, true)} label="Billed" />
                    <ReadoutRow color={COVERED} value={`≈ ${formatUSD(p().covered, true)}`} label="Plan value" />
                    <div class="mt-1 text-micro tabular-nums text-[color:var(--text-muted)]">
                      {formatTokens(p().input)} in · {formatTokens(p().output)} out
                    </div>
                  </div>
                );
              }}
            </Show>
          </div>

          {/* The same numbers without hovering. */}
          <details class="mt-3 group/tbl">
            <summary class="text-micro text-[color:var(--text-tertiary)] cursor-pointer select-none hover:text-[color:var(--text-secondary)]">
              Show as table
            </summary>
            <table class="mt-2 w-full text-micro tabular-nums">
              <thead>
                <tr class="text-left text-[color:var(--text-muted)]">
                  <th class="font-medium py-1">{built().weekly ? 'Week' : 'Day'}</th>
                  <th class="font-medium py-1 text-right">Billed</th>
                  <th class="font-medium py-1 text-right">Plan value</th>
                  <th class="font-medium py-1 text-right">Input</th>
                  <th class="font-medium py-1 text-right">Output</th>
                </tr>
              </thead>
              <tbody class="text-[color:var(--text-secondary)]">
                <For each={[...points()].reverse()}>
                  {(p) => (
                    <tr class="border-t border-[color:var(--border-subtle)]">
                      <td class="py-1">{p.long}</td>
                      <td class="py-1 text-right font-mono">{formatUSD(p.billed, true)}</td>
                      <td class="py-1 text-right font-mono">≈ {formatUSD(p.covered, true)}</td>
                      <td class="py-1 text-right font-mono">{p.input.toLocaleString()}</td>
                      <td class="py-1 text-right font-mono">{p.output.toLocaleString()}</td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </details>
        </div>
      </Section>
    </Show>
  );
}

function LegendKey(props: { color: string; label: string }) {
  return (
    <span class="flex items-center gap-1.5">
      <span class="w-2.5 h-2.5 rounded-[2px]" style={{ background: props.color }} />
      {props.label}
    </span>
  );
}

function ReadoutRow(props: { color: string; value: string; label: string }) {
  return (
    <div class="flex items-center gap-2 py-px">
      <span class="w-2.5 h-[2px] rounded-full shrink-0" style={{ background: props.color }} />
      <span class="text-meta font-medium tabular-nums text-[color:var(--text-primary)]">{props.value}</span>
      <span class="text-micro text-[color:var(--text-tertiary)]">{props.label}</span>
    </div>
  );
}
