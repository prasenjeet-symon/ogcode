import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount, untrack } from 'solid-js';
import type { JSX } from 'solid-js';
import type { BoardHandle } from '../api/client';
import {
  layoutGraph,
  statusColor,
  NODE_W,
  NODE_H,
  type GraphNode,
  type GraphEdge,
  type GraphCluster,
} from '../lib/board-graph';
import { formatElapsed, kindLabel, statusMeta, type TreeRow } from '../lib/board-tree';
import './agent-graph.css';

// The agent graph, drawn the way a real graph tool draws one: a deterministic
// layered-tree layout. Every agent is a card node, every spawned_by edge a
// bezier link coloured by where it leads, and each top-level family sits in a
// soft tinted cluster with its own label in the corner. Families pack
// left-to-right and wrap onto a new shelf, so the layout never wobbles —
// it moves only when the roster moves. Wheel zooms around the cursor,
// background drag pans, and the zoom pill fits the graph. Hovering a node
// lights its whole path — ancestors and descendants — and dims the rest;
// families of finished agents stay visible but faded. Chat sessions without
// children are not graph nodes: they list in the strip below the canvas.

interface AgentGraphProps {
  /** Depth-first rows, collapsed and hidden-finished already dropped. */
  rows: TreeRow[];
  counts: Map<string, { total: number; live: number }>;
  collapsed: Set<string>;
  isMe?: string;
  now: number;
  stopArmed: string | null;
  stopping: Set<string>;
  onOpen: (h: BoardHandle) => void;
  onStop: (handle: string) => void;
  onToggleCollapse: (handle: string) => void;
}

const ZOOM_MIN = 0.18;
const ZOOM_MAX = 2.5;

const LABEL_CHARS = 24;

function clip(s: string, n: number): string {
  return s.length > n ? s.slice(0, n - 1).trimEnd() + '…' : s;
}

// Small kind glyphs for the card's avatar tile.
function GlyphSvg(props: { children?: JSX.Element }) {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      {props.children}
    </svg>
  );
}

function KindGlyph(props: { kind: string }) {
  if (props.kind === 'task') {
    return (
      <GlyphSvg>
        <circle cx="11" cy="11" r="6" />
        <path d="m20 20-3.5-3.5" />
      </GlyphSvg>
    );
  }
  if (props.kind === 'session') {
    return (
      <GlyphSvg>
        <rect x="3" y="3" width="18" height="18" rx="2" />
        <path d="m7 11 2-2-2-2" />
        <path d="M11 13h4" />
      </GlyphSvg>
    );
  }
  // teammate: git-fork
  return (
    <GlyphSvg>
      <circle cx="12" cy="18" r="3" />
      <circle cx="6" cy="6" r="3" />
      <circle cx="18" cy="6" r="3" />
      <path d="M18 9v1a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V9" />
      <path d="M12 12v3" />
    </GlyphSvg>
  );
}

const TERMINAL = new Set(['done', 'stopped', 'stale']);

