import type { BoardHandle } from '../api/client';
import { isLive, type TreeRow } from './board-tree';

// Board layout: the geometry of the Agents page.
//
// The roster is a forest — one tree per connected component of the spawned_by
// edge. A force simulation smears that forest into a single blob: families
// drift into each other, the eye cannot tell which dots belong together, and
// the same roster draws differently every load. This module gives every
// component its own bordered cell, lays its tree out as a radial fan inside
// that cell, and shelf-packs the cells into one board.
//
// Pure and deterministic: rows in, geometry out. No DOM, no randomness, no
// physics — so a roster poll that changed nothing redraws nothing and moves
// nothing.

/** Node radius, in board units. Uniform: status is colour, never size. */
export const NODE_R = 6;

// Cell geometry, in board units.
const CELL_PAD = 14; // inner padding around a family's nodes
const CELL_CAPTION = 22; // caption band along the top edge
const CELL_GAP = 14; // gap between two cells
const CELL_MIN_W = 116; // a family too narrow to fill its caption band
const CELL_MIN_H = 64;

// A lone agent has no tree to draw, so it gets a chip rather than a card: one
// dot with its handle beside it. CELL_CHIP_INSET is the dot's centre, measured
// from the chip's left edge — the renderer draws its caption from it too.
export const CELL_CHIP_INSET = 13;
const CELL_CHIP_W = 132;
const CELL_CHIP_H = 38;

// Radial tree: distance between two depths, and the least arc two members of
// one ring may share. A ring with many members pushes the next ring outward
// rather than letting its dots touch.
const RING_GAP = 34;
const RING_MIN_ARC = 24;

// Soft board width the shelf packer aims for. It is only a target: the board
// pans, so a family larger than this still gets its own row.
const BOARD_MIN_W = 1100;
const BOARD_MAX_W = 2100;

export interface BoardNode {
  /** The agent's handle — also the node's identity across refreshes. */
  id: string;
  h: BoardHandle;
  /** Distance from the family's root, in edges. */
  depth: number;
  /** Board coordinates, absolute (not cell-relative). */
  x: number;
  y: number;
}

export interface BoardLink {
  /** The spawner's handle. */
  source: string;
  /** The spawned agent's handle. */
  target: string;
  /** The child is running: the edge carries flow particles. */
  running: boolean;
}

/** One family — one connected component — inside its own bordered box. */
export interface BoardCell {
  /** The root handle of the family this cell holds. */
  id: string;
  count: number;
  /** Members whose status is running. */
  running: number;
  /** Members that have not finished (running or idle). */
  live: number;
  /** The deepest member, in edges below the root. */
  depth: number;
  x: number;
  y: number;
  w: number;
  h: number;
  nodes: BoardNode[];
  links: BoardLink[];
}

export interface BoardRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface BoardLayout {
  cells: BoardCell[];
  nodes: BoardNode[];
  links: BoardLink[];
  /** The whole board, origin at 0,0. */
  bounds: BoardRect;
  /** The cells holding at least one running agent, unioned. Null when none runs. */
  activeRect: BoardRect | null;
}

interface PendingCell {
  cell: BoardCell;
  nodes: BoardNode[];
  links: BoardLink[];
}

function addTo(map: Map<string, string[]>, key: string, value: string) {
  const list = map.get(key);
  if (list) list.push(value);
  else map.set(key, [value]);
}

function unionRect(acc: BoardRect | null, r: BoardRect): BoardRect {
  if (!acc) return { ...r };
  const x = Math.min(acc.x, r.x);
  const y = Math.min(acc.y, r.y);
  return {
    x,
    y,
    w: Math.max(acc.x + acc.w, r.x + r.w) - x,
    h: Math.max(acc.y + acc.h, r.y + r.h) - y,
  };
}

