/**
 * RoughJS diagram renderer for ```rough fenced code blocks.
 *
 * Accepts a JSON spec describing shapes, lines, arrows, and text, and renders
 * them as hand-drawn SVGs inside the provided container element.
 *
 * Spec format:
 * {
 *   width?: number,         // viewBox width  (default 600)
 *   height?: number,        // viewBox height (default 400)
 *   elements: [
 *     { type: "rectangle", x, y, width, height, ...options },
 *     { type: "circle",    x, y, diameter, ...options },
 *     { type: "ellipse",  x, y, width, height, ...options },
 *     { type: "line",     x1, y1, x2, y2, ...options },
 *     { type: "arrow",    x1, y1, x2, y2, ...options },
 *     { type: "path",     d: "M...", ...options },
 *     { type: "text",     x, y, text, fontSize?, ...options },
 *   ],
 *   options?: { roughness?, bowing?, seed?, ... }  // global defaults
 * }
 *
 * Each element can specify RoughJS Options fields (stroke, fill, strokeWidth,
 * roughness, bowing, fillStyle, hachureAngle, hachureGap, seed, etc.).
 */

import rough from 'roughjs';
import type { Options } from 'roughjs/bin/core';
import type { RoughSVG } from 'roughjs/bin/svg';

export interface RoughElement {
  type: string;
  [key: string]: unknown;
}

export interface RoughSpec {
  width?: number;
  height?: number;
  elements: RoughElement[];
  options?: Options;
}

/* ---- Default palette that works on dark backgrounds ---- */
const DEFAULT_STROKE = 'var(--text-secondary)';
const DEFAULT_FILL = 'transparent';
const DEFAULT_TEXT_FILL = 'var(--text-primary)';
const ARROW_HEAD_SIZE = 10;

/** Coerce an unknown value to a finite number, falling back when NaN/Infinity. */
function num(v: unknown, fallback: number): number {
  const n = Number(v);
  return Number.isFinite(n) ? n : fallback;
}

function clampSize(n: number): number {
  return Math.min(Math.max(n, 10), 2000);
}

function buildOptions(el: RoughElement, globalOpts: Options): Options {
  const { type, ...rest } = el;
  // Remove non-option keys that are not RoughJS options
  const cleaned: Record<string, unknown> = {};
  const optionKeys = new Set([
    'stroke', 'strokeWidth', 'fill', 'fillStyle', 'fillWeight',
    'hachureAngle', 'hachureGap', 'roughness', 'bowing', 'seed',
    'curveFitting', 'curveTightness', 'curveStepCount',
    'simplification', 'dashOffset', 'dashGap', 'zigzagOffset',
    'strokeLineDash', 'strokeLineDashOffset', 'fillLineDash', 'fillLineDashOffset',
    'disableMultiStroke', 'disableMultiStrokeFill', 'preserveVertices',
    'maxRandomnessOffset', 'fixedDecimalPlaceDigits',
  ]);
  for (const [k, v] of Object.entries(rest)) {
    if (optionKeys.has(k)) cleaned[k] = v;
  }
  return {
    stroke: DEFAULT_STROKE,
    fill: DEFAULT_FILL,
    strokeWidth: 1.5,
    ...(globalOpts ?? {}),
    ...cleaned,
  };
}

function drawArrowHead(
  rc: RoughSVG,
  x1: number, y1: number,
  x2: number, y2: number,
  size: number,
  options: Options,
) {
  const angle = Math.atan2(y2 - y1, x2 - x1);
  const a1 = angle + Math.PI * 0.8;
  const a2 = angle - Math.PI * 0.8;
  const ax1 = x2 + Math.cos(a1) * size;
  const ay1 = y2 + Math.sin(a1) * size;
  const ax2 = x2 + Math.cos(a2) * size;
  const ay2 = y2 + Math.sin(a2) * size;
  // Two small lines forming arrowhead
  const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
  g.appendChild(rc.line(x2, y2, ax1, ay1, options));
  g.appendChild(rc.line(x2, y2, ax2, ay2, options));
  return g;
}

