import { For, Show } from 'solid-js';
import type { JSX } from 'solid-js';

// IndexEntries renders a document index (the output of the pdf_index and
// docx_index tools) as a scrollable page-by-page list instead of raw JSON.
export default function IndexEntries(props: {
  entries: { page: number; labels: string[] }[];
  maxHeight?: number;
}): JSX.Element {
  const labelsOf = (entry: { page: number; labels: string[] }) => entry.labels ?? [];

  return (
    <div class="rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] overflow-hidden">
      <div class="flex items-center gap-2 px-3 h-7 border-b border-[color:var(--border-subtle)] text-micro">
        <span class="uppercase tracking-wide text-[color:var(--text-muted)]">PAGES</span>
        <span class="font-mono tabular-nums text-[color:var(--text-muted)]">
          · {props.entries.length} pages
        </span>
      </div>

      <div
        class="overflow-y-auto px-3 py-2 space-y-1.5"
        style={{ 'max-height': `${props.maxHeight ?? 280}px` }}
      >
        <For each={props.entries}>
          {(entry) => (
            <div class="flex gap-2 items-baseline text-meta leading-snug">
              <span class="font-mono text-micro text-[color:var(--text-muted)] select-none shrink-0 w-10">
                p.{entry.page}
              </span>
              <Show
                when={labelsOf(entry).length > 0}
                fallback={<span class="italic text-[color:var(--text-muted)]">(no labels)</span>}
              >
                <span class="min-w-0 break-words">{labelsOf(entry).join(' · ')}</span>
              </Show>
            </div>
          )}
        </For>
      </div>
    </div>
  );
}
