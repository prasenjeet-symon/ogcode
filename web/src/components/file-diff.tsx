import { For, Show, createMemo } from 'solid-js';
import { diffLines } from 'diff';

export interface DiffStat {
  adds: number;
  dels: number;
}

// splitLines splits a diff chunk into lines, dropping the empty tail that comes
// from a value ending in a newline.
function splitLines(value: string): string[] {
  const lines = value.split('\n');
  if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
  return lines;
}

// diffStat returns the added/removed line counts between two texts.
export function diffStat(oldText: string, newText: string): DiffStat {
  let adds = 0;
  let dels = 0;
  for (const part of diffLines(oldText || '', newText || '')) {
    if (!part.added && !part.removed) continue;
    const n = splitLines(part.value).length;
    if (part.added) adds += n;
    else dels += n;
  }
  return { adds, dels };
}

type Row = { type: 'add' | 'del' | 'ctx'; text: string };

const MAX_ROWS = 600;

// FileDiff renders a GitHub-style unified line diff for a single file change.
export default function FileDiff(props: {
  oldText: string;
  newText: string;
  mode: 'create' | 'edit' | 'overwrite';
  omitted?: boolean;
}) {
  const rows = createMemo<Row[]>(() => {
    if (props.omitted) return [];
    const out: Row[] = [];
    for (const part of diffLines(props.oldText || '', props.newText || '')) {
      const type: Row['type'] = part.added ? 'add' : part.removed ? 'del' : 'ctx';
      for (const ln of splitLines(part.value)) out.push({ type, text: ln });
    }
    return out;
  });

  const shown = createMemo(() => rows().slice(0, MAX_ROWS));
  const truncated = createMemo(() => rows().length - shown().length);
  const stat = createMemo(() => diffStat(props.oldText, props.newText));
  const modeLabel = () =>
    props.mode === 'create' ? 'new file' : props.mode === 'edit' ? 'edit' : 'overwrite';

  return (
    <div class="rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] overflow-hidden">
      <div class="flex items-center gap-2 px-3 h-7 border-b border-[color:var(--border-subtle)] text-micro">
        <span class="uppercase tracking-wide font-medium" style={{ color: 'var(--text-muted)' }}>
          {modeLabel()}
        </span>
        <div class="flex-1" />
        <Show when={!props.omitted}>
          <span class="font-mono tabular-nums" style={{ color: 'var(--success)' }}>+{stat().adds}</span>
          <span class="font-mono tabular-nums" style={{ color: 'var(--danger)' }}>−{stat().dels}</span>
        </Show>
      </div>

      <Show
        when={!props.omitted}
        fallback={
          <div class="px-3 py-3 text-meta" style={{ color: 'var(--text-muted)' }}>
            File too large to show a diff.
          </div>
        }
      >
        <div class="overflow-x-auto font-mono text-meta leading-[1.5] py-1">
          <For each={shown()}>{(row) => <DiffRow row={row} />}</For>
          <Show when={truncated() > 0}>
            <div class="px-3 py-1 text-micro" style={{ color: 'var(--text-muted)' }}>
              … {truncated()} more lines
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}

function DiffRow(props: { row: Row }) {
  const t = () => props.row.type;
  const bg = () =>
    t() === 'add' ? 'rgba(16,185,129,0.10)' : t() === 'del' ? 'rgba(239,68,68,0.10)' : 'transparent';
  const gutter = () => (t() === 'add' ? '+' : t() === 'del' ? '−' : ' ');
  const gutterColor = () =>
    t() === 'add' ? 'var(--success)' : t() === 'del' ? 'var(--danger)' : 'var(--text-muted)';
  const textColor = () => (t() === 'add' ? '#a7f3d0' : t() === 'del' ? '#fecaca' : 'var(--text-secondary)');
  return (
    <div class="flex" style={{ background: bg() }}>
      <span class="select-none w-5 shrink-0 text-center" style={{ color: gutterColor() }}>{gutter()}</span>
      <span class="whitespace-pre pr-3" style={{ color: textColor() }}>{props.row.text || ' '}</span>
    </div>
  );
}
