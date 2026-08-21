import { For, Show } from 'solid-js';
import { type IndexFile } from '../api/client';

// ---------------------------------------------------------------------------
// Tree model
// ---------------------------------------------------------------------------

export interface TreeNode {
  /** Stable identity: the relative path for folders, the absolute path for files. */
  id: string;
  /** Display label. Collapsed folder chains render as "a/b/c". */
  name: string;
  kind: 'dir' | 'file';
  children: TreeNode[];
  /** Whether this file is already in the index. */
  indexed: boolean;
  fileCount: number;
  /** Indexed files only — count of pages the index holds for this document. */
  pageCount: number;
  /** Count of indexed files within this subtree (folders only). */
  indexedCount: number;
}

interface RawDir {
  name: string;
  path: string;
  dirs: Map<string, RawDir>;
  files: IndexFile[];
}

export function basename(path: string): string {
  return path.split('/').pop() || path;
}

export function fileExt(name: string): string {
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
}

/** Longest common directory prefix across all file paths. */
export function commonPrefix(files: { path: string }[]): string {
  if (files.length === 0) return '';
  const parts = files.map((f) => f.path.split('/').slice(0, -1));
  const minLen = Math.min(...parts.map((p) => p.length));
  let len = 0;
  for (let i = 0; i < minLen; i++) {
    if (parts.every((p) => p[i] === parts[0][i])) len = i + 1;
    else break;
  }
  return parts[0].slice(0, len).join('/');
}

/** Path of a doc relative to the tree root. */
export function relPath(docPath: string, prefix: string): string {
  if (!prefix) return docPath;
  return docPath.startsWith(prefix + '/') ? docPath.slice(prefix.length + 1) : docPath;
}

function toNode(raw: RawDir, isRoot: boolean): TreeNode {
  const dirs = [...raw.dirs.values()]
    .map((d) => toNode(d, false))
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));

  const files: TreeNode[] = raw.files
    .slice()
    .sort((a, b) => basename(a.path).localeCompare(basename(b.path), undefined, { numeric: true }))
    .map((file) => ({
      id: file.path,
      name: basename(file.path),
      kind: 'file' as const,
      children: [],
      indexed: file.indexed,
      fileCount: 1,
      pageCount: file.indexed ? file.pageCount : 0,
      indexedCount: file.indexed ? 1 : 0,
    }));

  let node: TreeNode = {
    id: raw.path,
    name: raw.name,
    kind: 'dir',
    children: [...dirs, ...files],
    indexed: false,
    fileCount: 0,
    pageCount: 0,
    indexedCount: 0,
  };

  // Collapse single-child folder chains the way file browsers do: a folder that
  // holds nothing but one subfolder renders as "parent/child" on one row.
  if (!isRoot && files.length === 0 && dirs.length === 1 && dirs[0].kind === 'dir') {
    node = { ...dirs[0], name: `${raw.name}/${dirs[0].name}` };
  }

  node.fileCount = node.children.reduce((s, c) => s + (c.kind === 'file' ? 1 : c.fileCount), 0);
  node.pageCount = node.children.reduce((s, c) => s + c.pageCount, 0);
  node.indexedCount = node.children.reduce((s, c) => s + (c.kind === 'file' ? (c.indexed ? 1 : 0) : c.indexedCount), 0);
  return node;
}

/** Build a nested folder tree from a flat file list, rooted at their common prefix. */
export function buildTree(files: IndexFile[]): { root: TreeNode; prefix: string } {
  const prefix = commonPrefix(files);
  const root: RawDir = { name: '', path: '', dirs: new Map(), files: [] };

  for (const file of files) {
    const dir = file.path.split('/').slice(0, -1).join('/');
    const rel = dir === prefix ? '' : dir.startsWith(prefix + '/') ? dir.slice(prefix.length + 1) : dir;
    const segs = rel ? rel.split('/') : [];
    let cur = root;
    for (const seg of segs) {
      let next = cur.dirs.get(seg);
      if (!next) {
        next = { name: seg, path: cur.path ? `${cur.path}/${seg}` : seg, dirs: new Map(), files: [] };
        cur.dirs.set(seg, next);
      }
      cur = next;
    }
    cur.files.push(file);
  }

  return { root: toNode(root, true), prefix };
}

export interface FlatRow {
  node: TreeNode;
  depth: number;
}

/** Depth-first walk of the rows currently visible under the expansion state. */
export function flattenTree(root: TreeNode, isExpanded: (id: string) => boolean): FlatRow[] {
  const out: FlatRow[] = [];
  const walk = (nodes: TreeNode[], depth: number) => {
    for (const n of nodes) {
      out.push({ node: n, depth });
      if (n.kind === 'dir' && isExpanded(n.id)) walk(n.children, depth + 1);
    }
  };
  walk(root.children, 0);
  return out;
}

