import type { BoardHandle } from '../api/client';
import type { TreeRow } from './board-tree';

// Board graph: the geometry behind the Agents page graph view. Same pure-module
// rules as board-tree.ts — no SolidJS, no DOM: depth-first rows in, positions
// out, so the component memoises over a roster snapshot and the render stays
// declarative. The layout is deterministic — an approximation of Reingold–
// Tilford: leaves are laid left-to-right, a parent is centred over its
// children, and every subtree reserves its full width so siblings never
// overlap. Top-level families pack left-to-right and wrap onto a new shelf
// when one row of families runs past SHELF_W, so a wide roster never forces
// extreme zoom-out; between refreshes nothing moves unless the roster did.

// Shared geometry. The card is sized for two lines: handle/title, then kind,
// status and elapsed. Mirrored in agent-graph.css (.ag-card).
export const NODE_W = 200;
export const NODE_H = 60;
const V_GAP = 68; // vertical distance between levels — the edge channel lives here
const H_GAP = 22; // horizontal gap between sibling subtrees
const PAD = 20; // canvas inset on every side
const SHELF_W = 2400; // width at which a shelf of families wraps to a next row
const SHELF_GAP = 48; // extra vertical room between wrapped shelves

/** The status colour, as a value usable in inline SVG attributes and styles. */
export function statusColor(status: string): string {
  switch (status) {
    case 'running':
      return 'var(--accent)';
    case 'idle':
      return 'var(--text-tertiary)';
    case 'stale':
      return '#fbbf24';
    case 'done':
      return '#34d399';
    case 'stopped':
      return '#f87171';
    default:
      return '#71717a';
  }
}

export interface GraphNode {
  h: BoardHandle;
  depth: number;
  /** Top-left corner of the card, in canvas coordinates. */
  x: number;
  y: number;
  /** True when the node visibly hangs children or hides a collapsed subtree. */
  hasChildren: boolean;
}

export interface GraphEdge {
  id: string;
  /** The parent's handle — hover lighting walks ancestor chains with it. */
  parent: string;
  /** SVG path from the parent's bottom centre to the child's top centre. */
  path: string;
  /** The child's status — an edge is coloured by where it leads. */
  color: string;
  running: boolean;
  child: string;
}

/** One root family: the tinted region grouping a root and every descendant. */
export interface GraphCluster {
  x: number;
  y: number;
  w: number;
  h: number;
  color: string;
  label: string;
  total: number;
}

export interface GraphLayout {
  width: number;
  height: number;
  nodes: GraphNode[];
  edges: GraphEdge[];
  clusters: GraphCluster[];
}

function edgePath(parent: { x: number; y: number }, child: { x: number; y: number }): string {
  // x/y are top-left corners; the edge runs centre-bottom to centre-top.
  const x1 = parent.x + NODE_W / 2;
  const y1 = parent.y + NODE_H;
  const x2 = child.x + NODE_W / 2;
  const y2 = child.y;
  const mid = (y1 + y2) / 2;
  return `M ${x1} ${y1} C ${x1} ${mid}, ${x2} ${mid}, ${x2} ${y2}`;
}

/**
 * layoutGraph positions one node per row. Rows arrive depth-first with parents
 * before children (buildTree's order, with collapsed and hidden-finished rows
 * already dropped), so each top-level subtree is a contiguous slice of the
 * array and can be laid out in isolation before the shelf packer moves it.
 */