export default function AgentGraph(props: AgentGraphProps) {
  const [hoveredHandle, setHoveredHandle] = createSignal<string | null>(null);
  const [panning, setPanning] = createSignal(false);
  const [view, setView] = createSignal({ z: 1, x: 0, y: 0 });
  const [vp, setVp] = createSignal({ w: 0, h: 0 });

  let hostEl!: HTMLDivElement;
  // Set after a pan so the click that follows pointerup is swallowed.
  let suppressClickUntil = 0;

  // --- visible structure -----------------------------------------------------

  // Families only: rows that either hang under a parent or visibly own
  // children. A childless top-level chat session is not a graph node — it
  // lands in the standalone strip below the canvas.
  const familyRows = createMemo(() =>
    props.rows.filter((r) => r.depth > 0 || (props.counts.get(r.h.handle)?.total ?? 0) > 0),
  );

  const layout = createMemo(() => layoutGraph(familyRows(), props.collapsed));

  const standalone = createMemo(() =>
    props.rows.filter((r) => r.depth === 0 && (props.counts.get(r.h.handle)?.total ?? 0) === 0),
  );

  const rowByHandle = createMemo(() => new Map(props.rows.map((r) => [r.h.handle, r])));

  // Hover lighting walks ancestor chains up and the edge set down.
  const parentOf = createMemo(() => {
    const m = new Map<string, string>();
    for (const e of layout().edges) m.set(e.child, e.parent);
    return m;
  });

  const childrenOf = createMemo(() => {
    const m = new Map<string, string[]>();
    for (const e of layout().edges) {
      const list = m.get(e.parent);
      if (list) list.push(e.child);
      else m.set(e.parent, [e.child]);
    }
    return m;
  });

  const litSet = createMemo(() => {
    const root = hoveredHandle();
    if (!root) return null;
    const lit = new Set<string>([root]);
    let cur = parentOf().get(root);
    while (cur && !lit.has(cur)) {
      lit.add(cur);
      cur = parentOf().get(cur);
    }
    const stack = [root];
    while (stack.length) {
      const x = stack.pop()!;
      for (const c of childrenOf().get(x) ?? []) {
        if (!lit.has(c)) {
          lit.add(c);
          stack.push(c);
        }
      }
    }
    return lit;
  });

  // The legend: one dot per status actually present, in a fixed order.
  const legend = createMemo(() => {
    const order = ['running', 'idle', 'done', 'stopped', 'stale'];
    const seen = new Map<string, number>();
    for (const r of props.rows) {
      const s = r.h.status;
      seen.set(s, (seen.get(s) ?? 0) + 1);
    }
    return order
      .filter((s) => seen.has(s))
      .map((s) => ({
        label: statusMeta(s).label,
        color: statusColor(s),
        n: seen.get(s)!,
      }));
  });

  const openable = (h: BoardHandle) => !!h.sessionId;
  const stoppable = (h: BoardHandle) =>
    (h.kind === 'teammate' || h.kind === 'task') && !!h.sessionId && h.status === 'running';

  // --- viewport --------------------------------------------------------------

  onMount(() => {
    const ro = new ResizeObserver(() => setVp({ w: hostEl.clientWidth, h: hostEl.clientHeight }));
    ro.observe(hostEl);
    onCleanup(() => ro.disconnect());
  });

  // Fit the graph's bounding box into the viewport, centred.
  const fitView = () => {
    const { width, height } = layout();
    const { w, h } = vp();
    if (width === 0 || height === 0 || w === 0 || h === 0) return;
    const z = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, Math.min(w / (width + 40), h / (height + 40), 0.8)));
    setView({ z, x: w / 2 - (width / 2) * z, y: h / 2 - (height / 2) * z });
  };

  // Auto-fit when the graph gains or loses a node, or the canvas resizes;
  // a refresh that merely moves cards leaves the user's pan/zoom alone.
  let fittedSig = '';
  createEffect(() => {
    const sig = `${layout().nodes.length}x${vp().w}x${vp().h}`;
    if (sig === fittedSig) return;
    fittedSig = sig;
    untrack(fitView);
  });

  const zoomAround = (factor: number, ev?: WheelEvent) => {
    setView((v) => {
      const z = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, v.z * factor));
      const rect = hostEl.getBoundingClientRect();
      const cx = ev ? ev.clientX - rect.left : rect.width / 2;
      const cy = ev ? ev.clientY - rect.top : rect.height / 2;
      const k = z / v.z;
      return { z, x: cx - (cx - v.x) * k, y: cy - (cy - v.y) * k };
    });
  };

  const onWheel = (e: WheelEvent) => {
    e.preventDefault();
    zoomAround(Math.exp(-e.deltaY * 0.0015), e);
  };

  const zoomBy = (factor: number) => zoomAround(factor);

  // Background drag pans. Cards sit in foreignObjects inside the svg, so the
  // closest('.ag-card') check is what keeps a card click from turning into a
  // pan.
  const onBackgroundDown = (e: PointerEvent) => {
    if ((e.target as Element | null)?.closest?.('.ag-card')) return;
    const start = { sx: e.clientX, sy: e.clientY, x: view().x, y: view().y };
    let moved = false;
    setPanning(true);
    const onMove = (ev: PointerEvent) => {
      if (!moved && Math.hypot(ev.clientX - start.sx, ev.clientY - start.sy) > 3) moved = true;
      setView((v) => ({
        ...v,
        x: start.x + (ev.clientX - start.sx),
        y: start.y + (ev.clientY - start.sy),
      }));
    };
    const onUp = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onUp);
      setPanning(false);
      if (moved) suppressClickUntil = Date.now() + 200;
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onUp);
  };

  // --- render ----------------------------------------------------------------

  const ClusterView = (c: GraphCluster) => (
    <g>
      <rect class="ag-cluster" x={c.x} y={c.y} width={c.w} height={c.h} rx={16} fill={c.color} stroke={c.color} />
      <text class="ag-hull-label" x={c.x + 16} y={c.y + 18} fill={c.color}>
        {`${c.label.toUpperCase()} · ${c.total}`}
      </text>
    </g>
  );

  const EdgeView = (e: GraphEdge) => {
    const dimmed = () => {
      const lit = litSet();
      return !!lit && !lit.has(e.child);
    };
    return (
      <g classList={{ 'ag-dim': dimmed() }}>
        <path class="ag-edge" d={e.path} stroke={e.color} />
        <Show when={e.running}>
          <path class="ag-edge-glow" d={e.path} stroke={e.color} />
          <path class="ag-edge-flow" d={e.path} stroke={e.color} />
        </Show>
      </g>
    );
  };

  // The subtree lit by the hover (see litSet): the node, its ancestors and
  // every descendant light up; everything else dims.
  const NodeView = (n: GraphNode) => {
    const row = () => rowByHandle().get(n.h.handle);
    const color = () => (row() ? statusColor(row()!.h.status) : 'var(--text-tertiary)');
    const dimmed = () => {
      const lit = litSet();
      return !!lit && !lit.has(n.h.handle);
    };
    const hovered = () => hoveredHandle() === n.h.handle;
    const armed = () => props.stopArmed === n.h.handle;
    const showStop = () => !!row() && stoppable(row()!.h) && (hovered() || armed());
    const hasChildren = () =>
      (props.counts.get(n.h.handle)?.total ?? 0) > 0 || props.collapsed.has(n.h.handle);

    return (
      <Show when={row()}>
        <g classList={{ 'ag-dim': dimmed(), 'ag-node': true }} style={{ transform: `translate(${n.x}px, ${n.y}px)` }}>
          <foreignObject x={0} y={0} width={NODE_W} height={NODE_H}>
            <div
              class="ag-card"
              classList={{
                'ag-armed': armed(),
                'ag-me': !!props.isMe && props.isMe === row()!.h.sessionId,
                'ag-faded': TERMINAL.has(row()!.h.status),
              }}
              style={{ '--ag-c': color() } as Record<string, string>}
              tabIndex={0}
              role="treeitem"
              aria-expanded={hasChildren() ? (props.collapsed.has(n.h.handle) ? 'false' : 'true') : undefined}
              data-board-handle={n.h.handle}
              aria-label={`${clip(row()!.h.title || n.h.handle, LABEL_CHARS)}, ${statusMeta(row()!.h.status).label} ${kindLabel(row()!.h.kind)}`}
              onMouseEnter={() => setHoveredHandle(n.h.handle)}
              onMouseLeave={() => setHoveredHandle((cur) => (cur === n.h.handle ? null : cur))}
              onClick={() => {
                if (Date.now() < suppressClickUntil) return;
                if (openable(row()!.h)) props.onOpen(row()!.h);
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && openable(row()!.h)) {
                  e.stopPropagation();
                  props.onOpen(row()!.h);
                }
              }}
            >
              <span class="ag-ava-wrap">
                <span class="ag-ava" aria-hidden="true">
                  <KindGlyph kind={kindLabel(row()!.h.kind)} />
                </span>
                <Show when={row()!.h.status === 'running'}>
                  <span class="ag-pulse" aria-hidden="true" />
                </Show>
              </span>
              <span class="ag-body">
                <span class="ag-title">{clip(row()!.h.title || n.h.handle, LABEL_CHARS)}</span>
                <span class="ag-meta">
                  <span class="ag-kind">{kindLabel(row()!.h.kind)}</span>
                  <span class="ag-status" style={{ color: color() }}>
                    {statusMeta(row()!.h.status).label}
                  </span>
                  <span class="ag-time">{formatElapsed(row()!.h, props.now)}</span>
                </span>
              </span>
              <Show
                when={showStop()}
                fallback={
                  <Show when={hasChildren()}>
                    <button
                      type="button"
                      class="ag-toggle"
                      title={props.collapsed.has(n.h.handle) ? `Expand ${row()!.h.title}` : `Collapse ${row()!.h.title}`}
                      aria-label={
                        props.collapsed.has(n.h.handle)
                          ? `Expand ${row()!.h.title}`
                          : `Collapse ${row()!.h.title}`
                      }
                      onPointerDown={(e) => e.stopPropagation()}
                      onClick={(e) => {
                        e.stopPropagation();
                        props.onToggleCollapse(n.h.handle);
                      }}
                    >
                      <Show
                        when={props.collapsed.has(n.h.handle)}
                        fallback={
                          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                            <path d="m6 9 6 6 6-6" />
                          </svg>
                        }
                      >
                        <span class="ag-n">{props.counts.get(n.h.handle)?.total ?? 0}</span>
                      </Show>
                    </button>
                  </Show>
                }
              >
                <button
                  type="button"
                  class="ag-stop"
                  classList={{ 'ag-stop-armed': armed() }}
                  aria-busy={props.stopping.has(n.h.handle)}
                  aria-label={armed() ? `Confirm stop ${row()!.h.title}` : `Stop ${row()!.h.title}`}
                  onPointerDown={(e) => e.stopPropagation()}
                  onClick={(e) => {
                    e.stopPropagation();
                    props.onStop(n.h.handle);
                  }}
                >
                  <Show
                    when={props.stopping.has(n.h.handle)}
                    fallback={
                      <Show when={armed()} fallback={<span class="ag-stop-icon" aria-hidden="true" />}>
                        <span class="ag-stop-word">Stop?</span>
                      </Show>
                    }
                  >
                    <span class="ag-stop-spin" aria-hidden="true" />
                  </Show>
                </button>
              </Show>
            </div>
          </foreignObject>
        </g>
      </Show>
    );
  };

  return (
    <div class="space-y-2">
      <div
        ref={hostEl}
        class="ag-canvas relative h-[min(70vh,660px)] w-full overflow-hidden rounded-xl border border-[color:var(--border-subtle)]"
        onWheel={onWheel}
      >
        {/* The legend, top-left: one dot per status actually present. */}
        <Show when={legend().length > 0}>
          <div
            role="list"
            aria-label="Status legend"
            class="ag-float absolute left-2 top-2 z-10 flex max-w-[calc(100%-4.5rem)] flex-wrap items-center gap-x-2.5 gap-y-1 rounded-lg border border-[color:var(--border-subtle)] px-2 py-1 shadow-sm"
          >
            <For each={legend()}>
              {(e) => (
                <span role="listitem" class="flex items-center gap-1 text-micro text-[color:var(--text-tertiary)]">
                  <span class="w-2 h-2 rounded-full shrink-0" style={{ background: e.color }} aria-hidden="true" />
                  {e.label}
                  <span class="tabular-nums">{e.n}</span>
                </span>
              )}
            </For>
          </div>
        </Show>

        {/* The zoom pill, bottom-right like a map app: − · % (click = fit) · +. */}
        <div class="ag-float absolute bottom-2 right-2 z-10 flex items-center gap-0.5 rounded-lg border border-[color:var(--border-subtle)] px-1 py-0.5 shadow-sm">
          <button
            type="button"
            onClick={() => zoomBy(1 / 1.15)}
            disabled={view().z <= ZOOM_MIN + 0.01}
            title="Zoom out"
            aria-label="Zoom out"
            class="icon-btn h-5 w-5! min-w-5! rounded disabled:opacity-40"
          >
            <svg class="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <path d="M5 12h14" />
            </svg>
          </button>
          <button
            type="button"
            onClick={fitView}
            title="Fit graph"
            aria-label="Fit graph"
            class="tabular-nums text-micro w-9 text-center text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] transition-colors"
          >
            {Math.round(view().z * 100)}%
          </button>
          <button
            type="button"
            onClick={() => zoomBy(1.15)}
            disabled={view().z >= ZOOM_MAX - 0.01}
            title="Zoom in"
            aria-label="Zoom in"
            class="icon-btn h-5 w-5! min-w-5! rounded disabled:opacity-40"
          >
            <svg class="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <path d="M12 5v14M5 12h14" />
            </svg>
          </button>
        </div>

        {/* The hint, bottom-left. */}
        <div class="pointer-events-none absolute bottom-2.5 left-2.5 z-10 text-micro text-[color:var(--text-tertiary)]">
          scroll to zoom · drag to pan · click a card to open
        </div>

        <svg class="absolute inset-0 h-full w-full touch-none select-none cursor-default" classList={{ 'cursor-grabbing': panning() }} onPointerDown={onBackgroundDown}>
          <g
            class="ag-view"
            style={{
              transform: `translate(${view().x}px, ${view().y}px) scale(${view().z})`,
              'transform-origin': '0 0',
            }}
          >
            <For each={layout().clusters}>{(c) => ClusterView(c)}</For>
            <For each={layout().edges}>{(e) => EdgeView(e)}</For>
            <For each={layout().nodes}>{(n) => NodeView(n)}</For>
          </g>
        </svg>

        <Show when={layout().nodes.length === 0}>
          <div class="pointer-events-none absolute inset-0 flex items-center justify-center">
            <p class="max-w-sm text-center text-[13px] text-[color:var(--text-tertiary)]">
              No agent families to graph — teammates you spawn will show up here as a tree.
              Standalone chat sessions are in the list below.
            </p>
          </div>
        </Show>
      </div>

      {/* Standalone chat sessions: top-level, no children — listed, not graphed. */}
      <Show when={standalone().length > 0}>
        <details class="ag-strip">
          <summary>
            <span>Standalone sessions</span>
            <span class="ag-strip-count tabular-nums">{standalone().length}</span>
          </summary>
          <div class="ag-strip-chips" role="list">
            <For each={standalone()}>
              {(r) => (
                <button
                  type="button"
                  role="listitem"
                  class="ag-chip"
                  classList={{ 'ag-faded': TERMINAL.has(r.h.status) }}
                  style={{ '--ag-c': statusColor(r.h.status) } as Record<string, string>}
                  onClick={() => props.onOpen(r.h)}
                  title={r.h.title || r.h.handle}
                >
                  <span class="ag-chip-dot" style={{ background: statusColor(r.h.status) }} aria-hidden="true" />
                  <span class="ag-chip-title">{clip(r.h.title || r.h.handle, 22)}</span>
                  <span class="ag-chip-time">{formatElapsed(r.h, props.now)}</span>
                </button>
              )}
            </For>
          </div>
        </details>
      </Show>
    </div>
  );
}