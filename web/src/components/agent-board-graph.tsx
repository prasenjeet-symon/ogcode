// agent-board-graph.tsx — the Agents page board.
//
// One bordered cell per family (one connected component of the spawned_by
// edge), each family laid out as a radial tree inside its cell, and the cells
// shelf-packed into a board that pans and zooms like a map. Nothing moves on
// its own: positions are geometry from agent-board-layout, not physics, so a
// roster refresh that changed nothing redraws nothing and the same roster
// always draws the same board.
//
// The view follows the work. While some agent is running, the board frames the
// cells that hold the running agents — all of them, however far apart — so
// "what is happening now" is always on screen. A manual pan or zoom hands the
// view to the user; the frame button takes it back.
//
// Painting: one canvas, no per-element DOM. Pulses and flow particles run only
// while something is alive; a quiet board redraws on interaction alone.

import { Show, createEffect, createMemo, createSignal, onCleanup, onMount } from 'solid-js';
import { Portal } from 'solid-js/web';
import type { BoardHandle } from '../api/client';
import type { TreeRow } from '../lib/board-tree';
import {
  CELL_CHIP_INSET,
  NODE_R,
  layoutBoard,
  type BoardCell,
  type BoardLink,
  type BoardNode,
  type BoardRect,
} from '../lib/agent-board-layout';
import './agent-board-graph.css';

interface AgentBoardGraphProps {
  rows: TreeRow[];
  /** The session id of the viewer's own session — its node gets a "you" ring. */
  isMe: string | undefined;
  now: number;
  stopArmed: string | null;
  stopping: Set<string>;
  onOpen: (h: BoardHandle) => void;
  onStop: (handle: string) => void;
}

// Concrete status colours for the canvas: CSS variables can't be read by the
// 2D context, so 'VAR:--name' entries resolve off the host element once per
// theme flip. Fixed hues match statusColor() in board-graph.ts.
const STATUS_HUES: Record<string, string> = {
  running: 'VAR:--accent',
  idle: 'VAR:--text-tertiary',
  stale: '#fbbf24',
  done: '#34d399',
  stopped: '#f87171',
};
const FALLBACK_HUE = '#71717a';

interface Palette {
  base: string;
  elevated: string;
  border: string;
  accent: string;
  textSecondary: string;
  textTertiary: string;
  textMuted: string;
  mono: string;
}

const MIN_SCALE = 0.14;
const MAX_SCALE = 2.6;
// Framing the running cells may not zoom in past this: one small active family
// blown up to fill the viewport hides the board it sits on, and the point of the
// frame is to show WHERE the work is, not to magnify a single card.
const ACTIVE_MAX_SCALE = 1;
/** Room left around a framed rect, in screen pixels. */
const FRAME_PAD = 46;
/** Animation length of a framed move, in milliseconds. */
const FRAME_MS = 420;

function toRgb(hex: string): [number, number, number] | null {
  const m = hex.trim().match(/^#([0-9a-f]{6}|[0-9a-f]{3})$/i);
  if (!m) return null;
  let s = m[1];
  if (s.length === 3) s = s[0] + s[0] + s[1] + s[1] + s[2] + s[2];
  return [parseInt(s.slice(0, 2), 16), parseInt(s.slice(2, 4), 16), parseInt(s.slice(4, 6), 16)];
}

// hexMix is the canvas equivalent of color-mix(in srgb, a t%, b): t is the
// share of `a` in `b`.
function hexMix(a: string, b: string, t: number): string {
  const ca = toRgb(a);
  const cb = toRgb(b);
  if (!ca || !cb) return t > 0.5 ? a : b;
  const c = ca.map((v, i) => Math.round(v * t + cb[i] * (1 - t)));
  return `#${c.map((v) => v.toString(16).padStart(2, '0')).join('')}`;
}

// Alpha over an opaque hex, for edges, arrows and halos.
function hexAlpha(hex: string, a: number): string {
  const c = toRgb(hex);
  if (!c) return hex;
  return `rgba(${c[0]},${c[1]},${c[2]},${a})`;
}

function fmtElapsed(iso: string, now: number): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const s = Math.max(0, Math.floor((now - t) / 1000));
  if (s < 60) return '';
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

// A stable phase per handle, so pulses and particles are out of step rather
// than in unison.
function phaseOf(id: string): number {
  let n = 0;
  for (let i = 0; i < id.length; i++) n = (n * 31 + id.charCodeAt(i)) & 0xffff;
  return (n % 1000) / 1000;
}

// clampScale keeps the board inside a range where it is still readable: below
// MIN_SCALE the whole board is a speck, above MAX_SCALE a single cell fills
// the screen and panning loses its bearings.
function clampScale(k: number): number {
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, k));
}