export function renderRoughDiagram(container: HTMLElement, spec: RoughSpec): void {
  const width = clampSize(num(spec.width, 600));
  const height = clampSize(num(spec.height, 400));
  const globalOpts = spec.options ?? {};
  const unknownTypes: string[] = [];

  // Create SVG element
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
  svg.setAttribute('width', '100%');
  svg.setAttribute('style', 'max-width:100%;height:auto;display:block;');
  svg.classList.add('rough-diagram');

  const rc = rough.svg(svg);

  for (const el of spec.elements ?? []) {
    const opts = buildOptions(el, globalOpts);
    const type = el.type;

    let node: SVGGElement | SVGGElement[] | null = null;

    switch (type) {
      case 'rectangle': {
        const x = num(el.x, 0);
        const y = num(el.y, 0);
        const w = num(el.width, 0);
        const h = num(el.height, 0);
        if (w <= 0 || h <= 0) {
          console.warn(`Skipping rectangle with non-positive size: ${w}x${h}`);
          break;
        }
        node = rc.rectangle(x, y, w, h, opts);
        break;
      }
      case 'circle': {
        const x = num(el.x, 0);
        const y = num(el.y, 0);
        const d = num(el.diameter, 0);
        if (d <= 0) {
          console.warn(`Skipping circle with non-positive diameter: ${d}`);
          break;
        }
        node = rc.circle(x, y, d, opts);
        break;
      }
      case 'ellipse': {
        const x = num(el.x, 0);
        const y = num(el.y, 0);
        const w = num(el.width ?? 80, 80);
        const h = num(el.height ?? 50, 50);
        if (w <= 0 || h <= 0) {
          console.warn(`Skipping ellipse with non-positive size: ${w}x${h}`);
          break;
        }
        node = rc.ellipse(x, y, w, h, opts);
        break;
      }
      case 'line': {
        node = rc.line(
          num(el.x1, 0), num(el.y1, 0),
          num(el.x2, 0), num(el.y2, 0), opts,
        );
        break;
      }
      case 'arrow': {
        const x1 = num(el.x1, 0);
        const y1 = num(el.y1, 0);
        const x2 = num(el.x2, 0);
        const y2 = num(el.y2, 0);
        const lineNode = rc.line(x1, y1, x2, y2, opts);
        svg.appendChild(lineNode);
        // Zero-length arrow: no direction exists, so skip the arrowhead
        // (atan2(0, 0) would draw a spurious V glyph).
        if (x1 === x2 && y1 === y2) {
          node = null; // already appended
          break;
        }
        const headSize = num(el.headSize ?? ARROW_HEAD_SIZE, ARROW_HEAD_SIZE);
        const head = drawArrowHead(rc, x1, y1, x2, y2, headSize, opts);
        svg.appendChild(head);
        node = null; // already appended
        break;
      }
      case 'path': {
        if (typeof el.d === 'string') {
          node = rc.path(el.d, opts);
        }
        break;
      }
      case 'linearPath': {
        if (Array.isArray(el.points)) {
          const pts: [number, number][] = [];
          for (const p of el.points as unknown[]) {
            if (!Array.isArray(p)) continue;
            const px = Number((p as unknown[])[0]);
            const py = Number((p as unknown[])[1]);
            if (!Number.isFinite(px) || !Number.isFinite(py)) continue;
            pts.push([px, py]);
          }
          if (pts.length < 2) {
            console.warn('Skipping linearPath with fewer than 2 valid points');
            break;
          }
          node = rc.linearPath(pts, opts);
        }
        break;
      }
      case 'polygon': {
        if (Array.isArray(el.points)) {
          const pts: [number, number][] = [];
          for (const p of el.points as unknown[]) {
            if (!Array.isArray(p)) continue;
            const px = Number((p as unknown[])[0]);
            const py = Number((p as unknown[])[1]);
            if (!Number.isFinite(px) || !Number.isFinite(py)) continue;
            pts.push([px, py]);
          }
          if (pts.length < 2) {
            console.warn('Skipping polygon with fewer than 2 valid points');
            break;
          }
          node = rc.polygon(pts, opts);
        }
        break;
      }
      case 'text': {
        // RoughJS has limited text support; use native SVG text for clarity
        const textEl = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        textEl.setAttribute('x', String(num(el.x, 0)));
        textEl.setAttribute('y', String(num(el.y, 0)));
        const textFill = opts.fill !== 'transparent' && opts.fill
          ? opts.fill
          : (el.fill ?? DEFAULT_TEXT_FILL);
        textEl.setAttribute('fill', String(textFill));
        textEl.setAttribute('font-size', String(num(el.fontSize ?? 14, 14)));
        textEl.setAttribute('font-family', 'var(--font-sans)');
        textEl.setAttribute('text-anchor', String(el.textAnchor ?? 'middle'));
        textEl.setAttribute('dominant-baseline', String(el.dominantBaseline ?? 'central'));
        textEl.textContent = String(el.text ?? '');
        svg.appendChild(textEl);
        node = null; // already appended
        break;
      }
      default:
        // Unknown type — collect for a visible warning below instead of skipping silently.
        unknownTypes.push(String(type));
        break;
    }

    if (node) {
      svg.appendChild(node as SVGGElement);
    }
  }

  if (unknownTypes.length > 0) {
    const unique = [...new Set(unknownTypes)];
    const msg = `Unsupported element type: ${unique.join(', ')}`;
    console.warn(msg);
    const warnEl = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    warnEl.setAttribute('x', '8');
    warnEl.setAttribute('y', '16');
    warnEl.setAttribute('fill', 'var(--text-primary)');
    warnEl.setAttribute('font-size', '12');
    warnEl.textContent = msg;
    svg.appendChild(warnEl);
  }

  container.textContent = '';
  container.appendChild(svg);
}