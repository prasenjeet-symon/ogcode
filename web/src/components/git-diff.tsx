import { For, Show, createMemo } from 'solid-js';

// A parsed unified-diff hunk.
export interface Hunk {
  header: string; // the @@ -a,b +c,d @@ line
  file: string; // the path of the file this hunk belongs to (from the diff --git line)
  lines: DiffLine[];
}

export type DiffLineKind = 'add' | 'del' | 'ctx' | 'note';

export interface DiffLine {
  kind: DiffLineKind;
  text: string; // line content without the leading +/−/space
}

// parseUnifiedDiff parses raw unified-diff text into a list of hunks. It skips
// the file-level header lines (index, +++, ---) and treats "\ No newline at end
// of file" lines as muted notes. The `diff --git` header is used to extract the
// file path and is attached to each hunk so the renderer can draw a per-file
// separator. Binary-file markers are passed through as a single note line so
// the caller can render them as-is.
export function parseUnifiedDiff(text: string): Hunk[] {
  const hunks: Hunk[] = [];
  let cur: Hunk | null = null;
  let file = '';
  const lines = text.split('\n');

  for (const line of lines) {
    if (line === '') continue;
    if (line.startsWith('diff --git ')) {
      // file header — start fresh (a single `git show` can contain multiple
      // files). Flush any in-progress hunk and capture the file path for the
      // next hunks.
      if (cur) hunks.push(cur);
      cur = null;
      file = extractFilePath(line);
      continue;
    }
    if (line.startsWith('index ') || line.startsWith('--- ') || line.startsWith('+++ ')) {
      continue;
    }
    if (line.startsWith('@@')) {
      if (cur) hunks.push(cur);
      cur = { header: line, file, lines: [] };
      continue;
    }
    if (line.startsWith('Binary files')) {
      if (cur) hunks.push(cur);
      cur = { header: '', file, lines: [{ kind: 'note', text: line }] };
      hunks.push(cur);
      cur = null;
      continue;
    }
    if (!cur) continue; // stray line outside a hunk; ignore
    if (line.startsWith('\\')) {
      // "\ No newline at end of file"
      cur.lines.push({ kind: 'note', text: line });
      continue;
    }
    const ch = line[0];
    if (ch === '+') {
      cur.lines.push({ kind: 'add', text: line.slice(1) });
    } else if (ch === '-') {
      cur.lines.push({ kind: 'del', text: line.slice(1) });
    } else if (ch === ' ') {
      cur.lines.push({ kind: 'ctx', text: line.slice(1) });
    } else {
      // Unknown line — render as context.
      cur.lines.push({ kind: 'ctx', text: line });
    }
  }
  if (cur) hunks.push(cur);
  return hunks;
}

// extractFilePath pulls the file path out of a `diff --git a/<path> b/<path>`
// header line. The `b/` (post-image) side is the authoritative path. Returns the
// raw line unchanged as a fallback if the format is unexpected.
function extractFilePath(diffLine: string): string {
  // `diff --git a/<path> b/<path>` — take the b/ (post-image) side. The first
  // group is non-greedy so it stops at the first " b/", which is the real
  // separator (the a/ side always precedes it in git's output).
  const m = diffLine.match(/diff --git a\/(.*?) b\/(.*)$/);
  if (m) return m[2];
  // Fallback: take everything after the last " b/".
  const idx = diffLine.indexOf(' b/');
  if (idx !== -1) return diffLine.slice(idx + 3);
  return diffLine;
}

// GitDiff renders raw unified-diff text (from `git diff` / `git show`) as a
// GitHub-style line diff. Unlike FileDiff it does not recompute a diff — it
// parses the @@ hunks directly.
export default function GitDiff(props: { diff: string; filename?: string }) {
  const hunks = createMemo(() => parseUnifiedDiff(props.diff));

  // Flatten hunks into renderable rows. A `file` row is emitted whenever the
  // file path changes between consecutive hunks, so multi-file commits (e.g.
  // `git show`) get a visible per-file separator. No row cap — the whole diff
  // is rendered.
  const rows = createMemo(() => {
    type Row =
      | { kind: 'file'; text: string; first: boolean }
      | { kind: 'hunk'; text: string }
      | { kind: 'line'; line: DiffLine };
    const out: Row[] = [];
    let lastFile = '';
    let fileIndex = -1;
    for (const h of hunks()) {
      if (h.file && h.file !== lastFile) {
        fileIndex++;
        out.push({ kind: 'file', text: h.file, first: fileIndex === 0 });
        lastFile = h.file;
      }
      if (h.header) out.push({ kind: 'hunk', text: h.header });
      for (const ln of h.lines) out.push({ kind: 'line', line: ln });
    }
    return out;
  });

  return (
    <div class="rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] overflow-hidden">
      <Show
        when={props.diff.trim() !== ''}
        fallback={
          <div class="px-3 py-3 text-[12px]" style={{ color: 'var(--text-muted)' }}>
            No changes to display.
          </div>
        }
      >
        <div class="overflow-x-auto font-mono text-[12px] leading-[1.5] py-1">
          <div class="w-max min-w-full">
          <For each={rows()}>
            {(row) => (
              <Show
                when={row.kind === 'file'}
                fallback={
                  <Show
                    when={row.kind === 'hunk'}
                    fallback={<DiffRow line={(row as { kind: 'line'; line: DiffLine }).line} />}
                  >
                    <div
                      class="px-3 py-0.5 text-[11px] select-none"
                      style={{ color: 'var(--text-muted)', background: 'rgba(148,163,184,0.08)' }}
                    >
                      {(row as { kind: 'hunk'; text: string }).text}
                    </div>
                  </Show>
                }
              >
                <div
                  class="px-3 py-1 text-[12px] font-semibold select-none"
                  style={{
                    color: 'var(--text-primary)',
                    background: 'rgba(148,163,184,0.12)',
                    borderTop: '1px solid var(--border-subtle)',
                    borderBottom: '1px solid var(--border-subtle)',
                    'margin-top': (row as { kind: 'file'; text: string; first: boolean }).first ? '0' : '12px',
                  }}
                >
                  {(row as { kind: 'file'; text: string; first: boolean }).text}
                </div>
              </Show>
            )}
          </For>
          </div>
        </div>
      </Show>
    </div>
  );
}

function DiffRow(props: { line: DiffLine }) {
  const k = () => props.line.kind;
  const bg = () =>
    k() === 'add'
      ? 'rgba(16,185,129,0.10)'
      : k() === 'del'
        ? 'rgba(239,68,68,0.10)'
        : 'transparent';
  const gutter = () => (k() === 'add' ? '+' : k() === 'del' ? '−' : ' ');
  const gutterColor = () =>
    k() === 'add'
      ? 'var(--success)'
      : k() === 'del'
        ? 'var(--danger)'
        : 'var(--text-muted)';
  const textColor = () =>
    k() === 'add'
      ? '#a7f3d0'
      : k() === 'del'
        ? '#fecaca'
        : k() === 'note'
          ? 'var(--text-muted)'
          : 'var(--text-secondary)';
  return (
    <div class="flex w-full" style={{ background: bg() }}>
      <span class="select-none w-5 shrink-0 text-center" style={{ color: gutterColor() }}>
        {gutter()}
      </span>
      <span class="whitespace-pre pr-3 flex-1" style={{ color: textColor() }}>
        {props.line.text || ' '}
      </span>
    </div>
  );
}