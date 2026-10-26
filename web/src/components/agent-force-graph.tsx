// agent-force-graph.tsx — the Agents page graph, force-directed. One node per
// agent, one edge per spawned_by, on a single canvas: small solid status-
// coloured discs, Neo4j style — nothing textual on the canvas, the hover
// tooltip is the only place a handle appears. Curved edges with arrowheads and
// moving particles on every edge whose child is still working. The physics
// (d3-force), zoom, pan and drag come from the force-graph library; the page
// only shapes data and paints.
//
// Performance: one canvas, no per-element DOM. force-graph pauses redrawing
// when nothing is changing; the loop stays on only while some agent runs, to
// animate its pulse ring. Hover lights a whole subtree (the hovered node, its
// ancestors, its descendants) and dims the rest.

import { Show, createEffect, createMemo, createSignal, onCleanup, onMount } from 'solid-js';
import { Portal } from 'solid-js/web';
import ForceGraph from 'force-graph';
import type { BoardHandle } from '../api/client';
import type { TreeRow } from '../lib/board-tree';
import './agent-force-graph.css';

interface AgentForceGraphProps {
  rows: TreeRow[];
  /** The session id of the viewer's own session — its node gets a "you" ring. */
  isMe: string | undefined;
  now: number;
  stopArmed: string | null;
  stopping: Set<string>;
  onOpen: (h: BoardHandle) => void;
  onStop: (handle: string) => void;
}

type AgNode = {
  id: string;
  h: BoardHandle;
  depth: number;
  r: number;
  x?: number;
  y?: number;
  // velocity accumulators written by the custom 'gather' force
  vx?: number;
  vy?: number;
};

type AgLink = {
  source: string | AgNode;
  target: string | AgNode;
  running: boolean;
};

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
  textTertiary: string;
  accent: string;
  subtle: string;
}

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

// Alpha over an opaque hex, for edge strokes, arrows and halos.
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

// Uniform node size: small fixed dots, never scaled — no subtree weighting, no
// root-vs-child difference. Status is colour only, so size carries nothing.
const NODE_R = 6;

// A stable phase per handle, so pulses are out of step rather than in unison.
function phaseOf(id: string): number {
  let n = 0;
  for (let i = 0; i < id.length; i++) n = (n * 31 + id.charCodeAt(i)) & 0xffff;
  return (n % 1000) / 1000;
}