export function allDirIds(root: TreeNode): Set<string> {
  const ids = new Set<string>();
  const walk = (nodes: TreeNode[]) => {
    for (const n of nodes) {
      if (n.kind !== 'dir') continue;
      ids.add(n.id);
      walk(n.children);
    }
  };
  walk(root.children);
  return ids;
}

/**
 * Open folders breadth-first until roughly `budget` rows are on screen, so a
 * small project lands fully expanded and a large one stays scannable.
 */
export function defaultExpanded(root: TreeNode, budget = 50): Set<string> {
  const open = new Set<string>();
  let visible = root.children.length;
  const queue = root.children.filter((n) => n.kind === 'dir');
  while (queue.length) {
    const n = queue.shift()!;
    if (visible + n.children.length > budget) break;
    visible += n.children.length;
    open.add(n.id);
    for (const c of n.children) if (c.kind === 'dir') queue.push(c);
  }
  return open;
}

// ---------------------------------------------------------------------------
// Language colors
// ---------------------------------------------------------------------------

const LANG_COLORS: Record<string, string> = {
  go: '#00add8',
  ts: '#3178c6',
  tsx: '#3178c6',
  js: '#f7df1e',
  jsx: '#f7df1e',
  py: '#3fa66c',
  rs: '#e07b53',
  java: '#e76f00',
  kt: '#a97bff',
  swift: '#f05138',
  c: '#7f9fbf',
  h: '#7f9fbf',
  cpp: '#9c72c9',
  cs: '#8a56d6',
  rb: '#cc342d',
  php: '#7b7fb5',
  dart: '#0f9d99',
  sh: '#89e051',
  sql: '#c084fc',
  css: '#38bdf8',
  scss: '#e0559a',
  html: '#e34c26',
  json: '#cbd5e1',
  yaml: '#e0b34d',
  yml: '#e0b34d',
  toml: '#a6a29a',
  md: '#94a3b8',
  mdx: '#94a3b8',
  txt: '#8b8b95',
  pdf: '#ef4444',
  docx: '#3b82f6',
  doc: '#3b82f6',
  xlsx: '#22c55e',
  csv: '#22c55e',
  png: '#f472b6',
  jpg: '#f472b6',
  svg: '#fbbf24',
};

export function langColor(ext: string): string {
  return LANG_COLORS[ext] || '#6b6b76';
}

