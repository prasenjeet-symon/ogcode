// activity-heatmap.tsx — a year of usage as a GitHub-style contribution grid.
//
// 53 week-columns of 7 days, Sunday-first, the last column holding today, so a
// year of work reads at a glance. Two metrics share the grid — sessions and
// tokens — each bucketed into four tints from its own distribution, because a
// session and a token do not share a scale. Hovering or focusing a cell names
// its day; a failed fetch draws the empty grid rather than an error, since the
// card is ambient and must never take the page down with it.

import { For, Show, createMemo, createResource, createSignal } from 'solid-js';
import { getActivity, type ActivityDay, type ActivitySummary } from '../api/client';
import './activity-heatmap.css';

const WEEKS = 53;
const DAYS = 7;
/** A card mounts on the home page and again in a new session; one fetch serves both. */
const CACHE_TTL_MS = 60_000;

type Metric = 'sessions' | 'tokens';

interface Cell {
  /** YYYY-MM-DD in the viewer's local time. */
  day: string;
  value: number;
  /** 0 for a day with nothing on it, else 1-4. */
  level: number;
  /** Later than today: drawn blank, and not part of the year that has happened. */
  future: boolean;
}

interface Tip {
  col: number;
  row: number;
  label: string;
}

// The grid is a pure function of the day it is drawn on, so the fetch source is
// stable for the life of the page: a re-render never re-requests.
const at = Date.now();
const dayStart = (ms: number): Date => {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d;
};
const addDays = (d: Date, days: number): Date =>
  new Date(d.getFullYear(), d.getMonth(), d.getDate() + days);
