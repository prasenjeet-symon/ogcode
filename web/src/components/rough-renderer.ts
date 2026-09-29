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
const DEFAULT_STROKE = '#a1a1aa'; // --text-secondary
const DEFAULT_FILL = 'transparent';
const DEFAULT_TEXT_FILL = '#f4f4f5'; // --text-primary
const ARROW_HEAD_SIZE = 10;

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
  const width = spec.width ?? 600;
  const height = spec.height ?? 400;
  const globalOpts = spec.options ?? {};

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
        node = rc.rectangle(
          Number(el.x), Number(el.y),
          Number(el.width), Number(el.height), opts,
        );
        break;
      }
      case 'circle': {
        node = rc.circle(
          Number(el.x), Number(el.y),
          Number(el.diameter), opts,
        );
        break;
      }
      case 'ellipse': {
        node = rc.ellipse(
          Number(el.x), Number(el.y),
          Number(el.width ?? 80), Number(el.height ?? 50), opts,
        );
        break;
      }
      case 'line': {
        node = rc.line(
          Number(el.x1), Number(el.y1),
          Number(el.x2), Number(el.y2), opts,
        );
        break;
      }
      case 'arrow': {
        const lineNode = rc.line(
          Number(el.x1), Number(el.y1),
          Number(el.x2), Number(el.y2), opts,
        );
        svg.appendChild(lineNode);
        const headSize = Number(el.headSize ?? ARROW_HEAD_SIZE);
        const head = drawArrowHead(
          rc,
          Number(el.x1), Number(el.y1),
          Number(el.x2), Number(el.y2),
          headSize, opts,
        );
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
          const pts: [number, number][] = (el.points as number[][]).map(
            (p: number[]) => [Number(p[0]), Number(p[1])] as [number, number],
          );
          node = rc.linearPath(pts, opts);
        }
        break;
      }
      case 'polygon': {
        if (Array.isArray(el.points)) {
          const pts: [number, number][] = (el.points as number[][]).map(
            (p: number[]) => [Number(p[0]), Number(p[1])] as [number, number],
          );
          node = rc.polygon(pts, opts);
        }
        break;
      }
      case 'text': {
        // RoughJS has limited text support; use native SVG text for clarity
        const textEl = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        textEl.setAttribute('x', String(el.x));
        textEl.setAttribute('y', String(el.y));
        const textFill = opts.fill !== 'transparent' && opts.fill
          ? opts.fill
          : (el.fill ?? DEFAULT_TEXT_FILL);
        textEl.setAttribute('fill', String(textFill));
        textEl.setAttribute('font-size', String(el.fontSize ?? 14));
        textEl.setAttribute('font-family', 'var(--font-sans)');
        textEl.setAttribute('text-anchor', String(el.textAnchor ?? 'middle'));
        textEl.setAttribute('dominant-baseline', String(el.dominantBaseline ?? 'central'));
        textEl.textContent = String(el.text ?? '');
        svg.appendChild(textEl);
        node = null; // already appended
        break;
      }
      default:
        // Unknown type — skip silently
        break;
    }

    if (node) {
      svg.appendChild(node as SVGGElement);
    }
  }

  container.textContent = '';
  container.appendChild(svg);
}