/** Translucent version of a hex color, for chips and bars. */
export function tint(hex: string, alpha: number): string {
  const h = hex.replace('#', '');
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

// ---------------------------------------------------------------------------
// Row rendering
// ---------------------------------------------------------------------------

function FolderGlyph(props: { open: boolean }) {
  return (
    <Show
      when={props.open}
      fallback={
        <svg class="w-[13px] h-[13px] shrink-0 text-[#7d8590]" viewBox="0 0 16 16" fill="currentColor">
          <path d="M1.75 2.5A1.75 1.75 0 000 4.25v7.5C0 12.716.784 13.5 1.75 13.5h12.5A1.75 1.75 0 0016 11.75v-6A1.75 1.75 0 0014.25 4H7.81l-1.2-1.2a1.75 1.75 0 00-1.238-.513H1.75z" />
        </svg>
      }
    >
      <svg class="w-[13px] h-[13px] shrink-0 text-[#9aa4b0]" viewBox="0 0 16 16" fill="currentColor">
        <path d="M1.75 2.5h3.622c.464 0 .909.184 1.237.513L7.81 4.22h4.44c.966 0 1.75.784 1.75 1.75v.53H4.6a1.75 1.75 0 00-1.68 1.257L1.2 13.5A1.75 1.75 0 010 11.75v-7.5C0 3.284.784 2.5 1.75 2.5z" />
        <path d="M4.6 7.75h10.4a1 1 0 01.958 1.288l-1.2 4A1.75 1.75 0 0113.07 14.3H2.4a1 1 0 01-.958-1.288l1.482-4.95A1 1 0 014.6 7.75z" />
      </svg>
    </Show>
  );
}

function FileGlyph(props: { ext: string }) {
  const color = () => langColor(props.ext);
  return (
    <svg class="w-[13px] h-[13px] shrink-0" viewBox="0 0 16 16" fill="none" style={{ color: color() }}>
      <path
        d="M9.5 1.5H4.25A1.75 1.75 0 002.5 3.25v9.5c0 .966.784 1.75 1.75 1.75h7.5a1.75 1.75 0 001.75-1.75V5.5L9.5 1.5z"
        fill={tint(color(), 0.16)}
        stroke="currentColor"
        stroke-width="1.2"
        stroke-linejoin="round"
      />
      <path d="M9.25 1.75V5.25h3.5" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round" />
    </svg>
  );
}

/** Case-insensitive match highlighting for the filter query. */
function Highlight(props: { text: string; query: string }) {
  const parts = () => {
    const q = props.query.trim().toLowerCase();
    if (!q) return [{ text: props.text, hit: false }];
    const out: { text: string; hit: boolean }[] = [];
    const lower = props.text.toLowerCase();
    let i = 0;
    while (i < props.text.length) {
      const at = lower.indexOf(q, i);
      if (at < 0) {
        out.push({ text: props.text.slice(i), hit: false });
        break;
      }
      if (at > i) out.push({ text: props.text.slice(i, at), hit: false });
      out.push({ text: props.text.slice(at, at + q.length), hit: true });
      i = at + q.length;
    }
    return out;
  };
  return (
    <For each={parts()}>
      {(p) => (
        <Show when={p.hit} fallback={<>{p.text}</>}>
          <mark class="bg-[color:var(--accent-ring)] text-zinc-100 rounded-[2px] px-px">{p.text}</mark>
        </Show>
      )}
    </For>
  );
}

export const ROW_HEIGHT = 26;
const INDENT = 14;

export interface TreeRowProps {
  node: TreeNode;
  depth: number;
  expanded: boolean;
  selected: boolean;
  query: string;
  onToggle: (id: string) => void;
  onSelect: (node: TreeNode) => void;
  attach?: (el: HTMLDivElement) => void;
}

export function TreeRow(props: TreeRowProps) {
  const isDir = () => props.node.kind === 'dir';
  const ext = () => (isDir() ? '' : fileExt(props.node.name));

  return (
    <div
      ref={(el) => props.attach?.(el)}
      onClick={() => {
        props.onSelect(props.node);
        if (isDir()) props.onToggle(props.node.id);
      }}
      class="group relative flex items-center gap-1.5 pr-2.5 cursor-pointer select-none"
      classList={{
        'bg-[color:var(--accent-soft)]': props.selected,
        'hover:bg-[color:var(--bg-elevated)]': !props.selected,
      }}
      style={{ height: `${ROW_HEIGHT}px`, 'padding-left': `${props.depth * INDENT + 6}px` }}
    >
      {/* Indent guides */}
      <For each={Array.from({ length: props.depth })}>
        {(_, i) => (
          <span
            class="pointer-events-none absolute top-0 bottom-0 w-px bg-[color:var(--border-subtle)]"
            style={{ left: `${i() * INDENT + 13}px` }}
          />
        )}
      </For>

      <Show when={props.selected}>
        <span class="pointer-events-none absolute left-0 top-0 bottom-0 w-[2px] bg-[color:var(--accent)]" />
      </Show>

      {/* Disclosure */}
      <span class="w-3.5 shrink-0 flex items-center justify-center">
        <Show when={isDir()}>
          <svg
            class="w-2.5 h-2.5 text-[color:var(--text-muted)] group-hover:text-[color:var(--text-secondary)] transition-transform duration-150"
            classList={{ '-rotate-90': !props.expanded }}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="3"
          >
            <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </Show>
      </span>

      <Show when={isDir()} fallback={<FileGlyph ext={ext()} />}>
        <FolderGlyph open={props.expanded} />
      </Show>

      <span
        class="flex-1 truncate text-[12px] leading-none"
        classList={{
          'text-[color:var(--text-primary)] font-medium': isDir(),
          'text-[color:var(--text-secondary)] group-hover:text-[color:var(--text-primary)]': !isDir() && !props.selected,
          'text-[color:var(--text-primary)]': !isDir() && props.selected,
        }}
        title={props.node.kind === 'file' ? props.node.id : props.node.id}
      >
        <Highlight text={props.node.name} query={props.query} />
      </span>

      <Show when={isDir()}>
        <Show when={props.node.indexedCount < props.node.fileCount} fallback={
          <span class="shrink-0 text-[10px] tabular-nums text-[color:var(--text-muted)] group-hover:text-[color:var(--text-tertiary)]">
            {props.node.fileCount}
          </span>
        }>
          <span
            class="shrink-0 text-[10px] tabular-nums flex items-center gap-0.5"
            title={`${props.node.indexedCount} of ${props.node.fileCount} indexed`}
          >
            <span class="text-[color:var(--success)]">{props.node.indexedCount}</span>
            <span class="text-[color:var(--text-muted)]">/{props.node.fileCount}</span>
          </span>
        </Show>
      </Show>

      <Show when={!isDir()}>
        <Show when={props.node.indexed} fallback={
          <span
            class="shrink-0 w-3 h-3 flex items-center justify-center text-[color:var(--text-muted)] opacity-0 group-hover:opacity-100 transition"
            title="Not indexed"
          >
            <svg class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
            </svg>
          </span>
        }>
          <span
            class="shrink-0 w-3 h-3 flex items-center justify-center text-[color:var(--success)]"
            title="Indexed"
          >
            <svg class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
            </svg>
          </span>
        </Show>
      </Show>

      <Show when={!isDir() && props.node.pageCount > 1}>
        <span
          class="shrink-0 text-[9px] font-mono px-1 py-px rounded"
          style={{ color: langColor(ext()), background: tint(langColor(ext()), 0.1) }}
        >
          {props.node.pageCount}p
        </span>
      </Show>
    </div>
  );
}
