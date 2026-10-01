import { onMount } from 'solid-js';
import DialogShell, { DialogHeader } from './dialog-shell';

/**
 * A diagram is often rendered at whatever width the chat column happens to be,
 * which is not always enough to read a dense mermaid graph or a labelled plotly
 * chart. The magnifier in a diagram's top-right corner re-renders it here, at
 * the width of a large dialog, so the detail the author encoded is legible.
 *
 * The caller hands over a `render` callback rather than markup, because every
 * diagram kind draws itself differently — mermaid from its source, plotly from
 * a spec, rough from a spec, latex from compiled page images. The callback runs
 * once the body is in the DOM, and only when the dialog opens, so nothing is
 * drawn twice.
 */

export interface DiagramZoomState {
  /** Shown in the dialog header — e.g. "Mermaid diagram". */
  title: string;
  /** Draws the full-size diagram into the dialog body. */
  render: (host: HTMLElement) => void | Promise<void>;
}

/** The magnifier-with-plus glyph, shared with the imperative trigger. */
export const DIAGRAM_ZOOM_ICON =
  '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
  '<circle cx="11" cy="11" r="7"/>' +
  '<line x1="21" y1="21" x2="16.65" y2="16.65"/>' +
  '<line x1="11" y1="7.5" x2="11" y2="14.5"/>' +
  '<line x1="7.5" y1="11" x2="14.5" y2="11"/>' +
  '</svg>';

export default function DiagramZoomDialog(props: {
  state: DiagramZoomState;
  onClose: () => void;
}) {
  let body: HTMLDivElement | undefined;

  onMount(() => {
    if (!body) return;
    Promise.resolve(props.state.render(body)).catch(() => {});
  });

  return (
    <DialogShell
      labelledBy="diagram-zoom-title"
      onClose={props.onClose}
      class="sm:max-w-[1100px]"
    >
      <DialogHeader
        id="diagram-zoom-title"
        title={props.state.title}
        description="Full size — press Esc to close."
        onClose={props.onClose}
        icon={
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="11" cy="11" r="7" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
            <line x1="11" y1="7.5" x2="11" y2="14.5" />
            <line x1="7.5" y1="11" x2="14.5" y2="11" />
          </svg>
        }
      />
      <div class="diagram-zoom-body">
        <div ref={body} class="diagram-zoom-host" />
      </div>
    </DialogShell>
  );
}
