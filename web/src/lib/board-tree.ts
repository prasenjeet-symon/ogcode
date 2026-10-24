import type { BoardHandle } from '../api/client';

// Board tree: the agent-tree logic the board drawer draws, lifted out of the
// component into a pure module. One row per build session or teammate, nested
// by the spawned_by edge, so the shape of the work — who spawned whom, and how
// many are still going — is computable without opening a session. No SolidJS,
// no DOM, no global state: each function takes what it needs as arguments and
// hands back a fresh value, so the drawer can memoise over a roster snapshot.

const STATUS_RANK: Record<string, number> = {
  running: 0,
  idle: 1,
  stale: 2,
  done: 3,
  stopped: 4,
};

/** done / stopped / stale — nothing will move the agent again. */
export const TERMINAL_STATUSES: ReadonlySet<string> = new Set(['done', 'stopped', 'stale']);

/** live = not terminal. */
export function isLive(h: BoardHandle): boolean {
  return !TERMINAL_STATUSES.has(h.status);
}

export interface TreeRow {
  h: BoardHandle;
  depth: number;
}

/** Label + colour for a status, for the dot and the text chip. */
export function statusMeta(status: string): {
  label: string; // 'running' | 'idle' | 'stale' | 'done' | 'stopped', or the raw status
  dot: string; // Tailwind classes, e.g. 'bg-[color:var(--accent)] animate-pulse'
  text: string; // Tailwind text colour classes for the status label
} {
  switch (status) {
    case 'running':
      return {
        label: 'running',
        dot: 'bg-[color:var(--accent)] animate-pulse',
        text: 'text-[color:var(--accent)]',
      };
    case 'idle':
      return {
        label: 'idle',
        dot: 'bg-[color:var(--text-tertiary)]',
        text: 'text-[color:var(--text-tertiary)]',
      };
    case 'stale':
      return { label: 'stale', dot: 'bg-amber-400', text: 'text-amber-400' };
    case 'done':
      return { label: 'done', dot: 'bg-emerald-400', text: 'text-emerald-400' };
    default:
      return { label: status, dot: 'bg-zinc-500', text: 'text-zinc-500' };
  }
}

/** 'teammate' | 'task' | 'session' — unknown kinds fall back to 'agent'. */
export function kindLabel(kind?: string): string {
  switch (kind) {
    case 'teammate':
    case 'task':
    case 'session':
      return kind;
    default:
      return 'agent';
  }
}

// childrenOf groups the roster by its spawned_by edge. A handle whose spawner
// is not on the roster is a root: it either joined on its own or its parent's
// row is gone.
function childrenOf(handles: BoardHandle[]): {
  roots: BoardHandle[];
  children: Map<string, BoardHandle[]>;
  byHandle: Map<string, BoardHandle>;
} {
  const byHandle = new Map(handles.map((h) => [h.handle, h]));
  const children = new Map<string, BoardHandle[]>();
  const roots: BoardHandle[] = [];
  for (const h of handles) {
    if (!h.spawnedBy || !byHandle.has(h.spawnedBy)) {
      roots.push(h);
      continue;
    }
    const list = children.get(h.spawnedBy) ?? [];
    list.push(h);
    children.set(h.spawnedBy, list);
  }
  return { roots, children, byHandle };
}

function byStatus(a: BoardHandle, b: BoardHandle): number {
  return (STATUS_RANK[a.status] ?? 9) - (STATUS_RANK[b.status] ?? 9) || a.handle.localeCompare(b.handle);
}

/** Flat, indented rows: parents before their children, cycle-safe, status-ranked siblings. */
export function buildTree(handles: BoardHandle[]): TreeRow[] {
  const { roots, children } = childrenOf(handles);
  const rows: TreeRow[] = [];
  const drawn = new Set<string>();
  const walk = (h: BoardHandle, depth: number) => {
    // A handle is drawn once: a cycle — two rows naming each other, which
    // nothing writes but a hand-edited database could hold — cannot hang the
    // render.
    if (drawn.has(h.handle)) return;
    drawn.add(h.handle);
    rows.push({ h, depth });
    for (const child of [...(children.get(h.handle) ?? [])].sort(byStatus)) {
      walk(child, depth + 1);
    }
  };
  for (const root of [...roots].sort(byStatus)) walk(root, 0);
  // A roster whose every row names a spawner inside it has no root; draw what
  // is left rather than losing it.
  for (const h of handles) walk(h, 0);
  return rows;
}

/** handle -> {total, live} descendants, cycle-safe, memoised. */
export function subtreeCounts(handles: BoardHandle[]): Map<string, { total: number; live: number }> {
  const { children, byHandle } = childrenOf(handles);
  const memo = new Map<string, { total: number; live: number }>();
  const count = (name: string, seen: Set<string>): { total: number; live: number } => {
    const cached = memo.get(name);
    if (cached) return cached;
    if (seen.has(name)) return { total: 0, live: 0 };
    seen.add(name);
    let total = 0;
    let live = 0;
    for (const child of children.get(name) ?? []) {
      if (!byHandle.has(child.handle)) continue;
      const below = count(child.handle, seen);
      total += 1 + below.total;
      live += (isLive(child) ? 1 : 0) + below.live;
    }
    seen.delete(name);
    const out = { total, live };
    memo.set(name, out);
    return out;
  };
  for (const h of handles) count(h.handle, new Set());
  return memo;
}