export function layoutGraph(rows: TreeRow[], collapsed: Set<string>): GraphLayout {
  interface Sub {
    idx: number;
    left: number;
    right: number;
    center: number;
    end: number;
  }

  // One top-level subtree, laid out with its own cursor. The returned
  // coordinates are shelf-local (PAD-based); the packer below shifts the
  // whole family into place and paths are drawn after that, so no edge is
  // ever computed against pre-shelf coordinates.
  function layOutFamily(start: number, end: number): {
    nodes: GraphNode[];
    edges: GraphEdge[];
    width: number;
    height: number;
  } {
    const nodes: GraphNode[] = [];
    const edges: GraphEdge[] = [];
    let cursor = PAD;

    function subtree(start: number): Sub {
      const row = rows[start];
      const depth = row.depth;
      const idx = nodes.length;
      const y = depth * (NODE_H + V_GAP);
      const after = start + 1;
      const leafLike =
        collapsed.has(row.h.handle) || after >= end || rows[after].depth <= depth;

      if (leafLike) {
        const left = cursor;
        cursor += NODE_W + H_GAP;
        nodes.push({ h: row.h, depth, x: left, y, hasChildren: collapsed.has(row.h.handle) });
        return { idx, left, right: left + NODE_W, center: left + NODE_W / 2, end: after };
      }

      // x is fixed once the children's extent is known.
      nodes.push({ h: row.h, depth, x: 0, y, hasChildren: true });
      let first: Sub | undefined;
      let last: Sub | undefined;
      let stop = after;
      const kids: number[] = [];
      while (stop < end && rows[stop].depth === depth + 1) {
        const sub = subtree(stop);
        stop = sub.end;
        kids.push(sub.idx);
        if (!first) first = sub;
        last = sub;
      }
      const center = first && last ? (first.center + last.center) / 2 : cursor - H_GAP / 2;
      const left = center - NODE_W / 2;
      nodes[idx].x = left;
      const minX = Math.min(left, first!.left);
      const maxX = Math.max(left + NODE_W, last!.right);
      for (const childIdx of kids) {
        const child = nodes[childIdx];
        edges.push({
          id: `${row.h.handle}->${child.h.handle}`,
          parent: row.h.handle,
          path: '',
          color: statusColor(child.h.status),
          running: child.h.status === 'running',
          child: child.h.handle,
        });
      }
      return { idx, left: minX, right: maxX, center, end: stop };
    }

    let stop = start;
    while (stop < end && rows[stop].depth === 0) {
      stop = subtree(stop).end;
    }
    // Anything the loop could not reach (a malformed, cyclic slice) is still
    // drawn rather than lost.
    for (let i = stop; i < end; i++) {
      if (nodes.every((n) => n.h.handle !== rows[i].h.handle)) subtree(i);
    }
    const width = nodes.length
      ? Math.max(...nodes.map((n) => n.x + NODE_W)) + PAD
      : PAD;
    const height = nodes.length
      ? Math.max(...nodes.map((n) => n.y + NODE_H)) + PAD
      : PAD;
    return { nodes, edges, width, height };
  }

  // Top-level slices: each starts at a depth-0 row and runs to the next one;
  // DFS order makes that exactly one subtree.
  const slices: Array<[number, number]> = [];
  let stop = 0;
  while (stop < rows.length) {
    if (rows[stop].depth === 0) {
      let end = stop + 1;
      while (end < rows.length && rows[end].depth > 0) end++;
      slices.push([stop, end]);
      stop = end;
    } else {
      // No depth-0 ancestor (a malformed roster): its own slice, still drawn.
      slices.push([stop, stop + 1]);
      stop++;
    }
  }

  // Shelf packer: families left→right, wrapping to the next shelf when the
  // cursor would run past SHELF_W.
  const nodes: GraphNode[] = [];
  const edges: GraphEdge[] = [];
  const clusters: GraphCluster[] = [];
  let packX = PAD;
  let packY = PAD;
  let shelfH = 0;
  for (const [s, e] of slices) {
    const fam = layOutFamily(s, e);
    const w = Math.max(fam.width, PAD);
    const h = Math.max(fam.height, PAD);
    if (packX > PAD && packX + (w - PAD) > SHELF_W) {
      packX = PAD;
      packY += shelfH + SHELF_GAP;
      shelfH = 0;
    }
    const dx = packX - PAD;
    const dy = packY - PAD;
    for (const n of fam.nodes) {
      n.x += dx;
      n.y += dy;
      nodes.push(n);
    }
    edges.push(...fam.edges);
    packX += w + H_GAP;
    shelfH = Math.max(shelfH, h - PAD);
  }

  if (nodes.length === 0) return { width: 0, height: 0, nodes, edges, clusters: [] };

  // Every node is placed now; edge paths can be final.
  const placed = new Map(nodes.map((n) => [n.h.handle, n]));
  for (const e of edges) {
    const p = placed.get(e.parent);
    const c = placed.get(e.child);
    if (p && c) e.path = edgePath(p, c);
  }

  // One cluster per root: every depth-0 node opens a region that runs until
  // the next depth-0 node, the DFS slice order above guaranteeing that is
  // exactly its family's own nodes.
  let groupStart = 0;
  const flush = (from: number, to: number) => {
    const group = nodes.slice(from, to);
    if (group.length === 0) return;
    const minX = Math.min(...group.map((n) => n.x));
    const maxX = Math.max(...group.map((n) => n.x + NODE_W));
    const minY = Math.min(...group.map((n) => n.y));
    const maxY = Math.max(...group.map((n) => n.y + NODE_H));
    const top = Math.max(0, minY - 30);
    // ~6px per character at font-size 10 monospace; keep room for the ·-count tail.
    const maxChars = Math.max(8, Math.min(28, Math.floor((maxX - minX - 34) / 6)));
    const rawLabel = group[0].h.title || group[0].h.handle;
    const label = rawLabel.length > maxChars ? rawLabel.slice(0, maxChars - 1).trimEnd() + '…' : rawLabel;
    clusters.push({
      x: Math.max(0, minX - 12),
      y: top,
      w: maxX - minX + 24,
      h: maxY - top + 14,
      color: statusColor(group[0].h.status),
      label,
      total: group.length,
    });
  };
  for (let i = 1; i <= nodes.length; i++) {
    if (i === nodes.length || nodes[i].depth === 0) {
      flush(groupStart, i);
      groupStart = i;
    }
  }

  const width = Math.max(...nodes.map((n) => n.x + NODE_W)) + PAD;
  const height = Math.max(...nodes.map((n) => n.y + NODE_H)) + PAD;
  return { width, height, nodes, edges, clusters };
}