export function layoutBoard(rows: TreeRow[]): BoardLayout {
  const byHandle = new Map<string, BoardHandle>();
  for (const row of rows) byHandle.set(row.h.handle, row.h);

  // Only spawn edges whose parent is on the roster. A handle with no on-roster
  // spawner is a root: it either joined on its own or its parent's row is gone.
  const children = new Map<string, string[]>();
  const neighbours = new Map<string, string[]>();
  for (const row of rows) {
    const parent = row.h.spawnedBy;
    if (!parent || parent === row.h.handle || !byHandle.has(parent)) continue;
    addTo(children, parent, row.h.handle);
    addTo(neighbours, parent, row.h.handle);
    addTo(neighbours, row.h.handle, parent);
  }

  // Connected components, walked in roster order so the board is stable.
  const seen = new Set<string>();
  const families: { root: string; members: string[] }[] = [];
  for (const row of rows) {
    const start = row.h.handle;
    if (seen.has(start)) continue;
    seen.add(start);
    const members: string[] = [];
    const stack = [start];
    while (stack.length) {
      const cur = stack.pop()!;
      members.push(cur);
      for (const nb of neighbours.get(cur) ?? []) {
        if (seen.has(nb)) continue;
        seen.add(nb);
        stack.push(nb);
      }
    }
    // The component's root is its one member no on-roster row spawned. Rows
    // holding a cycle have none, so the walk's first member anchors it.
    const root =
      members.find((m) => {
        const parent = byHandle.get(m)?.spawnedBy;
        return !parent || parent === m || !byHandle.has(parent);
      }) ?? start;
    families.push({ root, members });
  }

  const pending: PendingCell[] = [];

  for (const family of families) {
    // Depth from the root. Every member is stamped at most once, so a
    // hand-edited cycle cannot hang the walk.
    const depth = new Map<string, number>([[family.root, 0]]);
    const order = [family.root];
    for (let i = 0; i < order.length; i++) {
      const cur = order[i];
      for (const kid of children.get(cur) ?? []) {
        if (depth.has(kid)) continue;
        depth.set(kid, (depth.get(cur) ?? 0) + 1);
        order.push(kid);
      }
    }

    // Leaf counts set the angular share of each subtree: a branch with more
    // leaves gets a wider wedge, so no fan of siblings is squeezed to a line.
    const leaves = new Map<string, number>();
    const countLeaves = (id: string): number => {
      const known = leaves.get(id);
      if (known !== undefined) return known;
      leaves.set(id, 1); // guards a cycle while the recursion is in flight
      let n = 0;
      const d = depth.get(id) ?? 0;
      for (const kid of children.get(id) ?? []) {
        if (depth.get(kid) === d + 1) n += countLeaves(kid);
      }
      const v = Math.max(1, n);
      leaves.set(id, v);
      return v;
    };

    const perDepth = new Map<number, number>();
    let maxDepth = 0;
    for (const d of depth.values()) {
      perDepth.set(d, (perDepth.get(d) ?? 0) + 1);
      if (d > maxDepth) maxDepth = d;
    }

    // Ring radii: each depth sits at least RING_GAP further out, and far enough
    // out that its own members are at least RING_MIN_ARC apart.
    const radius: number[] = [0];
    for (let d = 1; d <= maxDepth; d++) {
      const arc = ((perDepth.get(d) ?? 1) * RING_MIN_ARC) / (2 * Math.PI);
      radius[d] = radius[d - 1] + Math.max(RING_GAP, arc);
    }

    // Angles: the root owns the whole circle, each node splits its wedge
    // between its children by leaf count and sits at the middle of its own.
    const angle = new Map<string, number>();
    const place = (id: string, from: number, span: number) => {
      angle.set(id, from + span / 2);
      const d = depth.get(id) ?? 0;
      const kids = (children.get(id) ?? []).filter((k) => depth.get(k) === d + 1);
      if (!kids.length) return;
      const weights = kids.map((k) => leaves.get(k) ?? 1);
      const total = weights.reduce((a, b) => a + b, 0);
      let cur = from;
      kids.forEach((kid, i) => {
        const w = (span * weights[i]) / total;
        place(kid, cur, w);
        cur += w;
      });
    };
    // Start at twelve o'clock, so a fan reads as a tree rather than a wheel.
    place(family.root, -Math.PI / 2, Math.PI * 2);

    const nodes: BoardNode[] = [];
    for (const id of order) {
      const d = depth.get(id) ?? 0;
      const a = angle.get(id) ?? 0;
      nodes.push({
        id,
        h: byHandle.get(id)!,
        depth: d,
        x: Math.cos(a) * radius[d],
        y: Math.sin(a) * radius[d],
      });
    }

    // Cell box, then move the family into it: horizontally centred, vertically
    // centred in the area under the caption band.
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const n of nodes) {
      if (n.x < minX) minX = n.x;
      if (n.x > maxX) maxX = n.x;
      if (n.y < minY) minY = n.y;
      if (n.y > maxY) maxY = n.y;
    }
    const contentW = maxX - minX + NODE_R * 2;
    const contentH = maxY - minY + NODE_R * 2;
    const chip = nodes.length === 1;
    const w = chip ? CELL_CHIP_W : Math.max(CELL_MIN_W, contentW + CELL_PAD * 2);
    const h = chip ? CELL_CHIP_H : Math.max(CELL_MIN_H, contentH + CELL_PAD * 2 + CELL_CAPTION);
    const dx = chip ? CELL_CHIP_INSET - NODE_R - minX : (w - contentW) / 2 + NODE_R - minX;
    const dy = chip
      ? h / 2 - NODE_R - minY
      : CELL_CAPTION + (h - CELL_CAPTION - contentH) / 2 + NODE_R - minY;
    for (const n of nodes) {
      n.x += dx;
      n.y += dy;
    }

    const ids = new Set(nodes.map((n) => n.id));
    const links: BoardLink[] = [];
    for (const n of nodes) {
      const parent = n.h.spawnedBy;
      if (!parent || !ids.has(parent)) continue;
      links.push({ source: parent, target: n.id, running: n.h.status === 'running' });
    }

    pending.push({
      cell: {
        id: family.root,
        count: nodes.length,
        running: nodes.filter((n) => n.h.status === 'running').length,
        live: nodes.filter((n) => isLive(n.h)).length,
        depth: maxDepth,
        x: 0,
        y: 0,
        w,
        h,
        nodes,
        links,
      },
      nodes,
      links,
    });
  }

  // Shelf packing: the biggest family first, lone agents last, so the board
  // reads top-left to bottom-right by weight and never reshuffles when a
  // status flips (status is not part of the order).
  pending.sort((a, b) => b.cell.count - a.cell.count || a.cell.id.localeCompare(b.cell.id));
  const area = pending.reduce((sum, p) => sum + p.cell.w * p.cell.h, 0);
  const targetW = Math.min(BOARD_MAX_W, Math.max(BOARD_MIN_W, Math.sqrt(area * 1.8)));
  let x = 0;
  let y = 0;
  let rowH = 0;
  let width = 0;
  for (const p of pending) {
    if (x > 0 && x + p.cell.w > targetW) {
      x = 0;
      y += rowH + CELL_GAP;
      rowH = 0;
    }
    p.cell.x = x;
    p.cell.y = y;
    if (x + p.cell.w > width) width = x + p.cell.w;
    x += p.cell.w + CELL_GAP;
    if (p.cell.h > rowH) rowH = p.cell.h;
  }
  const height = pending.length ? y + rowH : 0;

  const cells: BoardCell[] = [];
  const nodes: BoardNode[] = [];
  const links: BoardLink[] = [];
  let active: BoardRect | null = null;
  for (const p of pending) {
    for (const n of p.nodes) {
      n.x += p.cell.x;
      n.y += p.cell.y;
      nodes.push(n);
    }
    cells.push(p.cell);
    links.push(...p.links);
    if (p.cell.running > 0) active = unionRect(active, p.cell);
  }

  return { cells, nodes, links, bounds: { x: 0, y: 0, w: width, h: height }, activeRect: active };
}