/**
 * Drops terminal rows that have no live descendant; keeps any row a live one
 * hangs from.
 */
export function visibleWithFinishedHidden(handles: BoardHandle[], rows: TreeRow[]): TreeRow[] {
  // Keeps a finished agent only when it has a live descendant: hiding a parent
  // would orphan a teammate that is still working.
  const { byHandle } = childrenOf(handles);
  const keep = new Set<string>();
  for (const h of handles) {
    if (!isLive(h)) continue;
    keep.add(h.handle);
    let parent = h.spawnedBy;
    while (parent && byHandle.has(parent) && !keep.has(parent)) {
      keep.add(parent);
      parent = byHandle.get(parent)?.spawnedBy;
    }
  }
  return rows.filter((row) => keep.has(row.h.handle));
}

export interface DrawerRow extends TreeRow {
  /** length === depth; rails[i] true = the vertical guide for that ancestor level continues past this row. */
  rails: boolean[];
  /** true when this row is the last of its parent's visible children (or a depth-0 row). */
  last: boolean;
  /** true when visible rows follow it at depth+1. */
  hasChildren: boolean;
}

/**
 * The visible rows decorated for drawing: indent rails + sibling/child flags.
 * Works on the rows it is given — call it AFTER filtering: every flag is read
 * off the visible list only, so a hidden sibling can ghost neither a rail nor
 * an elbow.
 */
export function decorateRows(rows: TreeRow[]): DrawerRow[] {
  const out: DrawerRow[] = [];
  for (let i = 0; i < rows.length; i++) {
    const row = rows[i];
    const next = rows[i + 1];
    // end is the first row after this one back at its depth or above: where
    // the parent's visible span ends. Everything in between hangs from this
    // row.
    let end = i + 1;
    while (end < rows.length && rows[end].depth > row.depth) end++;
    const last = row.depth === 0 || end >= rows.length || rows[end].depth < row.depth;
    // The next row alone decides the guides: it either descends — the row's
    // visible subtree continues, and every ancestor's guide runs through here —
    // or it is back at this row's depth or above, and nothing continues.
    const rails: boolean[] = [];
    for (let level = 0; level < row.depth; level++) {
      rails.push(next !== undefined && next.depth > level);
    }
    out.push({
      ...row,
      rails,
      last,
      hasChildren: next !== undefined && next.depth === row.depth + 1,
    });
  }
  return out;
}

/**
 * Header summary over the whole roster.
 *
 * Counts exactly the handles it is given — the caller filters pseudo rows
 * (the 'user' handle) out first if it wants the roster the tree draws. The
 * live-agents-badge stays caller-side for the same reason: every agent but
 * the session in view is what the badge counts, and that needs the caller's
 * own session id, which this summary does not carry.
 */
export function rosterSummary(handles: BoardHandle[]): { total: number; live: number; idle: number; finished: number } {
  let live = 0;
  let idle = 0;
  let finished = 0;
  for (const h of handles) {
    if (isLive(h)) {
      live++;
      if (h.status === 'idle') idle++;
    } else {
      finished++;
    }
  }
  return { total: handles.length, live, idle, finished };
}

/** 'just now' / '4m ago' / '3h ago' / 'Mar 4' — empty string for a missing or unparseable stamp. */
export function formatAge(ts?: string): string {
  if (!ts) return '';
  const t = Date.parse(ts);
  if (Number.isNaN(t)) return '';
  const diffMin = Math.floor((Date.now() - t) / 60000);
  if (diffMin < 1) return 'just now';
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  return new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

/** Compact duration: '48s', '3m 05s', '1h 04m', '2d 3h'. Never negative. */
export function formatDuration(ms: number): string {
  // Clamped, so a row stamped ahead of the caller's clock reads '0s', not a
  // negative duration.
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  const min = Math.floor(s / 60);
  const sec = s % 60;
  if (min < 60) return `${min}m ${String(sec).padStart(2, '0')}s`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ${String(min % 60).padStart(2, '0')}m`;
  return `${Math.floor(hr / 24)}d ${hr % 24}h`;
}

/** How long a live row has been on the board: now - createdAt. Empty for terminal rows / bad stamps. */
export function formatElapsed(h: BoardHandle, now: number): string {
  if (!isLive(h)) return '';
  const t = Date.parse(h.createdAt);
  if (Number.isNaN(t)) return '';
  return formatDuration(now - t);
}

/** Case-insensitive match on handle, title, model, kind, sessionId, spawnedBy. Empty query matches all. */
export function matchesQuery(h: BoardHandle, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return [h.handle, h.title, h.model, h.kind, h.sessionId, h.spawnedBy].some(
    (field) => field != null && field.toLowerCase().includes(q),
  );
}