export default function AgentBoardGraph(props: AgentBoardGraphProps) {
  let hostEl: HTMLDivElement | undefined;
  let canvasEl: HTMLCanvasElement | undefined;

  const [hovered, setHovered] = createSignal<string | null>(null);
  const [pointerIn, setPointerIn] = createSignal(false);
  const [size, setSize] = createSignal({ w: 0, h: 0 });
  const [tipPos, setTipPos] = createSignal({ x: 0, y: 0 });
  const [cursor, setCursor] = createSignal<'grab' | 'pointer' | 'grabbing'>('grab');

  // keyed <Show> needs when() to yield the node itself (not a boolean), so
  // fold pointerIn into the hover lookup here.
  const tipShown = () => {
    if (!pointerIn()) return null;
    const handle = hovered();
    if (!handle) return null;
    return nodesById().get(handle) ?? null;
  };

  const layout = createMemo(() => layoutBoard(props.rows));
  const nodesById = createMemo(() => {
    const map = new Map<string, BoardNode>();
    for (const n of layout().nodes) map.set(n.id, n);
    return map;
  });

  // parentOf and childrenOf light a hovered subtree. Declared before the lit
  // effect reads them; relationMaps is a hoisted function so the order holds.
  const parentOf = createMemo(() => relationMaps(props.rows).parentOf);
  const childrenOf = createMemo(() => relationMaps(props.rows).childrenOf);

  // The view: board coordinates to screen, as x = tx + world * k.
  const view = { x: 0, y: 0, k: 1 };
  let userMoved = false;
  let rafId = 0;
  let anim: {
    fx: number;
    fy: number;
    fk: number;
    tx: number;
    ty: number;
    tk: number;
    start: number;
  } | null = null;

  const anyRunning = () => props.rows.some((r) => r.h.status === 'running');

  // The subtree lit by hover: the hovered node, its ancestors, every
  // descendant. A plain mutable Set read by the canvas each frame — the
  // reactive cost is one rebuild per hover change, not per frame.
  let lit = new Set<string>();
  createEffect(() => {
    const handle = hovered();
    lit = new Set<string>();
    if (handle) {
      let cur: string | undefined = handle;
      while (cur) {
        lit.add(cur);
        cur = parentOf().get(cur);
      }
      const stack = [handle];
      const kids = childrenOf();
      while (stack.length) {
        const cur2 = stack.pop()!;
        lit.add(cur2);
        for (const c of kids.get(cur2) ?? []) {
          if (!lit.has(c)) stack.push(c);
        }
      }
    }
    draw();
  });

  function kick() {
    if (!rafId) rafId = requestAnimationFrame(frame);
  }

  function frame(ts: number) {
    rafId = 0;
    if (anim) {
      const a = anim;
      const p = Math.min(1, (ts - a.start) / FRAME_MS);
      // Ease-out cubic: fast enough to feel immediate, slow enough to follow.
      const e = 1 - Math.pow(1 - p, 3);
      view.x = a.fx + (a.tx - a.fx) * e;
      view.y = a.fy + (a.ty - a.fy) * e;
      view.k = a.fk + (a.tk - a.fk) * e;
      if (p >= 1) anim = null;
      draw();
    } else if (anyRunning()) {
      draw();
    }
    if (anim || anyRunning()) rafId = requestAnimationFrame(frame);
  }

  function animateView(to: { x: number; y: number; k: number }) {
    anim = {
      fx: view.x,
      fy: view.y,
      fk: view.k,
      tx: to.x,
      ty: to.y,
      tk: to.k,
      start: performance.now(),
    };
    kick();
  }

  // frameRect centres `rect` and picks the largest scale that still shows all
  // of it, so an auto-frame never cuts an active family off at the edge. maxK
  // caps the zoom-in for callers that want the rect in context rather than
  // filling the screen.
  function frameRect(rect: BoardRect, animate: boolean, maxK = MAX_SCALE) {
    const s = size();
    if (!s.w || !s.h || !rect.w || !rect.h) return;
    const k = clampScale(
      Math.min(maxK, s.w / (rect.w + FRAME_PAD * 2), s.h / (rect.h + FRAME_PAD * 2)),
    );
    // Centring alone would park a lone running cell mid-viewport and fill the
    // rest with the empty outside of the board. Clamped to the board, the frame
    // pans out only as far as the board actually reaches.
    const b = layout().bounds;
    const x = clampPan((s.w - rect.w * k) / 2 - rect.x * k, b.x, b.w, s.w, k);
    const y = clampPan((s.h - rect.h * k) / 2 - rect.y * k, b.y, b.h, s.h, k);
    if (animate) animateView({ x, y, k });
    else {
      view.x = x;
      view.y = y;
      view.k = k;
      draw();
    }
  }

  // Centre the board on an axis the viewport covers, and keep its edge on the
  // viewport edge on an axis it overflows. `rect` sits inside the board, so this
  // never pulls the framed rect out of view.
  function clampPan(v: number, bPos: number, bLen: number, sLen: number, k: number) {
    const len = bLen * k;
    if (!len) return v;
    if (len <= sLen) return (sLen - len) / 2 - bPos * k;
    return Math.min(-bPos * k, Math.max(sLen - (bPos + bLen) * k, v));
  }

  // Follow the work: reframe whenever the frame target changes — the running
  // set, the board geometry, or the viewport. A manual pan or zoom stops it.
  let lastFrameKey = '';
  createEffect(() => {
    const board = layout();
    const active = board.activeRect;
    const target = active ?? board.bounds;
    const s = size();
    if (!s.w || !target.w) return;
    const key = `${Math.round(target.x)},${Math.round(target.y)},${Math.round(target.w)},${Math.round(target.h)}|${s.w}x${s.h}`;
    if (key === lastFrameKey) return;
    const first = lastFrameKey === '';
    lastFrameKey = key;
    if (userMoved) {
      draw();
      return;
    }
    frameRect(target, !first, active ? ACTIVE_MAX_SCALE : MAX_SCALE);
  });

  // Pulse rings and flow particles want frames; a quiet board needs none.
  createEffect(() => {
    if (anyRunning()) kick();
  });

  // Theme colours, resolved off this element's computed style. The cache is
  // dropped on any data-theme flip on <html> (the pre-paint mechanism), so a
  // theme switch repaints in the new palette without a remount.
  let palCache: Palette | null = null;
  const palette = (): Palette => {
    if (palCache) return palCache;
    const cs = getComputedStyle(hostEl ?? document.documentElement);
    const read = (name: string, fallback: string) => cs.getPropertyValue(name).trim() || fallback;
    palCache = {
      base: read('--bg-base', '#09090b'),
      elevated: read('--bg-elevated', '#141418'),
      border: read('--border-subtle', '#1e1e23'),
      accent: read('--accent', '#ff5e1f'),
      textSecondary: read('--text-secondary', '#a1a1aa'),
      textTertiary: read('--text-tertiary', '#8b8b94'),
      textMuted: read('--text-muted', '#6b6b75'),
      mono: read('--font-mono', 'ui-monospace, SFMono-Regular, Menlo, monospace'),
    };
    return palCache;
  };
  const statusHue = (status: string): string => {
    const raw = STATUS_HUES[status] ?? FALLBACK_HUE;
    if (!raw.startsWith('VAR:')) return raw;
    const name = raw.slice(4) as string;
    const val = palette()[name === '--text-tertiary' ? 'textTertiary' : 'accent'];
    return val || FALLBACK_HUE;
  };

  onMount(() => {
    const el = hostEl;
    if (!el) return;
    const ro = new ResizeObserver(applySize);
    ro.observe(el);
    applySize();
    onCleanup(() => ro.disconnect());

    const mo = new MutationObserver(() => {
      palCache = null;
      draw();
    });
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class'] });
    onCleanup(() => mo.disconnect());

    const cv = canvasEl;
    if (cv) {
      cv.addEventListener('wheel', onWheel, { passive: false });
      cv.addEventListener('pointerdown', onPointerDown);
      cv.addEventListener('pointermove', onPointerMove);
      cv.addEventListener('pointerup', onPointerUp);
      cv.addEventListener('pointercancel', onPointerUp);
      cv.addEventListener('pointerleave', onPointerLeave);
      onCleanup(() => {
        cv.removeEventListener('wheel', onWheel);
        cv.removeEventListener('pointerdown', onPointerDown);
        cv.removeEventListener('pointermove', onPointerMove);
        cv.removeEventListener('pointerup', onPointerUp);
        cv.removeEventListener('pointercancel', onPointerUp);
        cv.removeEventListener('pointerleave', onPointerLeave);
      });
    }

    onCleanup(() => {
      if (rafId) cancelAnimationFrame(rafId);
      rafId = 0;
    });
  });

  function applySize() {
    const el = hostEl;
    const cv = canvasEl;
    if (!el || !cv) return;
    const w = el.clientWidth || 600;
    const h = el.clientHeight || 420;
    const dpr = window.devicePixelRatio || 1;
    cv.width = Math.max(1, Math.round(w * dpr));
    cv.height = Math.max(1, Math.round(h * dpr));
    cv.style.width = `${w}px`;
    cv.style.height = `${h}px`;
    setSize({ w, h });
    draw();
  }

  // --- painting ------------------------------------------------------------

  function draw() {
    const cv = canvasEl;
    const ctx = cv?.getContext('2d');
    if (!cv || !ctx) return;
    const { w, h } = size();
    if (!w || !h) return;
    const dpr = window.devicePixelRatio || 1;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, h);

    const pal = palette();
    drawBackdrop(ctx, w, h, pal);

    const k = view.k;
    const board = layout();
    const byId = nodesById();
    // The visible window in board coordinates, padded so a cell or a curve
    // that starts just outside the screen still paints.
    const pad = 48 / k;
    const left = -view.x / k - pad;
    const top = -view.y / k - pad;
    const right = left + w / k + pad * 2;
    const bottom = top + h / k + pad * 2;
    const visible = (cell: BoardCell) =>
      cell.x < right && cell.x + cell.w > left && cell.y < bottom && cell.y + cell.h > top;

    ctx.save();
    ctx.translate(view.x, view.y);
    ctx.scale(k, k);
    // Frames first, so a family's nodes sit on top of their own border and no
    // edge is clipped by a neighbouring cell.
    for (const cell of board.cells) {
      if (visible(cell)) drawCell(ctx, cell, pal);
    }
    for (const cell of board.cells) {
      if (!visible(cell)) continue;
      for (const link of cell.links) drawLink(ctx, link, byId, pal);
      for (const node of cell.nodes) drawNode(ctx, node, pal);
    }
    ctx.restore();

    // Captions in screen space, so they stay legible at any zoom. Below the
    // width threshold a cell has no room for a name and the border alone reads.
    for (const cell of board.cells) {
      const screenW = cell.w * k;
      if (screenW < 84) continue;
      drawCaption(ctx, cell, pal, screenW, w, h);
    }
  }

  function drawBackdrop(ctx: CanvasRenderingContext2D, w: number, h: number, pal: Palette) {
    // A faint dot grid anchored in board space, so a pan reads as moving over a
    // board rather than over an empty void. The spacing doubles as the view
    // zooms out, so the dots never turn into a wash.
    const k = view.k;
    let step = 32;
    while (step * k < 22) step *= 2;
    const left = -view.x / k;
    const top = -view.y / k;
    const right = left + w / k;
    const bottom = top + h / k;
    const x0 = Math.ceil(left / step) * step;
    const y0 = Math.ceil(top / step) * step;
    ctx.fillStyle = hexAlpha(pal.border, 0.9);
    for (let bx = x0; bx < right; bx += step) {
      const sx = view.x + bx * k;
      for (let by = y0; by < bottom; by += step) {
        ctx.fillRect(sx, view.y + by * k, 1.3, 1.3);
      }
    }
  }

  function drawCell(ctx: CanvasRenderingContext2D, cell: BoardCell, pal: Palette) {
    const inv = 1 / view.k;
    const running = cell.running > 0;
    // Square corners: the cell is a grouping frame, not a card.
    ctx.beginPath();
    ctx.rect(cell.x, cell.y, cell.w, cell.h);
    ctx.fillStyle = running ? hexAlpha(pal.accent, 0.05) : hexAlpha(pal.elevated, 0.55);
    ctx.fill();
    ctx.lineWidth = (running ? 1.4 : 1) * inv;
    ctx.strokeStyle = running ? hexAlpha(pal.accent, 0.42) : pal.border;
    ctx.stroke();
  }

  function drawLink(
    ctx: CanvasRenderingContext2D,
    link: BoardLink,
    byId: Map<string, BoardNode>,
    pal: Palette,
  ) {
    const from = byId.get(link.source);
    const to = byId.get(link.target);
    if (!from || !to) return;
    const litLink = lit.size > 0 && lit.has(link.target);
    const dim = lit.size > 0 && !litLink;
    const hue = statusHue(to.h.status);
    const inv = 1 / view.k;

    // A gentle bow, so a parent with many children reads as a fan of separate
    // strokes instead of one dark wedge.
    const dx = to.x - from.x;
    const dy = to.y - from.y;
    const len = Math.hypot(dx, dy) || 1;
    const bow = len * 0.16;
    const cx = (from.x + to.x) / 2 - (dy / len) * bow;
    const cy = (from.y + to.y) / 2 + (dx / len) * bow;

    ctx.beginPath();
    ctx.moveTo(from.x, from.y);
    ctx.quadraticCurveTo(cx, cy, to.x, to.y);
    ctx.lineWidth = (dim ? 1 : litLink ? 1.6 : 1) * inv;
    ctx.strokeStyle = dim ? hexAlpha(pal.border, 0.5) : hexAlpha(hue, 0.42);
    ctx.stroke();

    const at = (t: number) => {
      const it = 1 - t;
      return {
        x: it * it * from.x + 2 * it * t * cx + t * t * to.x,
        y: it * it * from.y + 2 * it * t * cy + t * t * to.y,
      };
    };

    // Arrowhead near the child, pointing along the curve.
    const head = at(0.86);
    const ahead = at(0.9);
    const ang = Math.atan2(ahead.y - head.y, ahead.x - head.x);
    const a = 4.2 * inv;
    ctx.beginPath();
    ctx.moveTo(head.x + Math.cos(ang) * a * 0.6, head.y + Math.sin(ang) * a * 0.6);
    ctx.lineTo(head.x + Math.cos(ang + 2.5) * a, head.y + Math.sin(ang + 2.5) * a);
    ctx.lineTo(head.x + Math.cos(ang - 2.5) * a, head.y + Math.sin(ang - 2.5) * a);
    ctx.closePath();
    ctx.fillStyle = dim ? hexAlpha(pal.border, 0.6) : hexAlpha(hue, 0.8);
    ctx.fill();

    // Flow particles: a still-working child has work moving down its edge.
    if (!link.running || dim) return;
    const phase = phaseOf(link.target);
    const travel = performance.now() * 0.00035;
    for (let i = 0; i < 3; i++) {
      const p = at((phase + i / 3 + travel) % 1);
      ctx.beginPath();
      ctx.arc(p.x, p.y, 1.7 * inv, 0, 2 * Math.PI);
      ctx.fillStyle = hexAlpha(hue, 0.95);
      ctx.fill();
    }
  }

  function drawNode(ctx: CanvasRenderingContext2D, node: BoardNode, pal: Palette) {
    const inv = 1 / view.k;
    // Never a sub-pixel speck: a zoomed-out board still shows where its agents
    // are, the way a map keeps its cities.
    const r = Math.max(NODE_R, 3.4 * inv);
    const dimmed = lit.size > 0 && !lit.has(node.id);
    const hue = statusHue(node.h.status);
    const isMe = props.isMe !== undefined && node.h.sessionId === props.isMe;

    if (dimmed) ctx.globalAlpha = 0.22;

    ctx.beginPath();
    ctx.arc(node.x, node.y, r, 0, 2 * Math.PI);
    ctx.fillStyle = hue;
    ctx.fill();
    // A subtle darker rim is the only shaping beyond the fill.
    ctx.strokeStyle = hexMix(hue, pal.base, 0.55);
    ctx.lineWidth = inv;
    ctx.stroke();

    // The viewer's own session: a dashed selection ring (colour, not text).
    if (isMe) {
      ctx.setLineDash([2.5 * inv, 2.2 * inv]);
      ctx.beginPath();
      ctx.arc(node.x, node.y, r + 3.4 * inv, 0, 2 * Math.PI);
      ctx.strokeStyle = hexAlpha(pal.accent, 0.9);
      ctx.lineWidth = 1.1 * inv;
      ctx.stroke();
      ctx.setLineDash([]);
    }

    // Live agents breathe: a soft ring pulsing outside the disc.
    if (node.h.status === 'running') {
      const t = performance.now() / 700 + phaseOf(node.id) * Math.PI * 2;
      ctx.beginPath();
      ctx.arc(node.x, node.y, r + (2.6 + Math.sin(t) * 1.6) * inv, 0, 2 * Math.PI);
      ctx.fillStyle = hexAlpha(hue, 0.16 + 0.09 * Math.sin(t));
      ctx.fill();
    }

    ctx.globalAlpha = 1;
  }

  function drawCaption(
    ctx: CanvasRenderingContext2D,
    cell: BoardCell,
    pal: Palette,
    screenW: number,
    w: number,
    h: number,
  ) {
    // A family carries its caption in a band along the top edge; a lone agent's
    // chip carries it beside the dot, vertically centred.
    const chip = cell.count === 1;
    const left = view.x + cell.x * view.k;
    const dotX = left + (chip ? CELL_CHIP_INSET : 13.5);
    const baseline = chip
      ? view.y + (cell.y + cell.h / 2) * view.k
      : view.y + cell.y * view.k + 14;
    if (left > w || left + screenW < 0 || baseline < -16 || baseline > h + 16) return;
    const running = cell.running > 0;

    ctx.beginPath();
    ctx.arc(dotX, baseline, 2.4, 0, 2 * Math.PI);
    ctx.fillStyle = running ? pal.accent : cell.live > 0 ? pal.textTertiary : pal.textMuted;
    ctx.fill();

    ctx.font = `600 10px ${pal.mono}`;
    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';
    const label = cell.count > 1 ? `${cell.id} · ${cell.count}` : cell.id;
    const textX = dotX + (chip ? 9 : 8);
    const maxW = screenW - (textX - left) - 8;
    let text = label;
    if (ctx.measureText(text).width > maxW) {
      while (text.length > 1 && ctx.measureText(`${text}…`).width > maxW) text = text.slice(0, -1);
      text = `${text}…`;
    }
    ctx.fillStyle = running
      ? pal.textSecondary
      : cell.count > 1
        ? pal.textTertiary
        : pal.textMuted;
    ctx.fillText(text, textX, baseline);
  }

  // --- interaction ---------------------------------------------------------

  function zoomAt(clientX: number, clientY: number, factor: number) {
    const rect = hostEl?.getBoundingClientRect();
    if (!rect) return;
    const k = clampScale(view.k * factor);
    if (k === view.k) return;
    const sx = clientX - rect.left;
    const sy = clientY - rect.top;
    const wx = (sx - view.x) / view.k;
    const wy = (sy - view.y) / view.k;
    view.x = sx - wx * k;
    view.y = sy - wy * k;
    view.k = k;
    draw();
  }

  function onWheel(e: WheelEvent) {
    e.preventDefault();
    anim = null;
    userMoved = true;
    // deltaMode: 1 = lines, 2 = pages. Normalise both to pixels.
    const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? size().h : 1;
    if (e.ctrlKey || e.metaKey) {
      // A pinch gesture arrives as ctrl+wheel on every desktop browser.
      zoomAt(e.clientX, e.clientY, Math.exp(-e.deltaY * unit * 0.0018));
    } else {
      view.x -= e.deltaX * unit;
      view.y -= e.deltaY * unit;
      draw();
    }
  }

  const pointers = new Map<number, { x: number; y: number }>();
  let drag: { id: number; sx: number; sy: number; vx: number; vy: number; moved: boolean } | null = null;
  let pinch: { distance: number; k: number } | null = null;

  function pointerDistance() {
    const [a, b] = [...pointers.values()];
    return Math.hypot(a.x - b.x, a.y - b.y) || 1;
  }

  function pointerMid() {
    const [a, b] = [...pointers.values()];
    return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
  }

  function hitTest(clientX: number, clientY: number): BoardNode | null {
    const rect = hostEl?.getBoundingClientRect();
    if (!rect) return null;
    const wx = (clientX - rect.left - view.x) / view.k;
    const wy = (clientY - rect.top - view.y) / view.k;
    // Generous target: a node drawn at 3.4px is still clickable.
    const reach = Math.max(NODE_R + 4, 9 / view.k);
    const r2 = reach * reach;
    let best: BoardNode | null = null;
    let bestD = Infinity;
    for (const n of layout().nodes) {
      const dx = n.x - wx;
      const dy = n.y - wy;
      const d = dx * dx + dy * dy;
      if (d <= r2 && d < bestD) {
        bestD = d;
        best = n;
      }
    }
    if (best) return best;
    // A lone agent is a chip card: the card itself is the target, so pressing
    // its caption opens the session just like pressing the dot does.
    for (const cell of layout().cells) {
      if (cell.count !== 1 || !cell.nodes.length) continue;
      if (wx < cell.x || wx > cell.x + cell.w) continue;
      if (wy < cell.y || wy > cell.y + cell.h) continue;
      return cell.nodes[0];
    }
    return null;
  }

  function updateHover(e: PointerEvent) {
    const hit = hitTest(e.clientX, e.clientY);
    setCursor(hit ? 'pointer' : 'grab');
    const id = hit?.id ?? null;
    if (id !== hovered()) setHovered(id);
  }

  function onPointerDown(e: PointerEvent) {
    if (e.pointerType === 'mouse' && e.button !== 0) return;
    canvasEl?.setPointerCapture(e.pointerId);
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    anim = null;
    if (pointers.size === 1) {
      drag = { id: e.pointerId, sx: e.clientX, sy: e.clientY, vx: view.x, vy: view.y, moved: false };
    } else {
      drag = null;
      pinch = { distance: pointerDistance(), k: view.k };
    }
  }

  function onPointerMove(e: PointerEvent) {
    if (!pointers.has(e.pointerId)) {
      updateHover(e);
      return;
    }
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (pinch && pointers.size >= 2) {
      const k = clampScale(pinch.k * (pointerDistance() / pinch.distance));
      const mid = pointerMid();
      if (k !== view.k) {
        zoomAt(mid.x, mid.y, k / view.k);
        userMoved = true;
      }
      return;
    }
    if (!drag || drag.id !== e.pointerId) return;
    const dx = e.clientX - drag.sx;
    const dy = e.clientY - drag.sy;
    if (!drag.moved && Math.hypot(dx, dy) > 3) {
      drag.moved = true;
      userMoved = true;
      setCursor('grabbing');
      setHovered(null);
    }
    if (drag.moved) {
      view.x = drag.vx + dx;
      view.y = drag.vy + dy;
      draw();
    }
  }

  function onPointerUp(e: PointerEvent) {
    const finished = drag && drag.id === e.pointerId ? drag : null;
    pointers.delete(e.pointerId);
    if (pointers.size < 2) pinch = null;
    if (finished) {
      drag = null;
      // A press that never moved is a click: a node opens its session.
      if (!finished.moved) {
        const hit = hitTest(e.clientX, e.clientY);
        if (hit?.h.sessionId) props.onOpen(hit.h);
      }
    }
    setCursor(hitTest(e.clientX, e.clientY) ? 'pointer' : 'grab');
  }

  function onPointerLeave() {
    pointers.clear();
    drag = null;
    pinch = null;
    setCursor('grab');
    setPointerIn(false);
    setHovered(null);
  }

  const openable = (h: BoardHandle) => !!h.sessionId;
  const stoppable = (h: BoardHandle) =>
    (h.kind === 'teammate' || h.kind === 'task') && !!h.sessionId && h.status === 'running';

  // Keep the hover tooltip anchored to its node while the board moves.
  function positionTip() {
    const handle = hovered();
    const node = handle ? nodesById().get(handle) : undefined;
    const rect = hostEl?.getBoundingClientRect();
    if (!node || !rect) return;
    const sx = rect.left + view.x + node.x * view.k;
    const sy = rect.top + view.y + node.y * view.k;
    setTipPos({
      x: Math.max(8, Math.min(window.innerWidth - 244, sx + 12)),
      y: Math.max(8, Math.min(window.innerHeight - 52, sy - 18)),
    });
  }
  createEffect(() => {
    if (!hovered() || !pointerIn()) return;
    positionTip();
    const id = setInterval(positionTip, 120);
    onCleanup(() => clearInterval(id));
  });

  function frameActive() {
    userMoved = false;
    const board = layout();
    frameRect(
      board.activeRect ?? board.bounds,
      true,
      board.activeRect ? ACTIVE_MAX_SCALE : MAX_SCALE,
    );
  }

  function zoomStep(factor: number) {
    userMoved = true;
    const s = size();
    const k = clampScale(view.k * factor);
    const wx = (s.w / 2 - view.x) / view.k;
    const wy = (s.h / 2 - view.y) / view.k;
    animateView({ x: s.w / 2 - wx * k, y: s.h / 2 - wy * k, k });
  }

  return (
    <>
      <div
        ref={hostEl}
        class="abg-host"
        onMouseEnter={() => setPointerIn(true)}
        onMouseLeave={() => {
          setPointerIn(false);
          setHovered(null);
        }}
      >
        <canvas ref={canvasEl} class="abg-canvas" style={{ cursor: cursor() }} aria-hidden="true" />
        {/* Viewport controls: frame the work on screen, and zoom. */}
        <div class="abg-float abg-ctl" role="group" aria-label="Board view controls">
          <button
            type="button"
            title={anyRunning() ? 'Frame the running agents' : 'Frame the whole board'}
            aria-label="Frame the board"
            onClick={frameActive}
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" />
            </svg>
          </button>
          <button type="button" title="Zoom in" aria-label="Zoom in" onClick={() => zoomStep(1.25)}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <path d="M12 5v14M5 12h14" />
            </svg>
          </button>
          <button type="button" title="Zoom out" aria-label="Zoom out" onClick={() => zoomStep(0.8)}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <path d="M5 12h14" />
            </svg>
          </button>
        </div>
      </div>

      <Portal>
        <Show when={tipShown()}>
          {(t) => (
            <div
              class="abg-tip"
              style={{
                left: `${tipPos().x}px`,
                top: `${tipPos().y}px`,
              }}
            >
              <div class="abg-tip-head">
                <span class="w-2 h-2 rounded-full shrink-0" style={{ background: statusHue(t().h.status) }} />
                <span class="abg-tip-handle">{t().id}</span>
                <span class="abg-tip-kind">{t().h.kind ?? 'agent'}</span>
              </div>
              <Show when={t().h.title}>
                <div class="abg-tip-title">{t().h.title}</div>
              </Show>
              <div class="abg-tip-meta">
                {[t().h.model, fmtElapsed(t().h.createdAt, props.now)].filter(Boolean).join(' · ')}
              </div>
              <div class="abg-tip-actions">
                <Show when={openable(t().h)}>
                  <button type="button" class="abg-btn" onClick={() => props.onOpen(t()!.h)}>
                    Open session
                  </button>
                </Show>
                <Show when={stoppable(t().h)}>
                  <button
                    type="button"
                    class="abg-btn abg-btn-stop"
                    classList={{ 'abg-armed': props.stopArmed === t().id }}
                    disabled={props.stopping.has(t().id)}
                    onClick={() => props.onStop(t()!.id)}
                  >
                    {props.stopping.has(t().id)
                      ? 'Stopping…'
                      : props.stopArmed === t().id
                        ? 'Confirm stop'
                        : 'Stop'}
                  </button>
                </Show>
              </div>
            </div>
          )}
        </Show>
      </Portal>
    </>
  );
}

// relationMaps builds the parent/child adjacency for one rows array. Pure:
// rows in, two maps out.
function relationMaps(rows: TreeRow[]): {
  parentOf: Map<string, string>;
  childrenOf: Map<string, Set<string>>;
} {
  const parent = new Map<string, string>();
  const kids = new Map<string, Set<string>>();
  for (const row of rows) {
    const p = row.h.spawnedBy;
    if (!p) continue;
    parent.set(row.h.handle, p);
    let set = kids.get(p);
    if (!set) kids.set(p, (set = new Set()));
    set.add(row.h.handle);
  }
  return { parentOf: parent, childrenOf: kids };
}