const weekStart = (d: Date): Date => addDays(dayStart(d.getTime()), -d.getDay());
const dayKey = (d: Date): string => {
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${d.getFullYear()}-${m}-${day}`;
};
const parseDay = (day: string): Date => {
  const [y, m, d] = day.split('-').map(Number);
  return new Date(y, m - 1, d);
};

// The current week is the last column; the grid starts 52 weeks before it, so
// it is exactly 53 columns and today always lands inside the final one.
const gridEnd = weekStart(new Date(at));
const gridStart = addDays(gridEnd, -(WEEKS - 1) * DAYS);
const rangeFrom = gridStart.getTime();

let cache: { from: number; at: number; data: ActivitySummary } | null = null;
let inflight: { from: number; promise: Promise<ActivitySummary> } | null = null;

function loadActivity(from: number, to: number): Promise<ActivitySummary> {
  if (cache && cache.from === from && Date.now() - cache.at < CACHE_TTL_MS) {
    return Promise.resolve(cache.data);
  }
  if (inflight && inflight.from === from) return inflight.promise;
  const promise = getActivity(from, to)
    .then((data) => {
      cache = { from, at: Date.now(), data };
      inflight = null;
      return data;
    })
    .catch((err) => {
      inflight = null;
      throw err;
    });
  inflight = { from, promise };
  return promise;
}

// Quartile cut points over the days that saw something, so the darkest tint
// stays rare even when one day dwarfs the rest.
function thresholds(values: number[]): number[] {
  const sorted = values.filter((v) => v > 0).sort((a, b) => a - b);
  if (sorted.length === 0) return [];
  const quantile = (p: number) => sorted[Math.min(sorted.length - 1, Math.floor(p * sorted.length))];
  return [quantile(0.25), quantile(0.5), quantile(0.75)];
}

function levelOf(value: number, cuts: number[]): number {
  if (value <= 0) return 0;
  let level = 1;
  for (const cut of cuts) if (value > cut) level += 1;
  return Math.min(4, level);
}

function cellLabel(cell: Cell, metric: Metric): string {
  const when = parseDay(cell.day).toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  });
  if (cell.value <= 0) return `No ${metric} on ${when}`;
  const noun = metric === 'sessions' ? 'sessions' : 'tokens';
  return `${cell.value.toLocaleString('en-US')} ${noun} on ${when}`;
}

const MONTH_NAMES = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const WEEKDAY_LABELS = ['', 'Mon', '', 'Wed', '', 'Fri', ''];

// A label sits over the column where its month starts; one that would overlap
// the label before it is dropped rather than pushed into it.
function monthLabels(columns: Cell[][]): (string | null)[] {
  const out: (string | null)[] = [];
  let month = -1;
  let labelled = -10;
  for (let col = 0; col < columns.length; col++) {
    const first = parseDay(columns[col][0].day);
    let label: string | null = null;
    if (first.getMonth() !== month) {
      month = first.getMonth();
      if (col - labelled >= 3) {
        label = MONTH_NAMES[month];
        labelled = col;
      }
    }
    out.push(label);
  }
  return out;
}

export function ActivityHeatmap(props: { compact?: boolean }) {
  const [tab, setTab] = createSignal<Metric>('sessions');
  const [tip, setTip] = createSignal<Tip | null>(null);
  const [data] = createResource(() => rangeFrom, (from) => loadActivity(from, at));

  const grid = createMemo(() => {
    const byDay = new Map<string, ActivityDay>();
    for (const day of data()?.days ?? []) byDay.set(day.day, day);
    const metric = tab();
    const cuts = thresholds([...byDay.values()].map((d) => d[metric]));
    const today = dayKey(new Date(at));
    const columns: Cell[][] = [];
    let total = 0;
    for (let col = 0; col < WEEKS; col++) {
      const cells: Cell[] = [];
      for (let row = 0; row < DAYS; row++) {
        const day = dayKey(addDays(gridStart, col * DAYS + row));
        const value = byDay.get(day)?.[metric] ?? 0;
        total += value;
        cells.push({ day, value, level: levelOf(value, cuts), future: day > today });
      }
      columns.push(cells);
    }
    return { columns, total };
  });

  const summary = createMemo(() => {
    const { total } = grid();
    if (total <= 0) return 'No activity yet';
    const noun = tab() === 'sessions' ? 'sessions' : 'tokens';
    return `${total.toLocaleString('en-US')} ${noun} in the last year`;
  });

  // Geometry lives in CSS (--ah-cell/--ah-gap/--ah-step), so the tooltip tracks
  // the compact card without duplicating the measurements here. The month row
  // sits BELOW the grid, so only the bottom two rows open upward.
  const tipStyle = (col: number, row: number) => {
    const left = `calc(var(--ah-step) * ${col} + var(--ah-cell) / 2)`;
    return row <= DAYS - 3
      ? { left, top: `calc(var(--ah-step) * ${row} + var(--ah-cell) + 6px)`, transform: 'translateX(-50%)' }
      : { left, top: `calc(var(--ah-step) * ${row} - 6px)`, transform: 'translate(-50%, -100%)' };
  };

  const choose = (metric: Metric) => {
    setTab(metric);
    setTip(null);
  };

  return (
    <div class={`ah-card${props.compact ? ' is-compact' : ''}`}>
      <div class="ah-head">
        <div class="ah-tabs" role="group" aria-label="Activity metric">
          <button
            type="button"
            class={`ah-tab${tab() === 'sessions' ? ' is-active' : ''}`}
            aria-pressed={tab() === 'sessions'}
            onClick={() => choose('sessions')}
          >
            Sessions
          </button>
          <button
            type="button"
            class={`ah-tab${tab() === 'tokens' ? ' is-active' : ''}`}
            aria-pressed={tab() === 'tokens'}
            onClick={() => choose('tokens')}
          >
            Tokens
          </button>
        </div>
        <div class="ah-summary">{summary()}</div>
      </div>

      <div class="ah-body">
        <div class="ah-weekdays" aria-hidden="true">
          <For each={WEEKDAY_LABELS}>{(label) => <span class="ah-weekday">{label}</span>}</For>
        </div>

        <div class="ah-scroll">
          <div class="ah-grid" role="img" aria-label={summary()}>
            <For each={grid().columns}>
              {(column, col) => (
                <For each={column}>
                  {(cell, row) => (
                    <div
                      class={`ah-cell l${cell.level}${cell.future ? ' is-future' : ''}`}
                      tabindex="0"
                      aria-label={cellLabel(cell, tab())}
                      onPointerEnter={() => setTip({ col: col(), row: row(), label: cellLabel(cell, tab()) })}
                      onPointerLeave={() => setTip(null)}
                      onFocus={() => setTip({ col: col(), row: row(), label: cellLabel(cell, tab()) })}
                      onBlur={() => setTip(null)}
                    />
                  )}
                </For>
              )}
            </For>
            <Show when={tip()}>
              {(t) => (
                <div class="ah-tip" style={tipStyle(t().col, t().row)}>
                  {t().label}
                </div>
              )}
            </Show>
          </div>

          <div class="ah-months" aria-hidden="true">
            <For each={monthLabels(grid().columns)}>
              {(label, col) => (
                <Show when={label}>
                  <span
                    class="ah-month"
                    classList={{ 'is-end': col() + 1 > grid().columns.length - 3 }}
                    style={{ 'grid-column': String(col() + 1) }}
                  >
                    {label}
                  </span>
                </Show>
              )}
            </For>
          </div>
        </div>
      </div>

      <div class="ah-foot">
        <div class="ah-legend">
          <span class="ah-legend-text">Less</span>
          <For each={[0, 1, 2, 3, 4]}>{(level) => <span class={`ah-swatch l${level}`} />}</For>
          <span class="ah-legend-text">More</span>
        </div>
      </div>
    </div>
  );
}