export default function AgentForceGraph(props: AgentForceGraphProps) {
  let hostEl: HTMLDivElement | undefined;

  const [hovered, setHovered] = createSignal<string | null>(null);
  const [pointerIn, setPointerIn] = createSignal(false);
  const [size, setSize] = createSignal({ w: 0, h: 0 });
  const [tipPos, setTipPos] = createSignal({ x: 0, y: 0 });

  // keyed <Show> needs when() to yield the node itself (not a boolean), so
  // fold pointerIn into the hover lookup here.
  const tipShown = () => {
    if (!pointerIn()) return null;
    const handle = hovered();
    if (!handle) return null;
    return registry.get(handle) ?? null;
  };

  // Nodes keep identity across refreshes so force positions survive a roster
  // update: a re-fetched agent reuses its old object (carrying x/y) and only
  // its mutable fields are re-stamped. New handles join with fresh positions;
  // handles filtered away keep their place for when they come back.
  const registry = new Map<string, AgNode>();

  // parentOf and childrenOf, rebuilt with the rows. Declared before data()
  // reads them; relationMaps is a hoisted function so the order holds.
  const parentOf = createMemo(() => relationMaps(props.rows).parentOf);
  const childrenOf = createMemo(() => relationMaps(props.rows).childrenOf);

  const data = createMemo(() => {
    const present = new Set<string>();
    for (const row of props.rows) present.add(row.h.handle);
    const fresh: AgNode[] = [];
    const nodes: AgNode[] = props.rows.map((row) => {
      let n = registry.get(row.h.handle);
      if (n) {
        n.h = row.h;
        n.depth = row.depth;
        n.r = NODE_R;
      } else {
        n = {
          id: row.h.handle,
          h: row.h,
          depth: row.depth,
          r: NODE_R,
          x: 0,
          y: 0,
        };
        registry.set(n.id, n);
        fresh.push(n);
      }
      return n;
    });
    const links: AgLink[] = [];
    for (const row of props.rows) {
      const parent = row.h.spawnedBy;
      if (parent && present.has(parent)) {
        links.push({
          source: parent,
          target: row.h.handle,
          running: row.h.status === 'running',
        });
      }
    }
    // Family-cluster seed for freshly added nodes: connected components of the
    // parent graph each get a centre on a golden-angle spiral (families spread
    // across the canvas), and a family's members ring tightly around that
    // centre. Deterministic, and neo4j-style clumps form before the first
    // physics tick rather than after (or never, at low alpha).
    if (fresh.length) {
      const adjacency = new Map<string, string[]>();
      const addEdge = (a: string, b: string) => {
        const l = adjacency.get(a);
        if (l) l.push(b);
        else adjacency.set(a, [b]);
      };
      for (const l of links) {
        addEdge(l.source as string, l.target as string);
        addEdge(l.target as string, l.source as string);
      }
      const familyOf = new Map<string, number>();
      let nextFamily = 0;
      for (const n of nodes) {
        if (familyOf.has(n.id)) continue;
        const f = nextFamily++;
        const stack = [n.id];
        familyOf.set(n.id, f);
        while (stack.length) {
          const cur = stack.pop()!;
          for (const nb of adjacency.get(cur) ?? []) {
            if (!familyOf.has(nb)) {
              familyOf.set(nb, f);
              stack.push(nb);
            }
          }
        }
      }
      const memberIndex = new Map<number, number>();
      const centreIndex = new Map<number, number>();
      const sizeOf = new Map<number, number>();
      for (const f of familyOf.values()) sizeOf.set(f, (sizeOf.get(f) ?? 0) + 1);
      let multiFamilies = 0;
      let singIdx = 0;
      for (const n of nodes) {
        if (!fresh.includes(n)) continue; // seeded nodes keep their position
        const f = familyOf.get(n.id) ?? 0;
        const size = sizeOf.get(f) ?? 1;
        if (size === 1) {
          // A singleton is its own family — a dedicated far spiral just makes
          // dust ring the periphery. Seed it into a tight central cloud.
          const k = singIdx++;
          const a = k * 2.399963;
          const rr = 11 * Math.sqrt(k);
          n.x = rr * Math.cos(a);
          n.y = rr * Math.sin(a);
          continue;
        }
        let ci = centreIndex.get(f);
        if (ci === undefined) {
          ci = multiFamilies++;
          centreIndex.set(f, ci);
        }
        const m = memberIndex.get(f) ?? 0;
        memberIndex.set(f, m + 1);
        // Family centres: golden-angle spiral. Tight spacing — the span of
        // this spiral sets the post-fit zoom, so keeping it compact is what
        // makes the fitted bubbles big. ~52px between family centres.
        const ca = ci * 2.399963;
        const cr = 52 * Math.sqrt(ci);
        // Members: own spiral around the centre, radius grows with the
        // family so a big family still forms a loose clump, not a stack.
        const ma = m * 2.399963 + ci * 0.7;
        const mr = 7 * Math.sqrt(m) + n.r * 0.66;
        n.x = cr * Math.cos(ca) + mr * Math.cos(ma);
        n.y = cr * Math.sin(ca) + mr * Math.sin(ma);
      }
    }
    // Canvas paint order follows the nodes array — later entries draw on top.
    // Sort so attention-worthy bubbles are never buried under the idle clump
    // (stable sort: ties keep roster order, so layout and the data key stay
    // deterministic).
    const zOf = (s: string) =>
      s === 'running' ? 4 : s === 'stale' ? 3 : s === 'idle' ? 2 : s === 'stopped' ? 1 : 0;
    nodes.sort((a, b) => zOf(a.h.status) - zOf(b.h.status));
    return { nodes, links };
  });

  // The subtree lit by hover: the hovered node, its ancestors, every
  // descendant. A plain mutable Set read by the canvas accessors each frame —
  // the reactive cost is one rebuild per hover change, not per frame.
  let lit = new Set<string>();
  createEffect(() => {
    const handle = hovered();
    lit = new Set<string>();
    if (!handle) return;
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
  });

  // Theme colours, resolved off this element's computed style. The cache is
  // dropped on any data-theme flip on <html> (the pre-paint + ThemeContext
  // mechanism).
  let palCache: Palette | null = null;
  const palette = (): Palette => {
    if (palCache) return palCache;
    const cs = getComputedStyle(hostEl ?? document.documentElement);
    const read = (name: string, fallback: string) => cs.getPropertyValue(name).trim() || fallback;
    palCache = {
      base: read('--bg-base', '#09090b'),
      textTertiary: read('--text-tertiary', '#8b8b94'),
      accent: read('--accent', '#ff5e1f'),
      subtle: read('--border-subtle', '#1e1e23'),
    };
    return palCache;
  };
  const statusHue = (status: string): string => {
    const raw = STATUS_HUES[status] ?? FALLBACK_HUE;
    if (!raw.startsWith('VAR:')) return raw;
    const name = raw.slice(4) as string;
    const val = palette()[name === '--text-tertiary' ? 'textTertiary' : 'accent'];
    return val ?? FALLBACK_HUE;
  };

  const statusOf = (n: string): string => registry.get(n)?.h.status ?? 'idle';

  let graphRef: ForceGraph<AgNode, AgLink> | undefined;
  let fitted = false;
  let userMoved = false;
  let gatherTicks = 0;

  const onMountGraph = () => {
    const el = hostEl;
    if (!el || graphRef) return;
    const pal = palette();
    const graph = new ForceGraph<AgNode, AgLink>(el)
      .backgroundColor('transparent') // the host's flat background shows through
      .nodeId('id')
      .nodeCanvasObject(drawNode)
      // accessorFn interprets a bare string as a NODE PROPERTY name (node.replace →
      // undefined) — the mode constant must ride a function, or the custom paint
      // is silently skipped and the builtin blue-disc painter runs instead.
      .nodeCanvasObjectMode(() => 'replace')
      .nodePointerAreaPaint((n, color, ctx) => {
        ctx.beginPath();
        ctx.arc(n.x ?? 0, n.y ?? 0, (n.r ?? 8) + 2, 0, 2 * Math.PI);
        ctx.fillStyle = color;
        ctx.fill();
      })
      .nodeLabel(() => '') // nothing textual: the built-in hover label stays off too
      .linkColor((l) => {
        const child = typeof l.target === 'string' ? l.target : l.target.id;
        if (lit.size && !lit.has(child)) return hexAlpha(pal.subtle, 0.18);
        const col = statusHue(statusOf(child));
        return hexAlpha(col, 0.5);
      })
      .linkWidth((l) => {
        const child = typeof l.target === 'string' ? l.target : l.target.id;
        return lit.size && lit.has(child) ? 1.6 : 1;
      })
      .linkCurvature(0.16)
      .linkDirectionalArrowLength(4)
      .linkDirectionalArrowRelPos(0.88)
      .linkDirectionalArrowColor((l) => {
        const child = typeof l.target === 'string' ? l.target : l.target.id;
        if (lit.size && !lit.has(child)) return hexAlpha(pal.subtle, 0.22);
        return hexAlpha(statusHue(statusOf(child)), 0.85);
      })
      .linkDirectionalParticles((l) => (l.running ? 3 : 0))
      .linkDirectionalParticleSpeed(0.55)
      .linkDirectionalParticleWidth(1.6)
      .linkDirectionalParticleColor((l) => {
        const child = typeof l.target === 'string' ? l.target : l.target.id;
        return statusHue(statusOf(child));
      })
      .d3VelocityDecay(0.4)
      .d3AlphaDecay(0.032)
      .d3AlphaMin(0.005) // stop the engine at convergence; below this the sim only burns ticks
      .cooldownTicks(600)
      // Layout must not depend on the rAF loop: a hidden/background tab never
      // servos requestAnimationFrame, so an engine running on rAF alone sits
      // frozen forever (seen 2026-10-26 — no engineTick at all, screenshot
      // frozen at the seed spiral). warmupTicks ticks the sim SYNCHRONOUSLY
      // inside the kapsule's update() on every graphData set — alpha 0.032
      // decay leaves ~4.5e-4 after 240 ticks, i.e. converged before the first
      // painted frame, background tab or not.
      .warmupTicks(240)
      .onNodeHover((node) => setHovered(node?.id ?? null))
      .onNodeClick((node) => {
        if (node.h.sessionId) props.onOpen(node.h);
      })
      .onBackgroundClick(() => setHovered(null))
      .onEngineStop(() => {
        // Backstop for the settle-fit below — some force-graph releases don't
        // emit this event, so the gather force itself owns the fit.
        if (userMoved || fitted) return; fitted = true;
        graph.zoomToFit(480, 56);
      });

    const charge = graph.d3Force('charge') as unknown as {
      strength: (f: unknown) => unknown;
      distanceMax: (d: number) => void;
    } | undefined;
    charge?.strength(((n: AgNode) => -(12 + (n.r ?? 16) * 1.5)) as never);
    // Repulsion only within reach: otherwise the ~40 disconnected family
    // trees shove each other to the canvas edge, zoomToFit zooms out and
    // every bubble shrinks back to a dot. Local charge + a firm center pull
    // gathers the families into one neighbourhood instead. The reach is kept
    // SMALL — roughly intra-family spacing — so repulsion separates members
    // inside a clump but never inflates the distance BETWEEN clumps: the
    // bbox sets the post-fit zoom, so the fit is what makes bubbles big.
    charge?.distanceMax(120);
    (graph.d3Force('link') as unknown as { distance: (d: number) => void } | undefined)?.distance(28);
    // d3.forceCenter only translates the system — its strength is ignored —
    // so it can never pull disconnected families together. This constant-strength
    // spring toward the origin does: every node drifts gently home, families settle
    // into one neighbourhood, and the local charge (now reaching 320px, well past
    // the 38px link spacing) keeps them from collapsing into each other. The old
    // 0.2 gain made it integrate over the whole decay (Σalpha ≈ 36) and the graph
    // crumpled into a ball before the settle-fit could frame it.
    // graph.d3Force('center')?.strength(0.4);
    graph.d3Force('gather', ((alpha: number) => {
      gatherTicks++;
      const nodes = graph.graphData().nodes as AgNode[];
      // The settle-fit: once the layout has essentially finished, frame it.
      // onEngineStop is unreliable across force-graph releases, and this
      // condition is exact — alpha is the engine's own cooling measure.
      // Gathered ticks too, so an engine reheated with stale alpha can't
      // frame the seed spiral before any real tick has run.
      if (!fitted && !userMoved && nodes.length > 2 && gatherTicks > 120 && alpha < 0.012) {
        fitted = true;
        graph.zoomToFit(480, 56);
      }
      for (const n of nodes) {
        n.vx = (n.vx ?? 0) - (n.x ?? 0) * 0.04 * alpha;
        n.vy = (n.vy ?? 0) - (n.y ?? 0) * 0.04 * alpha;
      }
    }) as never);
    graph.maxZoom(6);

    // A manual wheel or drag marks the view as the user's; auto-refits stop.
    const markUserMoved = () => {
      userMoved = true;
    };
    el.addEventListener('wheel', markUserMoved, { passive: true });
    el.addEventListener('pointerdown', markUserMoved);
    onCleanup(() => {
      el.removeEventListener('wheel', markUserMoved);
      el.removeEventListener('pointerdown', markUserMoved);
    });

    graphRef = graph;

    onCleanup(() => {
      graph._destructor();
      graphRef = undefined;
    });
  };

  onMount(() => {
    onMountGraph();
    const ro = new ResizeObserver(applySize);
    ro.observe(hostEl!);
    onCleanup(() => ro.disconnect());
    applySize();
  });

  function applySize() {
    const el = hostEl;
    if (!el) return;
    const w = el.clientWidth || 600;
    const h = el.clientHeight || 420;
    setSize({ w, h });
    if (graphRef) {
      graphRef.width(w);
      graphRef.height(h);
    }
  }

  // force-graph re-heats the engine on every graphData() call — a roster poll
  // that changed nothing must not re-melt the layout, so a snapshot key gates
  // the setter. A real change resets the fit flag so the next settle frames it.
  let lastDataKey = '';
  createEffect(() => {
    const d = data();
    const graph = graphRef;
    if (!graph) return;
    const key =
      (d.nodes as AgNode[]).map((n) => `${n.id}|${n.h.status}`).join(',') +
      '#' +
      (d.links as AgLink[]).length;
    if (key === lastDataKey) return;
    const first = lastDataKey === '';
    lastDataKey = key;
    if (!first) fitted = false; // re-frame after a genuine change settles
    // v1.52 PAUSES the engine on every graphData set (and its update() path
    // pauses again on any later prop set) — nothing in the lib ever restarts
    // it, so without this explicit call the sim never ticks: no gather,
    // no engineStop, no settle-fit — just the frozen seed spiral.
    graph.graphData(d);
    graph.d3ReheatSimulation();
  });

  // Continuous redraw only while something is alive: pulse rings and flow
  // particles want frames; a quiet roster needs none.
  createEffect(() => {
    const anyRunning = props.rows.some((r) => r.h.status === 'running');
    graphRef?.autoPauseRedraw(!anyRunning);
  });

  // Keep the hover tooltip anchored to its node while the layout settles.
  createEffect(() => {
    if (!hovered() || !pointerIn()) return;
    const id = setInterval(positionTip, 120);
    onCleanup(() => clearInterval(id));
  });

  function positionTip() {
    const graph = graphRef;
    const handle = hovered();
    if (!graph || !handle) return;
    const node = registry.get(handle);
    if (!node || node.x === undefined || node.y === undefined) return;
    const s = graph.graph2ScreenCoords(node.x, node.y);
    // The tip teleports to <body>, so anchor on viewport coordinates: canvas
    // screen coords offset by wherever the host sits in the page.
    const rect = hostEl?.getBoundingClientRect();
    if (!rect) return;
    setTipPos({
      x: Math.max(8, Math.min(window.innerWidth - 244, rect.left + s.x + node.r * graph.zoom() + 10)),
      y: Math.max(8, Math.min(window.innerHeight - 52, rect.top + s.y - 18)),
    });
  }

  const openable = (h: BoardHandle) => !!h.sessionId;
  const stoppable = (h: BoardHandle) =>
    (h.kind === 'teammate' || h.kind === 'task') && !!h.sessionId && h.status === 'running';


  // --- node painting ---------------------------------------------------------

  function drawNode(node: AgNode, ctx: CanvasRenderingContext2D, _globalScale: number) {
    const pal = palette();
    const x = node.x ?? 0;
    const y = node.y ?? 0;
    const r = node.r;
    const dimmed = lit.size > 0 && !lit.has(node.id);
    const col = statusHue(node.h.status);
    const isMe = props.isMe !== undefined && node.h.sessionId === props.isMe;

    if (dimmed) ctx.globalAlpha = 0.22;

    // Node body: a small solid disc in the status hue — status is colour-only,
    // so no text is ever painted and the size never varies.
    ctx.beginPath();
    ctx.arc(x, y, r, 0, 2 * Math.PI);
    ctx.fillStyle = col;
    ctx.fill();
    // A subtle darker rim is the only shaping beyond the fill.
    ctx.strokeStyle = hexMix(col, pal.base, 0.55);
    ctx.lineWidth = 1;
    ctx.stroke();

    // The viewer's own session: a dashed selection ring (colour, not text).
    if (isMe) {
      ctx.setLineDash([2.5, 2.2]);
      ctx.beginPath();
      ctx.arc(x, y, r + 3.4, 0, 2 * Math.PI);
      ctx.strokeStyle = hexAlpha(pal.accent, 0.9);
      ctx.lineWidth = 1.1;
      ctx.stroke();
      ctx.setLineDash([]);
    }

    // Live agents breathe: a soft ring pulsing outside the disc.
    if (node.h.status === 'running') {
      const t = performance.now() / 700 + phaseOf(node.id) * Math.PI * 2;
      ctx.beginPath();
      ctx.arc(x, y, r + 2.6 + Math.sin(t) * 1.6, 0, 2 * Math.PI);
      ctx.fillStyle = hexAlpha(col, 0.16 + 0.09 * Math.sin(t));
      ctx.fill();
    }

    ctx.globalAlpha = 1;
  }

  function zoomStep(factor: number) {
    const graph = graphRef;
    if (!graph) return;
    graph.zoom(Math.max(0.2, Math.min(4, graph.zoom() * factor)));
  }

  // hostEl keeps ONLY static content — force-graph appends its canvas into it
  // directly, and a Solid-managed dynamic child beside that canvas tore the
  // DOM down mid-insert (insertBefore NotFoundError). The tooltip therefore
  // teleports to <body> and clamps against the viewport, not the host.
  return (
    <>
      <div
        ref={hostEl}
        class="afg-host"
        onMouseEnter={() => setPointerIn(true)}
        onMouseLeave={() => {
          setPointerIn(false);
          setHovered(null);
        }}
      >
        {/* Viewport controls: fit and zoom, the same affordances the old view had. */}
        <div class="afg-float afg-ctl" role="group" aria-label="Graph zoom controls">
          <button
            type="button"
            title="Fit view"
            aria-label="Fit the graph to the view"
            onClick={() => {
              fitted = true;
              graphRef?.zoomToFit(350, 44);
            }}
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
              class="afg-tip"
              style={{
                left: `${tipPos().x}px`,
                top: `${tipPos().y}px`,
              }}
            >
              <div class="afg-tip-head">
                <span class="w-2 h-2 rounded-full shrink-0" style={{ background: statusHue(t().h.status) }} />
                <span class="afg-tip-handle">{t().id}</span>
                <span class="afg-tip-kind">{t().h.kind ?? 'agent'}</span>
              </div>
              <Show when={t().h.title}>
                <div class="afg-tip-title">{t().h.title}</div>
              </Show>
              <div class="afg-tip-meta">
                {[t().h.model, fmtElapsed(t().h.createdAt, props.now)]
                  .filter(Boolean)
                  .join(' · ')}
              </div>
              <div class="afg-tip-actions">
                <Show when={openable(t().h)}>
                  <button type="button" class="afg-btn" onClick={() => props.onOpen(t()!.h)}>
                    Open session
                  </button>
                </Show>
                <Show when={stoppable(t().h)}>
                  <button
                    type="button"
                    class="afg-btn afg-btn-stop"
                    classList={{ 'afg-armed': props.stopArmed === t().id }}
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