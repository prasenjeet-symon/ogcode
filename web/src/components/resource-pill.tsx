import { createMemo, Show } from 'solid-js';
import { useServer } from '../context/server';
import type { ResourceSample, ResourceActivity } from '../api/client';

function formatBytes(n: number): string {
  if (!n) return '—';
  const mb = n / 1_048_576;
  if (mb < 1) return `${Math.round(n / 1024)} KB`;
  if (mb < 1000) return `${mb < 10 ? mb.toFixed(1) : Math.round(mb)} MB`;
  return `${(mb / 1024).toFixed(2)} GB`;
}

function formatPercent(pct: number): string {
  if (pct <= 0) return '0%';
  return `${pct < 10 ? pct.toFixed(1) : Math.round(pct)}%`;
}

function formatUptime(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

function progressPercent(activity: ResourceActivity): number {
  if (activity.total <= 0) return 0;
  return Math.min(100, Math.max(0, (activity.done / activity.total) * 100));
}

// Enough headroom that idle jitter of a few megabytes stays a flat line instead
// of being normalised up into a mountain range.
const MIN_RANGE_BYTES = 8 * 1_048_576;

function sparklinePoints(samples: ResourceSample[]): string {
  if (samples.length === 0) return '';
  if (samples.length === 1) return '0,12 100,12';

  const values = samples.map((s) => s.rss);
  const min = Math.min(...values);
  const max = Math.max(...values);
  // A pure min–max fit would make any wobble fill the whole height; flooring the
  // range keeps the line's amplitude proportional to how much memory actually
  // moved.
  const range = Math.max(max - min, max * 0.1, MIN_RANGE_BYTES);

  return values
    .map((v, i) => {
      const x = (i / (values.length - 1)) * 100;
      const y = 22 - ((v - min) / range) * 20;
      return `${x.toFixed(2)},${Math.max(2, Math.min(22, y)).toFixed(2)}`;
    })
    .join(' ');
}

/**
 * ResourcePill shows what ogcode itself is costing on this machine: a sparkline
 * of resident memory over the last few minutes, the current figure, and CPU.
 * The breakdown is on hover, because the point of the pill is to be glanceable
 * and quiet unless something is wrong.
 */
export default function ResourcePill() {
  const server = useServer();

  const samples = createMemo(() => server.resources());
  const latest = createMemo<ResourceSample | undefined>(() => {
    const all = samples();
    return all[all.length - 1];
  });
  const points = createMemo(() => sparklinePoints(samples()));

  return (
    <Show when={latest()}>
      {(sample) => (
        <div class="group relative flex items-center gap-1.5 h-7 px-2 rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] cursor-default select-none overflow-visible">
          <svg
            class="w-10 h-4 shrink-0 text-zinc-500"
            viewBox="0 0 100 24"
            preserveAspectRatio="none"
            aria-hidden="true"
          >
            <polyline
              points={points()}
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
              stroke-linejoin="round"
              vector-effect="non-scaling-stroke"
            />
          </svg>
          <span class="text-micro font-medium text-zinc-300 tabular-nums">
            {formatBytes(sample().rss)}
          </span>
          <span class="text-micro text-zinc-500 tabular-nums">
            {formatPercent(sample().cpuPercent)}
          </span>
          {/* A spike with no explanation reads as a bug. When the server has
              said what it is busy with, the pill says so inline rather than
              making the user hover to find out. */}
          <Show when={server.resourceMeta().activity}>
            {(activity) => (
              <>
                <span class="w-px h-3 bg-[color:var(--border-subtle)] shrink-0" />
                <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] animate-pulse shrink-0" />
                <span class="text-micro text-zinc-400 truncate max-w-[8rem]">
                  {activity().label}
                </span>
                <span class="text-micro text-zinc-500 tabular-nums shrink-0">
                  {activity().done}/{activity().total}
                </span>
              </>
            )}
          </Show>

          {/* Hover breakdown */}
          <div
            class="absolute top-full right-0 mt-1.5 w-64 p-3 rounded-lg border border-[color:var(--border-default)] bg-[color:var(--bg-overlay)] shadow-xl
                   opacity-0 pointer-events-none group-hover:opacity-100 group-hover:pointer-events-auto transition"
            style={{ 'z-index': 9999 }}
          >
            <div class="text-micro uppercase tracking-wider text-zinc-500 font-semibold mb-2">
              ogcode on this machine
            </div>
            <Show when={server.resourceMeta().activity}>
              {(activity) => (
                <div class="mb-2 pb-2 border-b border-[color:var(--border-subtle)]">
                  <div class="flex items-center justify-between mb-1.5">
                    <span class="text-meta text-zinc-300">{activity().label}</span>
                    <span class="text-meta font-mono tabular-nums text-zinc-400">
                      {activity().done} of {activity().total}
                    </span>
                  </div>
                  <div class="h-1 rounded-full bg-[color:var(--bg-elevated)] overflow-hidden">
                    <div
                      class="h-full rounded-full bg-[color:var(--accent)] transition-[width]"
                      style={{ width: `${progressPercent(activity())}%` }}
                    />
                  </div>
                </div>
              )}
            </Show>
            <Row
              label="CPU"
              value={formatPercent(sample().cpuPercent)}
              dot="bg-[color:var(--accent)]"
              note={server.resourceMeta().cores > 0 ? `of ${server.resourceMeta().cores} cores` : undefined}
            />
            <Row label="Resident" value={formatBytes(sample().rss)} dot="bg-emerald-400" />
            <Row label="Go heap" value={formatBytes(sample().heapInUse)} dot="bg-violet-400" />
            <Row label="Go runtime" value={formatBytes(sample().goTotal)} dot="bg-amber-400" />
            <Row label="Goroutines" value={sample().goroutines.toLocaleString()} dot="bg-sky-400" />
            <div class="mt-2 pt-2 border-t border-[color:var(--border-subtle)] flex items-center justify-between">
              <span class="text-micro font-semibold text-zinc-200">Uptime</span>
              <span class="text-meta font-mono tabular-nums text-zinc-100">
                {formatUptime(server.resourceMeta().uptime)}
              </span>
            </div>
            {/* Without this note the gap between resident and the Go figures
                reads as a leak. It is the single most likely misreading of this
                pill, and it goes both ways: Go frees lazily so resident lags
                above, while memory Go has reserved but never touched is not
                resident at all, so it can sit below. */}
            <p class="mt-2 text-micro leading-snug text-zinc-500">
              Resident is the OS figure, covering the embedding model and native parsers.
              On macOS it overstates: pages Go has already handed back stay counted until
              something else needs them, so it sits above what Activity Monitor reports.
              Go runtime is the honest total. 100% CPU is one full core.
            </p>
          </div>
        </div>
      )}
    </Show>
  );
}

function Row(props: { label: string; value: string; dot: string; note?: string }) {
  return (
    <div class="flex items-center justify-between py-0.5">
      <div class="flex items-center gap-1.5">
        <span class={`w-1.5 h-1.5 rounded-full ${props.dot}`} />
        <span class="text-meta text-zinc-400">{props.label}</span>
      </div>
      <div class="flex items-baseline gap-1.5">
        <Show when={props.note}>
          <span class="text-micro text-zinc-600">{props.note}</span>
        </Show>
        <span class="text-meta font-mono tabular-nums text-zinc-200">{props.value}</span>
      </div>
    </div>
  );
}
